package hub

import (
	"reflect"
	"strings"
	"testing"
)

func TestFindingCopyIsPlainAndPreservesUncertainty(t *testing.T) {
	tests := []struct {
		name, title, explanation string
		uncertainty              []string
	}{
		{"external credential", externalCredentialTitle, externalCredentialExplanation, []string{"did not read", "confirm what it can access"}},
		{"sensitive public service", sensitivePublicTitle, sensitivePublicExplanation, []string{"did not observe", "service being used"}},
		{"state-changing API", stateChangingAPITitle, stateChangingAPIExplanation, []string{"did not confirm", "observe the API being used"}},
		{"missing owner", missingOwnerTitle, missingOwnerExplanation, []string{"does not count as an owner"}},
	}
	wantTitles := []string{"External service connection uses a credential", "Sensitive AI use can connect to a public service", "Connected API may allow changes", "No owner assigned"}
	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if test.title != wantTitles[index] {
				t.Fatalf("title=%q, want %q", test.title, wantTitles[index])
			}
			for _, phrase := range test.uncertainty {
				if !strings.Contains(test.explanation, phrase) {
					t.Fatalf("explanation %q does not preserve uncertainty phrase %q", test.explanation, phrase)
				}
			}
		})
	}
}

func TestCatalogOperationClassificationAndSecurityInheritance(t *testing.T) {
	document := map[string]any{
		"security":   []any{map[string]any{"oauth": []any{"records:write"}}},
		"components": map[string]any{"securitySchemes": map[string]any{"oauth": map[string]any{"type": "oauth2"}}},
		"paths": map[string]any{
			"/records": map[string]any{
				"get":    map[string]any{"operationId": "listRecords", "tags": []any{"records"}},
				"post":   map[string]any{"operationId": "createRecord"},
				"delete": map[string]any{"operationId": "deleteRecords", "security": []any{}},
			},
		},
	}
	operations := catalogOperations(document)
	if len(operations) != 3 {
		t.Fatalf("expected three operations, got %#v", operations)
	}
	byID := map[string]catalogOperation{}
	for _, operation := range operations {
		byID[operation.ID] = operation
	}
	if byID["listRecords"].Class != "read" || byID["createRecord"].Class != "state_changing_potential" || byID["deleteRecords"].Class != "destructive_potential" {
		t.Fatalf("unexpected capability classes: %#v", byID)
	}
	if !reflect.DeepEqual(byID["createRecord"].AuthSchemes, []string{"oauth2"}) || !reflect.DeepEqual(byID["createRecord"].AuthScopes, []string{"records:write"}) {
		t.Fatalf("global security was not inherited: %#v", byID["createRecord"])
	}
	if len(byID["deleteRecords"].AuthSchemes) != 0 {
		t.Fatalf("operation-level anonymous security did not override the global requirement: %#v", byID["deleteRecords"])
	}
}

func TestPublicDestinationAndConnectorState(t *testing.T) {
	for _, test := range []struct {
		host string
		want bool
	}{{"api.example.com", true}, {"8.8.8.8", true}, {"127.0.0.1", false}, {"10.1.2.3", false}, {"localhost", false}, {"service.local", false}, {"", false}} {
		if got := isPublicDestination(test.host, map[string]any{}); got != test.want {
			t.Errorf("isPublicDestination(%q)=%v, want %v", test.host, got, test.want)
		}
	}
	if !explicitlyDisabled(map[string]any{"enabled": false}) || explicitlyDisabled(map[string]any{}) || explicitlyDisabled(map[string]any{"enabled": true}) {
		t.Fatal("enabled/not-explicitly-disabled handling changed")
	}
}

func TestFindingIDsAreDeterministicAndVersioned(t *testing.T) {
	left := newFinding("root", "destination", "missing_owner", "low", "title", "why", "next", nil, []string{"observed"})
	right := newFinding("root", "destination", "missing_owner", "high", "different copy", "different", "different", nil, []string{"observed"})
	if left.ID != right.ID {
		t.Fatalf("finding identity changed with presentation fields: %s != %s", left.ID, right.ID)
	}
}
