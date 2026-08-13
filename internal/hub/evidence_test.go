package hub

import (
	"strings"
	"testing"
)

func TestEvidenceLocationKeepsEndpointPathsPrivateAndUseful(t *testing.T) {
	locator := "sha256:8c878bbb3f824bc4"
	display, kind, integrity := evidenceLocation(&locator, "endpoint")
	if display != "Protected endpoint location" || kind != "protected_path" || integrity != locator {
		t.Fatalf("unexpected protected location: display=%q kind=%q integrity=%q", display, kind, integrity)
	}
	if strings.Contains(display, "8c878") {
		t.Fatalf("opaque path hash leaked into the user-facing location: %q", display)
	}

	repositoryPath := "agents/support/agent.yaml"
	display, kind, integrity = evidenceLocation(&repositoryPath, "repository")
	if display != repositoryPath || kind != "repository_path" || integrity != "" {
		t.Fatalf("repository-relative evidence should remain actionable: display=%q kind=%q integrity=%q", display, kind, integrity)
	}

	listener := "tcp-listener:11434"
	display, kind, _ = evidenceLocation(&listener, "endpoint")
	if display != "TCP port 11434" || kind != "network_listener" {
		t.Fatalf("listener evidence was not humanized: display=%q kind=%q", display, kind)
	}
}

func TestEvidenceMatchedFactsAreBoundedAndAllowlisted(t *testing.T) {
	attributes := map[string]any{
		"product_id":             "claude",
		"product_category":       "agent_tool",
		"source_surface":         "endpoint",
		"configured":             true,
		"configuration_scope":    "user",
		"enabled":                true,
		"environment_value":      "do-not-return",
		"configuration_body":     "do-not-return",
		"full_command_arguments": "do-not-return",
	}
	facts := evidenceMatchedFacts(attributes, "config_shape", "configuration")
	joined := ""
	for _, fact := range facts {
		joined += fact.Label + ":" + fact.Value + "\n"
	}
	for _, expected := range []string{"Product Id:claude", "Product Category:agent_tool", "Configured:Yes", "Configuration Scope:user", "Enabled:Yes"} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("expected safe matched fact %q in %q", expected, joined)
		}
	}
	for _, forbidden := range []string{"do-not-return", "Environment Value", "Configuration Body", "Full Command Arguments"} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("unsafe attribute escaped the evidence allowlist: %q", joined)
		}
	}
}

func TestEvidenceExplanationDescribesFindingAndNextStep(t *testing.T) {
	summary := evidenceSummary("Claude Code", "runtime", "", "endpoint.local", "config_shape", "configuration")
	reason := evidenceReason("config_shape", "configuration", "high", "Claude Code")
	hint := evidenceInvestigationHint("Claude Code", "runtime", "", "endpoint.local", "Protected endpoint location", "config_shape", "configuration")
	for label, value := range map[string]string{"summary": summary, "reason": reason, "hint": hint} {
		if strings.TrimSpace(value) == "" {
			t.Fatalf("%s is empty", label)
		}
	}
	if !strings.Contains(summary, "configuration structure") || !strings.Contains(reason, "product-specific configuration fields") || !strings.Contains(hint, "MCP servers") {
		t.Fatalf("evidence explanation is not investigation-oriented: summary=%q reason=%q hint=%q", summary, reason, hint)
	}
}

func TestSkillEvidenceNamesExactResourceAndSeparatesPathOnlySignals(t *testing.T) {
	subjects := []evidenceSubject{
		{ID: "runtime", Kind: "runtime", Name: "OpenAI Codex", Confidence: "confirmed"},
		{ID: "skill", Kind: "skill", Name: "imagegen", Confidence: "confirmed", Attributes: map[string]any{"descriptor_valid": true, "descriptor_format": "agent_skills", "description_present": true}},
	}
	primary := primaryEvidenceSubject(subjects, "runtime", "skill")
	if primary == nil || primary.ID != "skill" {
		t.Fatalf("skill was not selected as the exact evidence subject: %#v", primary)
	}
	if title := evidenceTitle("skill_descriptor", "skill", primary.Name); title != "Validated skill descriptor — imagegen" {
		t.Fatalf("validated skill title lost the exact resource: %q", title)
	}
	summary := evidenceSummary(primary.Name, primary.Kind, "OpenAI Codex", "endpoint.local", "skill_descriptor", "skill")
	reason := evidenceReason("skill_descriptor", "skill", "high", primary.Name)
	for _, expected := range []string{"imagegen", "OpenAI Codex", "valid SKILL.md"} {
		if !strings.Contains(summary+reason, expected) {
			t.Fatalf("deep skill evidence omitted %q: summary=%q reason=%q", expected, summary, reason)
		}
	}
	pathReason := evidenceReason("path", "skill", "high", ".system")
	if !strings.Contains(pathReason, "No valid SKILL.md") || !strings.Contains(pathReason, "path-only evidence") {
		t.Fatalf("unvalidated skill directory was overstated: %q", pathReason)
	}
}

func TestValidatedSkillEvidenceGetsSafeActionableLocation(t *testing.T) {
	subject := &evidenceSubject{Kind: "skill", Name: "imagegen", Attributes: map[string]any{"provider_product_id": "codex", "descriptor_relative": ".system/imagegen"}}
	location := evidenceResourceLocation("Protected endpoint location", "protected_path", subject, "skill_descriptor")
	if location != "~/.codex/skills/.system/imagegen/SKILL.md" {
		t.Fatalf("skill descriptor location was not made actionable: %q", location)
	}
	unsafe := &evidenceSubject{Kind: "skill", Name: "imagegen", Attributes: map[string]any{"provider_product_id": "codex", "descriptor_relative": "../private"}}
	if location := evidenceResourceLocation("Protected endpoint location", "protected_path", unsafe, "skill_descriptor"); location != "Protected endpoint location" {
		t.Fatalf("unsafe relative location escaped privacy boundary: %q", location)
	}
}
