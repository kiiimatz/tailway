package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

func runUninstall() {
	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Could not locate binary: %v\n", err)
		os.Exit(1)
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Could not resolve binary path: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("This will remove: %s\n", exe)
	fmt.Print("Continue? [y/N] ")

	scanner := bufio.NewScanner(os.Stdin)
	scanner.Scan()
	if strings.ToLower(strings.TrimSpace(scanner.Text())) != "y" {
		fmt.Println("Cancelled.")
		return
	}

	if runtime.GOOS == "windows" {
		uninstallWindows(exe)
	} else {
		uninstallUnix(exe)
	}
}

func uninstallUnix(exe string) {
	if err := os.Remove(exe); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to remove binary: %v\n", err)
		// Try with sudo
		fmt.Println("Retrying with sudo...")
		cmd := exec.Command("sudo", "rm", exe)
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "Uninstall failed: %v\n", err)
			os.Exit(1)
		}
	}

	fmt.Printf("Removed %s\n", exe)

	// If installed in ~/.local/bin, clean up the PATH line from shell configs.
	home, _ := os.UserHomeDir()
	localBin := filepath.Join(home, ".local", "bin")
	if filepath.Dir(exe) == localBin {
		cleanShellConfigs(localBin)
	}

	fmt.Println("tailway uninstalled.")
}

// cleanShellConfigs removes the PATH export line added by install.sh.
func cleanShellConfigs(dir string) {
	home, _ := os.UserHomeDir()
	candidates := []string{
		filepath.Join(home, ".bashrc"),
		filepath.Join(home, ".zshrc"),
		filepath.Join(home, ".bash_profile"),
		filepath.Join(home, ".zprofile"),
	}

	marker := "# Added by tailway installer"

	for _, cfg := range candidates {
		data, err := os.ReadFile(cfg)
		if err != nil {
			continue
		}

		lines := strings.Split(string(data), "\n")
		var out []string
		skip := false
		for _, line := range lines {
			if strings.TrimSpace(line) == marker {
				skip = true
				continue
			}
			if skip && strings.Contains(line, dir) {
				skip = false
				continue
			}
			skip = false
			out = append(out, line)
		}

		// Trim trailing blank lines that were left behind
		for len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" {
			out = out[:len(out)-1]
		}

		result := strings.Join(out, "\n") + "\n"
		if result == string(data) {
			continue
		}
		if err := os.WriteFile(cfg, []byte(result), 0644); err == nil {
			fmt.Printf("Cleaned PATH entry from %s\n", cfg)
		}
	}
}

func uninstallWindows(exe string) {
	dir := filepath.Dir(exe) // e.g. %LOCALAPPDATA%\tailway

	// Remove the binary (and the install directory if it only contained tailway).
	if err := os.Remove(exe); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to remove binary: %v\n", err)
		os.Exit(1)
	}

	// Remove the install directory if now empty.
	entries, _ := os.ReadDir(dir)
	// Also ignore leftover .old file from previous updates.
	realEntries := 0
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".old") {
			realEntries++
		}
	}
	if realEntries == 0 {
		os.RemoveAll(dir) // remove .old files and the dir itself
	}

	fmt.Printf("Removed %s\n", exe)

	// Remove dir from the User PATH via PowerShell.
	psScript := fmt.Sprintf(`
$dir = '%s'
$path = [Environment]::GetEnvironmentVariable('PATH', 'User')
$parts = $path -split ';' | Where-Object { $_ -ne $dir -and $_ -ne '' }
[Environment]::SetEnvironmentVariable('PATH', ($parts -join ';'), 'User')
`, dir)

	cmd := exec.Command("powershell", "-NoProfile", "-Command", psScript)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not remove PATH entry automatically: %v\n", err)
		fmt.Printf("Remove manually: remove '%s' from your User PATH.\n", dir)
	} else {
		fmt.Printf("Removed %s from PATH.\n", dir)
	}

	fmt.Println("tailway uninstalled.")
	fmt.Println("(Open a new terminal for PATH changes to take effect.)")
}
