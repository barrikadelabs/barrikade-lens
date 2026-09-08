package hub

import "testing"

func TestPostureRoleKeepsRuntimeAgentDefinitionsAsComponents(t *testing.T) {
	role, systemType := postureRole("agent", map[string]any{
		"defined":           true,
		"definition_format": "agent_markdown",
		"source_surface":    "endpoint",
	})
	if role != "component" || systemType != "" {
		t.Fatalf("runtime helper posture=(%q,%q), want component with no system type", role, systemType)
	}
}

func TestPostureRolePreservesStandaloneAgentsAsSystems(t *testing.T) {
	role, systemType := postureRole("agent", map[string]any{"deployed": true, "source_surface": "kubernetes"})
	if role != "system" || systemType != "autonomous_agent" {
		t.Fatalf("standalone agent posture=(%q,%q), want autonomous system", role, systemType)
	}
}
