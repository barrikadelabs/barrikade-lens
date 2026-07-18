package exporter

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/barrikadelabs/barrikade-lens/pkg/discovery"
)

func exportFixture() discovery.Snapshot {
	s := discovery.NewSnapshot("org", "source", discovery.SourceRepository, discovery.Collector{ID: "lens", Name: "Lens", Version: "2", Mode: "test"})
	s.Scope.Name = "fixture"
	id := discovery.StableID("org", discovery.KindAgent, "agent")
	s.Entities = append(s.Entities, discovery.Entity{ID: id, Kind: discovery.KindAgent, Name: "Agent", Confidence: discovery.ConfidenceLikely, Attributes: map[string]any{}})
	return s
}

func TestAllExports(t *testing.T) {
	for _, format := range []Format{FormatHuman, FormatJSON, FormatNDJSON, FormatCycloneDX} {
		t.Run(string(format), func(t *testing.T) {
			var output bytes.Buffer
			if err := Write(&output, exportFixture(), format); err != nil {
				t.Fatal(err)
			}
			if output.Len() == 0 {
				t.Fatal("empty output")
			}
			if format != FormatHuman {
				first := strings.Split(strings.TrimSpace(output.String()), "\n")[0]
				if format != FormatNDJSON {
					first = output.String()
				}
				var value any
				if err := json.Unmarshal([]byte(first), &value); err != nil {
					t.Fatalf("invalid JSON: %v", err)
				}
			}
		})
	}
}
