package hub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/barrikadelabs/barrikade-lens/internal/ard"
	"github.com/barrikadelabs/barrikade-lens/pkg/discovery"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type resourceCatalogRequest struct {
	Name                   string `json:"name"`
	Format                 string `json:"format"`
	URL                    string `json:"url"`
	RefreshIntervalSeconds int    `json:"refresh_interval_seconds"`
	NestedPolicy           string `json:"nested_policy"`
	Enabled                *bool  `json:"enabled,omitempty"`
}

func normalizeResourceCatalogRequest(request *resourceCatalogRequest, requireURL bool) error {
	request.Name = strings.TrimSpace(request.Name)
	if request.Format == "" {
		request.Format = ard.Format
	}
	if request.Format != ard.Format {
		return fmt.Errorf("format must be ard")
	}
	if request.RefreshIntervalSeconds == 0 {
		request.RefreshIntervalSeconds = int(ard.DefaultRefresh.Seconds())
	}
	if request.RefreshIntervalSeconds < 3600 || request.RefreshIntervalSeconds > 86400 {
		return fmt.Errorf("refresh interval must be between one and 24 hours")
	}
	if request.NestedPolicy == "" {
		request.NestedPolicy = "same_site"
	}
	if request.NestedPolicy != "same_site" && request.NestedPolicy != "disabled" {
		return fmt.Errorf("nested_policy must be same_site or disabled")
	}
	if requireURL || request.URL != "" {
		sanitized, err := ard.ValidateCatalogURL(request.URL)
		if err != nil {
			return err
		}
		request.URL = sanitized
	}
	if request.Name == "" {
		request.Name = "ARD Catalog"
	}
	if len(request.Name) > 500 {
		return fmt.Errorf("name exceeds 500 characters")
	}
	return nil
}

func requireCatalogAdmin(r *http.Request) (Principal, error) {
	principal, err := requireScope(r, "admin:catalogs")
	if err != nil || !principal.Admin {
		return Principal{}, fmt.Errorf("administrator access is required")
	}
	return principal, nil
}

func (s *Server) validateResourceCatalog(w http.ResponseWriter, r *http.Request) {
	if _, err := requireCatalogAdmin(r); err != nil {
		writeError(w, http.StatusForbidden, "forbidden", err.Error())
		return
	}
	var request resourceCatalogRequest
	if err := decodeJSON(w, r, &request, 64<<10); err != nil {
		return
	}
	if err := normalizeResourceCatalogRequest(&request, true); err != nil {
		writeError(w, 400, "invalid_catalog", err.Error())
		return
	}
	document, err := s.config.ARDProvider.Fetch(r.Context(), request.URL, ard.FetchState{})
	if err != nil {
		writeError(w, 422, "catalog_validation_failed", safeError(err))
		return
	}
	mediaTypes := map[string]int{}
	nested := 0
	for _, entry := range document.Result.Catalog.Entries {
		mediaTypes[entry.MediaType]++
		if entry.MappedKind == "catalog" {
			nested++
		}
	}
	writeJSON(w, 200, map[string]any{
		"url": document.URL, "host": document.Result.Catalog.Host, "spec_version": document.Result.Catalog.SpecVersion,
		"entry_count": len(document.Result.Catalog.Entries), "media_types": mediaTypes, "nested_catalogs": nested,
		"warnings": document.Result.Warnings, "content_hash": document.ContentHash,
	})
}

func (s *Server) createResourceCatalog(w http.ResponseWriter, r *http.Request) {
	principal, err := requireCatalogAdmin(r)
	if err != nil {
		writeError(w, http.StatusForbidden, "forbidden", err.Error())
		return
	}
	var request resourceCatalogRequest
	if err := decodeJSON(w, r, &request, 64<<10); err != nil {
		return
	}
	if err := normalizeResourceCatalogRequest(&request, true); err != nil {
		writeError(w, 400, "invalid_catalog", err.Error())
		return
	}
	var existing string
	err = s.config.Pool.QueryRow(r.Context(), `SELECT id::text FROM resource_catalog_configs WHERE organization_id=$1 AND url=$2`, principal.OrganizationID, request.URL).Scan(&existing)
	if err == nil {
		writeError(w, 409, "catalog_exists", "This catalog is already configured")
		return
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 500, "database_error", "Could not check catalog configuration")
		return
	}
	configID := uuid.New()
	targetID := discovery.StableID(principal.OrganizationID, discovery.KindCatalog, "catalog-target:"+request.URL)
	sourceID := discovery.StableID(principal.OrganizationID, discovery.KindCatalog, "catalog-source:"+request.URL)
	tx, err := s.config.Pool.Begin(r.Context())
	if err != nil {
		writeError(w, 500, "database_error", "Could not create catalog source")
		return
	}
	defer tx.Rollback(r.Context())
	_, err = tx.Exec(r.Context(), `INSERT INTO discovery_targets(organization_id,id,target_type,identity_quality,name,current)
		VALUES($1,$2,'catalog','persistent',$3,true) ON CONFLICT(organization_id,id) DO UPDATE SET name=EXCLUDED.name,current=true`, principal.OrganizationID, targetID, request.Name)
	if err == nil {
		_, err = tx.Exec(r.Context(), `INSERT INTO sources(organization_id,id,target_id,source_type,name,collector_version)
			VALUES($1,$2,$3,'catalog',$4,$5) ON CONFLICT(organization_id,id) DO UPDATE SET name=EXCLUDED.name,revoked_at=NULL`, principal.OrganizationID, sourceID, targetID, request.Name, Version)
	}
	if err == nil {
		_, err = tx.Exec(r.Context(), `INSERT INTO resource_catalog_configs(organization_id,id,source_id,target_id,name,format,url,refresh_interval_seconds,nested_policy,next_refresh_at)
			VALUES($1,$2,$3,$4,$5,'ard',$6,$7,$8,now())`, principal.OrganizationID, configID, sourceID, targetID, request.Name, request.URL, request.RefreshIntervalSeconds, request.NestedPolicy)
	}
	if err != nil {
		writeError(w, 500, "database_error", "Could not save catalog configuration")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, 500, "database_error", "Could not create catalog source")
		return
	}
	writeJSON(w, 201, map[string]any{"id": configID, "source_id": sourceID, "target_id": targetID, "name": request.Name, "format": "ard", "url": request.URL, "refresh_interval_seconds": request.RefreshIntervalSeconds, "nested_policy": request.NestedPolicy, "enabled": true, "status": "pending"})
}

