package login

import (
	"errors"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"themis-cli/internal/tui/textutil"
	"themis-cli/internal/tui/theme"
)

type Prefill struct {
	Username     string
	Password     string
	SaveUsername bool
	SavePassword bool
}

type SubmitRequest struct {
	Username     string
	Password     string
	TOTP         string
	SaveUsername bool
	SavePassword bool
}

type SubmitResult struct {
	UserFullName string
	UserEmail    string
}

type SubmitFunc func(req SubmitRequest) (SubmitResult, error)

var (
	titleStyle   = lipgloss.NewStyle().Bold(true).Foreground(theme.Accent)
	labelStyle   = lipgloss.NewStyle()
	focusStyle   = lipgloss.NewStyle().Bold(true).Foreground(theme.Info)
	errorStyle   = lipgloss.NewStyle().Foreground(theme.Error)
	successStyle = lipgloss.NewStyle().Foreground(theme.Success)
	mutedStyle   = lipgloss.NewStyle().Foreground(theme.Muted)
)

func Run(prefill Prefill, submit SubmitFunc) (SubmitResult, error) {
	m := newModel(prefill, submit)
	program := tea.NewProgram(m, tea.WithAltScreen())
	final, err := program.Run()
	if err != nil {
		return SubmitResult{}, err
	}
	m, ok := final.(model)
	if !ok {
		return SubmitResult{}, fmt.Errorf("unexpected login model type")
	}
	if m.canceled {
		return SubmitResult{}, errors.New("login canceled")
	}
	if m.submitErr != nil {
		return SubmitResult{}, m.submitErr
	}
	return m.submitResult, nil
}

type submitFinishedMsg struct {
	result SubmitResult
	err    error
}

type model struct {
	width, height int
	usernameInput textinput.Model
	passwordInput textinput.Model
	totpInput     textinput.Model
	saveUsername  bool
	savePassword  bool
	focusIndex    int
	submitting    bool
	submitResult  SubmitResult
	submitErr     error
	statusLine    string
	canceled      bool
	submit        SubmitFunc
}

func newModel(prefill Prefill, submit SubmitFunc) model {
	username := textinput.New()
	username.PlaceholderStyle = mutedStyle
	username.Placeholder = "s1234567"
	username.SetValue(strings.TrimSpace(prefill.Username))
	username.Focus()

	password := textinput.New()
	password.PlaceholderStyle = mutedStyle
	password.Placeholder = "password"
	password.EchoMode = textinput.EchoPassword
	password.EchoCharacter = '*'
	password.SetValue(prefill.Password)

	totp := textinput.New()
	totp.PlaceholderStyle = mutedStyle
	totp.Placeholder = "123456"
	totp.CharLimit = 12

	if prefill.SavePassword && !prefill.SaveUsername {
		prefill.SaveUsername = true
	}

	return model{
		usernameInput: username,
		passwordInput: password,
		totpInput:     totp,
		saveUsername:  prefill.SaveUsername,
		savePassword:  prefill.SavePassword,
		focusIndex:    0,
		submit:        submit,
	}
}

func (m model) Init() tea.Cmd { return nil }

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// Resizing and cancellation remain available while authentication is pending.
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		inputWidth := msg.Width - 13 // label, prompt, and cursor
		if inputWidth < 1 {
			inputWidth = 1
		}
		m.usernameInput.Width, m.passwordInput.Width, m.totpInput.Width = inputWidth, inputWidth, inputWidth
		// Recompute each input's horizontal viewport after its Width changes.
		m.usernameInput, _ = m.usernameInput.Update(msg)
		m.passwordInput, _ = m.passwordInput.Update(msg)
		m.totpInput, _ = m.totpInput.Update(msg)
		return m, nil
	case tea.KeyMsg:
		if msg.String() == "ctrl+c" || msg.String() == "esc" {
			m.canceled = true
			return m, tea.Quit
		}
	}
	if m.submitting {
		switch msg := msg.(type) {
		case submitFinishedMsg:
			m.submitting = false
			m.submitResult = msg.result
			m.submitErr = msg.err
			if msg.err != nil {
				m.statusLine = errorStyle.Render(msg.err.Error())
				return m, nil
			}
			m.statusLine = successStyle.Render(fmt.Sprintf("Login successful: %s (%s)", msg.result.UserFullName, msg.result.UserEmail))
			return m, tea.Quit
		}
		return m, nil
	}

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "tab", "shift+tab", "up", "down":
			m.moveFocus(msg.String())
			return m, nil
		case "enter":
			if m.focusIndex == 5 {
				return m.startSubmit()
			}
			m.moveFocus("tab")
			return m, nil
		case " ":
			if m.focusIndex == 3 {
				m.saveUsername = !m.saveUsername
				if !m.saveUsername {
					m.savePassword = false
				}
				return m, nil
			}
			if m.focusIndex == 4 {
				m.savePassword = !m.savePassword
				if m.savePassword {
					m.saveUsername = true
				}
				return m, nil
			}
		}
	}

	var cmd tea.Cmd
	switch m.focusIndex {
	case 0:
		m.usernameInput, cmd = m.usernameInput.Update(msg)
	case 1:
		m.passwordInput, cmd = m.passwordInput.Update(msg)
	case 2:
		m.totpInput, cmd = m.totpInput.Update(msg)
	}
	return m, cmd
}

