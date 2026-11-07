package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"themis-cli/internal/submission"
)

type localEntry struct {
	path, name string
	dir        bool
	size       int64
}
type submissionUI struct {
	service                        *submission.Service
	targetID, assignment, dir      string
	entries                        []localEntry
	selected                       map[string]bool // value is whether this selection is a whole folder
	cursor, offset                 int
	language                       string
	form                           submission.Form
	formError                      string
	prepared                       *submission.Prepared
	armed, busy, sending           bool
	sent                           *atomic.Int64
	generation                     uint64
	cancel                         context.CancelFunc
	editing                        bool
	input                          textinput.Model
	watchGeneration                uint64
	watchActive, watchFetching     bool
	watchCancel                    context.CancelFunc
	watchStarted                   time.Time
	watchURL                       string
	watchTargetID, watchAssignment string
	artifactNavigation             bool
	failures                       int
	result                         submission.Result
	receipt                        submission.Receipt
	resultCursor, resultOffset     int
	refreshPending                 string
}
type uploadOpenedMsg struct {
	generation uint64
	dir        string
	entries    []localEntry
	form       submission.Form
	err        error
}
type uploadPreparedMsg struct {
	generation uint64
	prepared   *submission.Prepared
	err        error
}
type uploadSentMsg struct {
	generation uint64
	receipt    submission.Receipt
	err        error
	persistErr error
}
type submissionUnknownMsg struct {
	generation uint64
	receipt    submission.Receipt
}
type submissionPollMsg struct{ generation uint64 }
type submissionResultMsg struct {
	generation      uint64
	result          submission.Result
	err, persistErr error
}

