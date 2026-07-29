package ard

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/barrikadelabs/barrikade-lens/pkg/discovery"
)

func TestParseReducesSensitiveARDFields(t *testing.T) {
	raw := []byte(`{
	  "specVersion":"1.0",
	  "host":{"displayName":"Acme","identifier":"https://acme.example/identity?token=host-secret"},
	  "entries":[{
	    "identifier":"urn:air:acme.example:agent:support",
	    "displayName":"Support Agent",
	    "type":"application/a2a-agent-card+json",
	    "data":{"name":"Support","skills":[{"name":"triage","description":"private body"}]},
	    "description":"Customer support",
	    "representativeQueries":["send the confidential prompt"],
	    "metadata":{"api_token":"never"},
	    "trustManifest":{"identity":"spiffe://acme.example/agent/support","signature":"secret-jws","attestations":[{"type":"SOC2-Type2","uri":"https://example.test/report"}]}
	  }]
	}`)
	result, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := Snapshot(result, SnapshotOptions{
		OrganizationID: "org", SourceID: "source", TargetID: "target", SourceType: discovery.SourceCatalog,
		SourceLocator: "https://acme.example/.well-known/ai-catalog.json", SourceSurface: "catalog",
	})
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(snapshot)
	for _, forbidden := range []string{"representativeQueries", "confidential prompt", "api_token", "secret-jws", "private body", "host-secret"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("snapshot leaked %q: %s", forbidden, encoded)
		}
	}
	if !strings.Contains(string(encoded), `"trust_identity_alignment":"aligned"`) {
		t.Fatalf("expected factual trust alignment: %s", encoded)
	}
}

func TestPinnedARDV09Example(t *testing.T) {
	body, err := os.ReadFile("testdata/official-v0.9-example.json")
	if err != nil {
		t.Fatal(err)
	}
	result, err := Parse(body)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Catalog.Entries) != 3 ||
		result.Catalog.Entries[0].MappedKind != "agent" ||
		result.Catalog.Entries[1].MappedKind != "mcp_server" ||
		result.Catalog.Entries[2].MappedKind != "skill" ||
		!contains(result.Catalog.Entries[1].Capabilities, "get_weather") {
		t.Fatalf("current ARD example was not reduced correctly: %#v", result.Catalog.Entries)
	}
}

func TestParseRequiresExactlyOneDelivery(t *testing.T) {
	for _, body := range []string{
		`{"specVersion":"1.0","entries":[{"identifier":"urn:air:example.test:agent:a","displayName":"A","type":"application/a2a-agent-card+json"}]}`,
		`{"specVersion":"1.0","entries":[{"identifier":"urn:air:example.test:agent:a","displayName":"A","type":"application/a2a-agent-card+json","url":"https://example.test/a","data":{}}]}`,
	} {
		result, err := Parse([]byte(body))
		if err == nil && len(result.Catalog.Entries) != 0 {
			t.Fatalf("expected invalid entry, got %#v", result)
		}
	}
}

