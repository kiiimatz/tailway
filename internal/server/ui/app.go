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
	accent   = lipgloss.Color("#3f3f46") // zinc-700
	accentFg = lipgloss.Color("#f4f4f5") // zinc-100
	muted    = lipgloss.Color("#71717a") // zinc-500
	errCol   = lipgloss.Color("#f87171") // red-400

	bgActive   = lipgloss.NewStyle().Background(accent).Foreground(accentFg).Padding(0, 1)
	mutedStyle = lipgloss.NewStyle().Foreground(muted)
	redStyle   = lipgloss.NewStyle().Foreground(errCol)
)

func badge(debug bool) string {
	if debug {
		return bgActive.Render("tailway server") + mutedStyle.Render(" with debug")
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
		return m, tea.ClearScreen

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
	var v string
	if m.state == stateKeyInput {
		v = m.viewKeyInput()
	} else {
		v = m.viewRunning()
	}
	// Clip to terminal height (prevents alt-screen scroll when content overflows)
	// and pad to fill the terminal (overwrites stale lines from previous screens).
	if m.height > 0 {
		v = lipgloss.NewStyle().
			Height(m.height - 1).
			MaxHeight(m.height - 1).
			Render(v)
	}
	return v
}

func (m Model) viewKeyInput() string {
	var b strings.Builder
	b.WriteString("\n")
	b.WriteString("  " + badge(m.debug) + "\n\n")
	b.WriteString("  Key:  " + m.keyInput.View() + "\n\n")
	if m.keyErr != "" {
		b.WriteString("  " + redStyle.Render("error: "+m.keyErr) + "\n\n")
	}
	b.WriteString("  " + mutedStyle.Render("enter: start  ctrl+c: quit") + "\n")
	return b.String()
}

func (m Model) viewRunning() string {
	var b strings.Builder
	b.WriteString("\n")
	b.WriteString("  " + badge(m.debug) + "\n\n")

	clients := 0
	var st server.TunnelStats
	if m.srv != nil {
		clients = m.srv.ClientCount()
		st = m.srv.TunnelCounts()
	}

	const labelW = 14
	row := func(label, value string) string {
		return "  " + mutedStyle.Render(padRight(label, labelW)) + bgActive.Render(value) + "\n"
	}

	b.WriteString(row("Connection", fmt.Sprintf("%d", clients)))
	b.WriteString(row("Data Usage", formatBytes(m.srv.TotalBytes())))
	b.WriteString("\n")
	b.WriteString(row("TCP", fmt.Sprintf("%d", st.TCP)))
	b.WriteString(row("UDP", fmt.Sprintf("%d", st.UDP)))
	b.WriteString(row("HTTP", fmt.Sprintf("%d", st.HTTP)))
	b.WriteString(row("HTTPS", fmt.Sprintf("%d", st.HTTPS)))
	b.WriteString(row("QUIC", fmt.Sprintf("%d", st.QUIC)))
	b.WriteString(row("SOCKS5", fmt.Sprintf("%d", st.SOCKS5)))

	b.WriteString("\n  " + mutedStyle.Render("q: quit  ctrl+c: quit") + "\n")
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

func formatBytes(b int64) string {
	const (
		KB int64 = 1024
		MB int64 = KB * 1024
		GB int64 = MB * 1024
		TB int64 = GB * 1024
	)
	if b == 0 {
		return "—"
	}
	trim := func(f float64) string {
		s := fmt.Sprintf("%.1f", f)
		if strings.HasSuffix(s, ".0") {
			return s[:len(s)-2]
		}
		return s
	}
	switch {
	case b >= TB:
		return trim(float64(b)/float64(TB)) + "TB"
	case b >= GB:
		return trim(float64(b)/float64(GB)) + "GB"
	case b >= MB:
		return trim(float64(b)/float64(MB)) + "MB"
	case b >= KB:
		return trim(float64(b)/float64(KB)) + "KB"
	default:
		return fmt.Sprintf("%dB", b)
	}
}
