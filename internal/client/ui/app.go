// Package ui implements the BubbleTea TUI for the tailway client.
package ui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/kiiimatz/tailway/internal/client"
	"github.com/kiiimatz/tailway/internal/proto"
)

// ─── Colors & styles ──────────────────────────────────────────────────────────

var (
	accent   = lipgloss.Color("#3f3f46") // zinc-700 — active background
	accentFg = lipgloss.Color("#f4f4f5") // zinc-100 — text on active bg
	muted    = lipgloss.Color("#71717a") // zinc-500 — secondary text
	errCol   = lipgloss.Color("#f87171") // red-400

	bgActive  = lipgloss.NewStyle().Background(accent).Foreground(accentFg).Padding(0, 1)
	mutedStyle = lipgloss.NewStyle().Foreground(muted)
	redStyle   = lipgloss.NewStyle().Foreground(errCol)
)

func badge() string {
	return bgActive.Render("tailway client")
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

// ─── Protocol list ────────────────────────────────────────────────────────────

var protocols = []string{"TCP", "UDP", "HTTP", "HTTPS", "QUIC", "SOCKS5"}

func protoName(idx int) string {
	if idx < 0 || idx >= len(protocols) {
		return "tcp"
	}
	return strings.ToLower(protocols[idx])
}

// isSocks5 reports whether the currently selected protocol is SOCKS5.
func isSocks5(idx int) bool { return idx == 5 }

// ─── Screens ──────────────────────────────────────────────────────────────────

type screen int

const (
	screenLogin screen = iota
	screenMain
	screenAdd
)

type connectResultMsg struct{ err error }
type clientEventMsg client.Event
type tickMsg struct{}

// ─── Model ────────────────────────────────────────────────────────────────────

// addFocus positions:
//   0 = protocol selector
//   1 = client port  (skipped for SOCKS5)
//   2 = server port
//   3 = submit

type Model struct {
	width  int
	height int
	screen screen
	c      *client.Client

	loginStep   int
	ipInput     textinput.Model
	keyInput    textinput.Model
	loginErr    string
	connecting  bool
	autoConnect bool // skip login screen and connect immediately

	tunnels   []*client.TunnelEntry
	cursor    int
	statusMsg string

	// add screen
	addProto int
	addFocus int
	cpInput  textinput.Model
	spInput  textinput.Model
	addErr   string
}

func NewModel(c *client.Client, autoIP, autoKey string) Model {
	ip := textinput.New()
	ip.Placeholder = "192.168.1.100:7000"
	ip.CharLimit = 128
	ip.Width = 32

	key := textinput.New()
	key.Placeholder = "your-secret-key"
	key.EchoMode = textinput.EchoPassword
	key.EchoCharacter = '•'
	key.CharLimit = 256
	key.Width = 32

	cp := textinput.New()
	cp.Placeholder = "25565"
	cp.CharLimit = 5
	cp.Width = 10

	sp := textinput.New()
	sp.Placeholder = "25565"
	sp.CharLimit = 5
	sp.Width = 10

	m := Model{
		c:        c,
		ipInput:  ip,
		keyInput: key,
		cpInput:  cp,
		spInput:  sp,
	}

	if autoIP != "" && autoKey != "" {
		m.ipInput.SetValue(autoIP)
		m.keyInput.SetValue(autoKey)
		m.autoConnect = true
		m.connecting = true
	} else {
		m.ipInput.Focus()
	}

	return m
}

func (m Model) Init() tea.Cmd {
	if m.autoConnect {
		addr := m.ipInput.Value()
		key := m.keyInput.Value()
		return tea.Batch(
			textinput.Blink,
			tickCmd(),
			func() tea.Msg { return connectResultMsg{m.c.Connect(addr, key)} },
		)
	}
	return tea.Batch(textinput.Blink, tickCmd())
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, tea.ClearScreen
	case tickMsg:
		// Refresh byte counts every tick so DATA USAGE updates in real time.
		if m.screen == screenMain {
			m.tunnels = m.c.Snapshot()
		}
		return m, tickCmd()
	case connectResultMsg:
		m.connecting = false
		if msg.err != nil {
			m.loginErr = msg.err.Error()
			return m, nil
		}
		m.screen = screenMain
		m.c.ListTunnels()
		return m, tea.Batch(waitEvent(m.c), tickCmd())
	case clientEventMsg:
		return m.handleEvent(client.Event(msg))
	case tea.KeyMsg:
		switch m.screen {
		case screenLogin:
			return m.updateLogin(msg)
		case screenMain:
			return m.updateMain(msg)
		case screenAdd:
			return m.updateAdd(msg)
		}
	}
	return m, nil
}

