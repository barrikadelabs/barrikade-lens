package hub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/barrikadelabs/barrikade-lens/pkg/discovery"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const exposureRuleVersion = "1"

type EntityContext struct {
	OwnerName      string     `json:"owner_name,omitempty"`
	OwnerType      string     `json:"owner_type,omitempty"`
	Environment    string     `json:"environment,omitempty"`
	Criticality    string     `json:"criticality,omitempty"`
	Sensitivity    string     `json:"sensitivity,omitempty"`
	DataCategories []string   `json:"data_categories"`
	TrustBoundary  string     `json:"trust_boundary,omitempty"`
	UpdatedBy      string     `json:"updated_by,omitempty"`
	UpdatedAt      *time.Time `json:"updated_at,omitempty"`
}

type exposureDestination struct {
	ID, Kind, Name, Host string
	Attributes           map[string]any
	Public               bool
	Credential           bool
	Catalog              *exposureCatalog
}

type exposureCatalog struct {
	EntityID, APIID, SourceID, Name, Version, MatchBasis string
	Operations                                           []catalogOperation
}

type exposureFinding struct {
	ID, RootID, DestinationID, RuleID, Severity string
	Title, Explanation, Recommendation          string
	Path                                        []map[string]any
	Bases                                       []string
}

type ExposureWorker struct {
	Pool     *pgxpool.Pool
	Logger   *slog.Logger
	Interval time.Duration
}

