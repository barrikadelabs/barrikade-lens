package hub

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/barrikadelabs/barrikade-lens/internal/catalog"
	"github.com/barrikadelabs/barrikade-lens/pkg/discovery"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type CatalogWorker struct {
	Pool     *pgxpool.Pool
	Provider catalog.Provider
	Logger   *slog.Logger
	Interval time.Duration
	index    catalog.Index
	state    catalog.State
}

func (w *CatalogWorker) Run(ctx context.Context) error {
	if w.Provider == nil {
		return fmt.Errorf("catalog provider is required")
	}
	if w.Logger == nil {
		w.Logger = slog.Default()
	}
	if w.Interval == 0 {
		w.Interval = 6 * time.Hour
	}
	if err := w.refreshAndEnrich(ctx); err != nil {
		w.Logger.Warn("catalog enrichment unavailable; discovery remains available", "error", err)
	}
	ticker := time.NewTicker(w.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := w.refreshAndEnrich(ctx); err != nil {
				w.Logger.Warn("catalog enrichment unavailable; using current inventory without it", "error", err)
			}
		}
	}
}

func (w *CatalogWorker) refreshAndEnrich(ctx context.Context) error {
	connection, err := w.Pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer connection.Release()
	var locked bool
	if err := connection.QueryRow(ctx, `SELECT pg_try_advisory_lock(hashtext('barrikade-lens-catalog'))`).Scan(&locked); err != nil {
		return err
	}
	if !locked {
		return nil
	}
	defer connection.Exec(context.Background(), `SELECT pg_advisory_unlock(hashtext('barrikade-lens-catalog'))`)
	index, err := w.Provider.Refresh(ctx, w.state)
	if err != nil {
		return err
	}
	if !index.NotModified {
		w.index = index
		w.state = catalog.State{ETag: index.ETag, Modified: index.Modified, SourceCommit: index.SourceCommit}
	}
	if len(w.index.Entries) == 0 {
		return fmt.Errorf("catalog index is empty")
	}
	organizations, err := w.Pool.Query(ctx, `SELECT id FROM organizations`)
	if err != nil {
		return err
	}
	orgIDs := []string{}
	for organizations.Next() {
		var id string
		if organizations.Scan(&id) == nil {
			orgIDs = append(orgIDs, id)
		}
	}
	organizations.Close()
	for _, orgID := range orgIDs {
		if err := w.enrichOrganization(ctx, orgID); err != nil {
			return err
		}
	}
	return nil
}

