package hub

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"
)

type evidenceFact struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

type evidenceSubject struct {
	ID         string         `json:"id"`
	Kind       string         `json:"kind"`
	Name       string         `json:"name"`
	Confidence string         `json:"confidence"`
	Current    bool           `json:"current"`
	Attributes map[string]any `json:"attributes"`
}

func (s *Server) evidenceForEntity(ctx context.Context, organizationID, entityID, entityName string, attributes map[string]any, limit int) []map[string]any {
	rows, err := s.config.Pool.Query(ctx, `WITH observations AS (
		SELECT eo.evidence_id,eo.source_id,eo.detector_id,eo.detector_version,eo.method,eo.family,eo.specificity,
			eo.locator,eo.content_hash,eo.entity_ids,max(eo.observed_at) observed_at,count(*) observations,
			row_number() OVER (PARTITION BY eo.source_id,eo.detector_id,eo.method,eo.family,COALESCE(eo.locator,'') ORDER BY max(eo.observed_at) DESC,eo.evidence_id DESC) version_rank
		FROM evidence_observations eo
		WHERE eo.organization_id=$1 AND $2=ANY(eo.entity_ids)
		GROUP BY eo.evidence_id,eo.source_id,eo.detector_id,eo.detector_version,eo.method,eo.family,eo.specificity,eo.locator,eo.content_hash,eo.entity_ids
	)
	SELECT o.evidence_id,o.source_id,o.detector_id,o.detector_version,o.method,o.family,o.specificity,o.locator,o.content_hash,o.observed_at,o.observations,
		COALESCE(s.name,''),COALESCE(s.source_type,''),COALESCE(s.target_id,''),COALESCE(t.name,''),COALESCE(t.target_type,''),t.last_seen_at,
		COALESCE((SELECT jsonb_agg(jsonb_build_object('id',subject.id,'kind',subject.kind,'name',subject.name,'confidence',subject.confidence,'current',subject.current,'attributes',subject.attributes) ORDER BY subject.kind,subject.name,subject.id)
			FROM (SELECT e.id,e.kind,e.name,e.confidence,e.current,e.attributes FROM entities e WHERE e.organization_id=$1 AND e.id=ANY(o.entity_ids) ORDER BY e.kind,e.name,e.id LIMIT 25) subject),'[]'::jsonb)
	FROM observations o
	LEFT JOIN sources s ON s.organization_id=$1 AND s.id=o.source_id
	LEFT JOIN discovery_targets t ON t.organization_id=$1 AND t.id=s.target_id
	WHERE o.version_rank=1
	ORDER BY o.observed_at DESC,o.evidence_id DESC LIMIT $3`, organizationID, entityID, limit)
	if err != nil {
		return []map[string]any{}
	}
	defer rows.Close()
	result := []map[string]any{}
	for rows.Next() {
		var id, sourceID, detectorID, detectorVersion, method, family, specificity string
		var sourceName, sourceType, targetID, targetName, targetType string
		var locator, contentHash *string
		var observedAt time.Time
		var targetLastSeen *time.Time
		var observations int64
		var subjectData []byte
		if rows.Scan(&id, &sourceID, &detectorID, &detectorVersion, &method, &family, &specificity, &locator, &contentHash, &observedAt, &observations, &sourceName, &sourceType, &targetID, &targetName, &targetType, &targetLastSeen, &subjectData) != nil {
			continue
		}
		subjects := []evidenceSubject{}
		_ = json.Unmarshal(subjectData, &subjects)
		primary := primaryEvidenceSubject(subjects, entityID, family)
		if primary != nil && primary.ID != entityID && !primary.Current {
			continue
		}
		location, locatorKind, locatorIntegrity := evidenceLocation(locator, sourceType)
		location = evidenceResourceLocation(location, locatorKind, primary, method)
		subject := strings.TrimSpace(entityName)
		subjectKind := ""
		subjectAttributes := attributes
		if primary != nil {
			subject = primary.Name
			subjectKind = primary.Kind
			subjectAttributes = primary.Attributes
		}
		if subject == "" {
			subject = humanizeDetector(detectorID)
		}
		contextName := evidenceContextName(subjects, primary, entityID, entityName)
		matchedFacts := evidenceMatchedFacts(subjectAttributes, method, family)
		summary := evidenceSummary(subject, subjectKind, contextName, targetName, method, family)
		if method == "skill_descriptor" {
			if purpose, _ := subjectAttributes["declared_purpose"].(string); strings.TrimSpace(purpose) != "" {
				summary += " Declared purpose: " + boundedEvidenceText(purpose, 1000)
			}
		}
		if primary != nil {
			matchedFacts = append([]evidenceFact{{Label: humanizeDetector(primary.Kind) + " name", Value: primary.Name}}, matchedFacts...)
		}
		item := map[string]any{
			"id": id, "source_id": sourceID, "source_name": sourceName, "source_type": sourceType,
			"target_id": targetID, "target_name": targetName, "target_type": targetType,
			"detector_id": detectorID, "detector_version": detectorVersion, "method": method, "family": family,
			"specificity": specificity, "title": evidenceTitle(method, family, subject), "summary": summary,
			"location": location, "locator_kind": locatorKind, "matched_facts": matchedFacts,
			"why_it_matched": evidenceReason(method, family, specificity, subject), "investigation_hint": evidenceInvestigationHint(subject, subjectKind, contextName, targetName, location, method, family),
			"observed_at": observedAt, "observations": observations,
		}
		if primary != nil {
			item["subject"] = map[string]any{"entity_id": primary.ID, "entity_kind": primary.Kind, "name": primary.Name, "confidence": primary.Confidence}
		}
		if related := evidenceSubjectViews(subjects, primary, method, family); len(related) > 0 {
			item["related_entities"] = related
		}
		if targetType != "" {
			item["target_freshness"] = freshnessState(targetType, targetLastSeen, time.Now().UTC())
		}
		integrity := map[string]string{}
		if locatorIntegrity != "" {
			integrity["locator_reference"] = locatorIntegrity
		}
		if contentHash != nil && *contentHash != "" {
			integrity["content_hash"] = *contentHash
		}
		if len(integrity) > 0 {
			item["integrity"] = integrity
		}
		result = append(result, item)
	}
	return result
}

