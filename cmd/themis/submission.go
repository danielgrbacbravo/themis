package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"time"

	"golang.org/x/term"
	"themis-cli/internal/discovery"
	"themis-cli/internal/state"
	"themis-cli/internal/submission"
	"themis-cli/internal/themis"
)

type pathsFlag []string

func (p *pathsFlag) String() string         { return strings.Join(*p, ", ") }
func (p *pathsFlag) Set(value string) error { *p = append(*p, value); return nil }

type requestContextTransport struct {
	ctx       context.Context
	transport http.RoundTripper
}

func (t requestContextTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return t.transport.RoundTrip(r.Clone(t.ctx))
}
func submissionService(common *commonFlags) *submission.Service {
	return &submission.Service{BaseURL: common.baseURL, Store: &submission.Store{Path: submission.DefaultStorePath()}, Client: func() (*http.Client, error) {
		s, err := themis.LoadSessionWithAuthConfig(common.baseURL, themis.AuthConfig{SessionFile: common.sessionFile})
		if err != nil {
			return nil, err
		}
		return s.Client, nil
	}, AfterChange: func(ctx context.Context, assignment string) error {
		session, err := themis.LoadSessionWithAuthConfig(common.baseURL, themis.AuthConfig{SessionFile: common.sessionFile})
		if err != nil {
			return err
		}
		transport := session.Client.Transport
		if transport == nil {
			transport = http.DefaultTransport
		}
		session.Client.Transport = requestContextTransport{ctx, transport}
		session.Client.Timeout = 20 * time.Second
		statePath, err := state.DefaultStatePath()
		if err != nil {
			return err
		}
		st, err := state.Load(statePath)
		if err != nil {
			return err
		}
		result, err := discovery.NewService(common.baseURL).RefreshNode(session.Client, &st, assignment, 0)
		if err != nil {
			return err
		}
		if len(result.Errors) > 0 {
			return fmt.Errorf("%s", strings.Join(result.Errors, "; "))
		}
		return state.SaveAtomic(statePath, st, true)
	}}
}

type submissionOutput struct {
	Status   string               `json:"status"`
	Prepared *submission.Prepared `json:"prepared,omitempty"`
	Receipt  *submission.Receipt  `json:"receipt,omitempty"`
	Result   *submission.Result   `json:"result,omitempty"`
	Error    string               `json:"error,omitempty"`
}

