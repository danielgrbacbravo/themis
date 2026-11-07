package submission

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
)

func clean(s string) string { return strings.Join(strings.Fields(s), " ") }
func text(s *goquery.Selection) string {
	c := s.Clone()
	c.Find(".tip-text,script,style").Remove()
	return clean(c.Text())
}
func cfg(s *goquery.Selection) map[string]string {
	out := map[string]string{}
	s.Find(".cfg-line").Each(func(_ int, l *goquery.Selection) {
		out[strings.ToLower(strings.TrimSuffix(text(l.Find(".cfg-key").First()), ":"))] = text(l.Find(".cfg-val").First())
	})
	return out
}
func (s *Service) resolve(raw string) (string, error) {
	base, err := url.Parse(s.BaseURL)
	if err != nil {
		return "", err
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("invalid Themis URL")
	}
	u = base.ResolveReference(u)
	if (u.Scheme != "https" && u.Scheme != "http") || u.Scheme != base.Scheme || !strings.EqualFold(u.Host, base.Host) || u.User != nil {
		return "", fmt.Errorf("URL must belong to the configured Themis origin")
	}
	u.Fragment = ""
	return u.String(), nil
}
func publicURL(raw string) string {
	u, e := url.Parse(raw)
	if e != nil {
		return ""
	}
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}
func statsURL(assignment string) string {
	return strings.Replace(publicURL(assignment), "/course/", "/stats/", 1)
}

type clientContextKey struct{}