func evidenceTitle(method, family, subject string) string {
	if family == "skill" {
		if method == "skill_descriptor" {
			return "Validated skill descriptor — " + subject
		}
		return "Unvalidated skill signal — " + subject
	}
	if family == "agent_definition" && method == "agent_descriptor" {
		return "Validated agent definition — " + subject
	}
	if title := map[string]string{
		"application": "Application installation found", "package": "Package installation found",
		"extension_manifest": "IDE extension manifest matched", "executable": "Executable available",
		"process": "Process running at scan time", "listener": "Listening service observed",
		"config_shape": "Configuration structure matched", "config_file": "Configuration file present",
		"skill_descriptor": "Skill descriptor validated", "descriptor": "Authoritative descriptor found",
		"workload_uid": "Workload identity observed", "import": "Framework import found",
	}[method]; title != "" {
		return title
	}
	if family == "runtime_state" {
		return "Runtime state location found"
	}
	return humanizeDetector(family) + " evidence found"
}

func evidenceSummary(subject, subjectKind, contextName, target, method, family string) string {
	where := "the reporting target"
	if target != "" {
		where = target
	}
	switch method {
	case "skill_descriptor":
		context := "an agent runtime"
		if contextName != "" {
			context = contextName
		}
		return fmt.Sprintf("%s reported a valid SKILL.md descriptor for %s, linked to %s.", where, subject, context)
	case "agent_descriptor":
		return fmt.Sprintf("%s reported a valid agent definition named %s.", where, subject)
	case "process":
		return fmt.Sprintf("%s reported a recognized %s process running when Lens scanned it.", where, subject)
	case "executable":
		return fmt.Sprintf("%s reported a recognized %s executable available on the endpoint.", where, subject)
	case "application", "package", "extension_manifest":
		return fmt.Sprintf("%s reported installation evidence associated with %s.", where, subject)
	case "config_shape":
		return fmt.Sprintf("%s reported a known %s configuration structure with meaningful settings.", where, subject)
	case "config_file":
		return fmt.Sprintf("%s reported a known %s configuration file, but Lens could not confirm a meaningful configuration shape.", where, subject)
	case "listener":
		return fmt.Sprintf("%s reported a listener associated with %s at scan time.", where, subject)
	case "descriptor":
		return fmt.Sprintf("%s reported a validated descriptor associated with %s.", where, subject)
	default:
		if family == "skill" {
			return fmt.Sprintf("%s reported a path named %s under a configured skill root. Lens did not validate it as a SKILL.md descriptor.", where, subject)
		}
		if subjectKind != "" {
			return fmt.Sprintf("%s reported %s evidence for the %s %s.", where, humanizeDetector(family), humanizeDetector(subjectKind), subject)
		}
		return fmt.Sprintf("%s reported %s evidence associated with %s.", where, humanizeDetector(family), subject)
	}
}

