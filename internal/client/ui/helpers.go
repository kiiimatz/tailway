package ui

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/kiiimatz/tailway/internal/client"
)

func tickCmd() tea.Cmd {
	return tea.Tick(time.Second, func(time.Time) tea.Msg { return tickMsg{} })
}

func waitEvent(c *client.Client) tea.Cmd {
	return func() tea.Msg { return clientEventMsg(<-c.Events) }
}
