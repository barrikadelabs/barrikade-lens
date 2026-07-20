package detector

import "testing"

func TestBuiltinPack(t *testing.T) {
	pack, err := Builtin()
	if err != nil {
		t.Fatal(err)
	}
	if len(pack.Runtimes) < 35 {
		t.Fatalf("expected broad runtime coverage, got %d", len(pack.Runtimes))
	}
	if len(pack.Frameworks) < 14 {
		t.Fatalf("expected framework coverage, got %d", len(pack.Frameworks))
	}
	if len(pack.ModelCaches) < 3 {
		t.Fatalf("expected local model cache coverage, got %d", len(pack.ModelCaches))
	}
	if len(pack.Listeners) < 7 {
		t.Fatalf("expected listener parity coverage, got %d", len(pack.Listeners))
	}
	if pack.CalculatedChecksum() == "" {
		t.Fatal("missing checksum")
	}
	if pack.Checksum != pack.CalculatedChecksum() {
		t.Fatal("built-in pack checksum was not attached")
	}
	categories := map[string]string{}
	for _, runtime := range pack.Runtimes {
		categories[runtime.ID] = runtime.Category
	}
	for id, category := range map[string]string{"python": "development_runtime", "nodejs": "development_runtime", "bun": "development_runtime", "uv": "development_runtime", "vscode": "host_application", "ollama": "model_runtime", "lm-studio": "model_runtime", "vllm": "model_runtime", "localai": "model_runtime", "codex": "agent_tool"} {
		if categories[id] != category {
			t.Errorf("runtime %q category=%q, want %q", id, categories[id], category)
		}
	}
	assertDetectorIDs(t, "runtime", runtimeIDs(pack), []string{
		"claude", "codex", "cursor", "copilot", "vscode", "gemini", "windsurf", "devin", "cline", "roo", "kiro", "amazon-q", "continue", "opencode", "openclaw", "aider", "goose", "factory", "grok", "hermes", "zed", "warp", "junie", "augment", "qodo", "ollama", "lm-studio", "localai", "vllm", "llama-cpp", "anythingllm", "trae",
	})
	assertDetectorIDs(t, "framework", frameworkIDs(pack), []string{
		"langchain", "langgraph", "crewai", "autogen", "semantic-kernel", "agents-sdk", "google-adk", "llamaindex", "pydantic-ai", "haystack", "smolagents", "mastra", "vercel-ai", "voltagent",
	})
}

func runtimeIDs(pack Pack) map[string]struct{} {
	ids := map[string]struct{}{}
	for _, runtime := range pack.Runtimes {
		ids[runtime.ID] = struct{}{}
	}
	return ids
}

func frameworkIDs(pack Pack) map[string]struct{} {
	ids := map[string]struct{}{}
	for _, framework := range pack.Frameworks {
		ids[framework.ID] = struct{}{}
	}
	return ids
}

func assertDetectorIDs(t *testing.T, kind string, present map[string]struct{}, required []string) {
	t.Helper()
	for _, id := range required {
		if _, exists := present[id]; !exists {
			t.Errorf("required %s detector %q is missing", kind, id)
		}
	}
}

func TestChecksumRetainsDetectorStructure(t *testing.T) {
	left := Pack{SchemaVersion: "2", ID: "test-pack", Version: "1", Runtimes: []RuntimeSignature{{ID: "one", Name: "One", Category: "unclassified", Processes: []string{"a"}}, {ID: "two", Name: "Two", Category: "unclassified", Processes: []string{"b"}}}}
	right := Pack{SchemaVersion: "2", ID: "test-pack", Version: "1", Runtimes: []RuntimeSignature{{ID: "one", Name: "One", Category: "unclassified", Processes: []string{"b"}}, {ID: "two", Name: "Two", Category: "unclassified", Processes: []string{"a"}}}}
	if left.CalculatedChecksum() == right.CalculatedChecksum() {
		t.Fatal("checksum lost detector field association")
	}
}

func TestPackV2RequiresRuntimeClassification(t *testing.T) {
	pack := Pack{SchemaVersion: "2", ID: "test-pack", Version: "1", Runtimes: []RuntimeSignature{{ID: "example", Name: "Example"}}}
	if err := pack.Validate(); err == nil {
		t.Fatal("pack without a runtime classification passed validation")
	}
	pack.Runtimes[0].Category = "agent_tool"
	if err := pack.Validate(); err != nil {
		t.Fatal(err)
	}
}
