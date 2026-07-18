package discovery

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// StableID produces an opaque, organization-scoped identifier. Canonical keys
// remain useful to a collector while paths and other local identifiers do not
// become globally correlatable hashes.
func StableID(orgID string, kind EntityKind, canonicalKey string) string {
	return stable("entity", orgID, string(kind), canonicalKey)
}

func EvidenceID(sourceID, detectorID, method, locator, contentHash string) string {
	return stable("evidence", sourceID, detectorID, method, locator, contentHash)
}

func RelationshipID(orgID string, kind RelationshipKind, from, to string) string {
	return stable("relationship", orgID, string(kind), from, to)
}

func HashLocator(orgID, value string) string {
	return "sha256:" + hash(strings.Join([]string{orgID, value}, "\x00"))
}

func ContentHash(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func stable(parts ...string) string {
	return "urn:lens:" + hash(strings.Join(parts, "\x00"))
}

func hash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
