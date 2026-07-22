package tui

import (
	"strings"
	"testing"

	"github.com/barrikadelabs/barrikade-lens/pkg/discovery"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func TestResponsiveViewsDoNotExceedWidth(t *testing.T) {
	snapshot := representativeSnapshot()
	for _, noColor := range []bool{true, false} {
		for _, width := range []int{60, 80, 140} {
			model := New(snapshot)
			model.noColor = noColor
			updated, _ := model.Update(tea.WindowSizeMsg{Width: width, Height: 24})
			model = updated.(Model)
			for tab := range tabNames {
				model.tab = tab
				for _, line := range strings.Split(model.View(), "\n") {
					if lipgloss.Width(line) > width {
						t.Fatalf("color=%v tab %d at width %d rendered %d columns: %q", !noColor, tab, width, lipgloss.Width(line), line)
					}
				}
			}
		}
	}
}

func TestOverviewSeparatesRootSystemsFromSupportingSoftware(t *testing.T) {
	view := noColorView(t, representativeSnapshot(), 0, 100)
	for _, expected := range []string{
		"ROOT SYSTEMS 3",
		"0 autonomous agents   2 agent-capable tools   1 model runtimes",
		"Supporting software: 1 host applications and 1 development runtimes",
		"Possible-only systems",
		"Non-loopback services",
	} {
		if !strings.Contains(view, expected) {
			t.Fatalf("overview did not contain %q:\n%s", expected, view)
		}
	}
	if strings.Contains(view, "ROOT SYSTEMS 5") {
		t.Fatal("supporting runtimes inflated the root-system total")
	}
}

func TestSystemsUseFactualStateAndObservedUserLabels(t *testing.T) {
	view := noColorView(t, representativeSnapshot(), 1, 100)
	for _, expected := range []string{
		"OpenAI Codex  [RUNNING]",
		"Cursor  [RESIDUAL]",
		"observed user developer",
		"SUPPORTING SOFTWARE  2",
		"host application",
		"development runtime",
	} {
		if !strings.Contains(view, expected) {
			t.Fatalf("systems view did not contain %q:\n%s", expected, view)
		}
	}
}

func TestCapabilitiesShowCacheAndSystemContext(t *testing.T) {
	view := noColorView(t, representativeSnapshot(), 2, 100)
	for _, expected := range []string{"MCP servers  1", "filesystem  [CONFIGURED]", "linked to OpenAI Codex", "local/model  [CACHED]"} {
		if !strings.Contains(view, expected) {
			t.Fatalf("capabilities view did not contain %q:\n%s", expected, view)
		}
	}
}

func TestNumberKeysNavigateViews(t *testing.T) {
	model := New(representativeSnapshot())
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'4'}})
	if updated.(Model).tab != 3 {
		t.Fatalf("number key selected tab %d, want 3", updated.(Model).tab)
	}
}

func TestNoColorEnvironmentDisablesANSI(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	view := New(representativeSnapshot()).View()
	if strings.Contains(view, "\x1b[") {
		t.Fatal("NO_COLOR view contained ANSI escape sequences")
	}
	if !strings.Contains(view, "■  BARRIKADE  /  LENS") {
		t.Fatal("NO_COLOR view lost the Barrikade identity")
	}
}

func TestHeaderDisclosesMergedDiscoverySurfaces(t *testing.T) {
	snapshot := representativeSnapshot()
	snapshot.Entities = append(snapshot.Entities, discovery.Entity{ID: "repo", Kind: discovery.KindRepository, Name: "current-repository", Confidence: discovery.ConfidenceConfirmed, Attributes: map[string]any{"source_surface": "repository"}})
	view := noColorView(t, snapshot, 0, 100)
	if !strings.Contains(view, "endpoint + repository") {
		t.Fatalf("merged scan surface was hidden from the header:\n%s", view)
	}
}

func noColorView(t *testing.T, snapshot discovery.Snapshot, tab, width int) string {
	t.Helper()
	model := New(snapshot)
	model.noColor = true
	model.tab = tab
	model.width = width
	model.height = 100
	return model.View()
}

func representativeSnapshot() discovery.Snapshot {
	snapshot := discovery.NewTargetSnapshot("org", "source", "target", discovery.SourceEndpoint, discovery.Collector{ID: "lens", Name: "Lens", Version: "2", Mode: "test"})
	snapshot.Scope.Name = "engineering-mac"
	snapshot.Coverage = discovery.Coverage{DetectorsRun: 48, LocationsChecked: 110}
	snapshot.Entities = []discovery.Entity{
		{ID: "endpoint", Kind: discovery.KindEndpoint, Name: "engineering-mac", Confidence: discovery.ConfidenceConfirmed},
		{ID: "user", Kind: discovery.KindUser, Name: "developer", Confidence: discovery.ConfidenceLikely},
		{ID: "codex", Kind: discovery.KindRuntime, Name: "OpenAI Codex", Confidence: discovery.ConfidenceConfirmed, Attributes: map[string]any{"product_category": "agent_tool", "installed": true, "configured": true, "running_at_scan": true}},
		{ID: "cursor", Kind: discovery.KindRuntime, Name: "Cursor", Confidence: discovery.ConfidencePossible, Attributes: map[string]any{"product_category": "agent_tool", "state_present": true}},
		{ID: "ollama", Kind: discovery.KindRuntime, Name: "Ollama", Confidence: discovery.ConfidenceLikely, Attributes: map[string]any{"product_category": "model_runtime", "installed": true}},
		{ID: "vscode", Kind: discovery.KindRuntime, Name: "Visual Studio Code", Confidence: discovery.ConfidenceConfirmed, Attributes: map[string]any{"product_category": "host_application", "configured": true}},
		{ID: "python", Kind: discovery.KindRuntime, Name: "Python", Confidence: discovery.ConfidenceLikely, Attributes: map[string]any{"product_category": "development_runtime", "installed": true}},
		{ID: "mcp", Kind: discovery.KindMCPServer, Name: "filesystem", Confidence: discovery.ConfidenceConfirmed, Attributes: map[string]any{"configured": true, "transport": "http", "binding": "all_interfaces"}},
		{ID: "model", Kind: discovery.KindModel, Name: "local/model", Confidence: discovery.ConfidenceLikely},
		{ID: "skill", Kind: discovery.KindSkill, Name: "code-review", Confidence: discovery.ConfidenceConfirmed, Attributes: map[string]any{"configured": true}},
	}
	snapshot.Relationships = []discovery.Relationship{
		{ID: "codex-user", Kind: discovery.RelationshipOwnedBy, From: "codex", To: "user", Confidence: discovery.ConfidenceConfirmed, Attributes: map[string]any{"attribution": "observed_user", "authoritative": false}},
		{ID: "codex-mcp", Kind: discovery.RelationshipConnectsTo, From: "codex", To: "mcp", Confidence: discovery.ConfidenceConfirmed},
		{ID: "codex-skill", Kind: discovery.RelationshipProvides, From: "codex", To: "skill", Confidence: discovery.ConfidenceConfirmed},
		{ID: "model-endpoint", Kind: discovery.RelationshipRunsOn, From: "model", To: "endpoint", Confidence: discovery.ConfidenceLikely, Attributes: map[string]any{"cached": true}},
	}
	snapshot.Evidence = []discovery.Evidence{{ID: "evidence", DetectorID: "runtime.codex", Method: "config_shape", Family: "configuration", Locator: "path_hash:abc", Specificity: "high"}}
	return snapshot
}
