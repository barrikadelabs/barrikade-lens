package hub

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/barrikadelabs/barrikade-lens/internal/ard"
	"github.com/barrikadelabs/barrikade-lens/pkg/discovery"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type fixtureARDProvider struct {
	document ard.Document
	calls    int
}

func (provider *fixtureARDProvider) Format() string { return ard.Format }
func (provider *fixtureARDProvider) Fetch(_ context.Context, rawURL string, state ard.FetchState) (ard.Document, error) {
	provider.calls++
	if state.ETag == provider.document.ETag {
		return ard.Document{URL: rawURL, ETag: state.ETag, LastModified: state.LastModified, NotModified: true}, nil
	}
	result := provider.document
	result.URL = rawURL
	return result, nil
}

type mappedARDProvider struct {
	documents map[string]ard.Document
	calls     []string
}

func (provider *mappedARDProvider) Format() string { return ard.Format }
func (provider *mappedARDProvider) Fetch(_ context.Context, rawURL string, _ ard.FetchState) (ard.Document, error) {
	provider.calls = append(provider.calls, rawURL)
	document, ok := provider.documents[rawURL]
	if !ok {
		return ard.Document{}, fmt.Errorf("unexpected catalog fetch %s", rawURL)
	}
	document.URL = rawURL
	return document, nil
}