func (s *Server) listResourceCatalogs(w http.ResponseWriter, r *http.Request) {
	principal, err := requireCatalogAdmin(r)
	if err != nil {
		writeError(w, http.StatusForbidden, "forbidden", err.Error())
		return
	}
	rows, err := s.config.Pool.Query(r.Context(), `SELECT id::text,source_id,target_id,name,format,url,refresh_interval_seconds,nested_policy,last_attempt_at,last_success_at,next_refresh_at,etag,last_modified,last_content_hash,last_error_code,last_error_message,enabled,created_at,updated_at
		FROM resource_catalog_configs WHERE organization_id=$1 ORDER BY created_at DESC`, principal.OrganizationID)
	if err != nil {
		writeError(w, 500, "database_error", "Could not list catalog sources")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, sourceID, targetID, name, format, url, nested string
		var interval int
		var lastAttempt, lastSuccess *time.Time
		var next, created, updated time.Time
		var etag, lastModified, contentHash, errorCode, errorMessage *string
		var enabled bool
		if rows.Scan(&id, &sourceID, &targetID, &name, &format, &url, &interval, &nested, &lastAttempt, &lastSuccess, &next, &etag, &lastModified, &contentHash, &errorCode, &errorMessage, &enabled, &created, &updated) == nil {
			items = append(items, map[string]any{"id": id, "source_id": sourceID, "target_id": targetID, "name": name, "format": format, "url": url, "refresh_interval_seconds": interval, "nested_policy": nested, "last_attempt_at": lastAttempt, "last_success_at": lastSuccess, "next_refresh_at": next, "etag": etag, "last_modified": lastModified, "last_content_hash": contentHash, "last_error_code": errorCode, "last_error_message": errorMessage, "enabled": enabled, "created_at": created, "updated_at": updated})
		}
	}
	writeJSON(w, 200, map[string]any{"items": items})
}

func (s *Server) updateResourceCatalog(w http.ResponseWriter, r *http.Request) {
	principal, err := requireCatalogAdmin(r)
	if err != nil {
		writeError(w, http.StatusForbidden, "forbidden", err.Error())
		return
	}
	var request resourceCatalogRequest
	if err := decodeJSON(w, r, &request, 64<<10); err != nil {
		return
	}
	if request.URL != "" {
		writeError(w, 400, "immutable_catalog_url", "Create a new catalog source to change its URL")
		return
	}
	if err := normalizeResourceCatalogRequest(&request, false); err != nil {
		writeError(w, 400, "invalid_catalog", err.Error())
		return
	}
	enabled := true
	if request.Enabled != nil {
		enabled = *request.Enabled
	}
	tag, err := s.config.Pool.Exec(r.Context(), `UPDATE resource_catalog_configs SET name=$3,refresh_interval_seconds=$4,nested_policy=$5,enabled=$6,updated_at=now(),next_refresh_at=CASE WHEN $6 THEN least(next_refresh_at,now()) ELSE next_refresh_at END WHERE organization_id=$1 AND id=$2::uuid`, principal.OrganizationID, r.PathValue("id"), request.Name, request.RefreshIntervalSeconds, request.NestedPolicy, enabled)
	if err != nil {
		writeError(w, 500, "database_error", "Could not update catalog source")
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, 404, "not_found", "Catalog source not found")
		return
	}
	writeJSON(w, 200, map[string]any{"id": r.PathValue("id"), "name": request.Name, "refresh_interval_seconds": request.RefreshIntervalSeconds, "nested_policy": request.NestedPolicy, "enabled": enabled})
}

func (s *Server) refreshResourceCatalog(w http.ResponseWriter, r *http.Request) {
	principal, err := requireCatalogAdmin(r)
	if err != nil {
		writeError(w, http.StatusForbidden, "forbidden", err.Error())
		return
	}
	tag, err := s.config.Pool.Exec(r.Context(), `UPDATE resource_catalog_configs SET next_refresh_at=now(),updated_at=now() WHERE organization_id=$1 AND id=$2::uuid AND enabled`, principal.OrganizationID, r.PathValue("id"))
	if err != nil {
		writeError(w, 500, "database_error", "Could not schedule catalog refresh")
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, 404, "not_found", "Enabled catalog source not found")
		return
	}
	writeJSON(w, 202, map[string]any{"id": r.PathValue("id"), "status": "scheduled"})
}

func (s *Server) deleteResourceCatalog(w http.ResponseWriter, r *http.Request) {
	principal, err := requireCatalogAdmin(r)
	if err != nil {
		writeError(w, http.StatusForbidden, "forbidden", err.Error())
		return
	}
	tx, err := s.config.Pool.Begin(r.Context())
	if err != nil {
		writeError(w, 500, "database_error", "Could not remove catalog source")
		return
	}
	defer tx.Rollback(r.Context())
	previousAlignment, err := currentARDAlignment(r.Context(), tx, principal.OrganizationID)
	if err != nil {
		writeError(w, 500, "database_error", "Could not remove catalog source")
		return
	}
	var sourceID, targetID string
	err = tx.QueryRow(r.Context(), `DELETE FROM resource_catalog_configs WHERE organization_id=$1 AND id=$2::uuid RETURNING source_id,target_id`, principal.OrganizationID, r.PathValue("id")).Scan(&sourceID, &targetID)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 404, "not_found", "Catalog source not found")
		return
	}
	if err != nil {
		writeError(w, 500, "database_error", "Could not remove catalog source")
		return
	}
	changeSnapshot := discovery.Snapshot{
		OrganizationID: principal.OrganizationID, SourceID: sourceID, SnapshotID: uuid.NewString(),
	}
	entityRows, err := tx.Query(r.Context(), `UPDATE source_entities SET current=false,stale=true WHERE organization_id=$1 AND source_id=$2 AND current RETURNING entity_id,observation_kind`, principal.OrganizationID, sourceID)
	type removedEntity struct{ ID, Kind string }
	entityIDs := []removedEntity{}
	if err == nil {
		for entityRows.Next() {
			var item removedEntity
			if entityRows.Scan(&item.ID, &item.Kind) == nil {
				entityIDs = append(entityIDs, item)
			}
		}
		entityRows.Close()
	}
	relationshipRows, relationshipErr := tx.Query(r.Context(), `UPDATE source_relationships SET current=false,stale=true WHERE organization_id=$1 AND source_id=$2 AND current RETURNING relationship_id`, principal.OrganizationID, sourceID)
	relationshipIDs := []string{}
	if relationshipErr == nil {
		for relationshipRows.Next() {
			var id string
			if relationshipRows.Scan(&id) == nil {
				relationshipIDs = append(relationshipIDs, id)
			}
		}
		relationshipRows.Close()
	} else if err == nil {
		err = relationshipErr
	}
	if err == nil {
		_, err = tx.Exec(r.Context(), `UPDATE sources SET revoked_at=now() WHERE organization_id=$1 AND id=$2`, principal.OrganizationID, sourceID)
	}
	if err == nil {
		_, err = tx.Exec(r.Context(), `UPDATE discovery_targets SET current=false WHERE organization_id=$1 AND id=$2`, principal.OrganizationID, targetID)
	}
	if err == nil {
		for _, item := range entityIDs {
			var current bool
			current, err = recomputeEntityFromCurrentObservations(r.Context(), tx, principal.OrganizationID, item.ID)
			if err != nil {
				break
			}
			if !current {
				summary := "Declaration removed"
				if item.Kind == string(discovery.KindCatalog) {
					summary = "Declaration catalog removed"
				}
				err = recordChange(r.Context(), tx, changeSnapshot, "entity.removed", item.ID, &changeMetadata{
					Category: "declaration", Summary: summary,
				})
				if err != nil {
					break
				}
			}
		}
	}
	if err == nil {
		for _, id := range relationshipIDs {
			_, err = recomputeRelationshipFromCurrentObservations(r.Context(), tx, principal.OrganizationID, id)
			if err != nil {
				break
			}
		}
	}
	if err == nil {
		err = refreshARDProjections(r.Context(), tx, principal.OrganizationID)
	}
	if err == nil {
		err = recordARDAlignmentChanges(r.Context(), tx, changeSnapshot, previousAlignment)
	}
	if err != nil {
		writeError(w, 500, "database_error", "Could not remove catalog source")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, 500, "database_error", "Could not remove catalog source")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type ARDWorker struct {
	Pool         *pgxpool.Pool
	Provider     ard.ResourceDeclarationProvider
	Logger       *slog.Logger
	PollInterval time.Duration
}

