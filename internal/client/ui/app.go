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
	orange      = lipgloss.Color("#e06c1f")
	orangeLight = lipgloss.Color("#ffb07a") // readable text on orange bg
	gray        = lipgloss.Color("#6c7086")
	red         = lipgloss.Color("#f38ba8")

	// bg=orange fg=light: used for badge, active buttons, selected rows
	bgActive = lipgloss.NewStyle().
			Background(orange).
			Foreground(orangeLight).
			Padding(0, 1)

	// plain gray text
	grayStyle = lipgloss.NewStyle().Foreground(gray)
	redStyle  = lipgloss.NewStyle().Foreground(red)
)

// badge renders " tailway client " with orange background.
func badge() string {
	return bgActive.Render("tailway client")
}

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

type Model struct {
	screen     screen
	c          *client.Client
	loginStep  int
	ipInput    textinput.Model
	keyInput   textinput.Model
	loginErr   string
	connecting bool

	tunnels   []*client.TunnelEntry
	cursor    int // 0=ADD, 1..n=tunnel rows
	statusMsg string

	// add screen
	addProto int // 0=TCP 1=UDP
	addFocus int // 0=proto 1=clientPort 2=serverPort 3=submit
	cpInput  textinput.Model
	spInput  textinput.Model
	addErr   string
}

func NewModel(c *client.Client) Model {
	ip := textinput.New()
	ip.Placeholder = "192.168.1.100:7000"
	ip.CharLimit = 128
	ip.Width = 32
	ip.Focus()

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

	return Model{
		c:        c,
		ipInput:  ip,
		keyInput: key,
		cpInput:  cp,
		spInput:  sp,
	}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(textinput.Blink, tickCmd())
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		return m, nil
	case tickMsg:
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
		b.WriteString("  " + grayStyle.Render("connecting...") + "\n")
	} else {
		b.WriteString("  " + grayStyle.Render("tab: next field  enter: connect  ctrl+c: quit") + "\n")
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

	b.WriteString("\n  " + badge() + "  " + grayStyle.Render("connected to "+m.c.ServerHost()) + "\n\n")

	// ADD row
	if m.cursor == 0 {
		b.WriteString("  " + bgActive.Render("+ ADD") + "\n")
	} else {
		b.WriteString("  " + grayStyle.Render("+ ADD") + "\n")
	}

	// Tunnel list
	if len(m.tunnels) > 0 {
		b.WriteString("\n")
		b.WriteString(grayStyle.Render(fmt.Sprintf("  %-10s  %-12s  %-12s", "PROTOCOL", "LOCAL PORT", "GLOBAL PORT")) + "\n")
		b.WriteString(grayStyle.Render("  "+strings.Repeat("-", 38)) + "\n")
		for i, t := range m.tunnels {
			row := fmt.Sprintf("%-10s  %-12d  %-12d",
				strings.ToUpper(t.TunnelInfo.Protocol),
				t.TunnelInfo.ClientPort,
				t.TunnelInfo.ServerPort,
			)
			if m.cursor == i+1 {
				b.WriteString("  " + bgActive.Render(row) + "\n")
			} else {
				b.WriteString("  " + row + "\n")
			}
		}
	} else {
		b.WriteString("\n  " + grayStyle.Render("no tunnels active") + "\n")
	}

	if m.statusMsg != "" {
		b.WriteString("\n  " + redStyle.Render(m.statusMsg) + "\n")
	}

	b.WriteString("\n  " + grayStyle.Render("↑↓: select  enter: add  d: delete  q: quit") + "\n")
	return b.String()
}

// ─── Add tunnel ───────────────────────────────────────────────────────────────