func (w *CatalogWorker) enrichOrganization(ctx context.Context, orgID string) error {
	configuration, _ := json.Marshal(map[string]any{"provider": w.Provider.ID(), "display_name": w.Provider.DisplayName()})
	_, err := w.Pool.Exec(ctx, `INSERT INTO catalog_sources(organization_id,id,provider_type,display_name,configuration,etag,source_commit,refreshed_at) VALUES($1,$2,'oak',$3,$4,$5,$6,now()) ON CONFLICT(organization_id,id) DO UPDATE SET display_name=EXCLUDED.display_name,configuration=EXCLUDED.configuration,etag=EXCLUDED.etag,source_commit=EXCLUDED.source_commit,refreshed_at=now()`, orgID, w.Provider.ID(), w.Provider.DisplayName(), configuration, nullString(w.state.ETag), nullString(w.state.SourceCommit))
	if err != nil {
		return err
	}
	if _, err := w.Pool.Exec(ctx, `DELETE FROM catalog_index_entries WHERE organization_id=$1 AND source_id=$2`, orgID, w.Provider.ID()); err != nil {
		return err
	}
	indexRows := make([][]any, 0, len(w.index.Entries))
	for _, entry := range w.index.Entries {
		indexRows = append(indexRows, []any{orgID, w.Provider.ID(), entry.ID, entry.ProviderID, nullString(entry.APIFamily), nullString(entry.Version), cleanCatalogText(entry.Name, 500), entry.Reference})
	}
	if _, err := w.Pool.CopyFrom(ctx, pgx.Identifier{"catalog_index_entries"}, []string{"organization_id", "source_id", "entry_id", "provider_id", "api_family", "api_version", "display_name", "entry_reference"}, pgx.CopyFromRows(indexRows)); err != nil {
		return err
	}
	provenance := "catalog:" + w.Provider.ID()
	if _, err := w.Pool.Exec(ctx, `UPDATE entities SET current=false,stale=true WHERE organization_id=$1 AND provenance @> ARRAY[$2::text]`, orgID, provenance); err != nil {
		return err
	}
	if _, err := w.Pool.Exec(ctx, `UPDATE relationships SET current=false,stale=true WHERE organization_id=$1 AND attributes->>'catalog_provider'=$2`, orgID, w.Provider.ID()); err != nil {
		return err
	}
	if _, err := w.Pool.Exec(ctx, `DELETE FROM catalog_matches WHERE organization_id=$1 AND source_id=$2`, orgID, w.Provider.ID()); err != nil {
		return err
	}
	rows, err := w.Pool.Query(ctx, `SELECT id,COALESCE(attributes->>'host',''),COALESCE(attributes->>'provider_id','') FROM entities WHERE organization_id=$1 AND current=true AND kind IN ('mcp_server','api_service','model_server') AND ((attributes ? 'host' OR attributes ? 'provider_id') OR EXISTS(SELECT 1 FROM catalog_link_overrides o WHERE o.organization_id=entities.organization_id AND o.entity_id=entities.id AND o.source_id=$2))`, orgID, w.Provider.ID())
	if err != nil {
		return err
	}
	type candidate struct{ id, host, provider string }
	candidates := []candidate{}
	for rows.Next() {
		var item candidate
		if rows.Scan(&item.id, &item.host, &item.provider) == nil {
			candidates = append(candidates, item)
		}
	}
	rows.Close()
	for _, candidate := range candidates {
		matches := w.Provider.Match(w.index, candidate.host, candidate.provider)
		var overrideReference string
		if err := w.Pool.QueryRow(ctx, `SELECT entry_reference FROM catalog_link_overrides WHERE organization_id=$1 AND entity_id=$2 AND source_id=$3`, orgID, candidate.id, w.Provider.ID()).Scan(&overrideReference); err == nil {
			matches = nil
			for _, entry := range w.index.Entries {
				if entry.Reference == overrideReference {
					entry.MatchHost = candidate.host
					matches = []catalog.Match{{Entry: entry, Confidence: "confirmed", Exact: true, Reason: "administrator-reviewed catalogue link"}}
					break
				}
			}
		}
		for index, match := range matches {
			if index >= 5 {
				break
			}
			status := "suggested"
			if match.Exact && match.Confidence == "confirmed" {
				status = "linked"
			}
			metadata := map[string]any{"catalog_id": match.Entry.ID, "catalog_name": match.Entry.Name, "provider": match.Entry.ProviderID, "api_family": match.Entry.APIFamily, "catalog_version": match.Entry.Version, "reason": match.Reason, "source_ref": match.Entry.Reference, "source_etag": w.state.ETag, "source_commit": w.state.SourceCommit}
			apiID := match.Entry.ID
			if status == "linked" {
				var cachedAPIID, cachedETag, cachedSHA string
				var cachedMetadata []byte
				_ = w.Pool.QueryRow(ctx, `SELECT api_id,COALESCE(etag,''),COALESCE(source_sha,''),metadata FROM catalog_documents WHERE organization_id=$1 AND source_id=$2 AND metadata->>'source_ref'=$3 ORDER BY cached_at DESC LIMIT 1`, orgID, w.Provider.ID(), match.Entry.Reference).Scan(&cachedAPIID, &cachedETag, &cachedSHA, &cachedMetadata)
				document, fetchErr := w.Provider.Fetch(ctx, match.Entry, catalog.State{ETag: cachedETag, SourceCommit: cachedSHA})
				if fetchErr != nil {
					w.Logger.Debug("catalog detail fetch failed", "entry", match.Entry.ID, "error", fetchErr)
					status = "suggested"
				} else if document.API.ID == "" && cachedAPIID != "" {
					apiID = cachedAPIID
					prior := jsonObject(cachedMetadata)
					for key, value := range metadata {
						prior[key] = value
					}
					metadata = prior
					if err := w.reactivateCapabilityGraph(ctx, orgID, candidate.id, apiID); err != nil {
						return err
					}
				} else {
					apiID = document.API.ID
					if apiID == "" {
						apiID = match.Entry.ID
					}
					metadata = mergeCatalogMetadata(metadata, document)
					if err := w.cacheDocument(ctx, orgID, apiID, document, metadata); err != nil {
						return err
					}
					if err := w.upsertCapabilityGraph(ctx, orgID, candidate.id, apiID, match.Confidence, document); err != nil {
						return err
					}
				}
			}
			encoded, _ := json.Marshal(metadata)
			_, err = w.Pool.Exec(ctx, `INSERT INTO catalog_matches(organization_id,entity_id,source_id,api_id,confidence,status,metadata,matched_at) VALUES($1,$2,$3,$4,$5,$6,$7,now()) ON CONFLICT(organization_id,entity_id,source_id,api_id) DO UPDATE SET confidence=EXCLUDED.confidence,status=EXCLUDED.status,metadata=EXCLUDED.metadata,matched_at=now()`, orgID, candidate.id, w.Provider.ID(), apiID, match.Confidence, status, encoded)
			if err != nil {
				return err
			}
		}
	}
	_, _ = w.Pool.Exec(ctx, `INSERT INTO exposure_evaluation_jobs(organization_id,status,next_attempt_at,updated_at) VALUES($1,'pending',now(),now()) ON CONFLICT(organization_id) DO UPDATE SET status='pending',next_attempt_at=now(),updated_at=now()`, orgID)
	return nil
}

