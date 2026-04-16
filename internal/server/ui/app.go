// Package ui implements the BubbleTea TUI for the tailway server.
package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/kiiimatz/tailway/internal/server"
)

// ─── Colors & styles ──────────────────────────────────────────────────────────

var (
	orange      = lipgloss.Color("#e06c1f")
	orangeLight = lipgloss.Color("#ffb07a")
	gray        = lipgloss.Color("#6c7086")
	red         = lipgloss.Color("#f38ba8")

	bgActive = lipgloss.NewStyle().
			Background(orange).
			Foreground(orangeLight).
			Padding(0, 1)

	grayStyle = lipgloss.NewStyle().Foreground(gray)
	redStyle  = lipgloss.NewStyle().Foreground(red)
)

func badge(debug bool) string {
	if debug {
		return bgActive.Render("tailway server") + grayStyle.Render(" with debug")
	}
	return bgActive.Render("tailway server")
}

// ─── Messages ─────────────────────────────────────────────────────────────────

type serverStartedMsg struct {
	err   error
	errCh <-chan error
}
type serverErrMsg struct{ err error }
type tickMsg struct{}

// ─── State ────────────────────────────────────────────────────────────────────

type uiState int

const (
	stateKeyInput uiState = iota
	stateRunning
)

// ─── Model ────────────────────────────────────────────────────────────────────

type Model struct {
	width  int
	height int
	state  uiState

	port  int
	debug bool
	srv   *server.Server
	key   string

	keyInput textinput.Model
	keyErr   string
}

func NewModel(port int, debug bool) Model {
	ki := textinput.New()
	ki.Placeholder = "authentication key"
	ki.EchoMode = textinput.EchoPassword
	ki.EchoCharacter = '•'
	ki.CharLimit = 256
	ki.Width = 28
	ki.Focus()

	return Model{
		port:     port,
		debug:    debug,
		keyInput: ki,
	}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(textinput.Blink, tickCmd())
}

func tickCmd() tea.Cmd {
	return tea.Tick(time.Second, func(time.Time) tea.Msg { return tickMsg{} })
}

func waitErr(ch <-chan error) tea.Cmd {
	return func() tea.Msg {
		if err, ok := <-ch; ok && err != nil {
			return serverErrMsg{err}
		}
		return nil
	}
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case tickMsg:
		return m, tickCmd()

	case serverStartedMsg:
		if msg.err != nil {
			m.keyErr = msg.err.Error()
			m.state = stateKeyInput
			m.keyInput.Focus()
			return m, nil
		}
		m.state = stateRunning
		return m, tea.Batch(tickCmd(), waitErr(msg.errCh))

	case serverErrMsg:
		m.keyErr = "server error: " + msg.err.Error()
		m.state = stateKeyInput
		m.srv = nil
		m.key = ""
		m.keyInput.SetValue("")
		m.keyInput.Focus()
		return m, nil

	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
		if m.state == stateRunning && msg.String() == "q" {
			return m, tea.Quit
		}
		if m.state == stateKeyInput {
			return m.updateKeyInput(msg)
		}
	}
	return m, nil
}

func (m Model) updateKeyInput(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	if k.Type == tea.KeyEnter {
		key := strings.TrimSpace(m.keyInput.Value())
		if key == "" {
			m.keyErr = "key is required"
			return m, nil
		}
		m.keyErr = ""
		m.key = key
		m.keyInput.Blur()
		s := server.New(key, m.port, m.debug)
		m.srv = s
		return m, func() tea.Msg {
			errCh, err := s.Start()
			return serverStartedMsg{err: err, errCh: errCh}
		}
	}
	var cmd tea.Cmd
	m.keyInput, cmd = m.keyInput.Update(k)
	return m, cmd
}

// ─── View ─────────────────────────────────────────────────────────────────────

func (m Model) View() string {
	if m.state == stateKeyInput {
		return m.viewKeyInput()
	}
	return m.viewRunning()
}

func (m Model) viewKeyInput() string {
	var b strings.Builder
	b.WriteString("\n")
	b.WriteString("  " + badge(m.debug) + "\n\n")
	b.WriteString("  Key:  " + m.keyInput.View() + "\n\n")
	if m.keyErr != "" {
		b.WriteString("  " + redStyle.Render("error: "+m.keyErr) + "\n\n")
	}
	b.WriteString("  " + grayStyle.Render("enter: start  ctrl+c: quit") + "\n")
	return b.String()
}

func (m Model) viewRunning() string {
	var b strings.Builder
	b.WriteString("\n")
	b.WriteString("  " + badge(m.debug) + "\n\n")

	clients := 0
	tcp, udp := 0, 0
	if m.srv != nil {
		clients = m.srv.ClientCount()
		tcp, udp = m.srv.TunnelCounts()
	}

	maskedKey := strings.Repeat("*", 12)

	rows := []struct{ label, value string }{
		{"Connection", fmt.Sprintf("%d", clients)},
		{"Key", maskedKey},
		{"Open UDP", fmt.Sprintf("%d", udp)},
		{"Open TCP", fmt.Sprintf("%d", tcp)},
	}

	const labelW = 16
	for _, r := range rows {
		b.WriteString("  " + grayStyle.Render(padRight(r.label, labelW)) + bgActive.Render(r.value) + "\n")
	}

	b.WriteString("\n  " + grayStyle.Render("q: quit  ctrl+c: quit") + "\n")
	return b.String()
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

func padRight(s string, w int) string {
	n := w - len(s)
	if n <= 0 {
		return s
	}
	return s + strings.Repeat(" ", n)
}
