package tui

import (
	"strings"
	"testing"

	"github.com/barrikadelabs/barrikade-lens/pkg/discovery"
	tea "github.com/charmbracelet/bubbletea"
)

func TestResponsiveViewsDoNotExceedWidth(t *testing.T) {
	snapshot := discovery.NewSnapshot("org", "source", discovery.SourceEndpoint, discovery.Collector{ID: "lens", Name: "Lens", Version: "2", Mode: "test"})
	snapshot.Scope.Name = "test-device"
	snapshot.Entities = append(snapshot.Entities, discovery.Entity{ID: "agent", Kind: discovery.KindAgent, Name: "An agent with a deliberately long name that needs to wrap cleanly", Confidence: discovery.ConfidenceLikely, Attributes: map[string]any{"configured": true}})
	for _, width := range []int{60, 80, 140} {
		model := New(snapshot)
		model.noColor = true
		updated, _ := model.Update(tea.WindowSizeMsg{Width: width, Height: 24})
		model = updated.(Model)
		for tab := range tabNames {
			model.tab = tab
			for _, line := range strings.Split(model.View(), "\n") {
				if len([]rune(line)) > width {
					t.Fatalf("tab %d at width %d rendered %d columns: %q", tab, width, len([]rune(line)), line)
				}
			}
		}
	}
}
