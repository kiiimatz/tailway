package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/kiiimatz/tailway/internal/client"
	clientui "github.com/kiiimatz/tailway/internal/client/ui"
	"github.com/kiiimatz/tailway/internal/config"
	serverui "github.com/kiiimatz/tailway/internal/server/ui"
)

// version is set at build time via -ldflags "-X main.version=vX.Y.Z"
var version = "dev"

const usage = `tailway — TCP/UDP reverse tunnel proxy

Usage:
  tailway                    interactive mode selector
  tailway server [--port N]  start server
  tailway client             start client
  tailway config             edit saved config
  tailway update             update to latest release
  tailway uninstall          remove tailway

Flags:
  --c, --config   use saved config (skip prompts)
`

func main() {
	// Strip --c / --config from args; collect the rest.
	useConfig := false
	var args []string
	for _, a := range os.Args[1:] {
		if a == "--c" || a == "--config" {
			useConfig = true
		} else {
			args = append(args, a)
		}
	}

	if len(args) == 0 {
		runSelector(useConfig)
		return
	}

	switch args[0] {
	case "server":
		runServer(args[1:], useConfig)
	case "client":
		runClient(useConfig)
	case "config":
		runConfigTUI()
	case "update":
		runUpdate()
	case "uninstall":
		runUninstall()
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n%s", args[0], usage)
		os.Exit(1)
	}
}

// loadConfigOrExit loads the saved config. If useConfig is false, returns an
// empty config (callers get empty strings for IP/key → normal prompt flow).
func loadConfigOrExit(useConfig bool) *config.Config {
	if !useConfig {
		return &config.Config{}
	}
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "could not load config: %v\n", err)
		os.Exit(1)
	}
	return cfg
}

func runServer(serverArgs []string, useConfig bool) {
	fs := flag.NewFlagSet("server", flag.ExitOnError)
	port := fs.Int("port", 7000, "control port (data port = control port + 1)")
	debug := fs.Bool("debug", false, "enable debug logging")

	// Strip our custom flags before passing to flag.Parse
	var clean []string
	for _, a := range serverArgs {
		if a != "--c" && a != "--config" {
			clean = append(clean, a)
		} else {
			useConfig = true
		}
	}
	fs.Parse(clean)

	cfg := loadConfigOrExit(useConfig)

	// If useConfig but no key saved, fall through to normal key prompt.
	autoKey := ""
	if useConfig && cfg.ServerKey != "" {
		autoKey = cfg.ServerKey
	}

	m := serverui.NewModel(*port, *debug, autoKey)
	p := tea.NewProgram(m,
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
	)
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func runClient(useConfig bool) {
	cfg := loadConfigOrExit(useConfig)

	autoIP, autoKey := "", ""
	if useConfig && cfg.ClientIP != "" && cfg.ClientKey != "" {
		autoIP = strings.TrimSpace(cfg.ClientIP)
		autoKey = cfg.ClientKey
	}

	c := client.New()
	m := clientui.NewModel(c, autoIP, autoKey)
	p := tea.NewProgram(m,
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
	)
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
