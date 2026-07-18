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
	rows, err := w.Pool.Query(ctx, `SELECT id,COALESCE(attributes->>'host',''),COALESCE(attributes->>'provider_id','') FROM entities WHERE organization_id=$1 AND current=true AND kind IN ('mcp_server','api_service','model_server') AND (attributes ? 'host' OR attributes ? 'provider_id')`, orgID)
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
		for index, match := range matches {
			if index >= 5 {
				break
			}
			status := "suggested"
			if match.Confidence == "confirmed" || match.Confidence == "likely" {
				status = "linked"
			}
			metadata := map[string]any{"catalog_name": match.Entry.Name, "reason": match.Reason, "source_ref": match.Entry.Reference, "source_etag": w.state.ETag}
			apiID := match.Entry.ID
			if status == "linked" {
				document, fetchErr := w.Provider.Fetch(ctx, match.Entry, catalog.State{})
				if fetchErr != nil {
					w.Logger.Debug("catalog detail fetch failed", "entry", match.Entry.ID, "error", fetchErr)
					status = "suggested"
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
	return nil
}

func (w *CatalogWorker) upsertCapabilityGraph(ctx context.Context, orgID, candidateID, apiID, confidence string, document catalog.Document) error {
	provenance := "catalog:" + w.Provider.ID()
	canonical := provenance + ":api:" + apiID
	serviceID := discovery.StableID(orgID, discovery.KindAPIService, canonical)
	entityConfidence := discovery.ConfidenceLikely
	if confidence == "confirmed" {
		entityConfidence = discovery.ConfidenceConfirmed
	}
	operations, auth, _ := openAPIMetadata(document.OpenAPI)
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
	for _, operation := range operations {
		operation = cleanCatalogText(operation, 500)
		if operation == "" {
			continue
		}
		operationCanonical := canonical + ":operation:" + operation
		operationID := discovery.StableID(orgID, discovery.KindAPIOperation, operationCanonical)
		operationAttributes, _ := json.Marshal(map[string]any{"catalog_enriched": true, "catalog_provider": w.Provider.DisplayName(), "operation_id": operation})
		_, err = w.Pool.Exec(ctx, `INSERT INTO entities(organization_id,id,kind,canonical_key,name,attributes,confidence,provenance,current,stale,first_seen_at,last_seen_at) VALUES($1,$2,'api_operation',$3,$4,$5,$6,ARRAY[$7::text],true,false,now(),now()) ON CONFLICT(organization_id,id) DO UPDATE SET name=EXCLUDED.name,attributes=EXCLUDED.attributes,confidence=EXCLUDED.confidence,provenance=EXCLUDED.provenance,current=true,stale=false,last_seen_at=now()`, orgID, operationID, operationCanonical, operation, operationAttributes, entityConfidence, provenance)
		if err != nil {
			return err
		}
		if err := w.upsertCatalogRelationship(ctx, orgID, serviceID, operationID, discovery.RelationshipProvides, entityConfidence); err != nil {
			return err
		}
	}
	for _, workflow := range workflows {
		workflow = cleanCatalogText(workflow, 500)
		if workflow == "" {
			continue
		}
		workflowCanonical := canonical + ":workflow:" + workflow
		workflowID := discovery.StableID(orgID, discovery.KindWorkflow, workflowCanonical)
		workflowAttributes, _ := json.Marshal(map[string]any{"catalog_enriched": true, "catalog_provider": w.Provider.DisplayName(), "workflow_id": workflow})
		_, err = w.Pool.Exec(ctx, `INSERT INTO entities(organization_id,id,kind,canonical_key,name,attributes,confidence,provenance,current,stale,first_seen_at,last_seen_at) VALUES($1,$2,'workflow',$3,$4,$5,$6,ARRAY[$7::text],true,false,now(),now()) ON CONFLICT(organization_id,id) DO UPDATE SET name=EXCLUDED.name,attributes=EXCLUDED.attributes,confidence=EXCLUDED.confidence,provenance=EXCLUDED.provenance,current=true,stale=false,last_seen_at=now()`, orgID, workflowID, workflowCanonical, workflow, workflowAttributes, entityConfidence, provenance)
		if err != nil {
			return err
		}
		if err := w.upsertCatalogRelationship(ctx, orgID, serviceID, workflowID, discovery.RelationshipProvides, entityConfidence); err != nil {
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
	operations, auth, scoring := openAPIMetadata(document.OpenAPI)
	base["operation_count"] = len(operations)
	if len(operations) > 0 {
		base["operations"] = operations
	}
	if len(auth) > 0 {
		base["auth_scheme_types"] = auth
	}
	if len(scoring) > 0 {
		base["scoring_metadata"] = scoring
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

func openAPIMetadata(document map[string]any) ([]string, []string, map[string]any) {
	operations := []string{}
	authSet := map[string]bool{}
	scoring := map[string]any{}
	paths, _ := document["paths"].(map[string]any)
	for path, raw := range paths {
		methods, _ := raw.(map[string]any)
		for method, operation := range methods {
			switch strings.ToLower(method) {
			case "get", "put", "post", "delete", "options", "head", "patch", "trace":
				if operationMap, ok := operation.(map[string]any); ok {
					if id, ok := operationMap["operationId"].(string); ok {
						operations = append(operations, id)
					} else {
						operations = append(operations, strings.ToUpper(method)+" "+path)
					}
				}
			}
		}
	}
	components, _ := document["components"].(map[string]any)
	schemes, _ := components["securitySchemes"].(map[string]any)
	for _, raw := range schemes {
		scheme, _ := raw.(map[string]any)
		if kind, ok := scheme["type"].(string); ok {
			authSet[kind] = true
		}
	}
	for key, value := range document {
		lower := strings.ToLower(key)
		if strings.HasPrefix(lower, "x-score") || strings.HasPrefix(lower, "x-quality") {
			switch value.(type) {
			case string, float64, bool:
				scoring[key] = value
			}
		}
	}
	auth := []string{}
	for kind := range authSet {
		auth = append(auth, kind)
	}
	sort.Strings(auth)
	sort.Strings(operations)
	if len(operations) > 2000 {
		operations = operations[:2000]
	}
	return operations, auth, scoring
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