func (w ARDWorker) Run(ctx context.Context) error {
	if w.Provider == nil {
		return fmt.Errorf("ARD provider is required")
	}
	if w.Logger == nil {
		w.Logger = slog.Default()
	}
	if w.PollInterval == 0 {
		w.PollInterval = 30 * time.Second
	}
	if err := w.processAvailable(ctx); err != nil {
		w.Logger.Warn("ARD catalog refresh failed", "error", err)
	}
	ticker := time.NewTicker(w.PollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := w.processAvailable(ctx); err != nil {
				w.Logger.Warn("ARD catalog refresh failed", "error", err)
			}
		}
	}
}

func (w ARDWorker) processAvailable(ctx context.Context) error {
	var firstErr error
	for processed := 0; processed < ard.MaxCatalogs; processed++ {
		found, err := w.processOne(ctx)
		if err != nil && firstErr == nil {
			firstErr = err
		}
		if !found || ctx.Err() != nil {
			break
		}
	}
	return firstErr
}

type claimedCatalog struct {
	OrganizationID, ID, SourceID, TargetID, Name, URL, NestedPolicy, ETag, LastModified string
	Interval                                                                            int
}

func (w ARDWorker) processOne(ctx context.Context) (bool, error) {
	tx, err := w.Pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	var item claimedCatalog
	err = tx.QueryRow(ctx, `SELECT organization_id,id::text,source_id,target_id,name,url,nested_policy,COALESCE(etag,''),COALESCE(last_modified,''),refresh_interval_seconds
		FROM resource_catalog_configs WHERE enabled AND next_refresh_at<=now() ORDER BY next_refresh_at FOR UPDATE SKIP LOCKED LIMIT 1`).
		Scan(&item.OrganizationID, &item.ID, &item.SourceID, &item.TargetID, &item.Name, &item.URL, &item.NestedPolicy, &item.ETag, &item.LastModified, &item.Interval)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	_, err = tx.Exec(ctx, `UPDATE resource_catalog_configs SET last_attempt_at=now(),next_refresh_at=now()+make_interval(secs=>refresh_interval_seconds)+(random()*600)*interval '1 second',updated_at=now() WHERE organization_id=$1 AND id=$2::uuid`, item.OrganizationID, item.ID)
	if err != nil {
		return true, err
	}
	if err := tx.Commit(ctx); err != nil {
		return true, err
	}
	document, err := w.Provider.Fetch(ctx, item.URL, ard.FetchState{ETag: item.ETag, LastModified: item.LastModified})
	if err != nil {
		_, _ = w.Pool.Exec(ctx, `UPDATE resource_catalog_configs SET last_error_code='fetch_failed',last_error_message=$3,updated_at=now() WHERE organization_id=$1 AND id=$2::uuid`, item.OrganizationID, item.ID, safeError(err))
		return true, err
	}
	now := time.Now().UTC()
	if document.NotModified {
		refreshTx, beginErr := w.Pool.Begin(ctx)
		if beginErr != nil {
			return true, beginErr
		}
		defer refreshTx.Rollback(ctx)
		_, err = refreshTx.Exec(ctx, `UPDATE resource_catalog_configs SET last_success_at=$3,last_error_code=NULL,last_error_message=NULL,updated_at=now() WHERE organization_id=$1 AND id=$2::uuid`, item.OrganizationID, item.ID, now)
		if err == nil {
			_, err = refreshTx.Exec(ctx, `UPDATE sources SET last_seen_at=$3,last_full_at=$3 WHERE organization_id=$1 AND id=$2`, item.OrganizationID, item.SourceID, now)
		}
		if err == nil {
			_, err = refreshTx.Exec(ctx, `UPDATE discovery_targets SET last_seen_at=$3,last_full_at=$3,current=true WHERE organization_id=$1 AND id=$2`, item.OrganizationID, item.TargetID, now)
		}
		if err == nil {
			_, err = refreshTx.Exec(ctx, `UPDATE source_entities SET last_seen_at=$3
				WHERE organization_id=$1 AND source_id=$2 AND current`, item.OrganizationID, item.SourceID, now)
		}
		if err == nil {
			_, err = refreshTx.Exec(ctx, `UPDATE source_relationships SET last_seen_at=$3
				WHERE organization_id=$1 AND source_id=$2 AND current`, item.OrganizationID, item.SourceID, now)
		}
		if err == nil {
			_, err = refreshTx.Exec(ctx, `UPDATE entities e SET last_seen_at=GREATEST(e.last_seen_at,$3)
				WHERE e.organization_id=$1 AND EXISTS (
					SELECT 1 FROM source_entities se
					WHERE se.organization_id=e.organization_id AND se.entity_id=e.id
						AND se.source_id=$2 AND se.current
				)`, item.OrganizationID, item.SourceID, now)
		}
		if err == nil {
			_, err = refreshTx.Exec(ctx, `UPDATE relationships rel SET last_seen_at=GREATEST(rel.last_seen_at,$3)
				WHERE rel.organization_id=$1 AND EXISTS (
					SELECT 1 FROM source_relationships sr
					WHERE sr.organization_id=rel.organization_id AND sr.relationship_id=rel.id
						AND sr.source_id=$2 AND sr.current
				)`, item.OrganizationID, item.SourceID, now)
		}
		if err == nil {
			_, err = refreshTx.Exec(ctx, `UPDATE resource_declarations
				SET last_seen_at=GREATEST(last_seen_at,$3)
				WHERE organization_id=$1 AND $2=ANY(source_ids) AND current`, item.OrganizationID, item.SourceID, now)
		}
		if err != nil {
			return true, err
		}
		return true, refreshTx.Commit(ctx)
	}
	snapshot, err := ard.Snapshot(document.Result, ard.SnapshotOptions{
		OrganizationID: item.OrganizationID, SourceID: item.SourceID, TargetID: item.TargetID, SourceType: discovery.SourceCatalog,
		SourceName: item.Name, SourceLocator: document.URL, ContentHash: document.ContentHash, SourceSurface: "catalog",
		Collector: discovery.Collector{ID: "lens-hub-ard", Name: "Barrikade Lens Hub ARD", Version: Version, Mode: "catalog"},
	})
	if err != nil {
		return true, err
	}
	if item.NestedPolicy == "same_site" {
		snapshot = w.followNestedCatalogs(ctx, item, document, snapshot)
	}
	payload, _ := json.Marshal(snapshot)
	jobID := uuid.New()
	_, err = w.Pool.Exec(ctx, `INSERT INTO ingestion_jobs(id,organization_id,source_id,snapshot_id,status,payload) VALUES($1,$2,$3,$4,'pending',$5) ON CONFLICT(organization_id,snapshot_id) DO NOTHING`, jobID, item.OrganizationID, item.SourceID, snapshot.SnapshotID, payload)
	if err == nil {
		_, err = w.Pool.Exec(ctx, `UPDATE resource_catalog_configs SET etag=$3,last_modified=$4,last_content_hash=$5,last_success_at=$6,last_error_code=NULL,last_error_message=NULL,updated_at=now() WHERE organization_id=$1 AND id=$2::uuid`, item.OrganizationID, item.ID, nullString(document.ETag), nullString(document.LastModified), document.ContentHash, now)
	}
	return true, err
}

