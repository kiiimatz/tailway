package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

const (
	githubRepo = "kiiimatz/tailway"
	apiURL     = "https://api.github.com/repos/" + githubRepo + "/releases/latest"
)

type githubRelease struct {
	TagName string `json:"tag_name"`
}

// updateCheckMsg is delivered to the selector when the version check finishes.
// latestVersion is non-empty only when a newer release exists.
type updateCheckMsg struct {
	latestVersion string
}

// checkUpdateCmd is a Bubble Tea command that fetches the latest GitHub release
// in the background and returns an updateCheckMsg.
func checkUpdateCmd() tea.Msg {
	if version == "dev" {
		return updateCheckMsg{}
	}
	latest := fetchLatestVersion()
	if latest == "" || !newerVersion(version, latest) {
		return updateCheckMsg{}
	}
	return updateCheckMsg{latestVersion: latest}
}

// fetchLatestVersion queries the GitHub releases API.
// Returns "" on any error or network timeout.
func fetchLatestVersion() string {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return ""
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()

	var rel githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return ""
	}
	return rel.TagName
}

// newerVersion reports whether latest is strictly newer than current.
// Both strings may optionally start with "v".
func newerVersion(current, latest string) bool {
	cur := strings.TrimPrefix(current, "v")
	lat := strings.TrimPrefix(latest, "v")
	if cur == "" || lat == "" || cur == lat {
		return false
	}
	parse := func(s string) [3]int {
		var a, b, c int
		fmt.Sscanf(s, "%d.%d.%d", &a, &b, &c)
		return [3]int{a, b, c}
	}
	cv, lv := parse(cur), parse(lat)
	for i := range cv {
		if lv[i] > cv[i] {
			return true
		}
		if lv[i] < cv[i] {
			return false
		}
	}
	return false
}

// runUpdate is called by `tailway update`. It runs outside the TUI.
func runUpdate() {
	if version == "dev" {
		fmt.Fprintln(os.Stderr, "dev build — update not supported")
		os.Exit(1)
	}

	fmt.Println("Checking for updates...")
	latest := fetchLatestVersion()
	if latest == "" {
		fmt.Fprintln(os.Stderr, "Could not reach GitHub. Check your connection.")
		os.Exit(1)
	}
	if !newerVersion(version, latest) {
		fmt.Printf("tailway %s is already up to date.\n", version)
		return
	}

	fmt.Printf("Updating %s → %s\n", version, latest)
	if err := selfUpdate(latest); err != nil {
		fmt.Fprintf(os.Stderr, "Update failed: %v\n", err)
		os.Exit(1)
	}
}

// replaceFileUnix replaces dst with src safely on Linux/macOS.
//
// The key constraint: you must NOT open a running executable for writing
// (causes ETXTBSY). The solution is to unlink the old file first so its
// inode stays alive for the running process via its open fd, then place
// the new binary at the now-free path.
//
// When dst is in a root-owned directory (e.g. /usr/local/bin), os.Remove
// will fail with permission denied. In that case we fall back to sudo.
func replaceFileUnix(src, dst string) error {
	// Fast path: same filesystem → atomic rename (safe for running binaries).
	if err := os.Rename(src, dst); err == nil {
		return nil
	}

	// Cross-device path: unlink first, then place new file.
	if err := os.Remove(dst); err == nil {
		// Unlink succeeded — try rename (still cross-device) → copy.
		if err := os.Rename(src, dst); err == nil {
			return nil
		}
		return copyNewFile(src, dst)
	}

	// Remove failed (permission denied on protected dir) — use sudo.
	return replaceFileWithSudo(src, dst)
}

// copyNewFile writes src to dst assuming dst does not exist yet.
// Uses O_EXCL so we never accidentally truncate a running binary.
func copyNewFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0755)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
}

// replaceFileWithSudo runs `sudo sh -c "rm -f DST && mv SRC DST"`.
// rm unlinks the running binary (safe); mv then places the new one.
func replaceFileWithSudo(src, dst string) error {
	if _, err := exec.LookPath("sudo"); err != nil {
		return fmt.Errorf("no write permission to %s and sudo is not available", dst)
	}
	fmt.Printf("Needs elevated permission to write to %s — running sudo...\n", dst)
	script := "rm -f " + shellQuote(dst) + " && mv " + shellQuote(src) + " " + shellQuote(dst) + " && chmod 755 " + shellQuote(dst)
	cmd := exec.Command("sudo", "sh", "-c", script)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// selfUpdate downloads the binary for the current OS/arch and replaces the
// running executable.
func selfUpdate(tag string) error {
	goos := runtime.GOOS
	goarch := runtime.GOARCH

	var assetName string
	switch goos {
	case "linux":
		assetName = fmt.Sprintf("tailway-linux-%s", goarch)
	case "darwin":
		assetName = fmt.Sprintf("tailway-darwin-%s", goarch)
	case "windows":
		assetName = "tailway-windows-amd64.exe"
	default:
		return fmt.Errorf("unsupported OS: %s", goos)
	}

	url := fmt.Sprintf("https://github.com/%s/releases/download/%s/%s", githubRepo, tag, assetName)
	fmt.Printf("Downloading %s...\n", assetName)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	exe, err := os.Executable()
	if err != nil {
		return err
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return err
	}

	// Write new binary to a temp file. Prefer the same directory as the
	// executable so that rename is atomic (same device). Fall back to /tmp.
	tmp, err := os.CreateTemp(filepath.Dir(exe), ".tailway-update-*")
	if err != nil {
		tmp, err = os.CreateTemp("", ".tailway-update-*")
		if err != nil {
			return err
		}
	}
	tmpPath := tmp.Name()
	defer func() {
		tmp.Close()
		os.Remove(tmpPath)
	}()

	if _, err := io.Copy(tmp, resp.Body); err != nil {
		return err
	}
	tmp.Close()

	if err := os.Chmod(tmpPath, 0755); err != nil {
		return err
	}

	// Windows: can't replace a running exe — rename old aside, put new in place.
	if goos == "windows" {
		old := exe + ".old"
		os.Remove(old)
		if err := os.Rename(exe, old); err != nil {
			return fmt.Errorf("could not move old binary: %w", err)
		}
		if err := os.Rename(tmpPath, exe); err != nil {
			// Cross-device on Windows (unusual but possible).
			if copyErr := copyNewFile(tmpPath, exe); copyErr != nil {
				os.Rename(old, exe)
				return fmt.Errorf("could not place new binary: %w", err)
			}
		}
		fmt.Printf("Updated to %s. Please restart tailway.\n", tag)
		os.Exit(0)
	}

	// Unix: replace the binary, then exec the new one.
	if err := replaceFileUnix(tmpPath, exe); err != nil {
		return fmt.Errorf("could not replace binary: %w", err)
	}

	fmt.Printf("Updated to %s. Restarting...\n", tag)
	cmd := exec.Command(exe, os.Args[1:]...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			os.Exit(exitErr.ExitCode())
		}
		os.Exit(1)
	}
	os.Exit(0)
	return nil
}
