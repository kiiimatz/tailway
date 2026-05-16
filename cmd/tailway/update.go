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
)

const (
	githubRepo = "kiiimatz/tailway"
	apiURL     = "https://api.github.com/repos/" + githubRepo + "/releases/latest"
)

type githubRelease struct {
	TagName string `json:"tag_name"`
}

// fetchLatestVersion queries the GitHub releases API and returns the latest
// tag name (e.g. "v0.2.0"). Returns "" on any error.
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

// newerVersion returns true when latest is a strictly newer semver than current.
// Both strings may optionally start with "v".
func newerVersion(current, latest string) bool {
	cur := strings.TrimPrefix(current, "v")
	lat := strings.TrimPrefix(latest, "v")
	if cur == "" || lat == "" || cur == lat {
		return false
	}
	// Split into [major, minor, patch]
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

// checkAndUpdate checks for a newer release and, if one exists, prints a notice
// and offers the user the chance to self-update before the TUI starts.
func checkAndUpdate() {
	if version == "dev" {
		return
	}

	latest := fetchLatestVersion()
	if latest == "" || !newerVersion(version, latest) {
		return
	}

	fmt.Printf("\n  Update available: %s → %s\n", version, latest)
	fmt.Printf("  Update now? [y/N] ")

	var answer string
	fmt.Scanln(&answer)
	if strings.ToLower(strings.TrimSpace(answer)) != "y" {
		fmt.Println()
		return
	}

	if err := selfUpdate(latest); err != nil {
		fmt.Fprintf(os.Stderr, "  Update failed: %v\n\n", err)
	}
}

// selfUpdate downloads the binary for the current OS/arch from the given
// release tag and replaces the running executable.
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

	fmt.Printf("  Downloading %s...\n", assetName)

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

	// Write to a temp file next to the current executable
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return err
	}

	dir := filepath.Dir(exe)
	tmp, err := os.CreateTemp(dir, ".tailway-update-*")
	if err != nil {
		// Fall back to OS temp dir
		tmp, err = os.CreateTemp("", ".tailway-update-*")
		if err != nil {
			return err
		}
	}
	tmpPath := tmp.Name()
	defer func() {
		tmp.Close()
		os.Remove(tmpPath) // no-op if rename succeeded
	}()

	if _, err := io.Copy(tmp, resp.Body); err != nil {
		return err
	}
	tmp.Close()

	if err := os.Chmod(tmpPath, 0755); err != nil {
		return err
	}

	// On Windows we cannot replace a running exe; move it aside and put the
	// new one in place, then tell the user to restart.
	if goos == "windows" {
		old := exe + ".old"
		os.Remove(old)
		if err := os.Rename(exe, old); err != nil {
			return fmt.Errorf("could not move old binary: %w", err)
		}
		if err := os.Rename(tmpPath, exe); err != nil {
			// Restore
			os.Rename(old, exe)
			return fmt.Errorf("could not place new binary: %w", err)
		}
		fmt.Printf("  Updated to %s. Please restart tailway.\n\n", tag)
		os.Exit(0)
	}

	// Unix: atomic rename then exec the new binary
	if err := os.Rename(tmpPath, exe); err != nil {
		return fmt.Errorf("could not replace binary: %w", err)
	}

	fmt.Printf("  Updated to %s. Restarting...\n\n", tag)
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