func (m Model) submissionMode() bool {
	return m.mode == "upload" || m.mode == "review" || m.mode == "sending" || m.mode == "results"
}
func (m *Model) stopSubmission() {
	if m.submit.cancel != nil {
		m.submit.cancel()
	}
	if m.submit.watchCancel != nil {
		m.submit.watchCancel()
	}
	if m.submit.prepared != nil {
		_ = m.submit.prepared.Cleanup()
	}
	m.submit.generation++
	m.submit.watchGeneration++
	m.submit.watchActive = false
}
func readLocalEntries(ctx context.Context, dir string) ([]localEntry, error) {
	info, err := os.Lstat(dir)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("choose a directory")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := []localEntry{}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if entry.Name() == ".git" || entry.Name() == ".themis" || entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		info, e := entry.Info()
		if e != nil {
			return nil, e
		}
		if info.IsDir() || info.Mode().IsRegular() {
			out = append(out, localEntry{filepath.Join(dir, entry.Name()), entry.Name(), info.IsDir(), info.Size()})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].dir != out[j].dir {
			return out[i].dir
		}
		return out[i].name < out[j].name
	})
	return out, nil
}
func (m Model) openUpload() (tea.Model, tea.Cmd) {
	if m.submit.service == nil {
		m.statusText = "submission unavailable"
		return m, nil
	}
	if m.submit.busy {
		m.statusText = "an upload operation is already in progress"
		return m, nil
	}
	node := m.selectedNode()
	if node == nil {
		return m, nil
	}
	dir, err := os.Getwd()
	if err != nil {
		m.statusText = err.Error()
		return m, nil
	}
	if m.submit.prepared != nil {
		_ = m.submit.prepared.Cleanup()
		m.submit.prepared = nil
	}
	m.submit.targetID, m.submit.assignment = node.ID, node.CanonicalURL
	m.submit.selected = map[string]bool{}
	m.submit.language = ""
	m.submit.form = submission.Form{}
	m.submit.formError = ""
	m.submit.entries, m.submit.dir = nil, dir
	m.submit.editing = false
	m.mode = "upload"
	m.submit.cursor, m.submit.offset = 0, 0
	m.preview.focused = false
	return m.loadUploadDirectory(dir, true)
}
func (m Model) loadUploadDirectory(dir string, loadForm bool) (tea.Model, tea.Cmd) {
	if m.submit.cancel != nil {
		m.submit.cancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.submit.cancel = cancel
	m.submit.generation++
	gen := m.submit.generation
	m.submit.busy = true
	m.statusText = "Loading local files and submission requirements…"
	service, assignment, form := m.submit.service, m.submit.assignment, m.submit.form
	return m, func() tea.Msg {
		defer cancel()
		entries, err := readLocalEntries(ctx, dir)
		if err == nil && loadForm {
			form, err = service.ReadForm(ctx, assignment)
		}
		return uploadOpenedMsg{gen, dir, entries, form, err}
	}
}
func (m Model) prepareUpload() (tea.Model, tea.Cmd) {
	selection := submission.Selection{AssignmentURL: m.submit.assignment, Language: m.submit.language}
	paths := []string{}
	for path := range m.submit.selected {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		if m.submit.selected[path] {
			selection.Folders = append(selection.Folders, path)
		} else {
			selection.Files = append(selection.Files, path)
		}
	}
	if len(paths) == 0 {
		m.statusText = "Select files or folders with Space"
		return m, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.submit.cancel = cancel
	m.submit.generation++
	gen := m.submit.generation
	m.submit.busy = true
	m.statusText = "Validating and snapshotting selected files…"
	service := m.submit.service
	return m, func() tea.Msg {
		p, err := service.Prepare(ctx, selection)
		if err != nil {
			cancel()
		}
		if ctx.Err() != nil && p != nil {
			_ = p.Cleanup()
			p = nil
			err = ctx.Err()
		}
		return uploadPreparedMsg{gen, p, err}
	}
}
func (m Model) sendUpload() (tea.Model, tea.Cmd) {
	if m.submit.prepared == nil || !m.submit.armed || m.submit.busy {
		return m, nil
	}
	if m.submit.watchCancel != nil {
		m.submit.watchCancel()
	}
	m.submit.watchGeneration++
	m.submit.watchActive, m.submit.watchFetching = false, false
	m.submit.watchURL = ""
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	m.submit.cancel = cancel
	m.submit.generation++
	gen := m.submit.generation
	m.submit.busy, m.submit.sending = true, true
	m.submit.sent = &atomic.Int64{}
	sent := m.submit.sent
	m.mode = "sending"
	m.statusText = "Uploading reviewed files…"
	service, p := m.submit.service, m.submit.prepared
	return m, func() tea.Msg {
		defer cancel()
		receipt, err := service.Submit(ctx, p, func(n, total int64) { sent.Store(n) })
		var persistErr error
		if service.Store != nil && (err == nil || errors.Is(err, submission.ErrUnknown)) {
			persistErr = service.Store.SaveReceipt(receipt)
		}
		if !errors.Is(err, submission.ErrReview) {
			_ = p.Cleanup()
		}
		return uploadSentMsg{gen, receipt, err, persistErr}
	}
}
func (m *Model) beginWatch(url string) {
	if m.submit.watchCancel != nil {
		m.submit.watchCancel()
	}
	m.submit.watchGeneration++
	m.submit.watchURL = url
	m.submit.watchTargetID, m.submit.watchAssignment = m.submit.targetID, m.submit.assignment
	m.submit.watchStarted = time.Now()
	m.submit.failures = 0
	m.submit.watchActive = true
	m.submit.watchFetching = false
}
func (m Model) openResults() (tea.Model, tea.Cmd) {
	if m.submit.service == nil {
		m.statusText = "submission results unavailable"
		return m, nil
	}
	node := m.selectedNode()
	if node == nil {
		return m, nil
	}
	if m.submit.sending {
		m.statusText = "wait for upload acceptance"
		return m, nil
	}
	m.submit.targetID, m.submit.assignment = node.ID, node.CanonicalURL
	m.mode = "results"
	m.submit.result, m.submit.receipt = submission.Result{}, submission.Receipt{}
	m.submit.artifactNavigation = false
	m.submit.resultCursor, m.submit.resultOffset = 0, 0
	m.preview.focused = false
	status := strings.Replace(node.CanonicalURL, "/course/", "/stats/", 1)
	m.beginWatch(status)
	m.statusText = "Loading submission results…"
	return m, m.fetchSubmission(true)
}
func (m *Model) fetchSubmission(useStore bool) tea.Cmd {
	if m.submit.watchFetching || !m.submit.watchActive {
		return nil
	}
	ctx, cancel := context.WithDeadline(context.Background(), m.submit.watchStarted.Add(15*time.Minute))
	m.submit.watchCancel = cancel
	m.submit.watchFetching = true
	gen, url, assignment, service := m.submit.watchGeneration, m.submit.watchURL, m.submit.watchAssignment, m.submit.service
	return func() tea.Msg {
		defer cancel()
		var cached submission.Result
		if useStore && service.Store != nil {
			record, ok, err := service.Store.Latest(assignment)
			if err != nil {
				return submissionResultMsg{generation: gen, err: err}
			}
			if ok && record.Receipt.State == "unknown" {
				return submissionUnknownMsg{gen, record.Receipt}
			}
			if ok && record.Receipt.SubmissionURL != "" {
				url = record.Receipt.SubmissionURL
				if record.Result != nil {
					cached = *record.Result
				}
			}
		}
		result, err := service.FetchResult(ctx, url)
		var persistErr error
		if err != nil && cached.SubmissionURL != "" {
			result = cached
		}
		if err == nil && service.Store != nil {
			persistErr = service.Store.SaveResult(result)
		}
		return submissionResultMsg{gen, result, err, persistErr}
	}
}
func submissionTick(gen uint64, delay time.Duration) tea.Cmd {
	return tea.Tick(delay, func(time.Time) tea.Msg { return submissionPollMsg{gen} })
}

func (m Model) updateSubmission(msg tea.Msg) (tea.Model, tea.Cmd, bool) {
	switch msg := msg.(type) {
	case uploadOpenedMsg:
		if msg.generation != m.submit.generation {
			return m, nil, true
		}
		m.submit.busy = false
		if msg.err != nil {
			if m.submit.form.Action == "" {
				m.submit.formError = msg.err.Error()
			}
			m.statusText = msg.err.Error()
			return m, nil, true
		}
		m.submit.dir, m.submit.entries, m.submit.form = msg.dir, msg.entries, msg.form
		m.submit.cursor, m.submit.offset = 0, 0
		if len(msg.form.Languages) == 1 {
			m.submit.language = msg.form.Languages[0]
		}
		m.statusText = "Space selects files/folders · Enter reviews the upload"
		return m, nil, true
	case uploadPreparedMsg:
		if msg.generation != m.submit.generation {
			if msg.prepared != nil {
				_ = msg.prepared.Cleanup()
			}
			return m, nil, true
		}
		m.submit.busy = false
		if msg.err != nil {
			m.statusText = msg.err.Error()
			if errors.Is(msg.err, submission.ErrLanguage) {
				m.statusText += " · L changes language"
			}
			return m, nil, true
		}
		m.submit.prepared = msg.prepared
		m.submit.form = msg.prepared.Form
		m.submit.language = msg.prepared.Language
		m.submit.armed = false
		m.submit.offset = 0
		m.mode = "review"
		m.statusText = "Review the exact files; Tab selects Submit"
		return m, nil, true
	case uploadSentMsg:
		if msg.generation != m.submit.generation {
			return m, nil, true
		}
		m.submit.busy, m.submit.sending = false, false
		m.submit.receipt = msg.receipt
		if msg.err != nil {
			var changed *submission.ReviewChanged
			if errors.As(msg.err, &changed) {
				m.submit.prepared.Form = changed.Form
				m.submit.form = changed.Form
				m.submit.armed = false
				m.mode = "review"
				m.statusText = msg.err.Error() + " · L changes language"
				return m, nil, true
			}
			m.submit.prepared = nil
			m.submit.watchTargetID, m.submit.watchAssignment = m.submit.targetID, m.submit.assignment
			m.mode = "results"
			m.submit.result = submission.Result{}
			m.statusText = msg.err.Error() + " · v inspects status; do not retry an uncertain submission"
			if msg.persistErr != nil {
				m.statusText += " · history: " + msg.persistErr.Error()
			}
			return m, nil, true
		}
		m.submit.prepared = nil
		m.mode = "results"
		m.submit.result = submission.Result{}
		m.submit.resultCursor = 0
		m.submit.resultOffset = 0
		m.statusText = "Accepted · watching grading"
		m.submit.refreshPending = m.submit.targetID
		if msg.persistErr != nil {
			m.statusText += " · history: " + msg.persistErr.Error()
		}
		m.beginWatch(msg.receipt.SubmissionURL)
		return m, m.fetchSubmission(false), true
	case submissionUnknownMsg:
		if msg.generation != m.submit.watchGeneration {
			return m, nil, true
		}
		m.submit.watchActive, m.submit.watchFetching = false, false
		m.submit.receipt = msg.receipt
		m.submit.result = submission.Result{}
		m.statusText = "Submission outcome unknown · r inspects status without resubmitting"
		return m, nil, true
	case submissionPollMsg:
		if msg.generation != m.submit.watchGeneration || !m.submit.watchActive {
			return m, nil, true
		}
		if time.Since(m.submit.watchStarted) >= 15*time.Minute {
			m.submit.watchActive = false
			m.statusText = "Watch timed out · r resumes without resubmitting"
			return m, nil, true
		}
		return m, m.fetchSubmission(false), true
	case submissionResultMsg:
		if msg.generation != m.submit.watchGeneration {
			return m, nil, true
		}
		m.submit.watchFetching = false
		if msg.result.SubmissionURL != "" {
			if msg.result.Terminal() && !m.submit.result.Terminal() && m.mode == "results" {
				m.previewKey("r")
			}
			m.submit.result = msg.result
			m.submit.watchURL = msg.result.SubmissionURL
			m.submit.resultCursor = clamp(m.submit.resultCursor, 0, maxInt(0, len(msg.result.Artifacts)-1))
		}
		if msg.err != nil {
			m.submit.failures++
			m.statusText = msg.err.Error()
			if errors.Is(msg.err, submission.ErrAuth) || time.Since(m.submit.watchStarted) >= 15*time.Minute || errors.Is(msg.err, context.Canceled) || m.submit.failures >= 5 {
				m.submit.watchActive = false
				m.statusText += " · watch paused; r resumes"
				return m, nil, true
			}
			delay := submission.Backoff(m.submit.failures)
			m.statusText += fmt.Sprintf(" · retrying in %s", delay)
			return m, submissionTick(m.submit.watchGeneration, delay), true
		}
		m.submit.failures = 0
		m.statusText = "Submission: " + msg.result.RawStatus
		if msg.persistErr != nil {
			m.statusText += " · history: " + msg.persistErr.Error()
		}
		if msg.result.Terminal() {
			m.submit.watchActive = false
			m.submit.refreshPending = m.submit.watchTargetID
			return m, nil, true
		}
		return m, submissionTick(m.submit.watchGeneration, 2*time.Second), true
	case tea.KeyMsg:
		if !m.submissionMode() {
			return m, nil, false
		}
		key := msg.String()
		// A path is ordinary text, so q, u, ?, etc. must reach the input widget.
		if m.submit.editing {
			if key == "ctrl+c" {
				m.stopSubmission()
				return m, tea.Quit, true
			}
			if key == "esc" {
				m.submit.editing = false
				return m, nil, true
			}
			if key == "enter" {
				m.submit.editing = false
				p := m.submit.input.Value()
				if !filepath.IsAbs(p) {
					p = filepath.Join(m.submit.dir, p)
				}
				next, cmd := m.loadUploadDirectory(filepath.Clean(p), false)
				return next, cmd, true
			}
			var cmd tea.Cmd
			m.submit.input, cmd = m.submit.input.Update(msg)
			return m, cmd, true
		}
		if key == "q" || key == "ctrl+c" || key == "?" || m.showHelp {
			return m, nil, false
		}
		if m.mode == "sending" {
			if key == "esc" && m.submit.cancel != nil {
				m.submit.cancel()
				m.statusText = "Stopping transfer; sent bytes cannot be undone"
			}
			return m, nil, true
		}
		if key == "esc" {
			if m.submit.cancel != nil {
				m.submit.cancel()
			}
			m.submit.generation++
			m.submit.busy = false
			if m.submit.prepared != nil {
				_ = m.submit.prepared.Cleanup()
				m.submit.prepared = nil
			}
			if m.mode == "review" {
				m.mode = "upload"
			} else {
				m.mode = "browse"
			}
			return m, nil, true
		}
		if m.submit.busy {
			return m, nil, true
		}
		if m.mode == "review" {
			switch key {
			case "tab", "shift+tab":
				m.submit.armed = !m.submit.armed
			case "L":
				m.cycleSubmissionLanguage()
			case "j", "down":
				m.submit.offset++
			case "k", "up":
				m.submit.offset = maxInt(0, m.submit.offset-1)
			case "pgdown":
				m.submit.offset += m.panelContentHeight() - 3
			case "pgup":
				m.submit.offset = maxInt(0, m.submit.offset-m.panelContentHeight()+3)
			case "g", "home":
				m.submit.offset = 0
			case "G", "end":
				m.submit.offset = 100000
			case "enter":
				if m.submit.armed {
					if err := m.submit.prepared.ApplyReview(m.submit.form, m.submit.language); err != nil {
						m.statusText = err.Error()
						return m, nil, true
					}
					next, cmd := m.sendUpload()
					return next, cmd, true
				}
				_ = m.submit.prepared.Cleanup()
				m.submit.prepared = nil
				m.mode = "upload"
			}
			return m, nil, true
		}
		if m.mode == "results" {
			if key == "r" && !m.preview.focused {
				url := m.submit.watchURL
				if url == "" {
					url = m.submit.receipt.StatusURL
				}
				if url != "" {
					m.submit.targetID, m.submit.assignment = m.submit.watchTargetID, m.submit.watchAssignment
					m.beginWatch(url)
					return m, m.fetchSubmission(false), true
				}
			}
			if key == "v" {
				next, cmd := m.openResults()
				return next, cmd, true
			}
			if m.previewKey(key) {
				return m, nil, true
			}
			if key == "j" || key == "down" || key == "k" || key == "up" || key == "g" || key == "home" || key == "G" || key == "end" {
				m.submit.artifactNavigation = true
			}
			if key == "pgup" || key == "pgdown" {
				m.submit.artifactNavigation = false
			}
			switch key {
			case "j", "down":
				m.submit.resultCursor++
			case "k", "up":
				m.submit.resultCursor--
			case "g", "home":
				m.submit.resultCursor = 0
			case "G", "end":
				m.submit.resultCursor = len(m.submit.result.Artifacts) - 1
			case "pgdown":
				m.submit.resultOffset += m.panelContentHeight() - 3
			case "pgup":
				m.submit.resultOffset = maxInt(0, m.submit.resultOffset-m.panelContentHeight()+3)
			}
			m.submit.resultCursor = clamp(m.submit.resultCursor, 0, maxInt(0, len(m.submit.result.Artifacts)-1))
			return m, nil, true
		}
		if m.previewKey(key) {
			return m, nil, true
		}
		switch key {
		case "/":
			m.submit.editing = true
			m.submit.input = textinput.New()
			m.submit.input.SetValue(m.submit.dir)
			m.submit.input.Width = maxInt(8, m.width-12)
			m.submit.input.PlaceholderStyle = secondaryStyle
			return m, m.submit.input.Focus(), true
		case "L":
			m.cycleSubmissionLanguage()
		case "j", "down":
			m.submit.cursor++
		case "k", "up":
			m.submit.cursor--
		case "g", "home":
			m.submit.cursor = 0
		case "G", "end":
			m.submit.cursor = len(m.submit.entries) - 1
		case "pgdown":
			m.submit.cursor += maxInt(1, m.panelContentHeight()-5)
		case "pgup":
			m.submit.cursor -= maxInt(1, m.panelContentHeight()-5)
		case "h", "left":
			next, cmd := m.loadUploadDirectory(filepath.Dir(m.submit.dir), false)
			return next, cmd, true
		case "l", "right":
			if entry := m.uploadEntry(); entry != nil && entry.dir {
				next, cmd := m.loadUploadDirectory(entry.path, false)
				return next, cmd, true
			}
		case " ":
			if entry := m.uploadEntry(); entry != nil {
				if _, ok := m.submit.selected[entry.path]; ok {
					delete(m.submit.selected, entry.path)
				} else {
					m.submit.selected[entry.path] = entry.dir
				}
			}
		case "enter":
			next, cmd := m.prepareUpload()
			return next, cmd, true
		}
		m.submit.cursor = clamp(m.submit.cursor, 0, maxInt(0, len(m.submit.entries)-1))
		return m, nil, true
	}
	return m, nil, false
}
func (m *Model) cycleSubmissionLanguage() {
	languages := m.submit.form.Languages
	if len(languages) == 0 {
		return
	}
	index := -1
	for i, l := range languages {
		if l == m.submit.language {
			index = i
		}
	}
	m.submit.language = languages[(index+1)%len(languages)]
	m.statusText = "Judge language: " + m.submit.language
}
func (m Model) uploadEntry() *localEntry {
	if len(m.submit.entries) == 0 {
		return nil
	}
	entry := m.submit.entries[clamp(m.submit.cursor, 0, len(m.submit.entries)-1)]
	return &entry
}
