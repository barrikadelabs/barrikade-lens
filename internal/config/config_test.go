package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSaveUsesPrivateFilePermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "config.json")
	want := Config{HubURL: "https://lens.example.test", OrganizationID: "org", SourceID: "source", RefreshToken: "private"}
	if err := Save(path, want); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("got permissions %o", info.Mode().Perm())
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("round trip mismatch: %#v", got)
	}
}