func writeSubmissionOutput(out submissionOutput, asJSON bool) {
	if asJSON {
		_ = json.NewEncoder(os.Stdout).Encode(out)
		return
	}
	if out.Error != "" {
		fmt.Fprintln(os.Stderr, out.Error)
	}
	if out.Receipt != nil {
		fmt.Printf("%s\nStatus: %s\n", out.Receipt.State, out.Receipt.StatusURL)
		if out.Receipt.SubmissionURL != "" {
			fmt.Println("Submission:", out.Receipt.SubmissionURL)
		}
	}
	if out.Result != nil {
		printSubmissionResult(os.Stdout, *out.Result)
	}
}
func printSubmissionResult(w io.Writer, r submission.Result) {
	fmt.Fprintf(w, "%s: %s\n", r.Title, r.RawStatus)
	if r.Score != "" {
		fmt.Fprintln(w, "Score:", r.Score)
	}
	if r.KnownTotal > 0 {
		fmt.Fprintf(w, "Tests completed: %d/%d\n", r.Completed, r.KnownTotal)
	} else {
		fmt.Fprintln(w, "Test progress: not available")
	}
	for _, t := range r.Tests {
		fmt.Fprintf(w, "  %s  %s  %s\n", t.Name, t.RawStatus, t.Message)
	}
	for _, a := range r.Artifacts {
		fmt.Fprintf(w, "  %s: %s\n", a.Name, a.URL)
	}
}
func printReview(w io.Writer, p *submission.Prepared) {
	fmt.Fprintf(w, "Assignment: %s\n%s\nAccount: %s\nGroup: %s\nLanguage: %s\nFiles: %d / %d maximum (0 = unspecified)\nCombined size: %d / %d bytes maximum (0 = unspecified)\n", p.Form.Title, p.Form.AssignmentURL, p.Form.Account, p.Form.Group, p.Language, len(p.Files), p.Form.MaxFiles, p.TotalBytes, p.Form.MaxBytes)
	for _, key := range []string{"start", "deadline", "end", "submitting cooldown", "submission limit"} {
		if v := p.Form.Dates[key]; v != "" {
			fmt.Fprintf(w, "%s: %s\n", key, v)
		}
	}
	for _, f := range p.Files {
		fmt.Fprintf(w, "  %s  (%d B)  ← %s\n", f.Name, f.Size, f.Source)
	}
}
func submissionExit(result *submission.Result, err error) int {
	if errors.Is(err, context.Canceled) {
		return 130
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, submission.ErrUnknown) || errors.Is(err, submission.ErrPaused) {
		return 3
	}
	if err != nil {
		return 1
	}
	if result != nil && (result.Status == "failed" || result.Status == "unjudged") {
		return 2
	}
	return 0
}
func refreshAfterSubmission(ctx context.Context, s *submission.Service, assignment string) {
	if s.AfterChange != nil && assignment != "" {
		if err := s.AfterChange(ctx, assignment); err != nil {
			fmt.Fprintln(os.Stderr, "Submission recorded, but course refresh failed:", err)
		}
	}
}
func watchSubmission(ctx context.Context, s *submission.Service, url string, timeout time.Duration) (submission.Result, error) {
	return s.Watch(ctx, url, submission.WatchOptions{Timeout: timeout}, func(event submission.WatchEvent) {
		if event.Result != nil {
			r := event.Result
			if r.KnownTotal > 0 {
				fmt.Fprintf(os.Stderr, "%s · %d/%d known tests complete\n", r.RawStatus, r.Completed, r.KnownTotal)
			} else {
				fmt.Fprintf(os.Stderr, "%s · test progress unavailable\n", r.RawStatus)
			}
		} else if event.Err != nil {
			if event.Next > 0 {
				fmt.Fprintf(os.Stderr, "%v; next check in %s\n", event.Err, event.Next)
			} else {
				fmt.Fprintln(os.Stderr, event.Err)
			}
		}
	})
}
func runSubmit(args []string) int {
	fs := newFlagSet("submit")
	common := addCommonFlags(fs)
	assignment := fs.String("assignment-url", "", "Assignment course URL")
	var files, folders pathsFlag
	fs.Var(&files, "file", "Local file (repeatable)")
	fs.Var(&folders, "folder", "Local folder (repeatable)")
	language := fs.String("language", "", "Judge language")
	yes := fs.Bool("yes", false, "Submit the reviewed file manifest without an interactive prompt")
	dry := fs.Bool("dry-run", false, "Prepare and validate without uploading")
	watch := fs.Bool("watch", true, "Watch grading after acceptance")
	timeout := fs.Duration("timeout", 15*time.Minute, "Maximum grading watch duration")
	if err := fs.Parse(args); err != nil {
		return submissionFlagError(fs, args, err)
	}
	output := submissionOutput{Status: "error"}
	finish := func(err error) int {
		if err != nil {
			output.Error = err.Error()
		}
		writeSubmissionOutput(output, common.jsonOutput)
		return submissionExit(output.Result, err)
	}
	if *assignment == "" || fs.NArg() > 0 || *timeout <= 0 {
		return finish(fmt.Errorf("provide --assignment-url and --file/--folder; timeout must be positive"))
	}
	interactive := term.IsTerminal(int(os.Stdin.Fd()))
	if !*dry && !*yes && !interactive {
		return finish(fmt.Errorf("noninteractive submission requires --yes; use --dry-run to review first"))
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	s := submissionService(common)
	selection := submission.Selection{AssignmentURL: *assignment, Files: files, Folders: folders, Language: *language}
	input := bufio.NewReader(os.Stdin)
	prepared, err := s.Prepare(ctx, selection)
	if errors.Is(err, submission.ErrLanguage) && interactive && !*yes {
		form, e := s.ReadForm(ctx, *assignment)
		if e != nil {
			return finish(e)
		}
		fmt.Fprintf(os.Stderr, "Select language (%s): ", strings.Join(form.Languages, ", "))
		line, e := readSubmissionInput(ctx, input)
		if e != nil {
			return finish(e)
		}
		selection.Language = strings.TrimSpace(line)
		prepared, err = s.Prepare(ctx, selection)
	}
	if err != nil {
		return finish(err)
	}
	defer prepared.Cleanup()
	if *dry {
		output.Status = "prepared"
		output.Prepared = prepared
		if !common.jsonOutput {
			printReview(os.Stdout, prepared)
		}
		return finish(nil)
	}
	for {
		printReview(os.Stderr, prepared)
		if !*yes {
			fmt.Fprint(os.Stderr, "Type submit to send these files: ")
			line, e := readSubmissionInput(ctx, input)
			if e != nil {
				return finish(e)
			}
			if strings.TrimSpace(line) != "submit" {
				return finish(context.Canceled)
			}
		}
		var last int64 = -1
		receipt, e := s.Submit(ctx, prepared, func(sent, total int64) {
			percent := int64(100)
			if total > 0 {
				percent = sent * 100 / total
			}
			if percent != last && (percent == 100 || percent/10 != last/10) {
				fmt.Fprintf(os.Stderr, "Uploading: %d/%d bytes\n", sent, total)
				last = percent
			}
		})
		var changed *submission.ReviewChanged
		if errors.As(e, &changed) {
			if !interactive || *yes {
				return finish(e)
			}
			fmt.Fprintln(os.Stderr, e)
			if err = prepared.ApplyReview(changed.Form, prepared.Language); errors.Is(err, submission.ErrLanguage) {
				fmt.Fprintf(os.Stderr, "Select language (%s): ", strings.Join(changed.Form.Languages, ", "))
				line, e := readSubmissionInput(ctx, input)
				if e != nil {
					return finish(e)
				}
				err = prepared.ApplyReview(changed.Form, strings.TrimSpace(line))
			}
			if err != nil {
				return finish(err)
			}
			continue
		}
		if cleanupErr := prepared.Cleanup(); cleanupErr != nil {
			fmt.Fprintln(os.Stderr, "Could not remove upload snapshot:", cleanupErr)
		}
		if e != nil {
			if errors.Is(e, submission.ErrUnknown) {
				output.Status = "unknown"
				output.Receipt = &receipt
				if saveErr := s.Store.SaveReceipt(receipt); saveErr != nil {
					fmt.Fprintln(os.Stderr, "Could not save uncertain receipt:", saveErr)
				}
			}
			if ctx.Err() != nil {
				return finish(fmt.Errorf("%v: %w", e, ctx.Err()))
			}
			return finish(e)
		}
		output.Status = "accepted"
		output.Receipt = &receipt
		if err = s.Store.SaveReceipt(receipt); err != nil {
			output.Status = "accepted"
			fmt.Fprintln(os.Stderr, "Accepted; could not persist receipt:", err)
		}
		refreshAfterSubmission(ctx, s, receipt.AssignmentURL)
		if !*watch {
			return finish(nil)
		}
		result, e := watchSubmission(ctx, s, receipt.SubmissionURL, *timeout)
		if result.SubmissionURL != "" {
			output.Result = &result
			output.Status = result.Status
		}
		if e == nil && result.Terminal() {
			refreshAfterSubmission(ctx, s, receipt.AssignmentURL)
		}
		return finish(e)
	}
}
func runSubmissionStatus(args []string) int {
	fs := newFlagSet("status")
	common := addCommonFlags(fs)
	raw := fs.String("submission-url", "", "Concrete submission URL or assignment status page")
	watch := fs.Bool("watch", false, "Watch grading until terminal")
	timeout := fs.Duration("timeout", 15*time.Minute, "Maximum watch duration")
	if err := fs.Parse(args); err != nil {
		return submissionFlagError(fs, args, err)
	}
	out := submissionOutput{Status: "error"}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	var result submission.Result
	var err error
	if *raw == "" || fs.NArg() > 0 || *timeout <= 0 {
		err = fmt.Errorf("provide --submission-url and a positive timeout")
	} else {
		s := submissionService(common)
		result, err = s.FetchResult(ctx, *raw)
		if err == nil {
			if saveErr := s.Store.SaveResult(result); saveErr != nil {
				fmt.Fprintln(os.Stderr, "Could not save results:", saveErr)
			}
			if *watch && !result.Terminal() {
				result, err = watchSubmission(ctx, s, result.SubmissionURL, *timeout)
			}
			if err == nil && result.Terminal() {
				refreshAfterSubmission(ctx, s, result.AssignmentURL)
			}
		}
	}
	if result.SubmissionURL != "" {
		out.Result = &result
		out.Status = result.Status
	}
	if err != nil {
		out.Error = err.Error()
	}
	writeSubmissionOutput(out, common.jsonOutput)
	return submissionExit(out.Result, err)
}

// A blocked terminal read must not prevent Ctrl+C from cancelling review.
func readSubmissionInput(ctx context.Context, input *bufio.Reader) (string, error) {
	type answer struct {
		line string
		err  error
	}
	ch := make(chan answer, 1)
	go func() { line, err := input.ReadString('\n'); ch <- answer{line, err} }()
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case a := <-ch:
		return a.line, a.err
	}
}

func submissionFlagError(fs *flag.FlagSet, args []string, err error) int {
	if errors.Is(err, flag.ErrHelp) {
		fs.SetOutput(os.Stdout)
		fmt.Fprintf(os.Stdout, "Usage: themis %s [flags]\n", fs.Name())
		fs.PrintDefaults()
		return 0
	}
	writeSubmissionOutput(submissionOutput{Status: "error", Error: err.Error()}, wantsJSON(args))
	return 1
}