func (m Model) handleEvent(ev client.Event) (Model, tea.Cmd) {
	switch ev.Kind {
	case client.EventTunnelAdded, client.EventTunnelList, client.EventTunnelDeleted:
		if entries, ok := ev.Payload.([]*client.TunnelEntry); ok {
			m.tunnels = entries
			if m.cursor >= len(m.tunnels) {
				m.cursor = len(m.tunnels) - 1
			}
			if m.cursor < 0 {
				m.cursor = 0
			}
		}
	case client.EventTunnelAddError:
		if s, ok := ev.Payload.(string); ok {
			m.statusMsg = "error: " + s
		}
	case client.EventDisconnect:
		m.screen = screenLogin
		m.loginErr = "disconnected"
		m.loginStep = 0
		m.tunnels = nil
		m.cursor = 0
		m.ipInput.Focus()
	}
	return m, waitEvent(m.c)
}

// ─── Login ────────────────────────────────────────────────────────────────────

func (m Model) updateLogin(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.Type {
	case tea.KeyCtrlC:
		return m, tea.Quit
	case tea.KeyTab, tea.KeyEnter:
		if m.loginStep == 0 {
			m.loginStep = 1
			m.ipInput.Blur()
			m.keyInput.Focus()
			return m, nil
		}
		if m.connecting {
			return m, nil
		}
		addr := strings.TrimSpace(m.ipInput.Value())
		key := m.keyInput.Value()
		if addr == "" {
			m.loginErr = "server address is required"
			return m, nil
		}
		if key == "" {
			m.loginErr = "key is required"
			return m, nil
		}
		m.connecting = true
		m.loginErr = ""
		return m, func() tea.Msg { return connectResultMsg{m.c.Connect(addr, key)} }
	}
	var cmd tea.Cmd
	if m.loginStep == 0 {
		m.ipInput, cmd = m.ipInput.Update(k)
	} else {
		m.keyInput, cmd = m.keyInput.Update(k)
	}
	return m, cmd
}

func (m Model) viewLogin() string {
	var b strings.Builder
	b.WriteString("\n  " + badge() + "\n\n")
	b.WriteString("  Server: " + m.ipInput.View() + "\n\n")
	b.WriteString("  Key:    " + m.keyInput.View() + "\n\n")
	if m.loginErr != "" {
		b.WriteString("  " + redStyle.Render("error: "+m.loginErr) + "\n\n")
	}
	if m.connecting {
		b.WriteString("  " + mutedStyle.Render("connecting...") + "\n")
	} else {
		b.WriteString("  " + mutedStyle.Render("tab: next field  enter: connect  ctrl+c: quit") + "\n")
	}
	return b.String()
}

// ─── Main ─────────────────────────────────────────────────────────────────────

func (m Model) updateMain(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "ctrl+c", "q":
		return m, tea.Quit
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(m.tunnels) {
			m.cursor++
		}
	case "enter", "a":
		if m.cursor == 0 {
			m.screen = screenAdd
			m.addProto = 0
			m.addFocus = 0
			m.addErr = ""
			m.cpInput.SetValue("")
			m.spInput.SetValue("")
			m.cpInput.Blur()
			m.spInput.Blur()
		}
	case "d", "delete":
		if m.cursor > 0 && m.cursor <= len(m.tunnels) {
			m.c.DeleteTunnel(m.tunnels[m.cursor-1].TunnelInfo.ID)
		}
	}
	return m, nil
}