func evidenceReason(method, family, specificity, subject string) string {
	match := map[string]string{
		"config_shape":       "The parsed document matched product-specific configuration fields.",
		"config_file":        "A known configuration location existed, but its structure was not authoritative.",
		"application":        "A product-specific application installation location existed.",
		"package":            "A recognized package or extension installation matched the detector.",
		"extension_manifest": "A parsed extension manifest matched a known publisher and extension identifier.",
		"executable":         "A recognized executable name was present in the endpoint executable inventory.",
		"process":            "A recognized process name was present in the live process inventory.",
		"listener":           "A known port was listening and the owning process matched when ownership data was available.",
		"skill_descriptor":   fmt.Sprintf("SKILL.md parsed successfully, declared the name %q, matched its directory name, and included the required purpose field. Bounded declarative metadata was retained; the instruction body was not.", subject),
		"agent_descriptor":   fmt.Sprintf("The agent definition parsed successfully and declared the name %q. Its instruction body was not retained.", subject),
		"descriptor":         "A product- or protocol-defined descriptor matched the detector.",
		"workload_uid":       "The Kubernetes API reported the workload identity through a read-only informer.",
	}[method]
	if match == "" {
		if family == "skill" {
			match = fmt.Sprintf("A path named %q existed under the detector's configured skill root. No valid SKILL.md descriptor was associated with this observation, so it remains path-only evidence.", subject)
		} else {
			match = "The detector matched a known " + humanizeDetector(family) + " signal."
		}
	}
	return match + " Evidence specificity: " + humanizeDetector(specificity) + "."
}

func evidenceInvestigationHint(subject, subjectKind, contextName, target, location, method, family string) string {
	where := target
	if where == "" {
		where = "the reporting target"
	}
	switch method {
	case "skill_descriptor":
		context := "the associated runtime"
		if contextName != "" {
			context = contextName
		}
		return fmt.Sprintf("Review the %s skill on %s, confirm who added it, and verify that its instructions and allowed capabilities are expected for %s.", subject, where, context)
	case "agent_descriptor":
		return fmt.Sprintf("Review the %s agent definition on %s, confirm its owner, and verify that its declared purpose and capabilities are expected.", subject, where)
	case "process":
		return fmt.Sprintf("Confirm whether %s is expected to be running on %s and identify the responsible team.", subject, where)
	case "executable", "application", "package", "extension_manifest":
		return fmt.Sprintf("Confirm whether %s is an expected installation on %s and who uses it.", subject, where)
	case "config_shape", "config_file":
		return fmt.Sprintf("Review the local %s configuration on %s, including declared MCP servers, models, and enabled state.", subject, where)
	case "listener":
		return fmt.Sprintf("Review the listener binding and owning process for %s on %s.", subject, where)
	case "descriptor":
		if location != "" && location != "Protected endpoint location" {
			return fmt.Sprintf("Open %s and confirm the declared %s resource and its owner.", location, humanizeDetector(family))
		}
		return fmt.Sprintf("Review the %s descriptor for %s on %s and confirm its owner.", humanizeDetector(family), subject, where)
	default:
		if family == "skill" {
			return fmt.Sprintf("Inspect the %s entry in the local skill root on %s. Confirm whether it contains a valid SKILL.md; otherwise treat it as a directory signal, not a discovered skill.", subject, where)
		}
		if subjectKind != "" {
			return fmt.Sprintf("Review the %s %s on %s and confirm its owner and expected state.", humanizeDetector(subjectKind), subject, where)
		}
		return fmt.Sprintf("Review this %s observation on %s and confirm whether %s is expected.", humanizeDetector(family), where, subject)
	}
}

