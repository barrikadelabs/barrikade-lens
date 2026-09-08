package hub

import (
	"strings"
	"testing"
)

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