func (m Model) viewMain() string {
	var b strings.Builder

	b.WriteString("\n  " + badge() + "  " + mutedStyle.Render("connected to "+m.c.ServerHost()) + "\n\n")

	// ADD row
	if m.cursor == 0 {
		b.WriteString("  " + bgActive.Render("+ ADD") + "\n")
	} else {
		b.WriteString("  " + mutedStyle.Render("+ ADD") + "\n")
	}

	if len(m.tunnels) > 0 {
		b.WriteString("\n")
		b.WriteString(mutedStyle.Render(fmt.Sprintf("  %-10s  %-12s  %-12s  %-10s", "PROTOCOL", "LOCAL PORT", "GLOBAL PORT", "DATA USAGE")) + "\n")
		b.WriteString(mutedStyle.Render("  "+strings.Repeat("─", 50)) + "\n")
		for i, t := range m.tunnels {
			localPort := fmt.Sprintf("%d", t.TunnelInfo.ClientPort)
			if t.TunnelInfo.Protocol == "socks5" {
				localPort = "—"
			}
			row := fmt.Sprintf("%-10s  %-12s  %-12d  %-10s",
				strings.ToUpper(t.TunnelInfo.Protocol),
				localPort,
				t.TunnelInfo.ServerPort,
				formatBytes(t.Bytes),
			)
			if m.cursor == i+1 {
				b.WriteString("  " + bgActive.Render(row) + "\n")
			} else {
				b.WriteString("  " + row + "\n")
			}
		}
	} else {
		b.WriteString("\n  " + mutedStyle.Render("no tunnels active") + "\n")
	}

	if m.statusMsg != "" {
		b.WriteString("\n  " + redStyle.Render(m.statusMsg) + "\n")
	}

	b.WriteString("\n  " + mutedStyle.Render("↑↓: select  enter: add  d: delete  q: quit") + "\n")
	return b.String()
}

// ─── Add tunnel ───────────────────────────────────────────────────────────────

// addFocusMax returns the number of focus positions for the current protocol.
// SOCKS5 has no local port field → 3 positions (proto, serverPort, submit).
func (m Model) addFocusMax() int {
	if isSocks5(m.addProto) {
		return 3
	}
	return 4
}

// addFocusNext returns the next focus index, skipping client port for SOCKS5.
func (m Model) addFocusNext() int {
	next := (m.addFocus + 1) % m.addFocusMax()
	// Map SOCKS5 focus positions: 0→proto, 1→serverPort, 2→submit
	// Map normal focus positions:  0→proto, 1→clientPort, 2→serverPort, 3→submit
	return next
}

// addFocusPrev returns the previous focus index.
func (m Model) addFocusPrev() int {
	return (m.addFocus + m.addFocusMax() - 1) % m.addFocusMax()
}

// addFocusServerPort returns the focus index for server port in current mode.
func (m Model) addFocusServerPort() int {
	if isSocks5(m.addProto) {
		return 1
	}
	return 2
}

// addFocusSubmit returns the focus index for the submit button.
func (m Model) addFocusSubmit() int {
	if isSocks5(m.addProto) {
		return 2
	}
	return 3
}

func (m Model) updateAdd(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.Type {
	case tea.KeyCtrlC:
		return m, tea.Quit
	case tea.KeyEsc:
		m.screen = screenMain
		return m, nil
	case tea.KeyTab, tea.KeyDown:
		m.addFocus = m.addFocusNext()
		return m.syncAddFocus()
	case tea.KeyShiftTab, tea.KeyUp:
		m.addFocus = m.addFocusPrev()
		return m.syncAddFocus()
	case tea.KeyLeft:
		if m.addFocus == 0 {
			m.addProto = (m.addProto + len(protocols) - 1) % len(protocols)
			// Reset focus if moving away from SOCKS5 changes max.
			if m.addFocus >= m.addFocusMax() {
				m.addFocus = 0
			}
		}
		return m, nil
	case tea.KeyRight:
		if m.addFocus == 0 {
			m.addProto = (m.addProto + 1) % len(protocols)
			if m.addFocus >= m.addFocusMax() {
				m.addFocus = 0
			}
		}
		return m, nil
	case tea.KeyEnter:
		if m.addFocus == m.addFocusSubmit() {
			return m.submitAdd()
		}
		m.addFocus = m.addFocusNext()
		return m.syncAddFocus()
	}
	var cmd tea.Cmd
	switch m.addFocus {
	case 1:
		if !isSocks5(m.addProto) {
			m.cpInput, cmd = m.cpInput.Update(k)
		} else {
			m.spInput, cmd = m.spInput.Update(k)
		}
	case 2:
		if !isSocks5(m.addProto) {
			m.spInput, cmd = m.spInput.Update(k)
		}
	}
	return m, cmd
}