type nestedCatalogRequest struct {
	ParentURL string
	URL       string
	Depth     int
}

func (w ARDWorker) followNestedCatalogs(ctx context.Context, item claimedCatalog, root ard.Document, snapshot discovery.Snapshot) discovery.Snapshot {
	queue := nestedRequests(root.URL, root.Result.Catalog, 1)
	seenURLs := map[string]bool{root.URL: true}
	seenDigests := map[string]bool{root.ContentHash: true}
	processed := 1
	for len(queue) > 0 && processed < ard.MaxCatalogs {
		next := queue[0]
		queue = queue[1:]
		if next.Depth > ard.MaxDepth || seenURLs[next.URL] || !ard.SameSite(item.URL, next.URL) {
			continue
		}
		seenURLs[next.URL] = true
		document, err := w.Provider.Fetch(ctx, next.URL, ard.FetchState{})
		if err != nil {
			snapshot.Full = false
			snapshot.Coverage.Partial = true
			snapshot.Coverage.DetectorsFailed++
			snapshot.Errors = append(snapshot.Errors, discovery.ScanError{
				DetectorID: "ard.catalog", Code: "nested_catalog_fetch_failed",
				Message: "A same-site nested catalog could not be refreshed", Retryable: true,
			})
			continue
		}
		processed++
		if seenDigests[document.ContentHash] {
			continue
		}
		seenDigests[document.ContentHash] = true
		child, err := ard.Snapshot(document.Result, ard.SnapshotOptions{
			OrganizationID: item.OrganizationID, SourceID: item.SourceID, TargetID: item.TargetID, SourceType: discovery.SourceCatalog,
			SourceName: document.Result.Catalog.Host.DisplayName, SourceLocator: document.URL, ContentHash: document.ContentHash,
			SourceSurface: "catalog", Collector: discovery.Collector{
				ID: "lens-hub-ard", Name: "Barrikade Lens Hub ARD", Version: Version, Mode: "catalog",
			},
		})
		if err != nil {
			snapshot.Coverage.Partial = true
			continue
		}
		if declarationCount(snapshot)+declarationCount(child) > ard.MaxEntriesPerSource {
			snapshot.Full = false
			snapshot.Coverage.Partial = true
			snapshot.Coverage.Notes = append(snapshot.Coverage.Notes, "ARD declaration traversal reached its 25,000 entry safety limit")
			break
		}
		if err := discovery.MergeSnapshots(&snapshot, child); err != nil {
			snapshot.Full = false
			snapshot.Coverage.Partial = true
			continue
		}
		parentID := discovery.StableID(item.OrganizationID, discovery.KindCatalog, "ard:catalog:"+next.ParentURL)
		childID := discovery.StableID(item.OrganizationID, discovery.KindCatalog, "ard:catalog:"+document.URL)
		ref := ""
		if len(child.Evidence) > 0 {
			ref = child.Evidence[0].ID
		}
		relation := discovery.Relationship{
			ID:   discovery.RelationshipID(item.OrganizationID, discovery.RelationshipReferences, parentID, childID),
			Kind: discovery.RelationshipReferences, From: parentID, To: childID,
			Attributes: map[string]any{"delivery": "url", "same_site": true},
			Confidence: discovery.ConfidenceConfirmed,
		}
		if ref != "" {
			relation.EvidenceRefs = []string{ref}
		}
		snapshot.Relationships = append(snapshot.Relationships, relation)
		queue = append(queue, nestedRequests(document.URL, document.Result.Catalog, next.Depth+1)...)
	}
	if len(queue) > 0 {
		snapshot.Full = false
		snapshot.Coverage.Partial = true
		snapshot.Coverage.Notes = append(snapshot.Coverage.Notes, "ARD nested catalog traversal reached its configured safety limit")
	}
	snapshot.Normalize()
	return snapshot
}

func declarationCount(snapshot discovery.Snapshot) int {
	count := 0
	for _, entity := range snapshot.Entities {
		if entity.Kind == discovery.KindResourceDeclaration {
			count++
		}
	}
	return count
}

func nestedRequests(parentURL string, catalog ard.Catalog, depth int) []nestedCatalogRequest {
	requests := []nestedCatalogRequest{}
	for _, entry := range catalog.Entries {
		if entry.MappedKind == "catalog" && entry.Delivery == "url" && entry.ArtifactURL != "" {
			requests = append(requests, nestedCatalogRequest{ParentURL: parentURL, URL: entry.ArtifactURL, Depth: depth})
		}
	}
	return requests
}

