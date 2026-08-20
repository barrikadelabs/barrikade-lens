package discovery

import (
	"fmt"
	"strings"
	"time"
)

const RuntimeIdentityObservationVersion = "0.1.0-experimental"

type ProcessAncestor struct {
	PID                  int    `json:"pid"`
	StartTime            string `json:"start_time"`
	ExecutablePathDigest string `json:"executable_path_digest"`
}

type MacOSSigningFacts struct {
	SignatureVerified             bool   `json:"signature_verified"`
	HardenedRuntime               bool   `json:"hardened_runtime"`
	PublisherTeamID               string `json:"publisher_team_id,omitempty"`
	SigningIdentifier             string `json:"signing_identifier,omitempty"`
	CDHash                        string `json:"cdhash,omitempty"`
	DesignatedRequirementDigest   string `json:"designated_requirement_digest,omitempty"`
	SigningMetadataEvidenceDigest string `json:"signing_metadata_evidence_digest,omitempty"`
}

type RuntimeIdentityFact struct {
	Fact       string `json:"fact"`
	Method     string `json:"method"`
	ObservedAt string `json:"observed_at"`
}

// RuntimeIdentityObservation is experimental discovery evidence. It is not a
// Registry principal, admission record, runtime attestation, or authorization.
type RuntimeIdentityObservation struct {
	SchemaVersion        string                `json:"schema_version"`
	ObservationID        string                `json:"observation_id"`
	TargetID             string                `json:"target_id"`
	ProductID            string                `json:"product_id"`
	Platform             string                `json:"platform"`
	ObservedAt           string                `json:"observed_at"`
	PID                  int                   `json:"pid"`
	StartTime            string                `json:"start_time"`
	ParentPID            int                   `json:"parent_pid"`
	Ancestry             []ProcessAncestor     `json:"ancestry"`
	EffectiveUID         int                   `json:"effective_uid"`
	Version              string                `json:"version"`
	ExecutablePathDigest string                `json:"executable_path_digest"`
	ExecutableSHA256     string                `json:"executable_sha256"`
	MacOSSigning         MacOSSigningFacts     `json:"macos_signing"`
	Evidence             []RuntimeIdentityFact `json:"evidence"`
}

func (o RuntimeIdentityObservation) Validate() error {
	if o.SchemaVersion != RuntimeIdentityObservationVersion {
		return fmt.Errorf("runtime observation schema_version must be %q", RuntimeIdentityObservationVersion)
	}
	if o.ObservationID == "" || o.TargetID == "" || o.ProductID == "" || o.Platform != "darwin" {
		return fmt.Errorf("runtime observation identity and darwin platform are required")
	}
	if o.PID <= 0 || o.ParentPID < 0 || o.EffectiveUID < 0 {
		return fmt.Errorf("runtime observation process identifiers are invalid")
	}
	if strings.TrimSpace(o.Version) == "" || len(o.Version) > 128 {
		return fmt.Errorf("runtime observation version is required and bounded")
	}
	for name, value := range map[string]string{"observed_at": o.ObservedAt, "start_time": o.StartTime} {
		if _, err := time.Parse(time.RFC3339Nano, value); err != nil {
			return fmt.Errorf("runtime observation %s: %w", name, err)
		}
	}
	for name, value := range map[string]string{"executable_path_digest": o.ExecutablePathDigest, "executable_sha256": o.ExecutableSHA256} {
		if !validSHA256(value) {
			return fmt.Errorf("runtime observation %s is invalid", name)
		}
	}
	for name, value := range map[string]string{
		"signing_metadata_evidence_digest": o.MacOSSigning.SigningMetadataEvidenceDigest,
		"designated_requirement_digest":    o.MacOSSigning.DesignatedRequirementDigest,
	} {
		if value == "" && name == "designated_requirement_digest" {
			continue
		}
		if !validSHA256(value) {
			return fmt.Errorf("runtime observation %s is invalid", name)
		}
	}
	if len(o.Ancestry) == 0 || len(o.Ancestry) > 64 {
		return fmt.Errorf("runtime observation ancestry must be bounded and non-empty")
	}
	for _, ancestor := range o.Ancestry {
		if ancestor.PID <= 0 || !validSHA256(ancestor.ExecutablePathDigest) {
			return fmt.Errorf("runtime observation ancestry is invalid")
		}
		if _, err := time.Parse(time.RFC3339Nano, ancestor.StartTime); err != nil {
			return fmt.Errorf("runtime observation ancestor start_time: %w", err)
		}
	}
	facts := map[string]bool{}
	for _, evidence := range o.Evidence {
		if evidence.Fact == "" || evidence.Method == "" {
			return fmt.Errorf("runtime observation evidence must name a fact and method")
		}
		if _, err := time.Parse(time.RFC3339Nano, evidence.ObservedAt); err != nil {
			return fmt.Errorf("runtime observation evidence timestamp: %w", err)
		}
		facts[evidence.Fact] = true
	}
	for _, required := range []string{"process", "ancestry", "executable", "macos_signing"} {
		if !facts[required] {
			return fmt.Errorf("runtime observation lacks %s evidence", required)
		}
	}
	return nil
}

func validSHA256(value string) bool {
	if !strings.HasPrefix(value, "sha256:") || len(value) != len("sha256:")+64 {
		return false
	}
	for _, character := range strings.TrimPrefix(value, "sha256:") {
		if !strings.ContainsRune("0123456789abcdef", character) {
			return false
		}
	}
	return true
}
