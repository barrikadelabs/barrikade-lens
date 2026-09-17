package hub

import (
	"strings"
	"testing"
	"time"
)

func TestQuickScanFreshnessUsesExplicitEvidenceExpiry(t *testing.T) {
	now := time.Now().UTC()
	lastSeen := now.Add(-2 * time.Hour)
	expires := now.Add(time.Hour)
	if state := freshnessStateForMode("endpoint", "quick_scan", &lastSeen, &expires, now); state != "fresh" {
		t.Fatalf("quick scan should remain fresh until its explicit expiry, got %s", state)
	}
	expires = now.Add(-time.Second)
	if state := freshnessStateForMode("endpoint", "quick_scan", &lastSeen, &expires, now); state != "stale" {
		t.Fatalf("expired quick scan should be stale, got %s", state)
	}
}

func TestEndpointInstallCommandUsesPublishedLauncher(t *testing.T) {
	command := endpointInstallCommand("macos", "single-use-token", "http://localhost:8080/")
	if !strings.Contains(command, "barrikade-lens@2.0.6") {
		t.Fatalf("command does not pin the published launcher: %s", command)
	}
	if strings.Contains(command, "2.0.0") {
		t.Fatalf("command references the unpublished launcher: %s", command)
	}
	if strings.Contains(command, "localhost:8080/") {
		t.Fatalf("command should normalize the Hub URL: %s", command)
	}
}

func TestWindowsEndpointInstallRemainsOnePasteCommand(t *testing.T) {
	command := endpointInstallCommand("windows", "single'use-token", "https://lens.example/")
	if strings.Count(command, "npx ") != 1 || !strings.Contains(command, " --install") {
		t.Fatalf("Windows setup is not a single enrollment command: %s", command)
	}
	if !strings.Contains(command, "'single''use-token'") {
		t.Fatalf("Windows enrollment token was not safely quoted: %s", command)
	}
}

func TestEndpointQuickScanCommandNeverInstallsCollector(t *testing.T) {
	command := endpointQuickScanCommand("macos", "single-use-token", "https://lens.example/")
	if !strings.Contains(command, " scan --enroll ") || !strings.Contains(command, "--hub 'https://lens.example'") {
		t.Fatalf("unexpected quick scan command: %s", command)
	}
	for _, forbidden := range []string{" --install", "service install", "LaunchAgent", "systemd"} {
		if strings.Contains(command, forbidden) {
			t.Fatalf("quick scan command includes %q: %s", forbidden, command)
		}
	}
}
