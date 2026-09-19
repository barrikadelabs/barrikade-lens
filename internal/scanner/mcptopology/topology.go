// Package mcptopology turns a sanitized MCP declaration into stable graph
// entities. It deliberately consumes only metadata already reduced by
// mcpconfig; commands, arguments, headers, values, descriptions, schemas, and
// instruction text never enter this layer.
package mcptopology

import (
	"strings"

	"github.com/barrikadelabs/barrikade-lens/internal/scanner/builder"
	"github.com/barrikadelabs/barrikade-lens/internal/scanner/mcpconfig"
	"github.com/barrikadelabs/barrikade-lens/pkg/discovery"
)

type Context struct {
	LocalCanonicalPrefix string
	SourceSurface        discovery.SourceType
	Descriptor           string
	Source               string
}

type Result struct {
	ServerID      string
	ToolIDs       []string
	DestinationID string
}

func Add(b *builder.Builder, context Context, ownerID string, server mcpconfig.Server, refs ...string) Result {
	attributes := map[string]any{
		"configured": true, "transport": server.Transport,
		"source_surface": string(context.SourceSurface), "capability_state": "declared",
	}
	if context.Descriptor != "" {
		attributes["descriptor"] = context.Descriptor
	}
	if context.Source != "" {
		attributes["source"] = context.Source
	}
	canonical := context.LocalCanonicalPrefix + ":mcp:" + strings.ToLower(server.Name)
	sanitized := ""
	if server.URL != "" {
		if value, err := discovery.SanitizeURL(server.URL); err == nil {
			sanitized = value
			attributes["endpoint"] = sanitized
			attributes["host"] = discovery.URLHost(sanitized)
			canonical = "mcp-endpoint:" + sanitized
		}
	}
	if server.Enabled != nil {
		attributes["enabled"] = *server.Enabled
	}
	if len(server.EnvironmentKeys) > 0 {
		attributes["environment_keys"] = server.EnvironmentKeys
	}
	if server.CredentialPresent {
		attributes["credential_present"] = true
	}
	serverID := b.AddEntity(discovery.KindMCPServer, canonical, server.Name, attributes, refs...)
	if ownerID != "" {
		b.AddRelationship(discovery.RelationshipConnectsTo, ownerID, serverID, map[string]any{"capability_state": "declared"}, refs...)
	}
	result := Result{ServerID: serverID}
	for _, tool := range server.Tools {
		toolAttributes := map[string]any{
			"declared": true, "capability_state": "declared", "source_surface": string(context.SourceSurface),
		}
		if tool.Enabled != nil {
			toolAttributes["enabled"] = *tool.Enabled
		}
		toolID := b.AddEntity(discovery.KindTool, canonical+":tool:"+strings.ToLower(tool.Name), tool.Name, toolAttributes, refs...)
		b.AddRelationship(discovery.RelationshipProvides, serverID, toolID, map[string]any{"capability_state": "declared"}, refs...)
		result.ToolIDs = append(result.ToolIDs, toolID)
	}
	if host := discovery.URLHost(sanitized); host != "" && !discovery.IsCloudMetadataHost(host) {
		destinationAttributes := map[string]any{
			"host": host, "endpoint": sanitized, "destination_kind": "service",
			"source_surface": string(context.SourceSurface), "sanitized": true,
		}
		result.DestinationID = b.AddEntity(discovery.KindAPIService, "api-host:"+host, host, destinationAttributes, refs...)
		b.AddRelationship(discovery.RelationshipConnectsTo, serverID, result.DestinationID, map[string]any{"direct_evidence": true, "capability_state": "declared"}, refs...)
	}
	return result
}