func (m Model) updateAdd(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.Type {
	case tea.KeyCtrlC:
		return m, tea.Quit
	case tea.KeyEsc:
		m.screen = screenMain
		return m, nil
	case tea.KeyTab, tea.KeyDown:
		m.addFocus = (m.addFocus + 1) % 4
		return m.syncAddFocus()
	case tea.KeyShiftTab, tea.KeyUp:
		m.addFocus = (m.addFocus + 3) % 4
		return m.syncAddFocus()
	case tea.KeyLeft, tea.KeyRight:
		if m.addFocus == 0 {
			m.addProto = 1 - m.addProto
		}
		return m, nil
	case tea.KeyEnter:
		if m.addFocus == 3 {
			return m.submitAdd()
		}
		m.addFocus = (m.addFocus + 1) % 4
		return m.syncAddFocus()
	}
	var cmd tea.Cmd
	switch m.addFocus {
	case 1:
		m.cpInput, cmd = m.cpInput.Update(k)
	case 2:
		m.spInput, cmd = m.spInput.Update(k)
	}
	return m, cmd
}

func (m Model) syncAddFocus() (Model, tea.Cmd) {
	m.cpInput.Blur()
	m.spInput.Blur()
	switch m.addFocus {
	case 1:
		m.cpInput.Focus()
	case 2:
		m.spInput.Focus()
	}
	return m, textinput.Blink
}

func (m Model) submitAdd() (Model, tea.Cmd) {
	cp, err := strconv.Atoi(strings.TrimSpace(m.cpInput.Value()))
	if err != nil || cp < 1 || cp > 65535 {
		m.addErr = "invalid client port (1-65535)"
		return m, nil
	}
	sp, err := strconv.Atoi(strings.TrimSpace(m.spInput.Value()))
	if err != nil || sp < 1 || sp > 65535 {
		m.addErr = "invalid server port (1-65535)"
		return m, nil
	}
	prot := "tcp"
	if m.addProto == 1 {
		prot = "udp"
	}
	m.c.AddTunnel(proto.TunnelInfo{Protocol: prot, ClientPort: cp, ServerPort: sp})
	m.screen = screenMain
	return m, nil
}

func (m Model) viewAdd() string {
	var b strings.Builder

	b.WriteString("\n  " + badge() + "\n\n")

	// Protocol toggle: active one gets orange bg
	var tcp, udp string
	if m.addProto == 0 {
		tcp = bgActive.Render("TCP")
		udp = grayStyle.Render("UDP")
	} else {
		tcp = grayStyle.Render("TCP")
		udp = bgActive.Render("UDP")
	}
	protoLabel := grayStyle.Render("Protocol:")
	if m.addFocus == 0 {
		protoLabel = bgActive.Render("Protocol:")
	}
	b.WriteString("  " + protoLabel + "    " + tcp + "  " + udp + "\n\n")

	// Client port
	cpLabel := grayStyle.Render("Client Port:")
	if m.addFocus == 1 {
		cpLabel = bgActive.Render("Client Port:")
	}
	b.WriteString("  " + cpLabel + " " + m.cpInput.View() + "\n\n")

	// Server port
	spLabel := grayStyle.Render("Server Port:")
	if m.addFocus == 2 {
		spLabel = bgActive.Render("Server Port:")
	}
	b.WriteString("  " + spLabel + " " + m.spInput.View() + "\n\n")

	// Submit
	if m.addFocus == 3 {
		b.WriteString("  " + bgActive.Render("Add Tunnel") + "\n")
	} else {
		b.WriteString("  " + grayStyle.Render("Add Tunnel") + "\n")
	}

	if m.addErr != "" {
		b.WriteString("\n  " + redStyle.Render(m.addErr) + "\n")
	}

	b.WriteString("\n  " + grayStyle.Render("tab/↑↓: navigate  ←/→: protocol  enter: confirm  esc: back") + "\n")
	return b.String()
}

// ─── View dispatch ────────────────────────────────────────────────────────────

func (m Model) View() string {
	switch m.screen {
	case screenLogin:
		return m.viewLogin()
	case screenMain:
		return m.viewMain()
	case screenAdd:
		return m.viewAdd()
	}
	return ""
}