func TestARDFeatureFlagRemovesRemoteSurface(t *testing.T) {
	ctx, pool := integrationPool(t)
	org := "ard-disabled-" + uuid.NewString()
	server, err := NewServer(ctx, Config{
		Pool: pool, JWTSecret: []byte("0123456789012345678901234567890123456789"),
		DevAdminToken: "disabled-admin", DefaultOrganizationID: org, DefaultOrganizationName: "ARD disabled",
		ARDDisabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM organizations WHERE id=$1`, org) })
	configRequest := httptest.NewRequest(http.MethodGet, "/v1/auth/config", nil)
	configResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(configResponse, configRequest)
	if configResponse.Code != http.StatusOK || !strings.Contains(configResponse.Body.String(), `"ard_enabled":false`) {
		t.Fatalf("disabled ARD state was not published to the UI: %d %s", configResponse.Code, configResponse.Body.String())
	}
	declarationsRequest := httptest.NewRequest(http.MethodGet, "/v1/declarations", nil)
	declarationsRequest.Header.Set("Authorization", "Bearer disabled-admin")
	declarationsResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(declarationsResponse, declarationsRequest)
	if declarationsResponse.Code != http.StatusNotFound {
		t.Fatalf("disabled ARD inventory route remained active: %d %s", declarationsResponse.Code, declarationsResponse.Body.String())
	}
}

func TestARDDeclarationsRemainSeparateAndCorrelateExactly(t *testing.T) {
	ctx, pool := integrationPool(t)
	org := "ard-" + uuid.NewString()
	endpointSource := "endpoint:" + uuid.NewString()
	catalogSource := "catalog:" + uuid.NewString()
	if _, err := pool.Exec(ctx, `INSERT INTO organizations(id,name) VALUES($1,'ARD test')`, org); err != nil {
		t.Fatal(err)
	}
	if err := insertTestSource(ctx, pool, org, endpointSource, "endpoint", "endpoint"); err != nil {
		t.Fatal(err)
	}
	if err := insertTestSource(ctx, pool, org, catalogSource, "catalog", "catalog"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM organizations WHERE id=$1`, org) })

	agentID := discovery.StableID(org, discovery.KindAgent, "observed-support")
	observed := discovery.NewSnapshot(org, endpointSource, discovery.SourceEndpoint, discovery.Collector{ID: "test", Name: "test", Version: "1", Mode: "test"})
	observed.Entities = []discovery.Entity{{
		ID: agentID, Kind: discovery.KindAgent, Name: "Support Agent", Confidence: discovery.ConfidenceConfirmed,
		Attributes: map[string]any{"running_at_scan": true, "descriptor_url": "https://agents.example.test/support.json"},
	}}
	applySnapshot(t, ctx, pool, observed)

	parsed, err := ard.Parse([]byte(`{"specVersion":"1.0","host":{"displayName":"Example"},"entries":[{"identifier":"urn:air:example.test:agent:support","displayName":"Support Agent","type":"application/a2a-agent-card+json","url":"https://agents.example.test/support.json"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	declarations, err := ard.Snapshot(parsed, ard.SnapshotOptions{
		OrganizationID: org, SourceID: catalogSource, TargetID: catalogSource, SourceType: discovery.SourceCatalog,
		SourceName: "Example", SourceLocator: "https://example.test/.well-known/ai-catalog.json", SourceSurface: "catalog",
	})
	if err != nil {
		t.Fatal(err)
	}
	applySnapshot(t, ctx, pool, declarations)

	var systems, declarationsCount, matches int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM entity_posture WHERE organization_id=$1 AND current AND system_role='system'`, org).Scan(&systems); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM resource_declarations WHERE organization_id=$1 AND current AND alignment_status='matched'`, org).Scan(&declarationsCount); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM resource_matches WHERE organization_id=$1 AND observed_entity_id=$2 AND status='linked'`, org, agentID).Scan(&matches); err != nil {
		t.Fatal(err)
	}
	if systems != 1 || declarationsCount != 1 || matches != 1 {
		t.Fatalf("expected one observed system and a separate matched declaration; systems=%d declarations=%d matches=%d", systems, declarationsCount, matches)
	}
	var declarationID, relationshipID string
	if err := pool.QueryRow(ctx, `SELECT entity_id FROM resource_declarations WHERE organization_id=$1 AND alignment_status='matched'`, org).Scan(&declarationID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT id FROM relationships WHERE organization_id=$1 AND kind='describes' AND from_entity=$2 AND to_entity=$3 AND current`, org, declarationID, agentID).Scan(&relationshipID); err != nil {
		t.Fatal(err)
	}
	if expected := discovery.RelationshipID(org, discovery.RelationshipDescribes, declarationID, agentID); relationshipID != expected {
		t.Fatalf("derived relationship ID was not canonical: got=%s want=%s", relationshipID, expected)
	}

	var observedConfidence string
	if err := pool.QueryRow(ctx, `SELECT confidence FROM entities WHERE organization_id=$1 AND id=$2`, org, agentID).Scan(&observedConfidence); err != nil {
		t.Fatal(err)
	}
	if observedConfidence != "confirmed" {
		t.Fatalf("declaration unexpectedly altered observed confidence: %s", observedConfidence)
	}
	fixedMatchTime := "2000-01-01T00:00:00Z"
	if _, err := pool.Exec(ctx, `UPDATE resource_matches SET matched_at=$4 WHERE organization_id=$1 AND declaration_entity_id=$2 AND observed_entity_id=$3`, org, declarationID, agentID, fixedMatchTime); err != nil {
		t.Fatal(err)
	}
	repeatedObserved := discovery.NewSnapshot(org, endpointSource, discovery.SourceEndpoint, discovery.Collector{ID: "test", Name: "test", Version: "1", Mode: "test"})
	repeatedObserved.Entities = observed.Entities
	applySnapshot(t, ctx, pool, repeatedObserved)
	var matchedAt string
	if err := pool.QueryRow(ctx, `SELECT to_char(matched_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"') FROM resource_matches WHERE organization_id=$1 AND declaration_entity_id=$2 AND observed_entity_id=$3`, org, declarationID, agentID).Scan(&matchedAt); err != nil {
		t.Fatal(err)
	}
	if matchedAt != fixedMatchTime {
		t.Fatalf("unchanged endpoint scan rebuilt the organization-wide ARD projection: %s", matchedAt)
	}
	changedObserved := discovery.NewSnapshot(org, endpointSource, discovery.SourceEndpoint, discovery.Collector{ID: "test", Name: "test", Version: "1", Mode: "test"})
	changedObserved.Entities = []discovery.Entity{{
		ID: agentID, Kind: discovery.KindAgent, Name: "Support Agent", Confidence: discovery.ConfidenceConfirmed,
		Attributes: map[string]any{"running_at_scan": true, "descriptor_url": "https://agents.example.test/replaced.json"},
	}}
	applySnapshot(t, ctx, pool, changedObserved)
	var linksAfterChange, correlationEvents int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM resource_matches WHERE organization_id=$1 AND declaration_entity_id=$2 AND status='linked'`, org, declarationID).Scan(&linksAfterChange)
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM changes WHERE organization_id=$1 AND entity_id=$2 AND category='declaration' AND summary='Declaration correlation changed'`, org, declarationID).Scan(&correlationEvents)
	if linksAfterChange != 0 || correlationEvents != 1 {
		t.Fatalf("material identity change did not update ARD alignment once: links=%d changes=%d", linksAfterChange, correlationEvents)
	}
}