func (w ExposureWorker) Run(ctx context.Context) error {
	if w.Logger == nil {
		w.Logger = slog.Default()
	}
	if w.Interval == 0 {
		w.Interval = 2 * time.Second
	}
	_, _ = w.Pool.Exec(ctx, `INSERT INTO exposure_evaluation_jobs(organization_id) SELECT id FROM organizations ON CONFLICT(organization_id) DO UPDATE SET status='pending',next_attempt_at=now(),updated_at=now()`)
	ticker := time.NewTicker(w.Interval)
	defer ticker.Stop()
	for {
		if err := w.runOne(ctx); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			w.Logger.Warn("exposure evaluation failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (w ExposureWorker) runOne(ctx context.Context) error {
	tx, err := w.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var orgID string
	err = tx.QueryRow(ctx, `SELECT organization_id FROM exposure_evaluation_jobs WHERE status='pending' AND next_attempt_at<=now() ORDER BY updated_at FOR UPDATE SKIP LOCKED LIMIT 1`).Scan(&orgID)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE exposure_evaluation_jobs SET status='processing',attempts=attempts+1,updated_at=now() WHERE organization_id=$1`, orgID); err != nil {
		return err
	}
	if err := recomputeOrganizationExposures(ctx, tx, orgID); err != nil {
		_, _ = tx.Exec(ctx, `UPDATE exposure_evaluation_jobs SET status='pending',next_attempt_at=now()+interval '30 seconds',updated_at=now() WHERE organization_id=$1`, orgID)
		return err
	}
	_, err = tx.Exec(ctx, `DELETE FROM exposure_evaluation_jobs WHERE organization_id=$1`, orgID)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func enqueueExposureEvaluation(ctx context.Context, tx pgx.Tx, orgID string) error {
	_, err := tx.Exec(ctx, `INSERT INTO exposure_evaluation_jobs(organization_id,status,next_attempt_at,updated_at) VALUES($1,'pending',now(),now()) ON CONFLICT(organization_id) DO UPDATE SET status='pending',next_attempt_at=now(),updated_at=now()`, orgID)
	return err
}

func recomputeOrganizationExposures(ctx context.Context, tx pgx.Tx, orgID string) error {
	rows, err := tx.Query(ctx, `SELECT e.id,e.name,e.attributes,p.discovery_state FROM entity_posture p JOIN entities e ON e.organization_id=p.organization_id AND e.id=p.entity_id WHERE p.organization_id=$1 AND p.current=true AND p.system_role='system' AND e.current=true AND (`+freshPostureTargetSQL("p")+`)`, orgID)
	if err != nil {
		return err
	}
	type root struct {
		id, name, state string
		attrs           map[string]any
	}
	roots := []root{}
	for rows.Next() {
		var item root
		var raw []byte
		if err := rows.Scan(&item.id, &item.name, &raw, &item.state); err != nil {
			rows.Close()
			return err
		}
		item.attrs = jsonObject(raw)
		roots = append(roots, item)
	}
	rows.Close()
	findings := []exposureFinding{}
	for _, root := range roots {
		rootContext, _ := loadEntityContext(ctx, tx, orgID, root.id)
		destinations, err := loadExposureDestinations(ctx, tx, orgID, root.id)
		if err != nil {
			return err
		}
		running := root.state == "running" || boolValue(root.attrs["running_at_scan"])
		hasExternal := false
		for _, destination := range destinations {
			if explicitlyDisabled(destination.Attributes) {
				continue
			}
			destinationContext, _ := loadEntityContext(ctx, tx, orgID, destination.ID)
			nonInternal := destination.Public || destinationContext.TrustBoundary == "partner" || destinationContext.TrustBoundary == "third_party"
			if !nonInternal {
				continue
			}
			hasExternal = true
			path := observedPath(root.id, root.name, destination)
			if destination.Credential {
				severity := "medium"
				if running {
					severity = "high"
				}
				findings = append(findings, newFinding(root.id, destination.ID, "credentialed_external_connector", severity, "Credentialed external connector", "A configured external connection has credential presence. Lens observed configuration only; it did not read the credential value or verify authorization.", "Confirm the destination, credential owner, effective scope, and whether this connector is still required.", path, []string{"observed"}))
			}
			if rootContext.Sensitivity == "confidential" || rootContext.Sensitivity == "restricted" {
				severity := "high"
				if rootContext.Sensitivity == "restricted" || containsAny(rootContext.DataCategories, "health", "payment", "credentials") {
					severity = "critical"
				}
				recommendation := "Confirm the destination trust boundary and validate that the data classification is appropriate for this connection."
				if destinationContext.TrustBoundary == "" {
					severity = lowerSeverity(severity)
					recommendation = "Confirm whether this public-network destination is internal, a partner, or third party, then validate the data path."
				}
				findings = append(findings, newFinding(root.id, destination.ID, "sensitive_public_destination", severity, "Sensitive system can reach a public destination", "Operator-supplied sensitivity is combined with an observed configured path to a public-network destination; data transfer or tool invocation was not observed.", recommendation, path, []string{"observed", "operator_context"}))
			}
			if destination.Credential && destination.Catalog != nil {
				hasState, hasDelete := false, false
				for _, operation := range destination.Catalog.Operations {
					if operation.Class == "destructive_potential" {
						hasDelete, hasState = true, true
					}
					if operation.Class == "state_changing_potential" {
						hasState = true
					}
				}
				if hasState {
					severity := "medium"
					if hasDelete {
						severity = "high"
					}
					catalogPath := append(append([]map[string]any{}, path...), map[string]any{"entity_id": destination.Catalog.EntityID, "name": destination.Catalog.Name, "kind": "api_service", "edge": "catalog_potential", "basis": "catalog_potential"})
					findings = append(findings, newFinding(root.id, destination.ID, "state_changing_api_potential", severity, "Linked API advertises state-changing operations", "A credential-backed connector is linked to a reviewed or uniquely matched API catalogue entry that advertises non-read operations. Effective credential scope and actual invocation were not verified.", "Review the representative operations and independently confirm the credential's effective scopes before changing configuration.", catalogPath, []string{"observed", "catalog_potential"}))
				}
			}
		}
		hasOwner := rootContext.OwnerName != ""
		if !hasOwner {
			_ = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM relationships WHERE organization_id=$1 AND current=true AND kind='owned_by' AND from_entity=$2 AND COALESCE((attributes->>'authoritative')::boolean,false)=true)`, orgID, root.id).Scan(&hasOwner)
		}
		if !hasOwner {
			severity := "low"
			if running && hasExternal {
				severity = "medium"
			}
			findings = append(findings, newFinding(root.id, "", "missing_owner", severity, "System has no authoritative owner", "No operator owner or authoritative ownership relationship is recorded. An observed operating-system user is not treated as ownership.", "Assign a person or team responsible for reviewing this system and its external connections.", []map[string]any{{"entity_id": root.id, "name": root.name, "kind": "system", "basis": "observed"}}, []string{"observed", "operator_context"}))
		}
	}
	ids := []string{}
	for _, finding := range findings {
		ids = append(ids, finding.ID)
		path, _ := json.Marshal(finding.Path)
		historyValue, _ := json.Marshal(map[string]any{"id": finding.ID, "root_entity_id": finding.RootID, "destination_entity_id": nullString(finding.DestinationID), "rule_id": finding.RuleID, "rule_version": exposureRuleVersion, "severity": finding.Severity, "title": finding.Title, "explanation": finding.Explanation, "recommended_next_step": finding.Recommendation, "path": finding.Path, "evidence_bases": finding.Bases})
		historyID := discovery.ContentHash(append([]byte("current\x00"+finding.ID+"\x00"), historyValue...))
		if _, err := tx.Exec(ctx, `INSERT INTO exposure_finding_history(id,organization_id,finding_id,state,finding) VALUES($1,$2,$3,'current',$4) ON CONFLICT(organization_id,id) DO NOTHING`, historyID, orgID, finding.ID, historyValue); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO exposure_findings(organization_id,id,root_entity_id,destination_entity_id,rule_id,rule_version,severity,title,explanation,recommended_next_step,path,evidence_bases,current,first_seen_at,last_seen_at,resolved_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,true,now(),now(),NULL) ON CONFLICT(organization_id,id) DO UPDATE SET severity=EXCLUDED.severity,title=EXCLUDED.title,explanation=EXCLUDED.explanation,recommended_next_step=EXCLUDED.recommended_next_step,path=EXCLUDED.path,evidence_bases=EXCLUDED.evidence_bases,current=true,last_seen_at=now(),resolved_at=NULL`, orgID, finding.ID, finding.RootID, nullString(finding.DestinationID), finding.RuleID, exposureRuleVersion, finding.Severity, finding.Title, finding.Explanation, finding.Recommendation, path, finding.Bases)
		if err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `INSERT INTO exposure_finding_history(id,organization_id,finding_id,state,finding) SELECT gen_random_uuid()::text,organization_id,id,'resolved',jsonb_build_object('id',id,'root_entity_id',root_entity_id,'destination_entity_id',destination_entity_id,'rule_id',rule_id,'rule_version',rule_version,'severity',severity,'title',title,'explanation',explanation,'recommended_next_step',recommended_next_step,'path',path,'evidence_bases',evidence_bases) FROM exposure_findings WHERE organization_id=$1 AND current=true AND NOT(id=ANY($2::text[]))`, orgID, ids); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE exposure_findings SET current=false,resolved_at=now(),last_seen_at=now() WHERE organization_id=$1 AND current=true AND NOT(id=ANY($2::text[]))`, orgID, ids)
	return err
}