func TestUnknownMediaTypeIsPreserved(t *testing.T) {
	result, err := Parse([]byte(`{"specVersion":"1.2","entries":[{"identifier":"urn:air:example.test:future:one","displayName":"Future","type":"application/vnd.example.future+json","url":"https://example.test/future"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if result.Catalog.Entries[0].MappedKind != "unclassified" || result.Catalog.Entries[0].MediaType != "application/vnd.example.future+json" {
		t.Fatalf("future media type was not preserved: %#v", result.Catalog.Entries[0])
	}
}

func TestSameSiteUsesPublicSuffixBoundaries(t *testing.T) {
	if !SameSite("https://catalog.example.co.uk/ai-catalog.json", "https://nested.example.co.uk/catalog.json") {
		t.Fatal("subdomains of the same registrable site should match")
	}
	if SameSite("https://example.co.uk/ai-catalog.json", "https://attacker.co.uk/catalog.json") {
		t.Fatal("different registrable domains under a multi-label suffix must not match")
	}
}

func TestParseRejectsNonObjectInlineDataAndMalformedIdentifiers(t *testing.T) {
	for _, body := range []string{
		`{"specVersion":"1.0","entries":[{"identifier":"urn:air:example.test:agent:one","displayName":"One","type":"application/example+json","data":["not","an","object"]}]}`,
		`{"specVersion":"1.0","entries":[{"identifier":"urn:air:localhost:agent:one","displayName":"One","type":"application/example+json","data":{}}]}`,
		`{"specVersion":"1.0","entries":[{"identifier":"urn:air:example.test:agent:","displayName":"One","type":"application/example+json","data":{}}]}`,
		`{"specVersion":"1.0","entries":[{"identifier":"urn:air:example.test:agent:one","displayName":"One","type":"not a media type","data":{}}]}`,
	} {
		result, err := Parse([]byte(body))
		if err == nil && len(result.Catalog.Entries) > 0 {
			t.Fatalf("invalid catalog entry was accepted: %#v", result)
		}
	}
}

func TestSignatureIsReportedButNeverRetained(t *testing.T) {
	result, err := Parse([]byte(`{"specVersion":"1.0","entries":[
	  {"identifier":"urn:air:example.test:agent:valid","displayName":"Valid","type":"application/example+json","data":{},"trustManifest":{"identity":"did:web:example.test","signature":"header.payload.signature"}},
	  {"identifier":"urn:air:example.test:agent:broken","displayName":"Broken","type":"application/example+json","data":{},"trustManifest":{"identity":"did:web:example.test","signature":"not-a-jws"}}
	]}`))
	if err != nil {
		t.Fatal(err)
	}
	if result.Catalog.Entries[0].SignatureStatus != "present_unverified" || result.Catalog.Entries[1].SignatureStatus != "malformed" {
		t.Fatalf("signature facts were classified incorrectly: %#v", result.Catalog.Entries)
	}
	encoded, _ := json.Marshal(result)
	if strings.Contains(string(encoded), "header.payload.signature") || strings.Contains(string(encoded), "not-a-jws") {
		t.Fatal("full signature was retained")
	}
}

func TestInlineDescriptorFactsAreReducedToBoundedMetadata(t *testing.T) {
	result, err := Parse([]byte(`{"specVersion":"1.0","entries":[
	  {"identifier":"urn:air:example.test:api:billing","displayName":"Billing","type":"application/openapi+json","data":{
	    "openapi":"3.1.0","servers":[{"url":"https://api.example.test"}],
	    "paths":{"/invoices":{"get":{"operationId":"listInvoices","x-private-prompt":"do not retain"},"post":{}}}
	  }},
	  {"identifier":"urn:air:example.test:workflow:close","displayName":"Close","type":"application/arazzo+json","data":{
	    "arazzo":"1.1.0","workflows":[{"workflowId":"closeBooks","description":"do not retain"}]
	  }}
	]}`))
	if err != nil {
		t.Fatal(err)
	}
	api := result.Catalog.Entries[0]
	if api.ProtocolIdentity != "https://api.example.test/" || !contains(api.Capabilities, "listInvoices") || !contains(api.Capabilities, "POST /invoices") {
		t.Fatalf("safe OpenAPI facts were not extracted: %#v", api)
	}
	if !contains(result.Catalog.Entries[1].Capabilities, "closeBooks") {
		t.Fatalf("safe Arazzo workflow IDs were not extracted: %#v", result.Catalog.Entries[1])
	}
	encoded, _ := json.Marshal(result)
	if strings.Contains(string(encoded), "do not retain") {
		t.Fatal("inline descriptor body escaped the privacy reducer")
	}
}

func TestTrustIdentityValueIsNotSerializedIntoSnapshot(t *testing.T) {
	result, err := Parse([]byte(`{"specVersion":"1.0","entries":[{
	  "identifier":"urn:air:example.test:agent:one","displayName":"One","type":"application/a2a-agent-card+json","data":{},
	  "trustManifest":{"identity":"https://example.test/workload?credential=do-not-store","identityType":"https"}
	}]}`))
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := Snapshot(result, SnapshotOptions{OrganizationID: "org", SourceID: "source", TargetID: "target", SourceLocator: "repository/ai-catalog.json"})
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(snapshot)
	if strings.Contains(string(encoded), "do-not-store") || strings.Contains(string(encoded), "credential") {
		t.Fatalf("trust identity value leaked into snapshot: %s", encoded)
	}
}

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
