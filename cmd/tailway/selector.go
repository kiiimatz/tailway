package main

import (
	"fmt"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ─── Selector styles ──────────────────────────────────────────────────────────

var (
	selAccent   = lipgloss.Color("#3f3f46")
	selAccentFg = lipgloss.Color("#f4f4f5")
	selMuted    = lipgloss.Color("#71717a")

	selBgActive  = lipgloss.NewStyle().Background(selAccent).Foreground(selAccentFg).Padding(0, 1)
	selMutedStyle = lipgloss.NewStyle().Foreground(selMuted)
)

func selBadge() string {
	return selBgActive.Render("tailway")
}

// ─── Selector model ───────────────────────────────────────────────────────────

type selChoice int

const (
	selChoiceNone   selChoice = iota
	selChoiceClient
	selChoiceServer
)

type selectorModel struct {
	cursor int // 0=client, 1=server
	choice selChoice
	width  int
	height int
}

func (m selectorModel) Init() tea.Cmd { return nil }

func (m selectorModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q":
			return m, tea.Quit
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < 1 {
				m.cursor++
			}
		case "enter", " ":
			if m.cursor == 0 {
				m.choice = selChoiceClient
			} else {
				m.choice = selChoiceServer
			}
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m selectorModel) View() string {
	var b strings.Builder

	b.WriteString("\n  " + selBadge() + "\n\n")

	items := []string{"client", "server"}
	for i, item := range items {
		if m.cursor == i {
			b.WriteString("  " + selBgActive.Render(item) + "\n")
		} else {
			b.WriteString("  " + selMutedStyle.Render(item) + "\n")
		}
	}

	b.WriteString("\n  " + selMutedStyle.Render("↑↓: select  enter: confirm  q: quit") + "\n")

	if m.height > 0 {
		return lipgloss.NewStyle().
			Height(m.height - 1).
			MaxHeight(m.height - 1).
			Render(b.String())
	}
	return b.String()
}

// ─── runSelector ─────────────────────────────────────────────────────────────

func runSelector() {
	m := selectorModel{}
	p := tea.NewProgram(m,
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
	)
	result, err := p.Run()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	final := result.(selectorModel)
	switch final.choice {
	case selChoiceClient:
		runClient()
	case selChoiceServer:
		runServer(nil)
	default:
		// user pressed q or ctrl+c — exit silently
	}
}