func TestARDNameOnlyMatchRemainsSuggestion(t *testing.T) {
	ctx, pool := integrationPool(t)
	org := "ard-suggestion-" + uuid.NewString()
	source := "repository:" + uuid.NewString()
	if _, err := pool.Exec(ctx, `INSERT INTO organizations(id,name) VALUES($1,'ARD suggestion')`, org); err != nil {
		t.Fatal(err)
	}
	if err := insertTestSource(ctx, pool, org, source, "repository", "repo"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM organizations WHERE id=$1`, org) })
	snapshot := discovery.NewSnapshot(org, source, discovery.SourceRepository, discovery.Collector{ID: "test", Name: "test", Version: "1", Mode: "repo"})
	agentID := discovery.StableID(org, discovery.KindAgent, "agent")
	declarationID := discovery.StableID(org, discovery.KindResourceDeclaration, "ard:urn:air:example.test:agent:support")
	snapshot.Entities = []discovery.Entity{
		{ID: agentID, Kind: discovery.KindAgent, Name: "Support Agent", Confidence: discovery.ConfidenceConfirmed, Attributes: map[string]any{"defined": true}},
		{ID: declarationID, Kind: discovery.KindResourceDeclaration, Name: "Support Agent", Confidence: discovery.ConfidenceConfirmed, Attributes: map[string]any{"ard_identifier": "urn:air:example.test:agent:support", "publisher_domain": "example.test", "media_type": "application/a2a-agent-card+json", "mapped_kind": "agent", "delivery": "inline", "defined": true, "trust_identity_alignment": "absent", "signature_status": "absent"}},
	}
	applySnapshot(t, ctx, pool, snapshot)
	var suggested, linked int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM resource_matches WHERE organization_id=$1 AND status='suggested'`, org).Scan(&suggested)
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM resource_matches WHERE organization_id=$1 AND status='linked'`, org).Scan(&linked)
	if suggested != 1 || linked != 0 {
		t.Fatalf("name-only correlation must remain a suggestion; suggested=%d linked=%d", suggested, linked)
	}
	var changesBefore int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM changes WHERE organization_id=$1`, org).Scan(&changesBefore)
	repeated := discovery.NewSnapshot(org, source, discovery.SourceRepository, discovery.Collector{ID: "test", Name: "test", Version: "1", Mode: "repo"})
	repeated.Entities = snapshot.Entities
	applySnapshot(t, ctx, pool, repeated)
	var changesAfter int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM changes WHERE organization_id=$1`, org).Scan(&changesAfter)
	if changesAfter != changesBefore {
		t.Fatalf("unchanged declaration scan created refresh noise: before=%d after=%d", changesBefore, changesAfter)
	}
}

func TestARDInlineFingerprintCanCorrelateWithoutRetainingBody(t *testing.T) {
	ctx, pool := integrationPool(t)
	org := "ard-fingerprint-" + uuid.NewString()
	source := "repository:" + uuid.NewString()
	if _, err := pool.Exec(ctx, `INSERT INTO organizations(id,name) VALUES($1,'ARD fingerprint')`, org); err != nil {
		t.Fatal(err)
	}
	if err := insertTestSource(ctx, pool, org, source, "repository", "repo"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM organizations WHERE id=$1`, org) })
	inline := `{"identifier":"urn:protocol:example","privatePrompt":"must not persist"}`
	fingerprint := discovery.ContentHash([]byte(inline))
	observedID := discovery.StableID(org, discovery.KindAgent, "observed")
	declarationID := discovery.StableID(org, discovery.KindResourceDeclaration, "ard:urn:air:example.test:agent:inline")
	snapshot := discovery.NewSnapshot(org, source, discovery.SourceRepository, discovery.Collector{ID: "test", Name: "test", Version: "1", Mode: "repo"})
	snapshot.Entities = []discovery.Entity{
		{ID: observedID, Kind: discovery.KindAgent, Name: "Observed", Confidence: discovery.ConfidenceConfirmed, Attributes: map[string]any{"defined": true, "config_fingerprint": fingerprint}},
		{ID: declarationID, Kind: discovery.KindResourceDeclaration, Name: "Declared", Confidence: discovery.ConfidenceConfirmed, Attributes: map[string]any{
			"ard_identifier": "urn:air:example.test:agent:inline", "publisher_domain": "example.test",
			"media_type": "application/a2a-agent-card+json", "mapped_kind": "agent", "delivery": "inline",
			"defined": true, "artifact_fingerprint": fingerprint, "protocol_identity": "urn:protocol:example",
			"trust_identity_alignment": "absent", "signature_status": "absent",
		}},
	}
	applySnapshot(t, ctx, pool, snapshot)
	var reason string
	if err := pool.QueryRow(ctx, `SELECT reason FROM resource_matches WHERE organization_id=$1 AND declaration_entity_id=$2 AND observed_entity_id=$3 AND status='linked'`, org, declarationID, observedID).Scan(&reason); err != nil {
		t.Fatal(err)
	}
	if reason != "exact artifact fingerprint" {
		t.Fatalf("unexpected fingerprint match reason: %s", reason)
	}
	encoded, _ := json.Marshal(snapshot)
	if strings.Contains(string(encoded), "must not persist") {
		t.Fatal("test fixture incorrectly retained the inline body")
	}
}