func loadExposureDestinations(ctx context.Context, q interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}, orgID, rootID string) ([]exposureDestination, error) {
	rows, err := q.Query(ctx, `SELECT e.id,e.kind,e.name,e.attributes FROM relationships r JOIN entities e ON e.organization_id=r.organization_id AND e.id=r.to_entity WHERE r.organization_id=$1 AND r.from_entity=$2 AND r.current=true AND r.kind='connects_to' AND e.current=true AND e.kind IN ('mcp_server','api_service') ORDER BY e.name`, orgID, rootID)
	if err != nil {
		return nil, err
	}
	result := []exposureDestination{}
	for rows.Next() {
		var item exposureDestination
		var raw []byte
		if err := rows.Scan(&item.ID, &item.Kind, &item.Name, &raw); err != nil {
			return nil, err
		}
		item.Attributes = jsonObject(raw)
		item.Host = attributeString(item.Attributes, "host")
		if item.Host == "" {
			item.Host = hostFromEndpoint(attributeString(item.Attributes, "endpoint"))
		}
		item.Public = isPublicDestination(item.Host, item.Attributes)
		item.Credential = boolValue(item.Attributes["credential_present"])
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	for index := range result {
		result[index].Catalog, _ = loadDestinationCatalog(ctx, q, orgID, result[index].ID)
	}
	return result, nil
}

// rowsConnQueryRow exists only to keep loadExposureDestinations usable with a
// transaction in the evaluator and the pool in read handlers.
func rowsConnQueryRow(ctx context.Context, q any, query string, args ...any) pgx.Row {
	switch typed := q.(type) {
	case pgx.Tx:
		return typed.QueryRow(ctx, query, args...)
	case *pgxpool.Pool:
		return typed.QueryRow(ctx, query, args...)
	}
	return errorRow{fmt.Errorf("unsupported query source")}
}

type errorRow struct{ err error }

func (r errorRow) Scan(...any) error { return r.err }

func loadDestinationCatalog(ctx context.Context, q any, orgID, destinationID string) (*exposureCatalog, error) {
	row := rowsConnQueryRow(ctx, q, `SELECT service.id,service.name,COALESCE(service.attributes->>'version',''),m.source_id,m.api_id,COALESCE(m.metadata->>'reason','reviewed link') FROM relationships cr JOIN entities service ON service.organization_id=cr.organization_id AND service.id=cr.to_entity JOIN catalog_matches m ON m.organization_id=cr.organization_id AND m.entity_id=cr.from_entity AND m.status='linked' AND m.api_id=service.attributes->>'api_id' WHERE cr.organization_id=$1 AND cr.from_entity=$2 AND cr.current=true AND cr.attributes->>'catalog_enriched'='true' AND service.current=true LIMIT 1`, orgID, destinationID)
	item := &exposureCatalog{}
	if err := row.Scan(&item.EntityID, &item.Name, &item.Version, &item.SourceID, &item.APIID, &item.MatchBasis); err != nil {
		return nil, err
	}
	rows, err := q.(interface {
		Query(context.Context, string, ...any) (pgx.Rows, error)
	}).Query(ctx, `SELECT operation_key,operation_id,method,path,COALESCE(summary,''),capability_class,tags,auth_scheme_types,auth_scopes FROM catalog_api_operations WHERE organization_id=$1 AND source_id=$2 AND api_id=$3 ORDER BY path,method`, orgID, item.SourceID, item.APIID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var op catalogOperation
		if err := rows.Scan(&op.Key, &op.ID, &op.Method, &op.Path, &op.Summary, &op.Class, &op.Tags, &op.AuthSchemes, &op.AuthScopes); err != nil {
			return nil, err
		}
		item.Operations = append(item.Operations, op)
	}
	return item, rows.Err()
}

func newFinding(root, destination, rule, severity, title, explanation, recommendation string, path []map[string]any, bases []string) exposureFinding {
	id := discovery.ContentHash([]byte(strings.Join([]string{exposureRuleVersion, rule, root, destination}, "\x00")))
	return exposureFinding{ID: id, RootID: root, DestinationID: destination, RuleID: rule, Severity: severity, Title: title, Explanation: explanation, Recommendation: recommendation, Path: path, Bases: bases}
}

func observedPath(rootID, rootName string, destination exposureDestination) []map[string]any {
	return []map[string]any{{"entity_id": rootID, "name": rootName, "kind": "system", "basis": "observed"}, {"entity_id": destination.ID, "name": destination.Name, "kind": destination.Kind, "edge": "connects_to", "basis": "observed"}}
}
func attributeString(attrs map[string]any, key string) string {
	value, _ := attrs[key].(string)
	return value
}
func boolValue(value any) bool { result, _ := value.(bool); return result }
func explicitlyDisabled(attrs map[string]any) bool {
	value, exists := attrs["enabled"]
	return exists && value == false
}
func hostFromEndpoint(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return parsed.Hostname()
}
func isPublicDestination(host string, attrs map[string]any) bool {
	if scope := attributeString(attrs, "network_scope"); scope == "external" || scope == "network" {
		return true
	}
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	if host == "" || host == "localhost" || strings.HasSuffix(host, ".local") {
		return false
	}
	if ip := net.ParseIP(host); ip != nil {
		return !(ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified())
	}
	return true
}
func containsAny(values []string, candidates ...string) bool {
	for _, value := range values {
		for _, candidate := range candidates {
			if value == candidate {
				return true
			}
		}
	}
	return false
}
func lowerSeverity(value string) string {
	return map[string]string{"critical": "high", "high": "medium", "medium": "low", "low": "low"}[value]
}

func loadEntityContext(ctx context.Context, q any, orgID, entityID string) (EntityContext, error) {
	var item EntityContext
	row := rowsConnQueryRow(ctx, q, `SELECT COALESCE(owner_name,''),COALESCE(owner_type,''),COALESCE(environment,''),COALESCE(criticality,''),COALESCE(sensitivity,''),data_categories,COALESCE(trust_boundary,''),updated_by,updated_at FROM entity_context WHERE organization_id=$1 AND entity_id=$2`, orgID, entityID)
	err := row.Scan(&item.OwnerName, &item.OwnerType, &item.Environment, &item.Criticality, &item.Sensitivity, &item.DataCategories, &item.TrustBoundary, &item.UpdatedBy, &item.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		item.DataCategories = []string{}
		return item, nil
	}
	return item, err
}

func (s *Server) getEntityContext(w http.ResponseWriter, r *http.Request) {
	principal, err := requireScope(r, "inventory:read")
	if err != nil {
		writeError(w, 403, "forbidden", err.Error())
		return
	}
	contextValue, err := loadEntityContext(r.Context(), s.config.Pool, principal.OrganizationID, r.PathValue("id"))
	if err != nil {
		writeError(w, 500, "database_error", "Could not read context")
		return
	}
	writeJSON(w, 200, contextValue)
}

func (s *Server) putEntityContext(w http.ResponseWriter, r *http.Request) {
	principal, err := requireScope(r, "context:write")
	if err != nil || !principal.Admin {
		writeError(w, 403, "forbidden", "Administrator context:write access is required")
		return
	}
	var value EntityContext
	if err := decodeJSON(w, r, &value, 32<<10); err != nil {
		return
	}
	value.OwnerName = strings.TrimSpace(value.OwnerName)
	if value.DataCategories == nil {
		value.DataCategories = []string{}
	}
	if err := validateEntityContext(value); err != nil {
		writeError(w, 400, "invalid_context", err.Error())
		return
	}
	tx, err := s.config.Pool.BeginTx(r.Context(), pgx.TxOptions{})
	if err != nil {
		writeError(w, 500, "database_error", "Could not save context")
		return
	}
	defer tx.Rollback(r.Context())
	var exists bool
	if err := tx.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM entities WHERE organization_id=$1 AND id=$2)`, principal.OrganizationID, r.PathValue("id")).Scan(&exists); err != nil || !exists {
		writeError(w, 404, "not_found", "Entity not found")
		return
	}
	_, err = tx.Exec(r.Context(), `INSERT INTO entity_context(organization_id,entity_id,owner_name,owner_type,environment,criticality,sensitivity,data_categories,trust_boundary,updated_by) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) ON CONFLICT(organization_id,entity_id) DO UPDATE SET owner_name=EXCLUDED.owner_name,owner_type=EXCLUDED.owner_type,environment=EXCLUDED.environment,criticality=EXCLUDED.criticality,sensitivity=EXCLUDED.sensitivity,data_categories=EXCLUDED.data_categories,trust_boundary=EXCLUDED.trust_boundary,updated_by=EXCLUDED.updated_by,updated_at=now()`, principal.OrganizationID, r.PathValue("id"), nullString(value.OwnerName), nullString(value.OwnerType), nullString(value.Environment), nullString(value.Criticality), nullString(value.Sensitivity), value.DataCategories, nullString(value.TrustBoundary), principal.Subject)
	if err == nil {
		encoded, _ := json.Marshal(value)
		_, err = tx.Exec(r.Context(), `INSERT INTO entity_context_history(id,organization_id,entity_id,context,changed_by) VALUES($1,$2,$3,$4,$5)`, uuid.New(), principal.OrganizationID, r.PathValue("id"), encoded, principal.Subject)
	}
	if err == nil {
		err = recomputeOrganizationExposures(r.Context(), tx, principal.OrganizationID)
	}
	if err == nil {
		err = tx.Commit(r.Context())
	}
	if err != nil {
		writeError(w, 500, "database_error", "Could not save context")
		return
	}
	value, _ = loadEntityContext(r.Context(), s.config.Pool, principal.OrganizationID, r.PathValue("id"))
	writeJSON(w, 200, value)
}

func validateEntityContext(value EntityContext) error {
	if len([]rune(value.OwnerName)) > 200 {
		return fmt.Errorf("owner_name must be at most 200 characters")
	}
	if (value.OwnerName == "") != (value.OwnerType == "") {
		return fmt.Errorf("owner_name and owner_type must be supplied together")
	}
	if !allowedValue(value.OwnerType, "", "person", "team") || !allowedValue(value.Environment, "", "development", "test", "staging", "production") || !allowedValue(value.Criticality, "", "low", "medium", "high", "critical") || !allowedValue(value.Sensitivity, "", "public", "internal", "confidential", "restricted") || !allowedValue(value.TrustBoundary, "", "internal", "partner", "third_party") {
		return fmt.Errorf("context contains an unsupported classification")
	}
	seen := map[string]bool{}
	for _, category := range value.DataCategories {
		if !allowedValue(category, "personal", "health", "payment", "financial", "credentials", "source_code", "customer") || seen[category] {
			return fmt.Errorf("data_categories contains an unsupported or duplicate value")
		}
		seen[category] = true
	}
	sort.Strings(value.DataCategories)
	return nil
}
func allowedValue(value string, values ...string) bool {
	for _, candidate := range values {
		if value == candidate {
			return true
		}
	}
	return false
}

func (s *Server) listExposures(w http.ResponseWriter, r *http.Request) {
	principal, err := requireScope(r, "inventory:read")
	if err != nil {
		writeError(w, 403, "forbidden", err.Error())
		return
	}
	limit := queryLimit(r)
	cursor, err := decodeCursor(r.URL.Query().Get("cursor"), "exposure")
	if err != nil {
		writeError(w, 400, "invalid_cursor", err.Error())
		return
	}
	query := `SELECT f.id,f.root_entity_id,root.name,f.destination_entity_id,destination.name,f.rule_id,f.rule_version,f.severity,f.title,f.explanation,f.recommended_next_step,f.path,f.evidence_bases,f.first_seen_at,f.last_seen_at FROM exposure_findings f JOIN entities root ON root.organization_id=f.organization_id AND root.id=f.root_entity_id LEFT JOIN entities destination ON destination.organization_id=f.organization_id AND destination.id=f.destination_entity_id WHERE f.organization_id=$1 AND f.current=true`
	args := []any{principal.OrganizationID}
	if cursor.ID != "" {
		parts := strings.SplitN(cursor.Value, "|", 2)
		rank, rankErr := strconv.Atoi(parts[0])
		var last time.Time
		if len(parts) != 2 || rankErr != nil {
			writeError(w, 400, "invalid_cursor", "invalid cursor")
			return
		}
		last, err = time.Parse(time.RFC3339Nano, parts[1])
		if err != nil {
			writeError(w, 400, "invalid_cursor", "invalid cursor")
			return
		}
		args = append(args, rank, last, cursor.ID)
		query += ` AND (CASE f.severity WHEN 'critical' THEN 1 WHEN 'high' THEN 2 WHEN 'medium' THEN 3 ELSE 4 END>$2 OR (CASE f.severity WHEN 'critical' THEN 1 WHEN 'high' THEN 2 WHEN 'medium' THEN 3 ELSE 4 END=$2 AND (f.last_seen_at<$3 OR (f.last_seen_at=$3 AND f.id<$4))))`
	}
	args = append(args, limit)
	query += fmt.Sprintf(` ORDER BY CASE f.severity WHEN 'critical' THEN 1 WHEN 'high' THEN 2 WHEN 'medium' THEN 3 ELSE 4 END,f.last_seen_at DESC,f.id DESC LIMIT $%d`, len(args))
	rows, err := s.config.Pool.Query(r.Context(), query, args...)
	if err != nil {
		writeError(w, 500, "database_error", "Could not read exposures")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	var next pageCursor
	for rows.Next() {
		item, scanErr := scanExposure(rows)
		if scanErr == nil {
			items = append(items, item)
			severity, _ := item["severity"].(string)
			last, _ := item["last_seen_at"].(time.Time)
			id, _ := item["id"].(string)
			next = pageCursor{Sort: "exposure", Value: strconv.Itoa(severityRank(severity)) + "|" + last.Format(time.RFC3339Nano), ID: id}
		}
	}
	response := map[string]any{"items": items, "limit": limit}
	if len(items) == limit {
		response["next_cursor"] = encodeCursor(next)
	}
	writeJSON(w, 200, response)
}

func severityRank(severity string) int {
	switch severity {
	case "critical":
		return 1
	case "high":
		return 2
	case "medium":
		return 3
	default:
		return 4
	}
}
func (s *Server) getExposure(w http.ResponseWriter, r *http.Request) {
	principal, err := requireScope(r, "inventory:read")
	if err != nil {
		writeError(w, 403, "forbidden", err.Error())
		return
	}
	row := s.config.Pool.QueryRow(r.Context(), `SELECT f.id,f.root_entity_id,root.name,f.destination_entity_id,destination.name,f.rule_id,f.rule_version,f.severity,f.title,f.explanation,f.recommended_next_step,f.path,f.evidence_bases,f.first_seen_at,f.last_seen_at FROM exposure_findings f JOIN entities root ON root.organization_id=f.organization_id AND root.id=f.root_entity_id LEFT JOIN entities destination ON destination.organization_id=f.organization_id AND destination.id=f.destination_entity_id WHERE f.organization_id=$1 AND f.id=$2`, principal.OrganizationID, r.PathValue("id"))
	item, err := scanExposure(row)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 404, "not_found", "Exposure not found")
		return
	}
	if err != nil {
		writeError(w, 500, "database_error", "Could not read exposure")
		return
	}
	writeJSON(w, 200, item)
}

type exposureRowScanner interface{ Scan(...any) error }

func scanExposure(row exposureRowScanner) (map[string]any, error) {
	var id, rootID, rootName, rule, version, severity, title, explanation, recommendation string
	var destinationID, destinationName *string
	var path []byte
	var bases []string
	var first, last time.Time
	err := row.Scan(&id, &rootID, &rootName, &destinationID, &destinationName, &rule, &version, &severity, &title, &explanation, &recommendation, &path, &bases, &first, &last)
	if err != nil {
		return nil, err
	}
	var decodedPath []map[string]any
	_ = json.Unmarshal(path, &decodedPath)
	return map[string]any{"id": id, "root_entity_id": rootID, "root_name": rootName, "destination_entity_id": destinationID, "destination_name": destinationName, "rule_id": rule, "rule_version": version, "severity": severity, "title": title, "explanation": explanation, "recommended_next_step": recommendation, "path": decodedPath, "evidence_bases": bases, "first_seen_at": first, "last_seen_at": last}, nil
}

func (s *Server) getExposureMap(w http.ResponseWriter, r *http.Request) {
	principal, err := requireScope(r, "inventory:read")
	if err != nil {
		writeError(w, 403, "forbidden", err.Error())
		return
	}
	orgID, id := principal.OrganizationID, r.PathValue("id")
	var rootName, state string
	var attrsRaw []byte
	err = s.config.Pool.QueryRow(r.Context(), `SELECT e.name,e.attributes,p.discovery_state FROM entities e JOIN entity_posture p ON p.organization_id=e.organization_id AND p.entity_id=e.id WHERE e.organization_id=$1 AND e.id=$2 AND p.system_role='system'`, orgID, id).Scan(&rootName, &attrsRaw, &state)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 404, "not_found", "System not found")
		return
	}
	if err != nil {
		writeError(w, 500, "database_error", "Could not read exposure map")
		return
	}
	destinations, err := loadExposureDestinations(r.Context(), s.config.Pool, orgID, id)
	if err != nil {
		writeError(w, 500, "database_error", "Could not read exposure map")
		return
	}
	contextValue, _ := loadEntityContext(r.Context(), s.config.Pool, orgID, id)
	destinationItems := []map[string]any{}
	for _, destination := range destinations {
		destinationContext, _ := loadEntityContext(r.Context(), s.config.Pool, orgID, destination.ID)
		item := map[string]any{"id": destination.ID, "kind": destination.Kind, "name": destination.Name, "host": destination.Host, "public_network": destination.Public, "credential_present": destination.Credential, "enabled": !explicitlyDisabled(destination.Attributes), "basis": "observed", "attributes": destination.Attributes}
		item["context"] = destinationContext
		if destination.Catalog != nil {
			counts := map[string]int{}
			representative := []map[string]any{}
			for _, operation := range destination.Catalog.Operations {
				counts[operation.Class]++
				if len(representative) < 8 {
					representative = append(representative, map[string]any{"operation_id": operation.ID, "method": operation.Method, "path": operation.Path, "summary": operation.Summary, "class": operation.Class, "tags": operation.Tags, "auth_scheme_types": operation.AuthSchemes, "auth_scopes": operation.AuthScopes})
				}
			}
			item["catalog"] = map[string]any{"status": "linked", "basis": "catalog_potential", "api_id": destination.Catalog.APIID, "name": destination.Catalog.Name, "version": destination.Catalog.Version, "match_basis": destination.Catalog.MatchBasis, "operation_counts": counts, "representative_operations": representative}
		} else {
			item["catalog"] = map[string]any{"status": "unmapped", "basis": "catalog_potential", "message": "API capabilities not mapped"}
		}
		destinationItems = append(destinationItems, item)
	}
	rows, err := s.config.Pool.Query(r.Context(), `SELECT f.id,f.root_entity_id,root.name,f.destination_entity_id,destination.name,f.rule_id,f.rule_version,f.severity,f.title,f.explanation,f.recommended_next_step,f.path,f.evidence_bases,f.first_seen_at,f.last_seen_at FROM exposure_findings f JOIN entities root ON root.organization_id=f.organization_id AND root.id=f.root_entity_id LEFT JOIN entities destination ON destination.organization_id=f.organization_id AND destination.id=f.destination_entity_id WHERE f.organization_id=$1 AND f.root_entity_id=$2 AND f.current=true ORDER BY CASE f.severity WHEN 'critical' THEN 1 WHEN 'high' THEN 2 WHEN 'medium' THEN 3 ELSE 4 END`, orgID, id)
	findings := []map[string]any{}
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			if item, scanErr := scanExposure(rows); scanErr == nil {
				findings = append(findings, item)
			}
		}
	}
	writeJSON(w, 200, map[string]any{"system": map[string]any{"id": id, "name": rootName, "state": state, "attributes": jsonObject(attrsRaw)}, "context": contextValue, "destinations": destinationItems, "findings": findings, "product_boundary": "Lens discovers and assesses exposure. It does not verify effective authorization, invoke tools, change credentials, remediate, or enforce policy."})
}

func (s *Server) searchCatalog(w http.ResponseWriter, r *http.Request) {
	principal, err := requireScope(r, "inventory:read")
	if err != nil {
		writeError(w, 403, "forbidden", err.Error())
		return
	}
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if len(query) > 200 {
		writeError(w, 400, "invalid_query", "Search must be at most 200 characters")
		return
	}
	rows, err := s.config.Pool.Query(r.Context(), `SELECT source_id,entry_id,provider_id,COALESCE(api_family,''),COALESCE(api_version,''),display_name FROM catalog_index_entries WHERE organization_id=$1 AND ($2='' OR provider_id ILIKE '%'||$2||'%' OR COALESCE(api_family,'') ILIKE '%'||$2||'%' OR display_name ILIKE '%'||$2||'%') ORDER BY provider_id,api_family,api_version LIMIT 50`, principal.OrganizationID, query)
	if err != nil {
		writeError(w, 500, "database_error", "Could not search catalogue")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var source, id, provider, family, version, name string
		if rows.Scan(&source, &id, &provider, &family, &version, &name) == nil {
			items = append(items, map[string]any{"source_id": source, "entry_id": id, "provider": provider, "api_family": family, "version": version, "name": name, "status": "suggestion"})
		}
	}
	writeJSON(w, 200, map[string]any{"items": items})
}

func (s *Server) putCatalogLink(w http.ResponseWriter, r *http.Request) {
	principal, err := requireScope(r, "context:write")
	if err != nil || !principal.Admin {
		writeError(w, 403, "forbidden", "Administrator context:write access is required")
		return
	}
	var request struct {
		SourceID string `json:"source_id"`
		EntryID  string `json:"entry_id"`
	}
	if err := decodeJSON(w, r, &request, 16<<10); err != nil {
		return
	}
	var reference string
	err = s.config.Pool.QueryRow(r.Context(), `SELECT entry_reference FROM catalog_index_entries WHERE organization_id=$1 AND source_id=$2 AND entry_id=$3`, principal.OrganizationID, request.SourceID, request.EntryID).Scan(&reference)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 404, "not_found", "Catalogue entry not found")
		return
	}
	if err != nil {
		writeError(w, 500, "database_error", "Could not link catalogue entry")
		return
	}
	_, err = s.config.Pool.Exec(r.Context(), `INSERT INTO catalog_link_overrides(organization_id,entity_id,source_id,api_id,entry_reference,selected_by) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(organization_id,entity_id,source_id) DO UPDATE SET api_id=EXCLUDED.api_id,entry_reference=EXCLUDED.entry_reference,selected_by=EXCLUDED.selected_by,selected_at=now()`, principal.OrganizationID, r.PathValue("id"), request.SourceID, request.EntryID, reference, principal.Subject)
	if err != nil {
		writeError(w, 500, "database_error", "Could not link catalogue entry")
		return
	}
	writeJSON(w, 202, map[string]any{"status": "pending_enrichment", "entry_id": request.EntryID})
}
func (s *Server) deleteCatalogLink(w http.ResponseWriter, r *http.Request) {
	principal, err := requireScope(r, "context:write")
	if err != nil || !principal.Admin {
		writeError(w, 403, "forbidden", "Administrator context:write access is required")
		return
	}
	tx, err := s.config.Pool.BeginTx(r.Context(), pgx.TxOptions{})
	if err != nil {
		writeError(w, 500, "database_error", "Could not remove catalogue link")
		return
	}
	defer tx.Rollback(r.Context())
	_, err = tx.Exec(r.Context(), `DELETE FROM catalog_link_overrides WHERE organization_id=$1 AND entity_id=$2`, principal.OrganizationID, r.PathValue("id"))
	if err == nil {
		_, err = tx.Exec(r.Context(), `UPDATE catalog_matches SET status='suggested' WHERE organization_id=$1 AND entity_id=$2 AND status='linked'`, principal.OrganizationID, r.PathValue("id"))
	}
	if err == nil {
		_, err = tx.Exec(r.Context(), `UPDATE relationships SET current=false,stale=true WHERE organization_id=$1 AND from_entity=$2 AND attributes->>'catalog_enriched'='true'`, principal.OrganizationID, r.PathValue("id"))
	}
	if err == nil {
		err = enqueueExposureEvaluation(r.Context(), tx, principal.OrganizationID)
	}
	if err == nil {
		err = tx.Commit(r.Context())
	}
	if err != nil {
		writeError(w, 500, "database_error", "Could not remove catalogue link")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func exposureSummary(ctx context.Context, pool *pgxpool.Pool, orgID, rootID string) map[string]any {
	query := `SELECT severity,count(*) FROM exposure_findings WHERE organization_id=$1 AND current=true`
	args := []any{orgID}
	if rootID != "" {
		query += ` AND root_entity_id=$2`
		args = append(args, rootID)
	}
	query += ` GROUP BY severity`
	rows, err := pool.Query(ctx, query, args...)
	counts := map[string]int{"critical": 0, "high": 0, "medium": 0, "low": 0}
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var severity string
			var count int
			if rows.Scan(&severity, &count) == nil {
				counts[severity] = count
			}
		}
	}
	return map[string]any{"counts": counts, "total": counts["critical"] + counts["high"] + counts["medium"] + counts["low"]}
}

func exposureOverviewSummary(ctx context.Context, pool *pgxpool.Pool, orgID string) map[string]any {
	result := exposureSummary(ctx, pool, orgID, "")
	rows, err := pool.Query(ctx, `SELECT id,root_entity_id,rule_id,severity,title FROM exposure_findings WHERE organization_id=$1 AND current=true ORDER BY CASE severity WHEN 'critical' THEN 1 WHEN 'high' THEN 2 WHEN 'medium' THEN 3 ELSE 4 END,last_seen_at DESC LIMIT 5`, orgID)
	top := []map[string]any{}
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var id, root, rule, severity, title string
			if rows.Scan(&id, &root, &rule, &severity, &title) == nil {
				top = append(top, map[string]any{"id": id, "root_entity_id": root, "rule_id": rule, "severity": severity, "title": title})
			}
		}
	}
	result["top_findings"] = top
	return result
}
