package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/kiiimatz/tailway/internal/config"
)

// ─── Config TUI styles ────────────────────────────────────────────────────────

var (
	cfgAccent    = lipgloss.Color("#3f3f46")
	cfgAccentFg  = lipgloss.Color("#f4f4f5")
	cfgMuted     = lipgloss.Color("#71717a")
	cfgSection   = lipgloss.Color("#a1a1aa")
	cfgErr       = lipgloss.Color("#f87171")

	cfgBadge     = lipgloss.NewStyle().Background(cfgAccent).Foreground(cfgAccentFg).Padding(0, 1)
	cfgMutedSt   = lipgloss.NewStyle().Foreground(cfgMuted)
	cfgSectionSt = lipgloss.NewStyle().Foreground(cfgSection)
	cfgErrSt     = lipgloss.NewStyle().Foreground(cfgErr)
	cfgLabelAct  = lipgloss.NewStyle().Foreground(cfgAccentFg)
)

// field indices
const (
	cfgFieldClientIP  = 0
	cfgFieldClientKey = 1
	cfgFieldServerKey = 2
	cfgFieldCount     = 3
)

// ─── Model ────────────────────────────────────────────────────────────────────

type configModel struct {
	inputs  [cfgFieldCount]textinput.Model
	focus   int
	saveErr string
	saved   bool
	height  int
}

func newConfigModel(cfg *config.Config) configModel {
	makeInput := func(placeholder string, password bool, val string) textinput.Model {
		t := textinput.New()
		t.Placeholder = placeholder
		t.CharLimit = 256
		t.Width = 36
		if password {
			t.EchoMode = textinput.EchoPassword
			t.EchoCharacter = '•'
		}
		t.SetValue(val)
		return t
	}

	m := configModel{}
	m.inputs[cfgFieldClientIP] = makeInput("192.168.1.100:7000", false, cfg.ClientIP)
	m.inputs[cfgFieldClientKey] = makeInput("secret-key", true, cfg.ClientKey)
	m.inputs[cfgFieldServerKey] = makeInput("secret-key", true, cfg.ServerKey)
	m.inputs[m.focus].Focus()
	return m
}

func (m configModel) Init() tea.Cmd {
	return textinput.Blink
}

func (m configModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.height = msg.Height

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c":
			return m, tea.Quit

		case "tab", "down":
			m.inputs[m.focus].Blur()
			m.focus = (m.focus + 1) % cfgFieldCount
			m.inputs[m.focus].Focus()
			return m, textinput.Blink

		case "shift+tab", "up":
			m.inputs[m.focus].Blur()
			m.focus = (m.focus + cfgFieldCount - 1) % cfgFieldCount
			m.inputs[m.focus].Focus()
			return m, textinput.Blink

		case "enter":
			if m.focus < cfgFieldCount-1 {
				// Move to next field
				m.inputs[m.focus].Blur()
				m.focus++
				m.inputs[m.focus].Focus()
				return m, textinput.Blink
			}
			// Last field: save
			return m.save()
		}
	}

	// Forward keystrokes to focused input
	var cmd tea.Cmd
	m.inputs[m.focus], cmd = m.inputs[m.focus].Update(msg)
	return m, cmd
}

func (m configModel) save() (tea.Model, tea.Cmd) {
	cfg := &config.Config{
		ClientIP:  strings.TrimSpace(m.inputs[cfgFieldClientIP].Value()),
		ClientKey: m.inputs[cfgFieldClientKey].Value(),
		ServerKey: m.inputs[cfgFieldServerKey].Value(),
	}
	if err := cfg.Save(); err != nil {
		m.saveErr = err.Error()
		return m, nil
	}
	m.saved = true
	return m, tea.Quit
}

func (m configModel) View() string {
	var b strings.Builder

	b.WriteString("\n  " + cfgBadge.Render("tailway config") + "\n\n")

	label := func(name string, active bool) string {
		padded := fmt.Sprintf("%-8s", name)
		if active {
			return cfgLabelAct.Render(padded)
		}
		return cfgMutedSt.Render(padded)
	}

	// ── Client section ────────────────────────────────────────────────────────
	b.WriteString("  " + cfgSectionSt.Render("Client") + "\n\n")
	b.WriteString("  " + label("IP", m.focus == cfgFieldClientIP) + "  " + m.inputs[cfgFieldClientIP].View() + "\n\n")
	b.WriteString("  " + label("Key", m.focus == cfgFieldClientKey) + "  " + m.inputs[cfgFieldClientKey].View() + "\n\n")

	// ── Server section ────────────────────────────────────────────────────────
	b.WriteString("  " + cfgSectionSt.Render("Server") + "\n\n")
	b.WriteString("  " + label("Key", m.focus == cfgFieldServerKey) + "  " + m.inputs[cfgFieldServerKey].View() + "\n\n")

	// ── Status / hint ─────────────────────────────────────────────────────────
	if m.saveErr != "" {
		b.WriteString("  " + cfgErrSt.Render("error: "+m.saveErr) + "\n\n")
	}
	if m.focus == cfgFieldCount-1 {
		b.WriteString("  " + cfgMutedSt.Render("tab: prev/next field  enter: save  ctrl+c: cancel") + "\n")
	} else {
		b.WriteString("  " + cfgMutedSt.Render("tab: next field  enter: next  ctrl+c: cancel") + "\n")
	}

	if m.height > 0 {
		return lipgloss.NewStyle().
			Height(m.height - 1).
			MaxHeight(m.height - 1).
			Render(b.String())
	}
	return b.String()
}

// ─── runConfigTUI ─────────────────────────────────────────────────────────────

func runConfigTUI() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "could not load config: %v\n", err)
		cfg = &config.Config{}
	}

	m := newConfigModel(cfg)
	p := tea.NewProgram(m,
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
	)
	result, err := p.Run()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	if final, ok := result.(configModel); ok && final.saved {
		fmt.Println("Config saved.")
	}
}