func (s *Service) client(ctx context.Context) (*http.Client, error) {
	if client, ok := ctx.Value(clientContextKey{}).(*http.Client); ok {
		return client, nil
	}
	if s.Client == nil {
		return nil, fmt.Errorf("submission client is unavailable")
	}
	return s.Client()
}
func (s *Service) get(ctx context.Context, raw string) (*goquery.Document, string, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	raw, err := s.resolve(raw)
	if err != nil {
		return nil, "", err
	}
	client, err := s.client(ctx)
	if err != nil {
		return nil, "", fmt.Errorf("%w: %v", ErrAuth, err)
	}
	copyClient := *client
	copyClient.CheckRedirect = func(r *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return fmt.Errorf("too many redirects")
		}
		if _, e := s.resolve(r.URL.String()); e != nil {
			return ErrAuth
		}
		if strings.Contains(r.URL.Path, "/log/") || strings.Contains(r.URL.Path, "/login") {
			return ErrAuth
		}
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, "GET", raw, nil)
	if err != nil {
		return nil, "", err
	}
	resp, err := copyClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, "", ctx.Err()
		}
		return nil, "", fmt.Errorf("fetch Themis page: %w", redactError(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		return nil, "", ErrAuth
	}
	if resp.StatusCode != 200 {
		return nil, "", fmt.Errorf("Themis returned HTTP %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 16*1024*1024+1))
	if err != nil {
		return nil, "", err
	}
	if len(b) > 16*1024*1024 {
		return nil, "", fmt.Errorf("Themis result page exceeds 16 MiB")
	}
	d, err := goquery.NewDocumentFromReader(strings.NewReader(string(b)))
	if err != nil {
		return nil, "", err
	}
	if d.Find("input[type=password],form[action*='/log/in'],form[action*='/sso/']").Length() > 0 {
		return nil, "", ErrAuth
	}
	return d, publicURL(resp.Request.URL.String()), nil
}
func redactError(err error) error {
	if u, ok := err.(*url.Error); ok {
		return fmt.Errorf("%s: %w", u.Op, u.Err)
	}
	return err
}

func ParseForm(doc *goquery.Document, assignmentURL string) (Form, error) {
	f := Form{AssignmentURL: publicURL(assignmentURL), Fields: url.Values{}, Suffixes: map[string]string{}, Dates: map[string]string{}}
	f.Title = text(doc.Find("h1").First())
	if f.Title == "" {
		f.Title = strings.TrimSuffix(text(doc.Find("title")), " - Themis")
	}
	f.Account = text(doc.Find("a[href='/user']").First())
	forms := doc.Find("form[data-submitform],form[action*='/submit/']")
	if forms.Length() != 1 {
		reason := text(doc.Find("section.submit,section.submission-form,.notice-error").First())
		if reason == "" {
			doc.Find("p,.notice,.sec-content,.status-icon.warning").Each(func(_ int, n *goquery.Selection) {
				v := text(n)
				if strings.Contains(strings.ToLower(v), "may not submit") && (reason == "" || len(v) < len(reason)) {
					reason = v
				}
			})
		}
		if reason == "" {
			reason = "this assignment has no available submission form"
		}
		return f, fmt.Errorf("cannot submit: %s", reason)
	}
	form := forms.First()
	if form.Find("input[name^='answer-'],textarea[name^='answer-']").Length() > 0 {
		return f, fmt.Errorf("exam and quiz answer forms are not supported")
	}
	if strings.ToLower(form.AttrOr("method", "get")) != "post" || form.Find("input[type=file][name='files[]']").Length() == 0 || form.Find("button[type=submit][disabled],input[type=submit][disabled]").Length() > 0 {
		return f, fmt.Errorf("submission form is unavailable or unsupported")
	}
	base, _ := url.Parse(assignmentURL)
	action, err := url.Parse(form.AttrOr("action", ""))
	if err != nil {
		return f, fmt.Errorf("invalid form action")
	}
	f.Action = base.ResolveReference(action).String()
	f.FoldersAllowed = form.Find("input[type=file][name='folders[]']").Length() > 0
	var countErr, sizeErr error
	f.MaxFiles, countErr = strconv.Atoi(form.AttrOr("data-files", "0"))
	f.MaxBytes, sizeErr = strconv.ParseInt(form.AttrOr("data-filesize", "0"), 10, 64)
	if countErr != nil || sizeErr != nil || f.MaxFiles < 0 || f.MaxBytes < 0 {
		return f, fmt.Errorf("invalid upload limits")
	}
	f.FilenameRule = form.AttrOr("data-regex", "")
	if raw := form.AttrOr("data-suffixes", ""); raw != "" {
		if err := json.Unmarshal([]byte(raw), &f.Suffixes); err != nil {
			return f, fmt.Errorf("invalid language mapping")
		}
	}
	for _, lang := range strings.Split(form.AttrOr("data-languages", ""), ",") {
		if lang = strings.TrimSpace(lang); lang != "" {
			f.Languages = append(f.Languages, lang)
		}
	}
	form.Find("input[type=hidden][name],input[type=checkbox][checked][name],input[type=radio][checked][name]").Each(func(_ int, i *goquery.Selection) {
		if _, disabled := i.Attr("disabled"); !disabled {
			f.Fields.Add(i.AttrOr("name", ""), i.AttrOr("value", "on"))
		}
	})
	form.Find("select[name]").Each(func(_ int, i *goquery.Selection) {
		name := i.AttrOr("name", "")
		options := i.Find("option")
		chosen := options.Filter("[selected]").First()
		if chosen.Length() == 0 {
			chosen = options.First()
		}
		f.Fields.Set(name, chosen.AttrOr("value", text(chosen)))
		if name == "judgeLanguage" {
			options.Each(func(_ int, o *goquery.Selection) {
				v := o.AttrOr("value", text(o))
				if !contains(f.Languages, v) {
					f.Languages = append(f.Languages, v)
				}
			})
		}
	})
	f.DefaultLanguage = f.Fields.Get("judgeLanguage")
	if len(f.Languages) == 0 && f.DefaultLanguage != "" {
		f.Languages = []string{f.DefaultLanguage}
	}
	values := cfg(doc.Selection)
	f.Group = values["group"]
	for _, key := range []string{"start", "deadline", "end", "submitting cooldown", "submission limit"} {
		if v := values[key]; v != "" {
			f.Dates[key] = v
		}
	}
	return f, nil
}
func (s *Service) ReadForm(ctx context.Context, assignment string) (Form, error) {
	client, err := s.client(ctx)
	if err != nil {
		return Form{}, fmt.Errorf("%w: %v", ErrAuth, err)
	}
	ctx = context.WithValue(ctx, clientContextKey{}, client)
	raw, err := s.resolve(assignment)
	if err != nil {
		return Form{}, err
	}
	u, _ := url.Parse(raw)
	if !strings.HasPrefix(u.Path, "/course/") {
		return Form{}, fmt.Errorf("select an assignment course URL")
	}
	d, final, err := s.get(ctx, raw)
	if err != nil {
		return Form{}, err
	}
	f, err := ParseForm(d, final)
	if err != nil {
		return f, err
	}
	if _, err = s.resolve(f.Action); err != nil {
		return Form{}, err
	}
	action, _ := url.Parse(f.Action)
	if !strings.HasPrefix(action.Path, "/submit/") {
		return Form{}, fmt.Errorf("unsupported submission action")
	}
	// Group is authoritative on the status page, not inferred from local state.
	if f.Group == "" {
		d, _, err = s.get(ctx, statsURL(f.AssignmentURL))
		if err != nil {
			return Form{}, fmt.Errorf("read submitting group: %w", err)
		}
		f.Group = cfg(d.Find("section.status").First())["group"]
	}
	if f.Group == "" {
		f.Group = "As assigned by Themis"
	}
	return f, nil
}
func contains(a []string, v string) bool {
	for _, s := range a {
		if s == v {
			return true
		}
	}
	return false
}
func sameRequirements(a, b Form) bool {
	a.Action, b.Action = reviewAction(a.Action), reviewAction(b.Action)
	a.Fields, b.Fields = publicFields(a.Fields), publicFields(b.Fields)
	return reflect.DeepEqual(a, b)
}
func publicFields(fields url.Values) url.Values {
	v := url.Values{}
	for k, values := range fields {
		if k != "_csrf" && k != "sudo" {
			v[k] = append([]string{}, values...)
		}
	}
	return v
}

func reviewAction(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	u.RawQuery = publicFields(u.Query()).Encode()
	return u.String()
}