func (m Model) syncAddFocus() (Model, tea.Cmd) {
	m.cpInput.Blur()
	m.spInput.Blur()
	if isSocks5(m.addProto) {
		// focus 0=proto, 1=serverPort, 2=submit
		if m.addFocus == 1 {
			m.spInput.Focus()
		}
	} else {
		// focus 0=proto, 1=clientPort, 2=serverPort, 3=submit
		switch m.addFocus {
		case 1:
			m.cpInput.Focus()
		case 2:
			m.spInput.Focus()
		}
	}
	return m, textinput.Blink
}

func (m Model) submitAdd() (Model, tea.Cmd) {
	sp, err := strconv.Atoi(strings.TrimSpace(m.spInput.Value()))
	if err != nil || sp < 1 || sp > 65535 {
		m.addErr = "invalid server port (1-65535)"
		return m, nil
	}

	cp := 0
	if !isSocks5(m.addProto) {
		cp, err = strconv.Atoi(strings.TrimSpace(m.cpInput.Value()))
		if err != nil || cp < 1 || cp > 65535 {
			m.addErr = "invalid local port (1-65535)"
			return m, nil
		}
	}

	m.c.AddTunnel(proto.TunnelInfo{
		Protocol:   protoName(m.addProto),
		ClientPort: cp,
		ServerPort: sp,
	})
	m.screen = screenMain
	return m, nil
}

func (m Model) viewAdd() string {
	var b strings.Builder

	b.WriteString("\n  " + badge() + "\n\n")

	// ── Protocol selector ──────────────────────────────────────────────────
	protoLabel := mutedStyle.Render("Protocol:")
	if m.addFocus == 0 {
		protoLabel = bgActive.Render("Protocol:")
	}
	b.WriteString("  " + protoLabel + "\n")
	b.WriteString("  ")
	for i, p := range protocols {
		if i == m.addProto {
			b.WriteString(bgActive.Render(p))
		} else {
			b.WriteString(mutedStyle.Render(p))
		}
		if i < len(protocols)-1 {
			b.WriteString("  ")
		}
	}
	b.WriteString("\n\n")

	// ── Local port (hidden for SOCKS5) ────────────────────────────────────
	if !isSocks5(m.addProto) {
		cpLabel := mutedStyle.Render("Local Port: ")
		if m.addFocus == 1 {
			cpLabel = bgActive.Render("Local Port: ")
		}
		b.WriteString("  " + cpLabel + " " + m.cpInput.View() + "\n\n")

		spLabel := mutedStyle.Render("Server Port:")
		if m.addFocus == 2 {
			spLabel = bgActive.Render("Server Port:")
		}
		b.WriteString("  " + spLabel + " " + m.spInput.View() + "\n\n")
	} else {
		spLabel := mutedStyle.Render("Server Port:")
		if m.addFocus == 1 {
			spLabel = bgActive.Render("Server Port:")
		}
		b.WriteString("  " + spLabel + " " + m.spInput.View() + "\n")
		b.WriteString("  " + mutedStyle.Render("  (SOCKS5: client dials destination directly)") + "\n\n")
	}

	// ── Submit ────────────────────────────────────────────────────────────
	if m.addFocus == m.addFocusSubmit() {
		b.WriteString("  " + bgActive.Render("Add Tunnel") + "\n")
	} else {
		b.WriteString("  " + mutedStyle.Render("Add Tunnel") + "\n")
	}

	if m.addErr != "" {
		b.WriteString("\n  " + redStyle.Render(m.addErr) + "\n")
	}

	b.WriteString("\n  " + mutedStyle.Render("tab/↑↓: navigate  ←/→: protocol  enter: confirm  esc: back") + "\n")
	return b.String()
}

// ─── View dispatch ────────────────────────────────────────────────────────────

func (m Model) View() string {
	var v string
	switch m.screen {
	case screenLogin:
		v = m.viewLogin()
	case screenMain:
		v = m.viewMain()
	case screenAdd:
		v = m.viewAdd()
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
