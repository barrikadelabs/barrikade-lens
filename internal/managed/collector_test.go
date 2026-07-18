package managed

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/barrikadelabs/barrikade-lens/internal/detector"
)

func TestWatchRootsIncludeRuntimeSkillConfigAndModelCacheRoots(t *testing.T) {
	home := t.TempDir()
	paths := []string{".agent", ".agent/skills", ".models"}
	for _, path := range paths {
		if err := os.MkdirAll(filepath.Join(home, path), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	pack := detector.Pack{Runtimes: []detector.RuntimeSignature{{
		Paths: []string{"~/.agent"}, SkillRoots: []string{"~/.agent/skills"},
		Configs: []detector.Config{{Path: "~/.agent/settings.json"}},
	}}, ModelCaches: []detector.ModelCache{{Paths: []string{"~/.models"}}}}
	got := watchRoots(pack, home)
	for _, expected := range []string{filepath.Join(home, ".agent"), filepath.Join(home, ".agent", "skills"), filepath.Join(home, ".models")} {
		if !slices.Contains(got, expected) {
			t.Fatalf("missing watch root %s in %v", expected, got)
		}
	}
}

func TestFullReconciliationJitterStaysWithinOneHour(t *testing.T) {
	for range 100 {
		interval := jitteredFullInterval()
		if interval < 23*time.Hour || interval >= 25*time.Hour {
			t.Fatalf("unexpected interval %s", interval)
		}
	}
}

func TestProfilesUnderExcludesSharedAndSymlinkedProfiles(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"alice", "bob", "Shared", ".hidden"} {
		if err := os.MkdirAll(filepath.Join(root, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(filepath.Join(root, "alice"), filepath.Join(root, "alias")); err != nil {
		t.Fatal(err)
	}
	profiles := profilesUnder(profile{Home: "/root", Username: "root"}, root)
	if len(profiles) != 3 || profiles[1].Username != "alice" || profiles[2].Username != "bob" {
		t.Fatalf("unexpected profiles: %#v", profiles)
	}
}