func (w *CatalogWorker) reactivateCapabilityGraph(ctx context.Context, orgID, candidateID, apiID string) error {
	serviceID := discovery.StableID(orgID, discovery.KindAPIService, "catalog:"+w.Provider.ID()+":api:"+apiID)
	if _, err := w.Pool.Exec(ctx, `UPDATE entities SET current=true,stale=false,last_seen_at=now() WHERE organization_id=$1 AND id=$2`, orgID, serviceID); err != nil {
		return err
	}
	return w.upsertCatalogRelationship(ctx, orgID, candidateID, serviceID, discovery.RelationshipConnectsTo, discovery.ConfidenceConfirmed)
}

func (w *CatalogWorker) upsertCapabilityGraph(ctx context.Context, orgID, candidateID, apiID, confidence string, document catalog.Document) error {
	provenance := "catalog:" + w.Provider.ID()
	canonical := provenance + ":api:" + apiID
	serviceID := discovery.StableID(orgID, discovery.KindAPIService, canonical)
	entityConfidence := discovery.ConfidenceLikely
	if confidence == "confirmed" {
		entityConfidence = discovery.ConfidenceConfirmed
	}
	operations := catalogOperations(document.OpenAPI)
	auth := operationAuthTypes(operations)
	workflows, _ := arazzoMetadata(document.Arazzo)
	attributes := map[string]any{
		"catalog_enriched": true, "catalog_provider": w.Provider.DisplayName(), "api_id": apiID,
		"operation_count": len(operations), "workflow_count": len(workflows),
	}
	if document.API.Description != "" {
		attributes["description"] = cleanCatalogText(document.API.Description, 2000)
	}
	if document.API.BaseURL != "" {
		attributes["base_url"] = document.API.BaseURL
		attributes["host"] = document.API.Host
	}
	if document.API.Version != "" {
		attributes["version"] = document.API.Version
	}
	if len(auth) > 0 {
		attributes["auth_scheme_types"] = auth
	}
	encoded, _ := json.Marshal(attributes)
	name := cleanCatalogText(document.API.Name, 500)
	if name == "" {
		name = apiID
	}
	_, err := w.Pool.Exec(ctx, `INSERT INTO entities(organization_id,id,kind,canonical_key,name,attributes,confidence,provenance,current,stale,first_seen_at,last_seen_at) VALUES($1,$2,'api_service',$3,$4,$5,$6,ARRAY[$7::text],true,false,now(),now()) ON CONFLICT(organization_id,id) DO UPDATE SET name=EXCLUDED.name,attributes=EXCLUDED.attributes,confidence=EXCLUDED.confidence,provenance=EXCLUDED.provenance,current=true,stale=false,last_seen_at=now()`, orgID, serviceID, canonical, name, encoded, entityConfidence, provenance)
	if err != nil {
		return err
	}
	if err := w.upsertCatalogRelationship(ctx, orgID, candidateID, serviceID, discovery.RelationshipConnectsTo, entityConfidence); err != nil {
		return err
	}
	if _, err := w.Pool.Exec(ctx, `DELETE FROM catalog_api_operations WHERE organization_id=$1 AND source_id=$2 AND api_id=$3`, orgID, w.Provider.ID(), apiID); err != nil {
		return err
	}
	for _, operation := range operations {
		_, err = w.Pool.Exec(ctx, `INSERT INTO catalog_api_operations(organization_id,source_id,api_id,operation_key,operation_id,method,path,summary,tags,capability_class,auth_scheme_types,auth_scopes) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`, orgID, w.Provider.ID(), apiID, operation.Key, operation.ID, operation.Method, operation.Path, nullString(operation.Summary), operation.Tags, operation.Class, operation.AuthSchemes, operation.AuthScopes)
		if err != nil {
			return err
		}
	}
	return nil
}

