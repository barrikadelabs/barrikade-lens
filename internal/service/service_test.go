package service

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStageExecutableUsesPrivateStableServiceLocation(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "downloaded-lens")
	if err := os.WriteFile(source, []byte("first-binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(root, "state", "config.json")
	staged, err := stageExecutable(source, configPath)
	if err != nil {
		t.Fatal(err)
	}
	expected := filepath.Join(root, "state", "bin", "barrikade-lens")
	if staged != expected {
		t.Fatalf("staged path=%q want %q", staged, expected)
	}
	content, err := os.ReadFile(staged)
	if err != nil || string(content) != "first-binary" {
		t.Fatalf("staged executable content=%q err=%v", content, err)
	}
	info, err := os.Stat(staged)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("staged executable mode=%v", info.Mode().Perm())
	}
	directoryInfo, err := os.Stat(filepath.Dir(staged))
	if err != nil {
		t.Fatal(err)
	}
	if directoryInfo.Mode().Perm() != 0o700 {
		t.Fatalf("private bin directory mode=%v", directoryInfo.Mode().Perm())
	}

	if err := os.WriteFile(source, []byte("upgraded-binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := stageExecutable(source, configPath); err != nil {
		t.Fatal(err)
	}
	content, err = os.ReadFile(staged)
	if err != nil || string(content) != "upgraded-binary" {
		t.Fatalf("staged upgrade was not atomic: content=%q err=%v", content, err)
	}
}
