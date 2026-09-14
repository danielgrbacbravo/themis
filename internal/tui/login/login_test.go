package login

import (
	"errors"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
	"strings"
	"testing"
)

func TestNewModel_PrefillSavePasswordImpliesSaveUsername(t *testing.T) {
	m := newModel(Prefill{
		Username:     "s1234567",
		Password:     "secret",
		SaveUsername: false,
		SavePassword: true,
	}, nil)

	if !m.saveUsername {
		t.Fatalf("expected saveUsername=true when savePassword=true")
	}
	if !m.savePassword {
		t.Fatalf("expected savePassword=true")
	}
	if got := m.usernameInput.Value(); got != "s1234567" {
		t.Fatalf("unexpected prefilled username: got %q", got)
	}
	if got := m.passwordInput.Value(); got != "secret" {
		t.Fatalf("unexpected prefilled password: got %q", got)
	}
}

func TestStartSubmit_ValidatesRequiredFields(t *testing.T) {
	called := false
	m := newModel(Prefill{}, func(req SubmitRequest) (SubmitResult, error) {
		called = true
		return SubmitResult{}, nil
	})
	m.focusIndex = 5

	out, cmd := m.startSubmit()
	next := out.(model)

	if called {
		t.Fatalf("submit should not be called with empty required fields")
	}
	if cmd != nil {
		t.Fatalf("expected nil command for validation failure")
	}
	if next.submitting {
		t.Fatalf("expected submitting=false on validation failure")
	}
	if next.statusLine == "" {
		t.Fatalf("expected validation error status")
	}
}

func TestStartSubmit_SuccessAndFailureMessages(t *testing.T) {
	submitErr := errors.New("bad credentials")
	m := newModel(Prefill{}, func(req SubmitRequest) (SubmitResult, error) {
		if req.TOTP == "111111" {
			return SubmitResult{}, submitErr
		}
		return SubmitResult{UserFullName: "Jane Doe", UserEmail: "jane@example.com"}, nil
	})
	m.focusIndex = 5
	m.usernameInput.SetValue("s1234567")
	m.passwordInput.SetValue("secret")
	m.totpInput.SetValue("111111")

	out, cmd := m.startSubmit()
	next := out.(model)
	if cmd == nil {
		t.Fatalf("expected async submit cmd")
	}
	if !next.submitting {
		t.Fatalf("expected submitting=true after submit start")
	}

	msg := cmd().(submitFinishedMsg)
	updated, _ := next.Update(msg)
	afterFail := updated.(model)
	if afterFail.submitting {
		t.Fatalf("expected submitting=false after submit finishes")
	}
	if afterFail.submitErr == nil {
		t.Fatalf("expected submitErr to be recorded")
	}
	if afterFail.statusLine == "" {
		t.Fatalf("expected failure status line")
	}

	afterFail.totpInput.SetValue("222222")
	out, cmd = afterFail.startSubmit()
	next = out.(model)
	if cmd == nil {
		t.Fatalf("expected cmd for retry submit")
	}
	msg = cmd().(submitFinishedMsg)
	updated, _ = next.Update(msg)
	afterSuccess := updated.(model)
	if afterSuccess.submitErr != nil {
		t.Fatalf("expected submitErr cleared on success, got %v", afterSuccess.submitErr)
	}
	if afterSuccess.submitResult.UserEmail != "jane@example.com" {
		t.Fatalf("unexpected success result: %+v", afterSuccess.submitResult)
	}
	if afterSuccess.statusLine == "" {
		t.Fatalf("expected success status line")
	}
}

func TestLoginResizeKeepsFocusedFieldVisible(t *testing.T) {
	m := newModel(Prefill{Username: strings.Repeat("student", 30), Password: strings.Repeat("secret", 30)}, nil)
	for _, size := range [][2]int{{0, 0}, {1, 1}, {23, 7}, {24, 8}, {40, 10}, {80, 24}, {24, 8}} {
		updated, _ := m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		m = updated.(model)
		for i := 0; i < 6; i++ {
			view := m.View()
			if size[0] <= 0 || size[1] <= 0 {
				if view != "" {
					t.Fatal("rendered before terminal size")
				}
			} else if lipgloss.Width(view) > size[0] || lipgloss.Height(view) > size[1] {
				t.Fatalf("login overflow at %v: %s", size, view)
			}
			if size[0] >= 24 && size[1] >= 8 {
				label := []string{"Username", "Password", "TOTP", "Save username", "Save password", "Submit"}[m.focusIndex]
				if !strings.Contains(view, label) {
					t.Fatalf("focused %s hidden at %v", label, size)
				}
				if strings.Contains(view, "secret") {
					t.Fatal("password leaked into view")
				}
			}
			m.moveFocus("tab")
		}
	}
	if m.usernameInput.Value() != strings.Repeat("student", 30) {
		t.Fatal("resizing modified username")
	}
}

func TestLoginCanResizeAndCancelWhileSubmitting(t *testing.T) {
	m := newModel(Prefill{}, nil)
	m.submitting = true
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 30, Height: 8})
	m = updated.(model)
	if m.width != 30 || m.height != 8 {
		t.Fatal("ignored resize while submitting")
	}
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd == nil || !updated.(model).canceled {
		t.Fatal("cannot cancel pending login")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatal("cancel did not quit")
	}
}

func TestLoginPlaceholdersUseTerminalPalette(t *testing.T) {
	profile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(profile)
	m := newModel(Prefill{}, nil)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = updated.(model)
	view := m.View()
	if strings.Contains(view, "38;") || strings.Contains(view, "48;") {
		t.Fatalf("login emitted fixed colors: %q", view)
	}
	if !strings.Contains(view, "\x1b[90m") {
		t.Fatal("placeholder is not using the terminal muted color")
	}
}