func (w *CatalogWorker) upsertCatalogRelationship(ctx context.Context, orgID, from, to string, kind discovery.RelationshipKind, confidence discovery.Confidence) error {
	id := discovery.RelationshipID(orgID, kind, from, to)
	attributes, _ := json.Marshal(map[string]any{"catalog_provider": w.Provider.ID(), "catalog_enriched": true})
	_, err := w.Pool.Exec(ctx, `INSERT INTO relationships(organization_id,id,kind,from_entity,to_entity,attributes,confidence,current,stale,first_seen_at,last_seen_at) VALUES($1,$2,$3,$4,$5,$6,$7,true,false,now(),now()) ON CONFLICT(organization_id,id) DO UPDATE SET attributes=EXCLUDED.attributes,confidence=EXCLUDED.confidence,current=true,stale=false,last_seen_at=now()`, orgID, id, kind, from, to, attributes, confidence)
	return err
}

func (w *CatalogWorker) cacheDocument(ctx context.Context, orgID, apiID string, document catalog.Document, metadata map[string]any) error {
	encoded, _ := json.Marshal(metadata)
	sourceRef := document.API.OpenAPIReference
	if sourceRef == "" {
		sourceRef = "catalog:" + apiID
	}
	_, err := w.Pool.Exec(ctx, `INSERT INTO catalog_documents(organization_id,source_id,api_id,document_type,source_ref,etag,source_sha,metadata,cached_at) VALUES($1,$2,$3,'api',$4,$5,$6,$7,now()) ON CONFLICT(organization_id,source_id,api_id,document_type) DO UPDATE SET source_ref=EXCLUDED.source_ref,etag=EXCLUDED.etag,source_sha=EXCLUDED.source_sha,metadata=EXCLUDED.metadata,cached_at=now()`, orgID, w.Provider.ID(), apiID, sourceRef, nullString(document.ETag), nullString(document.SourceSHA), encoded)
	return err
}

func mergeCatalogMetadata(base map[string]any, document catalog.Document) map[string]any {
	base["api_id"] = document.API.ID
	base["title"] = cleanCatalogText(document.API.Name, 500)
	if description := cleanCatalogText(document.API.Description, 2000); description != "" {
		base["description"] = description
	}
	if document.API.BaseURL != "" {
		base["base_url"] = document.API.BaseURL
	}
	if document.API.Version != "" {
		base["version"] = document.API.Version
	}
	operations := catalogOperations(document.OpenAPI)
	auth := operationAuthTypes(operations)
	base["operation_count"] = len(operations)
	if len(auth) > 0 {
		base["auth_scheme_types"] = auth
	}
	workflows, versions := arazzoMetadata(document.Arazzo)
	base["workflow_count"] = len(workflows)
	if len(workflows) > 0 {
		base["workflows"] = workflows
	}
	if len(versions) > 0 {
		sort.Strings(versions)
		base["arazzo_versions"] = versions
	}
	return base
}