func TestARDRepositoryCoordinatesCorrelateExactly(t *testing.T) {
	ctx, pool := integrationPool(t)
	org := "ard-repository-" + uuid.NewString()
	source := "repository:" + uuid.NewString()
	if _, err := pool.Exec(ctx, `INSERT INTO organizations(id,name) VALUES($1,'ARD repository')`, org); err != nil {
		t.Fatal(err)
	}
	if err := insertTestSource(ctx, pool, org, source, "repository", "repo"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM organizations WHERE id=$1`, org) })
	repositoryURL := "https://github.com/example/agents"
	descriptorPath := ".well-known/agent-card.json"
	observedID := discovery.StableID(org, discovery.KindAgent, "repository-agent")
	declarationID := discovery.StableID(org, discovery.KindResourceDeclaration, "ard:urn:air:example.test:agent:repository")
	snapshot := discovery.NewSnapshot(org, source, discovery.SourceRepository, discovery.Collector{ID: "test", Name: "test", Version: "1", Mode: "repo"})
	snapshot.Entities = []discovery.Entity{
		{ID: observedID, Kind: discovery.KindAgent, Name: "Repository Agent", Confidence: discovery.ConfidenceConfirmed, Attributes: map[string]any{
			"defined": true, "repository_url": repositoryURL, "repository_path": descriptorPath, "descriptor": descriptorPath,
		}},
		{ID: declarationID, Kind: discovery.KindResourceDeclaration, Name: "Published Repository Agent", Confidence: discovery.ConfidenceConfirmed, Attributes: map[string]any{
			"ard_identifier": "urn:air:example.test:agent:repository", "publisher_domain": "example.test",
			"media_type": "application/a2a-agent-card+json", "mapped_kind": "agent", "delivery": "url",
			"artifact_url":   "https://github.com/example/agents/blob/main/.well-known/agent-card.json",
			"repository_url": repositoryURL, "repository_path": descriptorPath,
			"defined": true, "trust_identity_alignment": "absent", "signature_status": "absent",
		}},
	}
	applySnapshot(t, ctx, pool, snapshot)
	var reason string
	if err := pool.QueryRow(ctx, `SELECT reason FROM resource_matches WHERE organization_id=$1 AND declaration_entity_id=$2 AND observed_entity_id=$3 AND status='linked'`, org, declarationID, observedID).Scan(&reason); err != nil {
		t.Fatal(err)
	}
	if reason != "exact repository and descriptor path" {
		t.Fatalf("unexpected repository match reason: %s", reason)
	}
}

func TestARDCatalogAPIRefreshExportAndRemoval(t *testing.T) {
	ctx, pool := integrationPool(t)
	org := "ard-api-" + uuid.NewString()
	parsed, err := ard.Parse([]byte(`{"specVersion":"1.0","host":{"displayName":"Example Engineering"},"entries":[{
	  "identifier":"urn:air:example.test:agent:support","displayName":"Support Agent","type":"application/a2a-agent-card+json",
	  "url":"https://agents.example.test/support.json","representativeQueries":["private customer request"],"metadata":{"token":"never"}
	}]}`))
	if err != nil {
		t.Fatal(err)
	}
	provider := &fixtureARDProvider{document: ard.Document{Result: parsed, ETag: `"fixture-v1"`, LastModified: "Tue, 28 Jul 2026 00:00:00 GMT", ContentHash: discovery.ContentHash([]byte("fixture"))}}
	server, err := NewServer(ctx, Config{
		Pool: pool, JWTSecret: []byte("0123456789012345678901234567890123456789"),
		DevAdminToken: "ard-admin", DefaultOrganizationID: org, DefaultOrganizationName: "ARD API", ARDProvider: provider,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM organizations WHERE id=$1`, org) })
	request := func(method, path, body string) *httptest.ResponseRecorder {
		input := httptest.NewRequest(method, path, strings.NewReader(body))
		input.Header.Set("Authorization", "Bearer ard-admin")
		if body != "" {
			input.Header.Set("Content-Type", "application/json")
		}
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, input)
		return response
	}

	validate := request(http.MethodPost, "/v1/admin/discovery/catalogs/validate", `{"url":"https://catalog.example.test/.well-known/ai-catalog.json","format":"ard"}`)
	if validate.Code != http.StatusOK || !strings.Contains(validate.Body.String(), `"entry_count":1`) {
		t.Fatalf("catalog validation failed: %d %s", validate.Code, validate.Body.String())
	}
	var configs int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM resource_catalog_configs WHERE organization_id=$1`, org).Scan(&configs)
	if configs != 0 {
		t.Fatal("validation unexpectedly persisted a catalog")
	}

	created := request(http.MethodPost, "/v1/admin/discovery/catalogs", `{"name":"Example","url":"https://catalog.example.test/.well-known/ai-catalog.json","format":"ard"}`)
	if created.Code != http.StatusCreated {
		t.Fatalf("catalog creation failed: %d %s", created.Code, created.Body.String())
	}
	var configID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM resource_catalog_configs WHERE organization_id=$1`, org).Scan(&configID); err != nil {
		t.Fatal(err)
	}
	ardWorker := ARDWorker{Pool: pool, Provider: provider}
	if processed, err := ardWorker.processOne(ctx); err != nil || !processed {
		t.Fatalf("catalog refresh failed: processed=%v err=%v", processed, err)
	}
	ingestion := Worker{Pool: pool}
	if processed, err := ingestion.processOne(ctx); err != nil || !processed {
		t.Fatalf("catalog ingestion failed: processed=%v err=%v", processed, err)
	}
	var declarationID string
	if err := pool.QueryRow(ctx, `SELECT entity_id FROM resource_declarations WHERE organization_id=$1 AND current`, org).Scan(&declarationID); err != nil {
		t.Fatal(err)
	}
	list := request(http.MethodGet, "/v1/declarations", "")
	if list.Code != http.StatusOK || strings.Contains(list.Body.String(), "private customer request") || strings.Contains(list.Body.String(), `"token"`) {
		t.Fatalf("declaration API leaked excluded data: %d %s", list.Code, list.Body.String())
	}
	export := request(http.MethodPost, "/v1/exports/ard", fmt.Sprintf(`{"publisher_domain":"example.test","host_display_name":"Example","entries":[{"entity_id":%q}]}`, declarationID))
	if export.Code != http.StatusOK || export.Header().Get("Content-Type") != "application/ai-catalog+json" || !strings.Contains(export.Body.String(), `"urn:air:example.test:agent:support"`) {
		t.Fatalf("ARD export failed: %d %s %s", export.Code, export.Header().Get("Content-Type"), export.Body.String())
	}
	invalidExport := request(http.MethodPost, "/v1/exports/ard", fmt.Sprintf(`{"publisher_domain":"example.test","host_display_name":"Example","entries":[{"entity_id":%q,"media_type":"not a media type"}]}`, declarationID))
	if invalidExport.Code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid export media type was accepted: %d %s", invalidExport.Code, invalidExport.Body.String())
	}
	coverage := request(http.MethodGet, "/v1/coverage", "")
	if coverage.Code != http.StatusOK || !strings.Contains(coverage.Body.String(), `"declaration_sources":[{`) || strings.Contains(coverage.Body.String(), `"target_type":"catalog"`) {
		t.Fatalf("catalog coverage was not separated from fleet population coverage: %d %s", coverage.Code, coverage.Body.String())
	}
	targets := request(http.MethodGet, "/v1/targets?include_catalog=false", "")
	if targets.Code != http.StatusOK || strings.Contains(targets.Body.String(), `"target_type":"catalog"`) {
		t.Fatalf("fleet target query included declaration targets: %d %s", targets.Code, targets.Body.String())
	}
	catalogSources := request(http.MethodGet, "/v1/admin/discovery/catalogs", "")
	if catalogSources.Code != http.StatusOK || !strings.Contains(catalogSources.Body.String(), `"etag":"\"fixture-v1\""`) || !strings.Contains(catalogSources.Body.String(), `"last_content_hash"`) {
		t.Fatalf("catalog conditional-request provenance was not exposed: %d %s", catalogSources.Code, catalogSources.Body.String())
	}

	jobsBefore := 0
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM ingestion_jobs WHERE organization_id=$1`, org).Scan(&jobsBefore)
	var declarationFreshnessBefore time.Time
	if err := pool.QueryRow(ctx, `SELECT last_seen_at FROM resource_declarations WHERE organization_id=$1 AND entity_id=$2`, org, declarationID).Scan(&declarationFreshnessBefore); err != nil {
		t.Fatal(err)
	}
	if response := request(http.MethodPost, "/v1/admin/discovery/catalogs/"+configID+"/refresh", ""); response.Code != http.StatusAccepted {
		t.Fatalf("refresh scheduling failed: %d %s", response.Code, response.Body.String())
	}
	if processed, err := ardWorker.processOne(ctx); err != nil || !processed {
		t.Fatalf("conditional refresh failed: processed=%v err=%v", processed, err)
	}
	jobsAfter := 0
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM ingestion_jobs WHERE organization_id=$1`, org).Scan(&jobsAfter)
	if jobsAfter != jobsBefore {
		t.Fatalf("not-modified refresh created an ingestion job: before=%d after=%d", jobsBefore, jobsAfter)
	}
	var declarationFreshnessAfter time.Time
	if err := pool.QueryRow(ctx, `SELECT last_seen_at FROM resource_declarations WHERE organization_id=$1 AND entity_id=$2`, org, declarationID).Scan(&declarationFreshnessAfter); err != nil {
		t.Fatal(err)
	}
	if !declarationFreshnessAfter.After(declarationFreshnessBefore) {
		t.Fatalf("not-modified refresh did not advance declaration freshness: before=%s after=%s", declarationFreshnessBefore, declarationFreshnessAfter)
	}

	removed := request(http.MethodDelete, "/v1/admin/discovery/catalogs/"+configID, "")
	if removed.Code != http.StatusNoContent {
		t.Fatalf("catalog removal failed: %d %s", removed.Code, removed.Body.String())
	}
	var currentDeclarations, currentTargets int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM resource_declarations WHERE organization_id=$1 AND current`, org).Scan(&currentDeclarations)
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM discovery_targets WHERE organization_id=$1 AND target_type='catalog' AND current`, org).Scan(&currentTargets)
	if currentDeclarations != 0 || currentTargets != 0 {
		t.Fatalf("catalog removal left current inventory: declarations=%d targets=%d", currentDeclarations, currentTargets)
	}
	var removalEvents int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM changes WHERE organization_id=$1 AND entity_id=$2 AND event_type='entity.removed' AND category='declaration'`, org, declarationID).Scan(&removalEvents); err != nil {
		t.Fatal(err)
	}
	if removalEvents != 1 {
		t.Fatalf("expected one factual declaration removal event, got %d", removalEvents)
	}
}

func TestARDNestedTraversalIsSameSiteBoundedAndDoesNotFetchArtifacts(t *testing.T) {
	rootURL := "https://catalog.example.co.uk/ai-catalog.json"
	childURL := "https://engineering.example.co.uk/catalog.json"
	rootResult, err := ard.Parse([]byte(`{"specVersion":"1.0","entries":[
	  {"identifier":"urn:air:example.co.uk:catalog:engineering","displayName":"Engineering","type":"application/ai-catalog+json","url":"https://engineering.example.co.uk/catalog.json"},
	  {"identifier":"urn:air:example.co.uk:catalog:external","displayName":"External","type":"application/ai-catalog+json","url":"https://other.example.net/catalog.json"},
	  {"identifier":"urn:air:example.co.uk:agent:root","displayName":"Root Agent","type":"application/a2a-agent-card+json","url":"https://agent.example.co.uk/card.json"}
	]}`))
	if err != nil {
		t.Fatal(err)
	}
	childResult, err := ard.Parse([]byte(`{"specVersion":"1.0","entries":[
	  {"identifier":"urn:air:example.co.uk:catalog:root","displayName":"Root","type":"application/ai-catalog+json","url":"https://catalog.example.co.uk/ai-catalog.json"},
	  {"identifier":"urn:air:example.co.uk:agent:child","displayName":"Child Agent","type":"application/a2a-agent-card+json","url":"https://agent.example.co.uk/child.json"}
	]}`))
	if err != nil {
		t.Fatal(err)
	}
	rootDocument := ard.Document{URL: rootURL, Result: rootResult, ContentHash: discovery.ContentHash([]byte("root"))}
	provider := &mappedARDProvider{documents: map[string]ard.Document{
		childURL: {Result: childResult, ContentHash: discovery.ContentHash([]byte("child"))},
	}}
	snapshot, err := ard.Snapshot(rootResult, ard.SnapshotOptions{
		OrganizationID: "org", SourceID: "source", TargetID: "target", SourceType: discovery.SourceCatalog,
		SourceName: "Root", SourceLocator: rootURL, ContentHash: rootDocument.ContentHash, SourceSurface: "catalog",
	})
	if err != nil {
		t.Fatal(err)
	}
	worker := ARDWorker{Provider: provider}
	snapshot = worker.followNestedCatalogs(context.Background(), claimedCatalog{
		OrganizationID: "org", SourceID: "source", TargetID: "target", URL: rootURL, NestedPolicy: "same_site",
	}, rootDocument, snapshot)
	catalogs, declarations, references := 0, 0, 0
	for _, entity := range snapshot.Entities {
		switch entity.Kind {
		case discovery.KindCatalog:
			catalogs++
		case discovery.KindResourceDeclaration:
			declarations++
		}
	}
	for _, relationship := range snapshot.Relationships {
		if relationship.Kind == discovery.RelationshipReferences {
			references++
		}
	}
	if len(provider.calls) != 1 || provider.calls[0] != childURL {
		t.Fatalf("unexpected nested/artifact fetches: %#v", provider.calls)
	}
	if catalogs != 2 || declarations != 5 || references != 1 {
		t.Fatalf("nested graph mismatch: catalogs=%d declarations=%d references=%d", catalogs, declarations, references)
	}
}

func applySnapshot(t *testing.T, ctx context.Context, pool interface {
	BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error)
}, snapshot discovery.Snapshot) {
	t.Helper()
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if err := normalizeSnapshot(ctx, tx, snapshot); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
}
