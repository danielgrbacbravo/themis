package submission

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
)

type uploadReader struct {
	reader   io.Reader
	sent     *int64
	total    int64
	progress Progress
}

func (r uploadReader) Read(b []byte) (int, error) {
	n, e := r.reader.Read(b)
	*r.sent += int64(n)
	if r.progress != nil {
		r.progress(*r.sent, r.total)
	}
	return n, e
}

func (s *Service) Submit(ctx context.Context, p *Prepared, progress Progress) (receipt Receipt, resultErr error) {
	ctx, requestCancel := context.WithTimeout(ctx, 5*time.Minute)
	defer requestCancel()
	receipt = Receipt{AssignmentURL: p.Form.AssignmentURL, StatusURL: statsURL(p.Form.AssignmentURL), State: "not_sent", CreatedAt: time.Now().UTC()}
	client, err := s.client(ctx)
	if err != nil {
		return receipt, ErrAuth
	}
	ctx = context.WithValue(ctx, clientContextKey{}, client)
	fresh, err := s.ReadForm(ctx, p.Form.AssignmentURL)
	if err != nil {
		return receipt, err
	}
	if !sameRequirements(p.Form, fresh) {
		return receipt, &ReviewChanged{Form: fresh}
	}
	if _, err := Validate(fresh, p.Files, p.Language); err != nil {
		return receipt, err
	}
	p.mu.Lock()
	if p.attempted || p.dir == "" {
		p.mu.Unlock()
		return receipt, fmt.Errorf("this reviewed upload has already been attempted or closed")
	}
	p.attempted = true
	p.mu.Unlock()
	// A pipe makes this a non-replayable request. net/http cannot replay it after
	// a connection failure; the application likewise never retries a POST.
	reader, writer := io.Pipe()
	multipartWriter := multipart.NewWriter(writer)
	transferCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	defer reader.Close()
	writeDone := make(chan error, 1)
	go func() {
		var writeErr error
		defer func() { _ = writer.CloseWithError(writeErr); writeDone <- writeErr }()
		fields := url.Values{}
		for k, v := range fresh.Fields {
			fields[k] = append([]string{}, v...)
		}
		if p.Language != "" {
			fields.Set("judgeLanguage", p.Language)
		}
		for k, values := range fields {
			for _, v := range values {
				if writeErr = multipartWriter.WriteField(k, v); writeErr != nil {
					return
				}
			}
		}
		var sent int64
		for _, file := range p.Files {
			if writeErr = transferCtx.Err(); writeErr != nil {
				return
			}
			var in *os.File
			in, writeErr = os.Open(file.Snapshot)
			if writeErr != nil {
				return
			}
			header := textproto.MIMEHeader{}
			header.Set("Content-Disposition", mime.FormatMediaType("form-data", map[string]string{"name": file.Field, "filename": file.Name}))
			header.Set("Content-Type", "application/octet-stream")
			var part io.Writer
			part, writeErr = multipartWriter.CreatePart(header)
			if writeErr == nil {
				_, writeErr = io.Copy(part, uploadReader{contextReader{transferCtx, in}, &sent, p.TotalBytes, progress})
			}
			in.Close()
			if writeErr != nil {
				return
			}
		}
		writeErr = multipartWriter.Close()
	}()
	req, err := http.NewRequestWithContext(ctx, "POST", fresh.Action, reader)
	if err != nil {
		reader.Close()
		<-writeDone
		return receipt, err
	}
	req.Header.Set("Content-Type", multipartWriter.FormDataContentType())
	req.Header.Set("Referer", fresh.AssignmentURL)
	origin, _ := url.Parse(s.BaseURL)
	req.Header.Set("Origin", origin.Scheme+"://"+origin.Host)
	postClient := *client
	postClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	// Persist uncertainty before the first byte can leave the process. A crash or
	// immediate TUI exit must not erase an in-flight submission from history.
	receipt.State = "unknown"
	if s.Store != nil {
		if err := s.Store.SaveReceipt(receipt); err != nil {
			reader.Close()
			<-writeDone
			receipt.State = "not_sent"
			return receipt, fmt.Errorf("save submission intent: %w", err)
		}
	}
	defer func() {
		if resultErr != nil && !errors.Is(resultErr, ErrUnknown) {
			receipt.State = "rejected"
		}
		if s.Store != nil {
			if e := s.Store.SaveReceipt(receipt); e != nil {
				receipt.Warning = "Could not update submission history: " + e.Error()
			}
		}
	}()
	if err := ctx.Err(); err != nil {
		reader.Close()
		<-writeDone
		return receipt, err
	}
	resp, err := postClient.Do(req)
	// Servers can reject the request before consuming the whole multipart body.
	cancel()
	reader.Close()
	writeErr := <-writeDone
	if err != nil {
		if ctx.Err() != nil {
			return receipt, fmt.Errorf("%w: connection interrupted after sending began", ErrUnknown)
		}
		return receipt, fmt.Errorf("%w: check the status page before submitting again", ErrUnknown)
	}
	defer resp.Body.Close()
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, 16*1024*1024+1))
	if readErr != nil || len(body) > 16*1024*1024 {
		return receipt, ErrUnknown
	}
	if resp.StatusCode >= 500 {
		return receipt, ErrUnknown
	}
	if resp.StatusCode >= 400 {
		d, _ := goquery.NewDocumentFromReader(bytes.NewReader(body))
		notice := ""
		if d != nil {
			notice = text(d.Find(".notice-error,.error,.alert-danger,.alert").First())
		}
		if resp.StatusCode == 401 || resp.StatusCode == 403 {
			return receipt, ErrAuth
		}
		return receipt, fmt.Errorf("submission rejected (HTTP %d): %s", resp.StatusCode, notice)
	}
	if writeErr != nil && resp.StatusCode == 200 {
		return receipt, ErrUnknown
	}
	var target string
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		target = resp.Header.Get("Location")
	} else if resp.StatusCode == 200 {
		d, _ := goquery.NewDocumentFromReader(bytes.NewReader(body))
		if d != nil {
			if notice := text(d.Find(".notice-error,.error,.alert-danger").First()); notice != "" {
				return receipt, fmt.Errorf("submission rejected: %s", notice)
			}
			// Only a concrete response submission is evidence of acceptance. A generic
			// history link cannot identify which submission this request created.
			candidates := map[string]bool{}
			d.Find("section.submission .sec-title a[href*='/submission/'],.notice-success a[href*='/submission/']").Each(func(_ int, a *goquery.Selection) {
				u, err := s.resolve(a.AttrOr("href", ""))
				if err == nil && concreteURL(u) && submissionAssignment(u) == fresh.AssignmentURL {
					candidates[publicURL(u)] = true
				}
			})
			if len(candidates) == 1 {
				for u := range candidates {
					target = u
				}
			}
		}
	}
	if target == "" {
		return receipt, ErrUnknown
	}
	base, _ := url.Parse(fresh.Action)
	u, e := url.Parse(target)
	if e != nil {
		return receipt, ErrUnknown
	}
	target = base.ResolveReference(u).String()
	if _, e = s.resolve(target); e != nil {
		return receipt, ErrUnknown
	}
	if strings.Contains(target, "/log/") || strings.Contains(target, "/login") {
		return receipt, ErrAuth
	}
	// Follow the server-provided target, including /stats/.../@latest, once to
	// resolve its concrete ID. Never manufacture such an alias ourselves.
	if !concreteURL(target) && !strings.Contains(u.Path, "/@latest") {
		return receipt, ErrUnknown
	}
	result, e := s.FetchResult(ctx, target)
	if e != nil {
		if concreteURL(target) && submissionAssignment(target) == fresh.AssignmentURL {
			receipt.SubmissionURL = publicURL(target)
			receipt.State = "accepted"
			return receipt, nil
		}
		return receipt, ErrUnknown
	}
	if result.AssignmentURL != publicURL(fresh.AssignmentURL) {
		return receipt, ErrUnknown
	}
	receipt.SubmissionURL = result.SubmissionURL
	receipt.State = "accepted"
	return receipt, nil
}
func concreteURL(raw string) bool {
	u, e := url.Parse(raw)
	if e != nil {
		return false
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	for i, p := range parts {
		if p == "@submissions" {
			return strings.HasPrefix(u.Path, "/submission/") && len(parts) == i+3 && parts[i+2] != ""
		}
	}
	return false
}