func arazzoMetadata(documents []map[string]any) ([]string, []string) {
	workflows := []string{}
	versions := []string{}
	for _, arazzo := range documents {
		version, _ := arazzo["arazzo"].(string)
		if !strings.HasPrefix(version, "1.") {
			continue
		}
		versions = append(versions, version)
		items, _ := arazzo["workflows"].([]any)
		for index, raw := range items {
			item, _ := raw.(map[string]any)
			name, _ := item["workflowId"].(string)
			if name == "" {
				name, _ = item["name"].(string)
			}
			if name == "" {
				name = fmt.Sprintf("workflow-%d", index+1)
			}
			workflows = append(workflows, name)
		}
	}
	sort.Strings(workflows)
	sort.Strings(versions)
	if len(workflows) > 2000 {
		workflows = workflows[:2000]
	}
	return workflows, versions
}

type catalogOperation struct {
	Key, ID, Method, Path, Summary, Class string
	Tags, AuthSchemes, AuthScopes         []string
}

func catalogOperations(document map[string]any) []catalogOperation {
	schemeTypes := map[string]string{}
	components, _ := document["components"].(map[string]any)
	schemes, _ := components["securitySchemes"].(map[string]any)
	for name, raw := range schemes {
		scheme, _ := raw.(map[string]any)
		if kind, _ := scheme["type"].(string); kind != "" {
			schemeTypes[name] = kind
		}
	}
	globalSecurity, globalSecuritySet := document["security"]
	paths, _ := document["paths"].(map[string]any)
	result := []catalogOperation{}
	for path, raw := range paths {
		methods, _ := raw.(map[string]any)
		for method, operationRaw := range methods {
			method = strings.ToUpper(method)
			class := ""
			switch method {
			case "GET", "HEAD", "OPTIONS":
				class = "read"
			case "POST", "PUT", "PATCH":
				class = "state_changing_potential"
			case "DELETE":
				class = "destructive_potential"
			default:
				continue
			}
			operation, ok := operationRaw.(map[string]any)
			if !ok {
				continue
			}
			id, _ := operation["operationId"].(string)
			if id == "" {
				id = method + " " + path
			}
			summary, _ := operation["summary"].(string)
			tags := stringList(operation["tags"])
			security := globalSecurity
			securitySet := globalSecuritySet
			if value, exists := operation["security"]; exists {
				security, securitySet = value, true
			}
			authTypes, authScopes := effectiveSecurity(security, securitySet, schemeTypes)
			result = append(result, catalogOperation{Key: discovery.ContentHash([]byte(method + "\x00" + path + "\x00" + id)), ID: cleanCatalogText(id, 500), Method: method, Path: cleanCatalogText(path, 1000), Summary: cleanCatalogText(summary, 1000), Class: class, Tags: tags, AuthSchemes: authTypes, AuthScopes: authScopes})
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Path == result[j].Path {
			return result[i].Method < result[j].Method
		}
		return result[i].Path < result[j].Path
	})
	return result
}

func effectiveSecurity(raw any, set bool, schemeTypes map[string]string) ([]string, []string) {
	if !set {
		return []string{}, []string{}
	}
	requirements, _ := raw.([]any)
	types, scopes := map[string]bool{}, map[string]bool{}
	for _, requirementRaw := range requirements {
		requirement, _ := requirementRaw.(map[string]any)
		for schemeName, scopeRaw := range requirement {
			kind := schemeTypes[schemeName]
			if kind == "" {
				kind = schemeName
			}
			types[kind] = true
			for _, scope := range stringList(scopeRaw) {
				scopes[scope] = true
			}
		}
	}
	return sortedKeys(types), sortedKeys(scopes)
}

func stringList(raw any) []string {
	items, _ := raw.([]any)
	result := make([]string, 0, len(items))
	for _, item := range items {
		if value, ok := item.(string); ok && value != "" {
			result = append(result, cleanCatalogText(value, 500))
		}
	}
	sort.Strings(result)
	return result
}

func sortedKeys(values map[string]bool) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func operationAuthTypes(operations []catalogOperation) []string {
	values := map[string]bool{}
	for _, operation := range operations {
		for _, kind := range operation.AuthSchemes {
			values[kind] = true
		}
	}
	return sortedKeys(values)
}

func cleanCatalogText(value string, limit int) string {
	value = strings.TrimSpace(strings.Map(func(r rune) rune {
		if r == '\x00' {
			return -1
		}
		return r
	}, value))
	runes := []rune(value)
	if len(runes) > limit {
		return string(runes[:limit])
	}
	return value
}