func (m model) View() string {
	if m.width <= 0 || m.height <= 0 {
		return ""
	}
	if m.width < 24 || m.height < 8 {
		return textutil.Fit("Terminal too small\nResize to 24 x 8\nEsc cancels", m.width, m.height)
	}
	submitLabel := "[ Submit ]"
	if m.submitting {
		submitLabel = "[ Submitting... ]"
	}
	if m.focusIndex == 5 {
		submitLabel = focusStyle.Render(submitLabel)
	}
	fields := []string{
		m.renderField("Username", m.usernameInput.View(), 0),
		m.renderField("Password", m.passwordInput.View(), 1),
		m.renderField("TOTP", m.totpInput.View(), 2),
		m.renderToggle("Save username", m.saveUsername, 3),
		m.renderToggle("Save password", m.savePassword, 4),
		submitLabel,
	}
	rows := m.height - 3 // fixed title, status, and help
	if rows > len(fields) {
		rows = len(fields)
	}
	start := 0
	if m.focusIndex >= rows {
		start = m.focusIndex - rows + 1
	}
	status := m.statusLine
	if m.submitting {
		status = "Signing in..."
	}
	lines := []string{titleStyle.Render("Themis Login")}
	lines = append(lines, fields[start:start+rows]...)
	lines = append(lines, status, labelStyle.Render("Tab move · Space toggle · Enter next/submit · Esc cancel"))
	return textutil.Fit(strings.Join(lines, "\n"), m.width, m.height)
}

func (m *model) moveFocus(key string) {
	m.usernameInput.Blur()
	m.passwordInput.Blur()
	m.totpInput.Blur()

	switch key {
	case "up", "shift+tab":
		m.focusIndex--
	default:
		m.focusIndex++
	}

	if m.focusIndex > 5 {
		m.focusIndex = 0
	}
	if m.focusIndex < 0 {
		m.focusIndex = 5
	}

	switch m.focusIndex {
	case 0:
		m.usernameInput.Focus()
	case 1:
		m.passwordInput.Focus()
	case 2:
		m.totpInput.Focus()
	}
}

func (m model) renderField(label string, value string, idx int) string {
	prefix := labelStyle.Render(label + ": ")
	if m.focusIndex == idx {
		prefix = focusStyle.Render(label + ": ")
	}
	return prefix + value
}

func (m model) renderToggle(label string, value bool, idx int) string {
	checked := "[ ]"
	if value {
		checked = "[x]"
	}
	line := fmt.Sprintf("%s %s", checked, label)
	if m.focusIndex == idx {
		return focusStyle.Render(line)
	}
	return labelStyle.Render(line)
}

func (m model) startSubmit() (tea.Model, tea.Cmd) {
	username := strings.TrimSpace(m.usernameInput.Value())
	password := m.passwordInput.Value()
	totp := strings.TrimSpace(m.totpInput.Value())
	if username == "" || password == "" || totp == "" {
		m.statusLine = errorStyle.Render("Username, password, and TOTP are required.")
		return m, nil
	}

	if m.submit == nil {
		m.statusLine = errorStyle.Render("Login unavailable.")
		return m, nil
	}
	req := SubmitRequest{
		Username:     username,
		Password:     password,
		TOTP:         totp,
		SaveUsername: m.saveUsername,
		SavePassword: m.savePassword,
	}
	m.submitting = true
	m.statusLine = ""

	return m, func() tea.Msg {
		result, err := m.submit(req)
		return submitFinishedMsg{result: result, err: err}
	}
}