func primaryEvidenceSubject(subjects []evidenceSubject, requestedEntityID, family string) *evidenceSubject {
	preferredKind := map[string]string{
		"skill": "skill", "agent_definition": "agent", "model_cache": "model",
		"network_listener": "model_server", "api_document": "api_service",
	}[family]
	if preferredKind != "" {
		for index := range subjects {
			if subjects[index].Kind == preferredKind {
				return &subjects[index]
			}
		}
	}
	for index := range subjects {
		if subjects[index].ID == requestedEntityID {
			return &subjects[index]
		}
	}
	for index := range subjects {
		if subjects[index].Kind != "endpoint" && subjects[index].Kind != "user" && subjects[index].Kind != "repository" && subjects[index].Kind != "cluster" {
			return &subjects[index]
		}
	}
	if len(subjects) > 0 {
		return &subjects[0]
	}
	return nil
}

func evidenceContextName(subjects []evidenceSubject, primary *evidenceSubject, requestedEntityID, requestedEntityName string) string {
	if primary == nil || primary.ID != requestedEntityID {
		if strings.TrimSpace(requestedEntityName) != "" {
			return requestedEntityName
		}
	}
	for _, subject := range subjects {
		if primary != nil && subject.ID == primary.ID {
			continue
		}
		if subject.Kind == "runtime" || subject.Kind == "agent" || subject.Kind == "framework" {
			return subject.Name
		}
	}
	return ""
}

func evidenceSubjectViews(subjects []evidenceSubject, primary *evidenceSubject, method, family string) []map[string]any {
	result := []map[string]any{}
	for _, subject := range subjects {
		if primary != nil && subject.ID == primary.ID {
			continue
		}
		if subject.Kind == "endpoint" || subject.Kind == "user" {
			continue
		}
		result = append(result, map[string]any{
			"entity_id": subject.ID, "entity_kind": subject.Kind, "name": subject.Name,
			"confidence": subject.Confidence, "matched_facts": evidenceMatchedFacts(subject.Attributes, method, family),
		})
		if len(result) == 8 {
			break
		}
	}
	return result
}

func evidenceResourceLocation(location, locatorKind string, subject *evidenceSubject, method string) string {
	if locatorKind != "protected_path" || subject == nil || subject.Kind != "skill" || method != "skill_descriptor" {
		return location
	}
	relative, _ := subject.Attributes["descriptor_relative"].(string)
	relative = strings.Trim(strings.TrimSpace(filepathToSlash(relative)), "/")
	if relative == "" || strings.Contains(relative, "..") {
		return location
	}
	provider, _ := subject.Attributes["provider_product_id"].(string)
	base := map[string]string{
		"codex": "~/.codex/skills", "claude": "~/.claude/skills", "gemini": "~/.gemini/skills", "kiro": "~/.kiro/skills",
	}[strings.ToLower(provider)]
	if base == "" {
		base = "Configured skill root"
	}
	return base + "/" + relative + "/SKILL.md"
}