func refreshARDProjections(ctx context.Context, tx pgx.Tx, organizationID string) error {
	if _, err := tx.Exec(ctx, `DELETE FROM resource_matches WHERE organization_id=$1`, organizationID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE relationships SET current=false,stale=true WHERE organization_id=$1 AND kind='describes' AND attributes->>'derived_by'='ard-correlation'`, organizationID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM resource_declarations d WHERE d.organization_id=$1 AND NOT EXISTS(
		SELECT 1 FROM entities e WHERE e.organization_id=d.organization_id AND e.id=d.entity_id AND e.current AND e.kind='resource_declaration')`, organizationID); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `INSERT INTO resource_declarations(
		organization_id,entity_id,identifier,publisher_domain,media_type,mapped_kind,delivery,artifact_url,descriptor_host,
		trust_identity_alignment,signature_status,source_ids,catalog_ids,alignment_status,matched_entity_id,material_digest,first_seen_at,last_seen_at,current)
		SELECT e.organization_id,e.id,e.attributes->>'ard_identifier',e.attributes->>'publisher_domain',e.attributes->>'media_type',
		COALESCE(e.attributes->>'mapped_kind','unclassified'),COALESCE(e.attributes->>'delivery','inline'),NULLIF(e.attributes->>'artifact_url',''),
		NULLIF(e.attributes->>'host',''),COALESCE(e.attributes->>'trust_identity_alignment','absent'),COALESCE(e.attributes->>'signature_status','absent'),
		COALESCE((SELECT array_agg(DISTINCT se.source_id ORDER BY se.source_id) FROM source_entities se WHERE se.organization_id=e.organization_id AND se.entity_id=e.id AND se.current),'{}'),
		COALESCE((SELECT array_agg(DISTINCT r.from_entity ORDER BY r.from_entity) FROM relationships r WHERE r.organization_id=e.organization_id AND r.to_entity=e.id AND r.kind='publishes' AND r.current),'{}'),
		'declared_only',NULL,md5(e.attributes::text||e.confidence||e.current::text),e.first_seen_at,e.last_seen_at,e.current
		FROM entities e WHERE e.organization_id=$1 AND e.current AND e.kind='resource_declaration'
		ON CONFLICT(organization_id,entity_id) DO UPDATE SET identifier=EXCLUDED.identifier,publisher_domain=EXCLUDED.publisher_domain,media_type=EXCLUDED.media_type,
		mapped_kind=EXCLUDED.mapped_kind,delivery=EXCLUDED.delivery,artifact_url=EXCLUDED.artifact_url,descriptor_host=EXCLUDED.descriptor_host,
		trust_identity_alignment=EXCLUDED.trust_identity_alignment,signature_status=EXCLUDED.signature_status,source_ids=EXCLUDED.source_ids,catalog_ids=EXCLUDED.catalog_ids,
		alignment_status='declared_only',matched_entity_id=NULL,material_digest=EXCLUDED.material_digest,last_seen_at=EXCLUDED.last_seen_at,current=EXCLUDED.current`, organizationID)
	if err != nil {
		return err
	}
	// Exact matching is deliberately expressed as indexed JSONB joins instead
	// of a declarations-by-entities loop. This keeps correlation bounded by the
	// declaration set and lets PostgreSQL use entities_attributes_gin at fleet
	// scale.
	if _, err := tx.Exec(ctx, `INSERT INTO resource_matches(
			organization_id,declaration_entity_id,observed_entity_id,status,confidence,reason,matched_at)
		SELECT d.organization_id,d.entity_id,e.id,'linked','confirmed',
			CASE
				WHEN e.attributes @> jsonb_build_object('ard_identifier',d.identifier) THEN 'exact ARD identifier'
				WHEN de.attributes ? 'artifact_fingerprint'
					AND (
						e.attributes @> jsonb_build_object('artifact_fingerprint',de.attributes->>'artifact_fingerprint')
						OR e.attributes @> jsonb_build_object('config_fingerprint',de.attributes->>'artifact_fingerprint')
						OR e.attributes @> jsonb_build_object('configuration_fingerprint',de.attributes->>'artifact_fingerprint')
					) THEN 'exact artifact fingerprint'
				WHEN de.attributes ? 'protocol_identity'
					AND e.attributes @> jsonb_build_object('protocol_identity',de.attributes->>'protocol_identity') THEN 'exact protocol identity'
				WHEN de.attributes ? 'repository_url' AND de.attributes ? 'repository_path'
					AND e.attributes @> jsonb_build_object('repository_url',de.attributes->>'repository_url') THEN 'exact repository and descriptor path'
				ELSE 'exact descriptor URL'
			END,
			now()
		FROM resource_declarations d
		JOIN entities de ON de.organization_id=d.organization_id AND de.id=d.entity_id
		JOIN entities e ON e.organization_id=d.organization_id
			AND e.current
			AND e.kind NOT IN ('catalog','resource_declaration')
			AND (
				e.attributes @> jsonb_build_object('ard_identifier',d.identifier)
				OR (
					d.artifact_url IS NOT NULL
					AND (
						e.attributes @> jsonb_build_object('artifact_url',d.artifact_url)
						OR e.attributes @> jsonb_build_object('descriptor_url',d.artifact_url)
						OR e.attributes @> jsonb_build_object('descriptor',d.artifact_url)
						OR e.attributes @> jsonb_build_object('url',d.artifact_url)
						OR e.attributes @> jsonb_build_object('endpoint',d.artifact_url)
						OR e.attributes @> jsonb_build_object('base_url',d.artifact_url)
						OR e.attributes @> jsonb_build_object('openapi_reference',d.artifact_url)
					)
				)
				OR (
					de.attributes ? 'artifact_fingerprint'
					AND (
						e.attributes @> jsonb_build_object('artifact_fingerprint',de.attributes->>'artifact_fingerprint')
						OR e.attributes @> jsonb_build_object('config_fingerprint',de.attributes->>'artifact_fingerprint')
						OR e.attributes @> jsonb_build_object('configuration_fingerprint',de.attributes->>'artifact_fingerprint')
					)
				)
				OR (
					de.attributes ? 'protocol_identity'
					AND e.attributes @> jsonb_build_object('protocol_identity',de.attributes->>'protocol_identity')
				)
				OR (
					de.attributes ? 'repository_url' AND de.attributes ? 'repository_path'
					AND e.attributes @> jsonb_build_object('repository_url',de.attributes->>'repository_url')
					AND (
						e.attributes @> jsonb_build_object('descriptor',de.attributes->>'repository_path')
						OR e.attributes @> jsonb_build_object('repository_path',de.attributes->>'repository_path')
					)
				)
			)
		WHERE d.organization_id=$1 AND d.current
		ON CONFLICT(organization_id,declaration_entity_id,observed_entity_id) DO UPDATE
		SET status='linked',confidence='confirmed',reason=EXCLUDED.reason,matched_at=now()`, organizationID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `WITH ambiguous AS (
			SELECT declaration_entity_id
			FROM resource_matches
			WHERE organization_id=$1 AND status='linked'
			GROUP BY declaration_entity_id
			HAVING count(*)>1
		)
		UPDATE resource_matches m SET status='conflict'
		FROM ambiguous a
		WHERE m.organization_id=$1 AND m.declaration_entity_id=a.declaration_entity_id AND m.status='linked'`, organizationID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE resource_declarations d SET
			alignment_status=CASE
				WHEN counts.conflicts>0 THEN 'conflict'
				WHEN counts.links=1 THEN 'matched'
				ELSE 'declared_only'
			END,
			matched_entity_id=CASE WHEN counts.links=1 AND counts.conflicts=0 THEN counts.linked_id ELSE NULL END
		FROM (
			SELECT d2.entity_id,
				count(*) FILTER (WHERE m.status='linked') AS links,
				count(*) FILTER (WHERE m.status='conflict') AS conflicts,
				min(m.observed_entity_id) FILTER (WHERE m.status='linked') AS linked_id
			FROM resource_declarations d2
			LEFT JOIN resource_matches m ON m.organization_id=d2.organization_id AND m.declaration_entity_id=d2.entity_id
			WHERE d2.organization_id=$1 AND d2.current
			GROUP BY d2.entity_id
		) counts
		WHERE d.organization_id=$1 AND d.entity_id=counts.entity_id`, organizationID); err != nil {
		return err
	}
	// Name-only agreement is useful for investigation but is never enough to
	// create a canonical relationship. Limit suggestions per declaration so a
	// common product name cannot fan out without bound.
	if _, err := tx.Exec(ctx, `INSERT INTO resource_matches(
			organization_id,declaration_entity_id,observed_entity_id,status,confidence,reason,matched_at)
		SELECT d.organization_id,d.entity_id,s.id,'suggested','possible',
			'exact display name; identity evidence required',now()
		FROM resource_declarations d
		JOIN entities de ON de.organization_id=d.organization_id AND de.id=d.entity_id
		JOIN LATERAL (
			SELECT e.id
			FROM entities e
			WHERE e.organization_id=d.organization_id
				AND e.current
				AND e.kind=CASE d.mapped_kind
					WHEN 'agent' THEN 'agent'
					WHEN 'mcp_server' THEN 'mcp_server'
					WHEN 'skill' THEN 'skill'
					WHEN 'api_service' THEN 'api_service'
					WHEN 'workflow' THEN 'workflow'
					ELSE ''
				END
				AND lower(btrim(e.name))=lower(btrim(de.name))
				AND NOT EXISTS (
					SELECT 1 FROM resource_matches exact
					WHERE exact.organization_id=d.organization_id
						AND exact.declaration_entity_id=d.entity_id
						AND exact.observed_entity_id=e.id
						AND exact.status IN ('linked','conflict')
				)
			ORDER BY e.id
			LIMIT 5
		) s ON true
		WHERE d.organization_id=$1 AND d.current
		ON CONFLICT(organization_id,declaration_entity_id,observed_entity_id) DO NOTHING`, organizationID); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO relationships(
			organization_id,id,kind,from_entity,to_entity,attributes,confidence,current,stale,first_seen_at,last_seen_at)
		SELECT m.organization_id,
			'urn:lens:'||encode(sha256(
				convert_to('relationship','UTF8')||decode('00','hex')||
				convert_to(m.organization_id,'UTF8')||decode('00','hex')||
				convert_to('describes','UTF8')||decode('00','hex')||
				convert_to(m.declaration_entity_id,'UTF8')||decode('00','hex')||
				convert_to(m.observed_entity_id,'UTF8')
			),'hex'),
			'describes',m.declaration_entity_id,m.observed_entity_id,
			jsonb_build_object('derived_by','ard-correlation','reason',m.reason),
			'confirmed',true,false,now(),now()
		FROM resource_matches m
		WHERE m.organization_id=$1 AND m.status='linked'
		ON CONFLICT(organization_id,id) DO UPDATE SET
			attributes=EXCLUDED.attributes,confidence='confirmed',current=true,stale=false,last_seen_at=now()`, organizationID)
	return err
}

