package main

import (
	"flag"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/kiiimatz/tailway/internal/client"
	clientui "github.com/kiiimatz/tailway/internal/client/ui"
	serverui "github.com/kiiimatz/tailway/internal/server/ui"
)

const usage = `tailway — TCP/UDP reverse tunnel proxy

Usage:
  tailway server [--port 7000]
  tailway client

Commands:
  server   Start the server (prompts for auth key)
  client   Start the interactive client TUI
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(1)
	}

	switch os.Args[1] {
	case "server":
		runServer(os.Args[2:])
	case "client":
		runClient()
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n%s", os.Args[1], usage)
		os.Exit(1)
	}
}

func runServer(args []string) {
	fs := flag.NewFlagSet("server", flag.ExitOnError)
	port := fs.Int("port", 7000, "control port (data port = control port + 1)")
	debug := fs.Bool("debug", false, "enable debug logging")
	fs.Parse(args)

	m := serverui.NewModel(*port, *debug)
	p := tea.NewProgram(m,
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
	)
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func runClient() {
	c := client.New()
	m := clientui.NewModel(c)
	p := tea.NewProgram(m,
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
	)
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
