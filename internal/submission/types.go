// Package submission implements reviewed, single-attempt uploads and result tracking.
package submission

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"sync"
	"time"
)

var (
	ErrAuth     = errors.New("authentication expired; sign in and retry")
	ErrUnknown  = errors.New("submission outcome unknown")
	ErrReview   = errors.New("submission requirements changed; review again")
	ErrLanguage = errors.New("choose a permitted judge language")
	ErrPaused   = errors.New("watch paused after five consecutive failures")
)

type Form struct {
	AssignmentURL   string            `json:"assignment_url"`
	Title           string            `json:"title"`
	Account         string            `json:"account"`
	Group           string            `json:"group"`
	Languages       []string          `json:"languages"`
	DefaultLanguage string            `json:"default_language"`
	Suffixes        map[string]string `json:"suffixes"`
	FoldersAllowed  bool              `json:"folders_allowed"`
	MaxFiles        int               `json:"max_files"`
	MaxBytes        int64             `json:"max_bytes"`
	FilenameRule    string            `json:"filename_rule,omitempty"`
	Dates           map[string]string `json:"dates,omitempty"`
	Action          string            `json:"-"`
	Fields          url.Values        `json:"-"`
}
type Selection struct {
	AssignmentURL string   `json:"assignment_url"`
	Files         []string `json:"files,omitempty"`
	Folders       []string `json:"folders,omitempty"`
	Language      string   `json:"language,omitempty"`
}
type File struct {
	Source   string `json:"source"`
	Name     string `json:"name"`
	Field    string `json:"field"`
	Size     int64  `json:"size"`
	SHA256   string `json:"sha256"`
	Snapshot string `json:"-"`
}
type Prepared struct {
	Form        Form   `json:"assignment"`
	Files       []File `json:"files"`
	Language    string `json:"language"`
	TotalBytes  int64  `json:"total_bytes"`
	dir         string
	mu          sync.Mutex
	attempted   bool
	stopCleanup func() bool
}
type ReviewChanged struct{ Form Form }

func (e *ReviewChanged) Error() string { return ErrReview.Error() }
func (e *ReviewChanged) Unwrap() error { return ErrReview }

type Receipt struct {
	AssignmentURL string    `json:"assignment_url"`
	SubmissionURL string    `json:"submission_url,omitempty"`
	StatusURL     string    `json:"status_url"`
	State         string    `json:"state"`
	Warning       string    `json:"warning,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
}
type Artifact struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}
type TestResult struct {
	Name      string     `json:"name"`
	RawStatus string     `json:"raw_status"`
	Status    string     `json:"status"`
	Message   string     `json:"message,omitempty"`
	Hint      string     `json:"hint,omitempty"`
	Artifacts []Artifact `json:"artifacts,omitempty"`
}
type Result struct {
	SubmissionURL string       `json:"submission_url"`
	AssignmentURL string       `json:"assignment_url"`
	Title         string       `json:"title"`
	Group         string       `json:"group,omitempty"`
	RawStatus     string       `json:"raw_status"`
	Status        string       `json:"status"`
	Message       string       `json:"message,omitempty"`
	Score         string       `json:"score,omitempty"`
	Tests         []TestResult `json:"tests"`
	Completed     int          `json:"completed_tests"`
	KnownTotal    int          `json:"known_tests"`
	Artifacts     []Artifact   `json:"artifacts,omitempty"`
	UpdatedAt     time.Time    `json:"updated_at"`
}

func (r Result) Terminal() bool {
	return r.Status == "passed" || r.Status == "failed" || r.Status == "unjudged"
}

type Service struct {
	BaseURL string
	Client  func() (*http.Client, error)
	Store   *Store
	// AfterChange refreshes authoritative course statistics; failures are reported
	// separately, never converting an accepted upload into a retryable failure.
	AfterChange func(context.Context, string) error
}
type Progress func(sent, total int64)
type WatchEvent struct {
	Result *Result
	Err    error
	Next   time.Duration
}
type WatchOptions struct {
	Timeout  time.Duration
	Interval time.Duration
}