type ardAlignmentState struct {
	Status          string
	MatchedEntityID string
}

func currentARDAlignment(ctx context.Context, tx pgx.Tx, organizationID string) (map[string]ardAlignmentState, error) {
	rows, err := tx.Query(ctx, `SELECT entity_id,alignment_status,COALESCE(matched_entity_id,'')
		FROM resource_declarations WHERE organization_id=$1 AND current`, organizationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[string]ardAlignmentState{}
	for rows.Next() {
		var id string
		var state ardAlignmentState
		if err := rows.Scan(&id, &state.Status, &state.MatchedEntityID); err != nil {
			return nil, err
		}
		result[id] = state
	}
	return result, rows.Err()
}

func recordARDAlignmentChanges(ctx context.Context, tx pgx.Tx, snapshot discovery.Snapshot, previous map[string]ardAlignmentState) error {
	current, err := currentARDAlignment(ctx, tx, snapshot.OrganizationID)
	if err != nil {
		return err
	}
	for entityID, before := range previous {
		after, exists := current[entityID]
		// Entity removal already has its own declaration event. Avoid turning a
		// single omission into both "removed" and "correlation changed".
		if !exists || before == after {
			continue
		}
		afterStatus, afterMatch := after.Status, after.MatchedEntityID
		fields := []map[string]any{{"path": "alignment_status", "before": before.Status, "after": afterStatus}}
		if before.MatchedEntityID != afterMatch {
			fields = append(fields, map[string]any{"path": "matched_entity_id", "before": before.MatchedEntityID, "after": afterMatch})
		}
		if err := recordChange(ctx, tx, snapshot, "entity.updated", entityID, &changeMetadata{
			Category: "declaration", Summary: "Declaration correlation changed",
			Details: map[string]any{"fields": fields},
		}); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) listDeclarations(w http.ResponseWriter, r *http.Request) {
	principal, err := requireScope(r, "inventory:read")
	if err != nil {
		writeError(w, 403, "forbidden", err.Error())
		return
	}
	limit := queryLimit(r)
	cursor, err := decodeCursor(r.URL.Query().Get("cursor"), "declaration_last_seen")
	if err != nil {
		writeError(w, 400, "invalid_cursor", err.Error())
		return
	}
	query := `SELECT d.entity_id,d.identifier,e.name,e.attributes,e.confidence,d.publisher_domain,d.media_type,d.mapped_kind,d.delivery,d.artifact_url,d.descriptor_host,d.trust_identity_alignment,d.signature_status,d.source_ids,d.catalog_ids,d.alignment_status,d.matched_entity_id,d.first_seen_at,d.last_seen_at
		FROM resource_declarations d JOIN entities e ON e.organization_id=d.organization_id AND e.id=d.entity_id
		WHERE d.organization_id=$1 AND d.current`
	args := []any{principal.OrganizationID}
	add := func(column, value string) {
		args = append(args, value)
		query += fmt.Sprintf(" AND "+column+"=$%d", len(args))
	}
	for _, filter := range []struct{ Param, Column string }{
		{"status", "d.alignment_status"}, {"publisher", "d.publisher_domain"}, {"media_type", "d.media_type"},
		{"mapped_kind", "d.mapped_kind"}, {"trust_alignment", "d.trust_identity_alignment"}, {"signature_status", "d.signature_status"},
	} {
		if value := r.URL.Query().Get(filter.Param); value != "" {
			add(filter.Column, value)
		}
	}
	if sourceID := r.URL.Query().Get("source_id"); sourceID != "" {
		args = append(args, sourceID)
		query += fmt.Sprintf(" AND $%d=ANY(d.source_ids)", len(args))
	}
	if freshness := r.URL.Query().Get("freshness"); freshness != "" {
		switch freshness {
		case "fresh":
			query += ` AND d.last_seen_at>=now()-interval '24 hours'`
		case "stale":
			query += ` AND d.last_seen_at<now()-interval '24 hours'`
		default:
			writeError(w, 400, "invalid_filter", "Freshness must be fresh or stale")
			return
		}
	}
	if search := strings.TrimSpace(r.URL.Query().Get("search")); search != "" {
		args = append(args, search)
		query += fmt.Sprintf(` AND (e.name ILIKE '%%'||$%d||'%%' OR d.identifier ILIKE '%%'||$%d||'%%' OR d.publisher_domain ILIKE '%%'||$%d||'%%')`, len(args), len(args), len(args))
	}
	if cursor.ID != "" {
		when, parseErr := time.Parse(time.RFC3339Nano, cursor.Value)
		if parseErr != nil {
			writeError(w, 400, "invalid_cursor", "Cursor timestamp is invalid")
			return
		}
		args = append(args, when, cursor.ID)
		query += fmt.Sprintf(` AND (d.last_seen_at,d.entity_id)<($%d,$%d)`, len(args)-1, len(args))
	}
	args = append(args, limit)
	query += fmt.Sprintf(` ORDER BY d.last_seen_at DESC,d.entity_id DESC LIMIT $%d`, len(args))
	rows, err := s.config.Pool.Query(r.Context(), query, args...)
	if err != nil {
		writeError(w, 500, "database_error", "Could not query declarations")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	var next pageCursor
	for rows.Next() {
		var id, identifier, name, confidence, publisher, mediaType, mappedKind, delivery, trustAlignment, signatureStatus, alignment string
		var attributes []byte
		var artifactURL, descriptorHost, matchedID *string
		var sourceIDs, catalogIDs []string
		var firstSeen, lastSeen time.Time
		if rows.Scan(&id, &identifier, &name, &attributes, &confidence, &publisher, &mediaType, &mappedKind, &delivery, &artifactURL, &descriptorHost, &trustAlignment, &signatureStatus, &sourceIDs, &catalogIDs, &alignment, &matchedID, &firstSeen, &lastSeen) != nil {
			continue
		}
		items = append(items, map[string]any{"id": id, "identifier": identifier, "name": name, "attributes": jsonObject(attributes), "confidence": confidence, "publisher_domain": publisher, "media_type": mediaType, "mapped_kind": mappedKind, "delivery": delivery, "artifact_url": artifactURL, "descriptor_host": descriptorHost, "trust_identity_alignment": trustAlignment, "signature_status": signatureStatus, "source_ids": sourceIDs, "catalog_ids": catalogIDs, "alignment_status": alignment, "matched_entity_id": matchedID, "first_seen_at": firstSeen, "last_seen_at": lastSeen})
		next = pageCursor{Sort: "declaration_last_seen", Value: lastSeen.Format(time.RFC3339Nano), ID: id}
	}
	response := map[string]any{"items": items, "limit": limit}
	if len(items) == limit {
		response["next_cursor"] = encodeCursor(next)
	}
	writeJSON(w, 200, response)
}

func (s *Server) getDeclaration(w http.ResponseWriter, r *http.Request) {
	principal, err := requireScope(r, "inventory:read")
	if err != nil {
		writeError(w, 403, "forbidden", err.Error())
		return
	}
	var id, identifier, name, confidence, publisher, mediaType, mappedKind, delivery, trustAlignment, signatureStatus, alignment string
	var attributes []byte
	var artifactURL, descriptorHost, matchedID *string
	var sourceIDs, catalogIDs []string
	var firstSeen, lastSeen time.Time
	err = s.config.Pool.QueryRow(r.Context(), `SELECT d.entity_id,d.identifier,e.name,e.attributes,e.confidence,d.publisher_domain,d.media_type,d.mapped_kind,d.delivery,d.artifact_url,d.descriptor_host,d.trust_identity_alignment,d.signature_status,d.source_ids,d.catalog_ids,d.alignment_status,d.matched_entity_id,d.first_seen_at,d.last_seen_at
		FROM resource_declarations d JOIN entities e ON e.organization_id=d.organization_id AND e.id=d.entity_id
		WHERE d.organization_id=$1 AND d.entity_id=$2 AND d.current`, principal.OrganizationID, r.PathValue("id")).
		Scan(&id, &identifier, &name, &attributes, &confidence, &publisher, &mediaType, &mappedKind, &delivery, &artifactURL, &descriptorHost, &trustAlignment, &signatureStatus, &sourceIDs, &catalogIDs, &alignment, &matchedID, &firstSeen, &lastSeen)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 404, "not_found", "Declaration not found")
		return
	}
	if err != nil {
		writeError(w, 500, "database_error", "Could not read declaration")
		return
	}
	matches := []map[string]any{}
	rows, _ := s.config.Pool.Query(r.Context(), `SELECT m.observed_entity_id,e.kind,e.name,m.status,m.confidence,m.reason,m.matched_at FROM resource_matches m JOIN entities e ON e.organization_id=m.organization_id AND e.id=m.observed_entity_id WHERE m.organization_id=$1 AND m.declaration_entity_id=$2 ORDER BY CASE m.status WHEN 'linked' THEN 1 WHEN 'conflict' THEN 2 ELSE 3 END,e.name`, principal.OrganizationID, id)
	if rows != nil {
		for rows.Next() {
			var entityID, kind, entityName, status, matchConfidence, reason string
			var matchedAt time.Time
			if rows.Scan(&entityID, &kind, &entityName, &status, &matchConfidence, &reason, &matchedAt) == nil {
				matches = append(matches, map[string]any{"entity_id": entityID, "kind": kind, "name": entityName, "status": status, "confidence": matchConfidence, "reason": reason, "matched_at": matchedAt})
			}
		}
		rows.Close()
	}
	evidence := []map[string]any{}
	evidenceRows, _ := s.config.Pool.Query(r.Context(), `SELECT evidence_id,source_id,detector_id,detector_version,method,family,specificity,locator,content_hash,max(observed_at),count(*)
		FROM evidence_observations WHERE organization_id=$1 AND $2=ANY(entity_ids)
		GROUP BY evidence_id,source_id,detector_id,detector_version,method,family,specificity,locator,content_hash ORDER BY max(observed_at) DESC LIMIT 250`, principal.OrganizationID, id)
	if evidenceRows != nil {
		for evidenceRows.Next() {
			var evidenceID, sourceID, detectorID, detectorVersion, method, family, specificity string
			var locator, hash *string
			var observedAt time.Time
			var observations int64
			if evidenceRows.Scan(&evidenceID, &sourceID, &detectorID, &detectorVersion, &method, &family, &specificity, &locator, &hash, &observedAt, &observations) == nil {
				evidence = append(evidence, map[string]any{"id": evidenceID, "source_id": sourceID, "detector_id": detectorID, "detector_version": detectorVersion, "method": method, "family": family, "specificity": specificity, "locator": locator, "content_hash": hash, "observed_at": observedAt, "observations": observations})
			}
		}
		evidenceRows.Close()
	}
	changes := []map[string]any{}
	changeRows, _ := s.config.Pool.Query(r.Context(), `SELECT id::text,event_type,category,summary,details,changed_at
		FROM changes WHERE organization_id=$1 AND entity_id=$2 AND category='declaration'
		ORDER BY changed_at DESC,id DESC LIMIT 100`, principal.OrganizationID, id)
	if changeRows != nil {
		for changeRows.Next() {
			var changeID, eventType, category, summary string
			var details []byte
			var changedAt time.Time
			if changeRows.Scan(&changeID, &eventType, &category, &summary, &details, &changedAt) == nil {
				changes = append(changes, map[string]any{"id": changeID, "event_type": eventType, "category": category, "summary": summary, "details": jsonObject(details), "changed_at": changedAt})
			}
		}
		changeRows.Close()
	}
	writeJSON(w, 200, map[string]any{"id": id, "identifier": identifier, "name": name, "attributes": jsonObject(attributes), "confidence": confidence, "publisher_domain": publisher, "media_type": mediaType, "mapped_kind": mappedKind, "delivery": delivery, "artifact_url": artifactURL, "descriptor_host": descriptorHost, "trust_identity_alignment": trustAlignment, "signature_status": signatureStatus, "source_ids": sourceIDs, "catalog_ids": catalogIDs, "alignment_status": alignment, "matched_entity_id": matchedID, "first_seen_at": firstSeen, "last_seen_at": lastSeen, "matches": matches, "evidence": evidence, "changes": changes})
}

func declarationAlignmentData(ctx context.Context, pool interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, organizationID string) map[string]any {
	counts := map[string]int{"matched": 0, "declared_only": 0, "conflict": 0, "observed_only": 0, "stale_sources": 0, "unverified_claims": 0}
	for _, status := range []string{"matched", "declared_only", "conflict"} {
		var count int
		_ = pool.QueryRow(ctx, `SELECT count(*) FROM resource_declarations WHERE organization_id=$1 AND current AND alignment_status=$2`, organizationID, status).Scan(&count)
		counts[status] = count
	}
	for key, query := range map[string]string{
		"observed_only": `SELECT count(*) FROM entities e WHERE e.organization_id=$1 AND e.current AND e.kind IN ('agent','mcp_server','skill','api_service','workflow')
			AND NOT EXISTS(SELECT 1 FROM unnest(e.provenance) value WHERE value LIKE 'catalog:%')
			AND NOT EXISTS(SELECT 1 FROM resource_matches m WHERE m.organization_id=e.organization_id AND m.observed_entity_id=e.id AND m.status='linked')`,
		"stale_sources":     `SELECT count(*) FROM resource_catalog_configs WHERE organization_id=$1 AND enabled AND (last_success_at IS NULL OR last_success_at<now()-interval '24 hours')`,
		"unverified_claims": `SELECT count(*) FROM resource_declarations WHERE organization_id=$1 AND current AND (signature_status='present_unverified' OR trust_identity_alignment IN ('misaligned','unresolved'))`,
	} {
		var count int
		_ = pool.QueryRow(ctx, query, organizationID).Scan(&count)
		counts[key] = count
	}
	var sources int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM resource_catalog_configs WHERE organization_id=$1 AND enabled`, organizationID).Scan(&sources)
	return map[string]any{"counts": counts, "configured_sources": sources, "configured": sources > 0}
}

func (s *Server) declarationAlignment(w http.ResponseWriter, r *http.Request) {
	principal, err := requireScope(r, "inventory:read")
	if err != nil {
		writeError(w, 403, "forbidden", err.Error())
		return
	}
	if _, _, err := parseWindow(r.URL.Query().Get("window")); err != nil {
		writeError(w, 400, "invalid_window", err.Error())
		return
	}
	result := declarationAlignmentData(r.Context(), s.config.Pool, principal.OrganizationID)
	result["generated_at"] = time.Now().UTC()
	writeJSON(w, 200, result)
}

type ardExportRequest struct {
	PublisherDomain string `json:"publisher_domain"`
	HostDisplayName string `json:"host_display_name"`
	Entries         []struct {
		EntityID    string `json:"entity_id"`
		Identifier  string `json:"identifier,omitempty"`
		MediaType   string `json:"media_type,omitempty"`
		ArtifactURL string `json:"artifact_url,omitempty"`
	} `json:"entries"`
}

func (s *Server) exportARD(w http.ResponseWriter, r *http.Request) {
	principal, err := requireScope(r, "inventory:read")
	if err != nil {
		writeError(w, 403, "forbidden", err.Error())
		return
	}
	var request ardExportRequest
	if err := decodeJSON(w, r, &request, 1<<20); err != nil {
		return
	}
	request.PublisherDomain = strings.ToLower(strings.TrimSpace(request.PublisherDomain))
	if !ard.ValidPublisherDomain(request.PublisherDomain) || len(request.Entries) == 0 || len(request.Entries) > 1000 {
		writeError(w, 422, "invalid_ard_export", "Publisher domain and between 1 and 1000 explicitly selected entries are required")
		return
	}
	if strings.TrimSpace(request.HostDisplayName) == "" {
		request.HostDisplayName = request.PublisherDomain
	}
	exportEntries := []map[string]any{}
	seen := map[string]bool{}
	for index, selected := range request.Entries {
		var kind, name string
		var attributes []byte
		err := s.config.Pool.QueryRow(r.Context(), `SELECT kind,name,attributes FROM entities WHERE organization_id=$1 AND id=$2 AND current`, principal.OrganizationID, selected.EntityID).Scan(&kind, &name, &attributes)
		if err != nil {
			writeError(w, 422, "invalid_ard_export_entry", fmt.Sprintf("Entry %d does not reference a current entity", index+1))
			return
		}
		attrs := jsonObject(attributes)
		identifier := strings.TrimSpace(selected.Identifier)
		if identifier == "" {
			identifier, _ = attrs["ard_identifier"].(string)
		}
		if !ard.ValidIdentifier(identifier) || !strings.HasPrefix(identifier, "urn:air:"+request.PublisherDomain+":") || seen[identifier] {
			writeError(w, 422, "invalid_ard_export_entry", fmt.Sprintf("Entry %d requires a unique identifier anchored to %s", index+1, request.PublisherDomain))
			return
		}
		seen[identifier] = true
		mediaType := strings.TrimSpace(selected.MediaType)
		if mediaType == "" {
			mediaType, _ = attrs["media_type"].(string)
		}
		if mediaType == "" {
			mediaType = defaultARDMediaType(kind)
		}
		mediaType, mediaTypeErr := ard.NormalizeMediaType(mediaType)
		artifactURL := strings.TrimSpace(selected.ArtifactURL)
		if artifactURL == "" {
			for _, key := range []string{"artifact_url", "descriptor_url", "url", "endpoint", "base_url", "openapi_reference"} {
				if value, _ := attrs[key].(string); value != "" {
					artifactURL = value
					break
				}
			}
		}
		artifactURL, err = ard.ValidateCatalogURL(artifactURL)
		if err != nil || mediaTypeErr != nil {
			writeError(w, 422, "invalid_ard_export_entry", fmt.Sprintf("Entry %d requires a media type and credential-free HTTPS artifact URL", index+1))
			return
		}
		entry := map[string]any{"identifier": identifier, "displayName": boundedARDText(name, 500), "type": mediaType, "url": artifactURL}
		if description, _ := attrs["description"].(string); description != "" {
			entry["description"] = boundedARDText(description, 1000)
		}
		if tags := stringSliceAttribute(attrs["tags"]); len(tags) > 0 {
			entry["tags"] = tags
		}
		if capabilities := stringSliceAttribute(attrs["declared_capabilities"]); len(capabilities) > 0 {
			entry["capabilities"] = capabilities
		}
		if version, _ := attrs["version"].(string); version != "" {
			entry["version"] = version
		}
		exportEntries = append(exportEntries, entry)
	}
	document := map[string]any{"specVersion": "1.0", "host": map[string]any{"displayName": request.HostDisplayName}, "entries": exportEntries}
	w.Header().Set("Content-Type", "application/ai-catalog+json")
	w.Header().Set("Content-Disposition", `attachment; filename="ai-catalog.json"`)
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(document)
}

func defaultARDMediaType(kind string) string {
	switch kind {
	case "agent":
		return "application/a2a-agent-card+json"
	case "mcp_server":
		return "application/mcp-server-card+json"
	case "skill":
		return "application/ai-skill+json"
	case "api_service":
		return "application/openapi+json"
	case "workflow":
		return "application/arazzo+json"
	}
	return ""
}

func boundedARDText(value string, limit int) string {
	value = strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	runes := []rune(value)
	if len(runes) > limit {
		value = string(runes[:limit])
	}
	return value
}

func stringSliceAttribute(value any) []string {
	result := []string{}
	switch values := value.(type) {
	case []string:
		result = append(result, values...)
	case []any:
		for _, item := range values {
			if text, ok := item.(string); ok {
				result = append(result, text)
			}
		}
	}
	seen := map[string]bool{}
	safe := []string{}
	for _, item := range result {
		item = boundedARDText(item, 128)
		key := strings.ToLower(item)
		if item == "" || seen[key] {
			continue
		}
		seen[key] = true
		safe = append(safe, item)
		if len(safe) == 100 {
			break
		}
	}
	sort.Strings(safe)
	return safe
}

func (s *Server) systemDeclarationInfo(ctx context.Context, organizationID, entityID string) (string, []string) {
	rows, err := s.config.Pool.Query(ctx, `SELECT declaration_entity_id,status FROM resource_matches WHERE organization_id=$1 AND observed_entity_id=$2 AND status IN ('linked','conflict') ORDER BY declaration_entity_id`, organizationID, entityID)
	if err != nil {
		return "unmatched", []string{}
	}
	defer rows.Close()
	status := "unmatched"
	ids := []string{}
	for rows.Next() {
		var id, matchStatus string
		if rows.Scan(&id, &matchStatus) == nil {
			ids = append(ids, id)
			if matchStatus == "conflict" {
				status = "conflict"
			} else if status != "conflict" {
				status = "matched"
			}
		}
	}
	return status, ids
}