func filepathToSlash(value string) string {
	return strings.ReplaceAll(value, `\`, "/")
}

func evidenceLocation(locator *string, sourceType string) (display, kind, integrity string) {
	if locator == nil || strings.TrimSpace(*locator) == "" {
		return "Location not retained", "unavailable", ""
	}
	value := strings.TrimSpace(*locator)
	lower := strings.ToLower(value)
	if strings.HasPrefix(lower, "sha256:") || strings.HasPrefix(lower, "path_hash:") {
		return "Protected endpoint location", "protected_path", value
	}
	if strings.HasPrefix(lower, "tcp-listener:") {
		return "TCP port " + strings.TrimPrefix(value, "tcp-listener:"), "network_listener", ""
	}
	if parsed, err := url.Parse(value); err == nil && parsed.Scheme != "" {
		return value, "endpoint", ""
	}
	if sourceType == "repository" || strings.Contains(value, "/") {
		return value, "repository_path", ""
	}
	return value, "resource_reference", ""
}

func evidenceMatchedFacts(attributes map[string]any, method, family string) []evidenceFact {
	keys := []string{"product_id", "product_category", "source_surface"}
	switch {
	case family == "installation":
		keys = append(keys, "installed", "installation_methods", "version", "package_version", "extension_ids")
	case family == "configuration" || family == "runtime_state":
		keys = append(keys, "configured", "configuration_scope", "enabled", "state_present", "descriptor_valid")
	case family == "process" || method == "process":
		keys = append(keys, "running_at_scan")
	case family == "network_listener" || method == "listener":
		keys = append(keys, "running_at_scan", "transport", "port", "binding", "listener_process_verified")
	case family == "skill" || family == "agent_definition":
		keys = append(keys, "defined", "configured", "descriptor_valid", "descriptor_format", "descriptor_relative", "skill_scope", "skill_root_id", "provider_product_id", "declared_purpose", "license", "compatibility", "allowed_tools", "descriptor_fields", "description_present", "license_declared", "compatibility_declared", "allowed_tools_declared", "definition_format")
	default:
		keys = append(keys, "running_at_scan", "deployed", "defined", "configured", "installed", "enabled")
	}
	seen := map[string]bool{}
	facts := []evidenceFact{}
	for _, key := range keys {
		if seen[key] {
			continue
		}
		seen[key] = true
		value, exists := attributes[key]
		if !exists {
			continue
		}
		formatted := safeFactValue(value)
		if formatted == "" {
			continue
		}
		facts = append(facts, evidenceFact{Label: humanizeDetector(key), Value: formatted})
	}
	return facts
}

func safeFactValue(value any) string {
	switch typed := value.(type) {
	case string:
		return boundedEvidenceText(typed, 160)
	case bool:
		if typed {
			return "Yes"
		}
		return "No"
	case float64:
		return fmt.Sprintf("%g", typed)
	case int:
		return fmt.Sprintf("%d", typed)
	case []any:
		items := []string{}
		for _, item := range typed {
			if text, ok := item.(string); ok && text != "" {
				items = append(items, boundedEvidenceText(text, 80))
			}
			if len(items) == 8 {
				break
			}
		}
		sort.Strings(items)
		return strings.Join(items, ", ")
	case []string:
		items := append([]string(nil), typed...)
		if len(items) > 8 {
			items = items[:8]
		}
		sort.Strings(items)
		return boundedEvidenceText(strings.Join(items, ", "), 320)
	}
	return ""
}

func humanizeDetector(value string) string {
	value = strings.TrimSpace(value)
	value = strings.NewReplacer("_", " ", "-", " ", ".", " ").Replace(value)
	words := strings.Fields(value)
	for index, word := range words {
		if len(word) > 0 {
			words[index] = strings.ToUpper(word[:1]) + word[1:]
		}
	}
	if len(words) == 0 {
		return "Discovery"
	}
	return strings.Join(words, " ")
}

func boundedEvidenceText(value string, limit int) string {
	value = strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	runes := []rune(value)
	if len(runes) > limit {
		value = string(runes[:limit])
	}
	return value
}
