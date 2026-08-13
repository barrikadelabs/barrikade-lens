package endpoint

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/barrikadelabs/barrikade-lens/internal/detector"
	"github.com/barrikadelabs/barrikade-lens/internal/scanner/builder"
	"github.com/barrikadelabs/barrikade-lens/internal/scanner/mcpconfig"
	"github.com/barrikadelabs/barrikade-lens/internal/scanner/skillconfig"
	"github.com/barrikadelabs/barrikade-lens/pkg/discovery"
	"github.com/pelletier/go-toml/v2"
	"gopkg.in/yaml.v3"
)

const (
	maxConfigSize        = 4 << 20
	maxModelCacheEntries = 5000
)

type Options struct {
	OrganizationID          string
	SourceID                string
	TargetID                string
	HomeDir                 string
	Hostname                string
	Username                string
	Platform                string
	Pack                    detector.Pack
	ProcessNames            map[string]bool
	ExecutableNames         map[string]bool
	ListeningPorts          map[int]bool
	ListeningBindings       map[int]string
	ListeningProcesses      map[int]map[string]bool
	DisableSystemInspection bool
}

func Scan(ctx context.Context, options Options) (discovery.Snapshot, error) {
	if options.OrganizationID == "" {
		options.OrganizationID = "local"
	}
	if options.HomeDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return discovery.Snapshot{}, fmt.Errorf("resolve home directory: %w", err)
		}
		options.HomeDir = home
	}
	if options.Hostname == "" {
		options.Hostname, _ = os.Hostname()
	}
	if options.Username == "" {
		if current, err := user.Current(); err == nil {
			options.Username = current.Username
		}
	}
	if options.Platform == "" {
		options.Platform = runtime.GOOS
	}
	if options.Pack.ID == "" {
		pack, err := detector.Builtin()
		if err != nil {
			return discovery.Snapshot{}, err
		}
		options.Pack = pack
	}
	if options.TargetID == "" {
		options.TargetID = discovery.StableID(options.OrganizationID, discovery.KindEndpoint, strings.ToLower(options.Hostname))
	}
	if options.SourceID == "" {
		options.SourceID = options.TargetID
	}
	if !options.DisableSystemInspection {
		if options.ProcessNames == nil {
			options.ProcessNames = processNames(ctx, options.Platform)
		}
		if options.ExecutableNames == nil {
			options.ExecutableNames = executableNames(options.Pack)
		}
		if options.ListeningBindings == nil {
			options.ListeningBindings = listeningBindings(ctx, options.Platform)
		}
		if options.ListeningProcesses == nil {
			options.ListeningProcesses = listeningProcessOwners(ctx, options.Platform)
		}
		if options.ListeningPorts == nil {
			options.ListeningPorts = map[int]bool{}
			for port := range options.ListeningBindings {
				options.ListeningPorts[port] = true
			}
		}
	}

	snapshot := discovery.NewTargetSnapshot(options.OrganizationID, options.SourceID, options.TargetID, discovery.SourceEndpoint, discovery.Collector{
		ID: "barrikade-lens", Name: "Barrikade Lens", Version: Version, Mode: "standalone",
	})
	snapshot.Scope = discovery.Scope{Name: options.Hostname, Attributes: map[string]string{"platform": options.Platform}}
	b := builder.New(snapshot)

	systemEvidence := b.AddEvidence(builder.Observation{
		DetectorID: "lens.endpoint", DetectorVersion: Version, Method: "system", Family: "identity", Specificity: "high",
		Locator:       discovery.HashLocator(options.OrganizationID, options.TargetID),
		Authoritative: true,
	})
	endpointID := b.AddEntity(discovery.KindEndpoint, options.TargetID, options.Hostname, map[string]any{
		"hostname": options.Hostname, "os": options.Platform, "architecture": runtime.GOARCH, "source_surface": "endpoint",
	}, systemEvidence)
	userID := ""
	if options.Username != "" {
		userID = b.AddEntity(discovery.KindUser, "target:"+options.TargetID+":user:"+strings.ToLower(options.Username), options.Username, map[string]any{
			"os_user": true,
		}, systemEvidence)
		b.AddRelationship(discovery.RelationshipOwnedBy, endpointID, userID, map[string]any{"attribution": "observed_user", "authoritative": false}, systemEvidence)
	}

	for _, signature := range options.Pack.Runtimes {
		select {
		case <-ctx.Done():
			return discovery.Snapshot{}, ctx.Err()
		default:
		}
		b.Snapshot.Coverage.DetectorsRun++
		scanRuntime(b, options, signature, endpointID, userID)
	}
	if len(options.Pack.ExtensionRoots) > 0 {
		b.Snapshot.Coverage.DetectorsRun++
		scanIDEExtensions(b, options, endpointID, userID)
	}
	for _, root := range options.Pack.SkillRoots {
		b.Snapshot.Coverage.DetectorsRun++
		scanPortableSkills(b, options, root, endpointID, userID)
	}
	for _, cache := range options.Pack.ModelCaches {
		b.Snapshot.Coverage.DetectorsRun++
		scanModelCache(ctx, b, options, cache, endpointID)
	}
	for _, listener := range options.Pack.Listeners {
		b.Snapshot.Coverage.DetectorsRun++
		scanKnownListener(b, options, listener, endpointID)
	}
	return b.Finish()
}

type extensionManifest struct {
	Name        string                     `json:"name"`
	Publisher   string                     `json:"publisher"`
	DisplayName string                     `json:"displayName"`
	Contributes map[string]json.RawMessage `json:"contributes"`
}

func scanIDEExtensions(b *builder.Builder, options Options, endpointID, userID string) {
	known := map[string]detector.RuntimeSignature{}
	for _, signature := range options.Pack.Runtimes {
		for _, extensionID := range signature.ExtensionIDs {
			if normalized := normalizeExtensionID(extensionID); normalized != "" {
				known[normalized] = signature
			}
		}
	}
	seenRoots := map[string]struct{}{}
	seenManifests := 0
	incomplete := false
	for _, configuredRoot := range options.Pack.ExtensionRoots {
		root := expandPath(configuredRoot, options.HomeDir)
		if root == "" {
			continue
		}
		root = filepath.Clean(root)
		if _, exists := seenRoots[root]; exists {
			continue
		}
		seenRoots[root] = struct{}{}
		b.Snapshot.Coverage.LocationsChecked++
		entries, err := os.ReadDir(root)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			if errors.Is(err, os.ErrPermission) {
				b.Snapshot.Coverage.LocationsDenied++
			}
			incomplete = true
			continue
		}
		for _, entry := range entries {
			if seenManifests >= 5000 {
				incomplete = true
				break
			}
			info, statErr := entry.Info()
			if statErr != nil || !info.IsDir() {
				continue
			}
			manifestPath := filepath.Join(root, entry.Name(), "package.json")
			data, readErr := readLimited(manifestPath, maxConfigSize)
			if errors.Is(readErr, os.ErrNotExist) {
				continue
			}
			if readErr != nil {
				if errors.Is(readErr, os.ErrPermission) {
					b.Snapshot.Coverage.LocationsDenied++
				}
				incomplete = true
				continue
			}
			seenManifests++
			b.Snapshot.Coverage.LocationsChecked++
			var manifest extensionManifest
			if json.Unmarshal(data, &manifest) != nil {
				incomplete = true
				continue
			}
			extensionID := normalizeExtensionID(manifest.Publisher + "." + manifest.Name)
			if extensionID == "" {
				continue
			}
			capabilities := extensionCapabilities(manifest.Contributes)
			signature, isKnown := known[extensionID]
			if !isKnown && len(capabilities) == 0 {
				continue
			}
			name := strings.TrimSpace(manifest.DisplayName)
			if !validDescriptorName(name) || strings.HasPrefix(name, "%") && strings.HasSuffix(name, "%") {
				name = extensionID
			}
			attributes := map[string]any{
				"installed": true, "installation_methods": []string{"ide_extension_manifest"},
				"source_surface": "endpoint", "discovery_surface": "ide_extension",
			}
			if isKnown {
				attributes["extension_ids"] = []string{extensionID}
				attributes["product_id"] = signature.ID
				attributes["product_category"] = signature.Category
			} else {
				attributes["extension_id"] = extensionID
				attributes["tool_category"] = "ide_extension"
			}
			if len(capabilities) > 0 {
				attributes["agent_capabilities"] = capabilities
			}
			ref := b.AddEvidence(builder.Observation{
				DetectorID: "ide-extension." + extensionID, DetectorVersion: options.Pack.Version,
				Method: "extension_manifest", Family: "installation", Specificity: "high",
				Locator:     discovery.SafeLocator(options.OrganizationID, "", manifestPath),
				ContentHash: discovery.ContentHash(data), Authoritative: true,
			})
			kind := discovery.KindTool
			canonical := "target:" + options.TargetID + ":ide-extension:" + extensionID
			if isKnown {
				kind = discovery.KindRuntime
				canonical = "target:" + options.TargetID + ":runtime:" + signature.ID
				name = signature.Name
			}
			entityID := b.AddEntity(kind, canonical, name, attributes, ref)
			b.AddRelationship(discovery.RelationshipRunsOn, entityID, endpointID, nil, ref)
			if userID != "" {
				b.AddRelationship(discovery.RelationshipOwnedBy, entityID, userID, map[string]any{"scope": "user", "attribution": "observed_user", "authoritative": false}, ref)
			}
		}
	}
	if incomplete {
		b.Error("ide-extensions", "extension_inventory_partial", "One or more IDE extension manifests could not be inspected", false)
	}
}

func normalizeExtensionID(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" || len(value) > 200 || strings.HasPrefix(value, ".") || strings.HasSuffix(value, ".") {
		return ""
	}
	for _, character := range value {
		if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '.' && character != '-' && character != '_' {
			return ""
		}
	}
	return value
}

func extensionCapabilities(contributes map[string]json.RawMessage) []string {
	present := map[string]bool{}
	for key, value := range contributes {
		trimmed := strings.TrimSpace(string(value))
		if trimmed == "" || trimmed == "null" || trimmed == "[]" || trimmed == "{}" {
			continue
		}
		normalized := strings.ToLower(strings.NewReplacer("_", "", "-", "").Replace(key))
		switch normalized {
		case "chatparticipants":
			present["chat_participant"] = true
		case "languagemodeltools":
			present["language_model_tool"] = true
		case "mcpserverdefinitionproviders":
			present["mcp_server_provider"] = true
		}
	}
	result := []string{}
	for _, capability := range []string{"chat_participant", "language_model_tool", "mcp_server_provider"} {
		if present[capability] {
			result = append(result, capability)
		}
	}
	return result
}

func scanPortableSkills(b *builder.Builder, options Options, signature detector.SkillRoot, endpointID, userID string) {
	for _, configuredRoot := range signature.Paths {
		root := expandPath(configuredRoot, options.HomeDir)
		if root == "" {
			continue
		}
		b.Snapshot.Coverage.LocationsChecked++
		descriptors, denied, truncated, err := discoverSkillDescriptors(root)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			b.Snapshot.Coverage.Partial = true
			continue
		}
		b.Snapshot.Coverage.LocationsDenied += denied
		b.Snapshot.Coverage.Partial = b.Snapshot.Coverage.Partial || denied > 0 || truncated
		for _, descriptor := range descriptors {
			metadata := skillconfig.Parse(descriptor.Data, descriptor.Directory)
			if !metadata.Valid {
				continue
			}
			ref := b.AddEvidence(builder.Observation{
				DetectorID: signature.ID, DetectorVersion: options.Pack.Version, Method: "skill_descriptor",
				Family: "skill", Specificity: "high", Locator: discovery.SafeLocator(options.OrganizationID, "", descriptor.Path),
				ContentHash: discovery.ContentHash(descriptor.Data), Authoritative: true,
			})
			attributes := map[string]any{
				"state_present": true, "descriptor_valid": true, "skill_scope": signature.Scope,
				"skill_root_id": signature.ID, "descriptor_relative": descriptor.Relative, "source_surface": "endpoint",
				"configured": true, "descriptor_format": "agent_skills", "description_present": metadata.DescriptionPresent,
				"license_declared": metadata.LicenseDeclared, "compatibility_declared": metadata.CompatibilityDeclared,
				"allowed_tools_declared": metadata.AllowedToolsDeclared,
			}
			addSkillMetadata(attributes, metadata)
			skillID := b.AddEntity(discovery.KindSkill, "target:"+options.TargetID+":portable-skill:"+signature.ID+":"+strings.ToLower(descriptor.Relative), metadata.Name, attributes, ref)
			b.AddRelationship(discovery.RelationshipRunsOn, skillID, endpointID, nil, ref)
			if userID != "" && signature.Scope == "user" {
				b.AddRelationship(discovery.RelationshipOwnedBy, skillID, userID, map[string]any{"scope": "user", "attribution": "observed_user", "authoritative": false}, ref)
			}
		}
	}
}

func scanRuntime(b *builder.Builder, options Options, signature detector.RuntimeSignature, endpointID, userID string) {
	canonical := "target:" + options.TargetID + ":runtime:" + signature.ID
	runtimeID := ""
	addRuntime := func(attributes map[string]any, ref string) string {
		if attributes == nil {
			attributes = map[string]any{}
		}
		attributes["product_id"] = signature.ID
		attributes["product_category"] = signature.Category
		attributes["source_surface"] = "endpoint"
		runtimeID = b.AddEntity(discovery.KindRuntime, canonical, signature.Name, attributes, ref)
		b.AddRelationship(discovery.RelationshipRunsOn, runtimeID, endpointID, nil, ref)
		if userID != "" {
			b.AddRelationship(discovery.RelationshipOwnedBy, runtimeID, userID, map[string]any{"scope": "user", "attribution": "observed_user", "authoritative": false}, ref)
		}
		return runtimeID
	}

	for _, pattern := range signature.InstallPaths {
		path := expandPath(pattern, options.HomeDir)
		if path == "" {
			continue
		}
		b.Snapshot.Coverage.LocationsChecked++
		matches, err := installedPathMatches(path)
		if err != nil {
			if errors.Is(err, os.ErrPermission) {
				b.Snapshot.Coverage.LocationsDenied++
				b.Snapshot.Coverage.Partial = true
			} else {
				b.Error(signature.ID, "install_pattern_invalid", "A detector installation path pattern was invalid", false)
			}
			continue
		}
		for _, match := range matches {
			method := "application"
			installationMethod := "application_path"
			if strings.Contains(strings.ToLower(filepath.ToSlash(match)), "/extensions/") {
				method = "package"
				installationMethod = "ide_extension"
			}
			ref := b.AddEvidence(builder.Observation{
				DetectorID: signature.ID, DetectorVersion: options.Pack.Version, Method: method,
				Family: "installation", Specificity: "high", Locator: discovery.SafeLocator(options.OrganizationID, "", match),
			})
			addRuntime(map[string]any{"installed": true, "installation_methods": []string{installationMethod}}, ref)
		}
	}

	for _, pattern := range signature.Paths {
		path := expandPath(pattern, options.HomeDir)
		if path == "" {
			continue
		}
		b.Snapshot.Coverage.LocationsChecked++
		if _, err := os.Stat(path); err == nil {
			ref := b.AddEvidence(builder.Observation{
				DetectorID: signature.ID, DetectorVersion: options.Pack.Version, Method: "path",
				Family: "runtime_state", Specificity: "low", Locator: discovery.SafeLocator(options.OrganizationID, "", path),
			})
			addRuntime(map[string]any{"state_present": true}, ref)
		} else if errors.Is(err, os.ErrPermission) {
			b.Snapshot.Coverage.LocationsDenied++
			b.Snapshot.Coverage.Partial = true
		}
	}

	for _, config := range signature.Configs {
		path := expandPath(config.Path, options.HomeDir)
		if path == "" {
			continue
		}
		b.Snapshot.Coverage.LocationsChecked++
		data, err := readLimited(path, maxConfigSize)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			if errors.Is(err, os.ErrPermission) {
				b.Snapshot.Coverage.LocationsDenied++
			}
			b.Error(signature.ID, "config_unreadable", "A known configuration path could not be inspected", false)
			continue
		}
		document, err := parseConfig(config.Format, data)
		if err != nil {
			ref := b.AddEvidence(builder.Observation{
				DetectorID: signature.ID, DetectorVersion: options.Pack.Version, Method: "config_file",
				Family: "runtime_state", Specificity: "low", Locator: discovery.SafeLocator(options.OrganizationID, "", path),
				ContentHash: discovery.ContentHash(data),
			})
			addRuntime(map[string]any{"state_present": true}, ref)
			b.Error(signature.ID, "config_malformed", "A known configuration file was present but malformed", false)
			continue
		}
		if !hasConfigurationContent(document) {
			ref := b.AddEvidence(builder.Observation{
				DetectorID: signature.ID, DetectorVersion: options.Pack.Version, Method: "config_file",
				Family: "runtime_state", Specificity: "low", Locator: discovery.SafeLocator(options.OrganizationID, "", path),
				ContentHash: discovery.ContentHash(data),
			})
			addRuntime(map[string]any{"state_present": true}, ref)
			continue
		}
		ref := b.AddEvidence(builder.Observation{
			DetectorID: signature.ID, DetectorVersion: options.Pack.Version, Method: "config_shape",
			Family: "configuration", Specificity: "high", Locator: discovery.SafeLocator(options.OrganizationID, "", path),
			ContentHash: discovery.ContentHash(data), Authoritative: true,
		})
		currentRuntimeID := addRuntime(map[string]any{"configured": true, "configuration_scope": config.Scope}, ref)
		addMCPServers(b, options, signature, currentRuntimeID, document, ref)
		addModels(b, options, currentRuntimeID, document, ref)
	}

	for _, root := range signature.SkillRoots {
		path := expandPath(root, options.HomeDir)
		if path != "" {
			scanSkills(b, options, signature, addRuntime, path)
		}
	}
	for _, root := range signature.AgentRoots {
		path := expandPath(root, options.HomeDir)
		if path != "" {
			scanAgentDefinitions(b, options, signature, addRuntime, endpointID, userID, path)
		}
	}

	for _, process := range signature.Processes {
		if options.ExecutableNames[strings.ToLower(process)] {
			ref := b.AddEvidence(builder.Observation{
				DetectorID: signature.ID, DetectorVersion: options.Pack.Version, Method: "executable",
				Family: "installation", Specificity: "high", Locator: discovery.HashLocator(options.OrganizationID, strings.ToLower(process)),
			})
			addRuntime(map[string]any{"installed": true, "installation_methods": []string{"executable_path"}}, ref)
			break
		}
	}
	processMatched := false
	for _, process := range signature.Processes {
		if options.ProcessNames[strings.ToLower(process)] {
			ref := b.AddEvidence(builder.Observation{
				DetectorID: signature.ID, DetectorVersion: options.Pack.Version, Method: "process",
				Family: "process", Specificity: "high", Locator: discovery.HashLocator(options.OrganizationID, strings.ToLower(process)),
			})
			addRuntime(map[string]any{"running_at_scan": true}, ref)
			processMatched = true
			break
		}
	}
	for _, modelServer := range signature.ModelServers {
		listenerMatched, ownershipVerified := matchingListenerProcess(options, modelServer.Port, signature.Processes)
		if !options.ListeningPorts[modelServer.Port] || !listenerMatched || !processMatched && !ownershipVerified {
			continue
		}
		ref := b.AddEvidence(builder.Observation{
			DetectorID: signature.ID, DetectorVersion: options.Pack.Version, Method: "listener",
			Family: "network_listener", Specificity: "high", Locator: fmt.Sprintf("tcp://127.0.0.1:%d", modelServer.Port),
		})
		currentRuntimeID := addRuntime(map[string]any{"running_at_scan": true}, ref)
		serverID := b.AddEntity(discovery.KindModelServer, fmt.Sprintf("target:%s:model-server:%d", options.TargetID, modelServer.Port), modelServer.Name, map[string]any{
			"running_at_scan": true, "transport": "http", "port": modelServer.Port, "source_surface": "endpoint",
		}, ref)
		if ownershipVerified {
			b.AddEntity(discovery.KindModelServer, fmt.Sprintf("target:%s:model-server:%d", options.TargetID, modelServer.Port), modelServer.Name, map[string]any{"listener_process_verified": true}, ref)
		}
		if binding := options.ListeningBindings[modelServer.Port]; binding != "" {
			b.AddEntity(discovery.KindModelServer, fmt.Sprintf("target:%s:model-server:%d", options.TargetID, modelServer.Port), modelServer.Name, map[string]any{"binding": binding}, ref)
		}
		b.AddRelationship(discovery.RelationshipProvides, currentRuntimeID, serverID, nil, ref)
		b.AddRelationship(discovery.RelationshipRunsOn, serverID, endpointID, nil, ref)
	}
}

func scanKnownListener(b *builder.Builder, options Options, listener detector.Listener, endpointID string) {
	processMatched, ownershipVerified := matchingListenerProcess(options, listener.Port, listener.Processes)
	if !options.ListeningPorts[listener.Port] || !processMatched {
		return
	}
	ref := b.AddEvidence(builder.Observation{
		DetectorID: listener.ID, DetectorVersion: options.Pack.Version, Method: "listener",
		Family: "network_listener", Specificity: "high", Locator: fmt.Sprintf("tcp-listener:%d", listener.Port),
	})
	attributes := map[string]any{"running_at_scan": true, "transport": "tcp", "port": listener.Port, "source_surface": "endpoint"}
	if ownershipVerified {
		attributes["listener_process_verified"] = true
	}
	if binding := options.ListeningBindings[listener.Port]; binding != "" {
		attributes["binding"] = binding
	}
	kind := discovery.EntityKind(listener.Kind)
	entityID := b.AddEntity(kind, fmt.Sprintf("target:%s:%s:%d", options.TargetID, listener.Kind, listener.Port), listener.Name, attributes, ref)
	b.AddRelationship(discovery.RelationshipRunsOn, entityID, endpointID, nil, ref)
}

func scanSkills(b *builder.Builder, options Options, signature detector.RuntimeSignature, addRuntime func(map[string]any, string) string, root string) {
	b.Snapshot.Coverage.LocationsChecked++
	descriptors, denied, truncated, err := discoverSkillDescriptors(root)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err != nil {
		b.Snapshot.Coverage.Partial = true
		return
	}
	b.Snapshot.Coverage.LocationsDenied += denied
	b.Snapshot.Coverage.Partial = b.Snapshot.Coverage.Partial || denied > 0 || truncated
	for _, descriptor := range descriptors {
		metadata := skillconfig.Parse(descriptor.Data, descriptor.Directory)
		if !metadata.Valid {
			continue
		}
		ref := b.AddEvidence(builder.Observation{
			DetectorID: signature.ID + ".skills", DetectorVersion: options.Pack.Version, Method: "skill_descriptor",
			Family: "skill", Specificity: "high", Locator: discovery.SafeLocator(options.OrganizationID, "", descriptor.Path),
			ContentHash: discovery.ContentHash(descriptor.Data), Authoritative: true,
		})
		runtimeAttributes := map[string]any{"skill_state_present": true, "skills_configured": true}
		skillAttributes := map[string]any{
			"state_present": true, "descriptor_valid": true, "source_surface": "endpoint", "configured": true,
			"descriptor_format": "agent_skills", "skill_scope": "user", "skill_root_id": signature.ID + ".skills",
			"provider_product_id": signature.ID, "descriptor_relative": descriptor.Relative, "description_present": metadata.DescriptionPresent,
			"license_declared": metadata.LicenseDeclared, "compatibility_declared": metadata.CompatibilityDeclared,
			"allowed_tools_declared": metadata.AllowedToolsDeclared,
		}
		addSkillMetadata(skillAttributes, metadata)
		runtimeID := addRuntime(runtimeAttributes, ref)
		skillID := b.AddEntity(discovery.KindSkill, "target:"+options.TargetID+":skill:"+strings.ToLower(options.Username)+":"+signature.ID+":"+strings.ToLower(descriptor.Relative), metadata.Name, skillAttributes, ref)
		b.AddRelationship(discovery.RelationshipProvides, runtimeID, skillID, nil, ref)
	}
}

func addSkillMetadata(attributes map[string]any, metadata skillconfig.Metadata) {
	if metadata.DeclaredPurpose != "" {
		attributes["declared_purpose"] = metadata.DeclaredPurpose
	}
	if metadata.License != "" {
		attributes["license"] = metadata.License
	}
	if metadata.Compatibility != "" {
		attributes["compatibility"] = metadata.Compatibility
	}
	if len(metadata.AllowedTools) > 0 {
		attributes["allowed_tools"] = metadata.AllowedTools
	}
	if len(metadata.DescriptorFields) > 0 {
		attributes["descriptor_fields"] = metadata.DescriptorFields
	}
}

type skillDescriptor struct {
	Path      string
	Relative  string
	Directory string
	Data      []byte
}

func discoverSkillDescriptors(root string) (items []skillDescriptor, denied int, truncated bool, err error) {
	const maxSkillsPerRoot = 1000
	appendDescriptor := func(path, relative, directory string) error {
		data, readErr := readLimited(path, maxConfigSize)
		if readErr != nil {
			if errors.Is(readErr, os.ErrPermission) {
				denied++
			}
			return nil
		}
		items = append(items, skillDescriptor{Path: path, Relative: filepath.ToSlash(relative), Directory: directory, Data: data})
		if len(items) >= maxSkillsPerRoot {
			truncated = true
			return filepath.SkipAll
		}
		return nil
	}
	err = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			if errors.Is(walkErr, os.ErrPermission) {
				denied++
				if entry != nil && entry.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			return walkErr
		}
		relative, relativeErr := filepath.Rel(root, path)
		if relativeErr != nil {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			info, statErr := os.Stat(path)
			if statErr != nil {
				if errors.Is(statErr, os.ErrPermission) {
					denied++
				}
				return nil
			}
			parts := strings.Split(filepath.ToSlash(relative), "/")
			if info.IsDir() && len(parts) >= 1 && len(parts) <= 2 {
				descriptorPath := filepath.Join(path, "SKILL.md")
				if _, descriptorErr := os.Stat(descriptorPath); descriptorErr == nil {
					return appendDescriptor(descriptorPath, relative, filepath.Base(path))
				} else if errors.Is(descriptorErr, os.ErrPermission) {
					denied++
				}
			} else if !info.IsDir() && strings.EqualFold(entry.Name(), "SKILL.md") && len(parts) >= 2 && len(parts) <= 3 {
				return appendDescriptor(path, filepath.Dir(relative), filepath.Base(filepath.Dir(path)))
			}
			return nil
		}
		if entry.IsDir() {
			if relative != "." && len(strings.Split(filepath.ToSlash(relative), "/")) > 2 {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.EqualFold(entry.Name(), "SKILL.md") {
			return nil
		}
		parts := strings.Split(filepath.ToSlash(relative), "/")
		if len(parts) < 2 || len(parts) > 3 {
			return nil
		}
		return appendDescriptor(path, filepath.Dir(relative), filepath.Base(filepath.Dir(path)))
	})
	return items, denied, truncated, err
}

func scanAgentDefinitions(
	b *builder.Builder,
	options Options,
	signature detector.RuntimeSignature,
	addRuntime func(map[string]any, string) string,
	endpointID, userID, root string,
) {
	b.Snapshot.Coverage.LocationsChecked++
	descriptors, denied, truncated, err := discoverAgentDescriptors(root)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err != nil {
		b.Snapshot.Coverage.Partial = true
		return
	}
	b.Snapshot.Coverage.LocationsDenied += denied
	b.Snapshot.Coverage.Partial = b.Snapshot.Coverage.Partial || denied > 0 || truncated
	for _, descriptor := range descriptors {
		name, valid := parseAgentDescriptor(descriptor.Data, descriptor.Path)
		if !valid {
			continue
		}
		ref := b.AddEvidence(builder.Observation{
			DetectorID: signature.ID + ".agents", DetectorVersion: options.Pack.Version, Method: "agent_descriptor",
			Family: "agent_definition", Specificity: "high", Locator: discovery.SafeLocator(options.OrganizationID, "", descriptor.Path),
			ContentHash: discovery.ContentHash(descriptor.Data), Authoritative: true,
		})
		runtimeAttributes := map[string]any{"agent_definition_state_present": true, "agent_definitions_configured": true}
		agentAttributes := map[string]any{
			"state_present": true, "descriptor_valid": true, "definition_format": "agent_markdown",
			"source_surface": "endpoint", "product_id": signature.ID, "defined": true,
		}
		runtimeID := addRuntime(runtimeAttributes, ref)
		agentID := b.AddEntity(
			discovery.KindAgent,
			"target:"+options.TargetID+":agent-definition:"+signature.ID+":"+strings.ToLower(descriptor.Relative),
			name,
			agentAttributes,
			ref,
		)
		b.AddRelationship(discovery.RelationshipProvides, runtimeID, agentID, nil, ref)
		b.AddRelationship(discovery.RelationshipRunsOn, agentID, endpointID, nil, ref)
		if userID != "" {
			b.AddRelationship(discovery.RelationshipOwnedBy, agentID, userID, map[string]any{"scope": "user", "attribution": "observed_user", "authoritative": false}, ref)
		}
	}
}

type agentDescriptor struct {
	Path     string
	Relative string
	Data     []byte
}

func discoverAgentDescriptors(root string) (items []agentDescriptor, denied int, truncated bool, err error) {
	const maxAgentsPerRoot = 1000
	err = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			if errors.Is(walkErr, os.ErrPermission) {
				denied++
				if entry != nil && entry.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			return walkErr
		}
		relative, relativeErr := filepath.Rel(root, path)
		if relativeErr != nil {
			return nil
		}
		depth := len(strings.Split(filepath.ToSlash(relative), "/"))
		if entry.IsDir() {
			if relative != "." && depth > 2 {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 || !strings.HasSuffix(strings.ToLower(entry.Name()), ".md") || depth > 3 {
			return nil
		}
		data, readErr := readLimited(path, maxConfigSize)
		if readErr != nil {
			if errors.Is(readErr, os.ErrPermission) {
				denied++
			}
			return nil
		}
		items = append(items, agentDescriptor{Path: path, Relative: filepath.ToSlash(relative), Data: data})
		if len(items) >= maxAgentsPerRoot {
			truncated = true
			return filepath.SkipAll
		}
		return nil
	})
	return items, denied, truncated, err
}

func parseAgentDescriptor(data []byte, path string) (string, bool) {
	normalized := strings.ReplaceAll(string(data), "\r\n", "\n")
	body := strings.TrimSpace(normalized)
	name := ""
	if strings.HasPrefix(normalized, "---\n") {
		end := strings.Index(normalized[4:], "\n---")
		if end < 0 {
			return filepath.Base(path), false
		}
		var metadata map[string]any
		if yaml.Unmarshal([]byte(normalized[4:4+end]), &metadata) != nil {
			return filepath.Base(path), false
		}
		name, _ = metadata["name"].(string)
		name = strings.TrimSpace(name)
		body = strings.TrimSpace(normalized[4+end+4:])
	}
	if !validDescriptorName(name) {
		base := filepath.Base(path)
		name = strings.TrimSuffix(strings.TrimSuffix(base, ".agent.md"), filepath.Ext(base))
	}
	return name, validDescriptorName(name) && body != ""
}

func validDescriptorName(name string) bool {
	name = strings.TrimSpace(name)
	return name != "" && len(name) <= 200 && !strings.ContainsAny(name, "\r\n\x00")
}

func scanModelCache(ctx context.Context, b *builder.Builder, options Options, cache detector.ModelCache, endpointID string) {
	seen := map[string]struct{}{}
	for _, configuredRoot := range cache.Paths {
		root := expandPath(configuredRoot, options.HomeDir)
		if root == "" {
			continue
		}
		b.Snapshot.Coverage.LocationsChecked++
		if cache.Layout == "directory" {
			if info, err := os.Stat(root); err == nil && info.IsDir() {
				ref := b.AddEvidence(builder.Observation{DetectorID: cache.ID, DetectorVersion: options.Pack.Version, Method: "path", Family: "model_cache", Specificity: "high", Locator: discovery.SafeLocator(options.OrganizationID, "", root)})
				modelID := b.AddEntity(discovery.KindModel, "target:"+options.TargetID+":model-cache:"+cache.ID, cache.Name, map[string]any{}, ref)
				b.AddRelationship(discovery.RelationshipRunsOn, modelID, endpointID, map[string]any{"cached": true, "cache_only": true, "cache_provider": cache.Name, "cache_id": cache.ID, "source_surface": "endpoint"}, ref)
			} else if errors.Is(err, os.ErrPermission) {
				b.Snapshot.Coverage.LocationsDenied++
				b.Snapshot.Coverage.Partial = true
			}
			continue
		}
		count := 0
		err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if walkErr != nil {
				if errors.Is(walkErr, os.ErrPermission) {
					b.Snapshot.Coverage.LocationsDenied++
					b.Snapshot.Coverage.Partial = true
				}
				return nil
			}
			if path == root {
				return nil
			}
			count++
			if count > maxModelCacheEntries {
				b.Snapshot.Coverage.Partial = true
				return filepath.SkipAll
			}
			if entry.Type()&os.ModeSymlink != 0 {
				if entry.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			relative, relativeErr := filepath.Rel(root, path)
			if relativeErr != nil {
				return nil
			}
			name, match := modelCacheName(cache.Layout, filepath.ToSlash(relative), entry.IsDir())
			if !match || name == "" {
				return nil
			}
			key := strings.ToLower(name)
			if _, exists := seen[key]; exists {
				return nil
			}
			seen[key] = struct{}{}
			ref := b.AddEvidence(builder.Observation{
				DetectorID: cache.ID, DetectorVersion: options.Pack.Version, Method: "path",
				Family: "model_cache", Specificity: "high", Locator: discovery.SafeLocator(options.OrganizationID, "", path),
			})
			modelID := b.AddEntity(discovery.KindModel, "model:"+key, name, map[string]any{}, ref)
			b.AddRelationship(discovery.RelationshipRunsOn, modelID, endpointID, map[string]any{"cached": true, "cache_provider": cache.Name, "cache_id": cache.ID, "source_surface": "endpoint"}, ref)
			return nil
		})
		if err != nil && !errors.Is(err, os.ErrNotExist) && !errors.Is(err, context.Canceled) {
			b.Error(cache.ID, "model_cache_unreadable", "A known local model cache could not be inspected", false)
		}
		if errors.Is(err, context.Canceled) {
			return
		}
	}
}

func modelCacheName(layout, relative string, directory bool) (string, bool) {
	parts := strings.Split(strings.Trim(relative, "/"), "/")
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return "", false
		}
	}
	switch layout {
	case "huggingface":
		if !directory || len(parts) != 1 || !strings.HasPrefix(parts[0], "models--") {
			return "", false
		}
		name := strings.TrimPrefix(parts[0], "models--")
		return strings.ReplaceAll(name, "--", "/"), name != ""
	case "ollama":
		if directory || len(parts) < 4 {
			return "", false
		}
		return strings.Join(parts[1:len(parts)-1], "/") + ":" + parts[len(parts)-1], true
	case "lm-studio":
		if !directory || len(parts) != 2 {
			return "", false
		}
		return strings.Join(parts, "/"), true
	default:
		return "", false
	}
}

func addMCPServers(b *builder.Builder, options Options, signature detector.RuntimeSignature, runtimeID string, document any, ref string) {
	for _, server := range mcpconfig.Find(document) {
		attributes := map[string]any{"configured": true, "transport": server.Transport, "source_surface": "endpoint"}
		canonical := "target:" + options.TargetID + ":mcp:" + strings.ToLower(options.Username) + ":" + signature.ID + ":" + server.Name
		if server.URL != "" {
			sanitized, err := discovery.SanitizeURL(server.URL)
			if err != nil {
				continue
			}
			attributes["endpoint"] = sanitized
			attributes["host"] = discovery.URLHost(sanitized)
			canonical = "target:" + options.TargetID + ":mcp-url:" + sanitized
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
		serverID := b.AddEntity(discovery.KindMCPServer, canonical, server.Name, attributes, ref)
		b.AddRelationship(discovery.RelationshipConnectsTo, runtimeID, serverID, nil, ref)
	}
}

func addModels(b *builder.Builder, options Options, runtimeID string, document any, ref string) {
	seen := map[string]struct{}{}
	var walk func(string, any)
	walk = func(key string, value any) {
		switch typed := value.(type) {
		case map[string]any:
			for childKey, child := range typed {
				walk(childKey, child)
			}
		case []any:
			for _, child := range typed {
				walk(key, child)
			}
		case string:
			normalized := strings.ToLower(key)
			if normalized != "model" && normalized != "model_id" && normalized != "modelid" {
				return
			}
			name := strings.TrimSpace(typed)
			if name == "" || len(name) > 200 {
				return
			}
			if _, exists := seen[name]; exists {
				return
			}
			seen[name] = struct{}{}
			modelID := b.AddEntity(discovery.KindModel, "model:"+strings.ToLower(name), name, map[string]any{"configured": true}, ref)
			b.AddRelationship(discovery.RelationshipUses, runtimeID, modelID, map[string]any{"configured": true}, ref)
		}
	}
	walk("", document)
}

func parseConfig(format string, data []byte) (any, error) {
	var value any
	var err error
	switch strings.ToLower(format) {
	case "json":
		err = json.Unmarshal(data, &value)
	case "jsonc":
		var normalized []byte
		normalized, err = normalizeJSONC(data)
		if err == nil {
			err = json.Unmarshal(normalized, &value)
		}
	case "yaml", "yml":
		err = yaml.Unmarshal(data, &value)
	case "toml":
		err = toml.Unmarshal(data, &value)
	default:
		return nil, fmt.Errorf("unsupported config format %q", format)
	}
	return value, err
}

func normalizeJSONC(data []byte) ([]byte, error) {
	withoutComments := make([]byte, 0, len(data))
	inString := false
	escaped := false
	for index := 0; index < len(data); index++ {
		current := data[index]
		if inString {
			withoutComments = append(withoutComments, current)
			if escaped {
				escaped = false
			} else if current == '\\' {
				escaped = true
			} else if current == '"' {
				inString = false
			}
			continue
		}
		if current == '"' {
			inString = true
			withoutComments = append(withoutComments, current)
			continue
		}
		if current == '/' && index+1 < len(data) && data[index+1] == '/' {
			withoutComments = append(withoutComments, ' ', ' ')
			index += 2
			for ; index < len(data) && data[index] != '\n' && data[index] != '\r'; index++ {
				withoutComments = append(withoutComments, ' ')
			}
			if index < len(data) {
				withoutComments = append(withoutComments, data[index])
			}
			continue
		}
		if current == '/' && index+1 < len(data) && data[index+1] == '*' {
			withoutComments = append(withoutComments, ' ', ' ')
			index += 2
			closed := false
			for ; index < len(data); index++ {
				if data[index] == '*' && index+1 < len(data) && data[index+1] == '/' {
					withoutComments = append(withoutComments, ' ', ' ')
					index++
					closed = true
					break
				}
				if data[index] == '\n' || data[index] == '\r' {
					withoutComments = append(withoutComments, data[index])
				} else {
					withoutComments = append(withoutComments, ' ')
				}
			}
			if !closed {
				return nil, fmt.Errorf("unterminated JSONC block comment")
			}
			continue
		}
		withoutComments = append(withoutComments, current)
	}

	result := make([]byte, 0, len(withoutComments))
	inString = false
	escaped = false
	for index, current := range withoutComments {
		if inString {
			result = append(result, current)
			if escaped {
				escaped = false
			} else if current == '\\' {
				escaped = true
			} else if current == '"' {
				inString = false
			}
			continue
		}
		if current == '"' {
			inString = true
			result = append(result, current)
			continue
		}
		if current == ',' {
			next := index + 1
			for next < len(withoutComments) && (withoutComments[next] == ' ' || withoutComments[next] == '\t' || withoutComments[next] == '\n' || withoutComments[next] == '\r') {
				next++
			}
			if next < len(withoutComments) && (withoutComments[next] == '}' || withoutComments[next] == ']') {
				result = append(result, ' ')
				continue
			}
		}
		result = append(result, current)
	}
	return result, nil
}

func hasConfigurationContent(value any) bool {
	switch typed := value.(type) {
	case map[string]any:
		return len(typed) > 0
	case []any:
		return len(typed) > 0
	default:
		return false
	}
}

func expandPath(path, home string) string {
	if path == "~" {
		return home
	}
	if strings.HasPrefix(path, "~/") || strings.HasPrefix(path, `~\`) {
		return filepath.Join(home, path[2:])
	}
	path = strings.ReplaceAll(path, "$HOME", home)
	path = strings.ReplaceAll(path, "${HOME}", home)
	path = strings.ReplaceAll(path, "%USERPROFILE%", home)
	currentHome, _ := os.UserHomeDir()
	for _, variable := range []string{"APPDATA", "LOCALAPPDATA"} {
		placeholder := "%" + variable + "%"
		if strings.Contains(strings.ToUpper(path), placeholder) {
			value := os.Getenv(variable)
			if runtime.GOOS == "windows" && filepath.Clean(currentHome) != filepath.Clean(home) {
				folder := "Roaming"
				if variable == "LOCALAPPDATA" {
					folder = "Local"
				}
				value = filepath.Join(home, "AppData", folder)
			}
			if value == "" {
				return ""
			}
			path = strings.ReplaceAll(path, placeholder, value)
		}
	}
	return filepath.Clean(path)
}

func readLimited(path string, limit int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	reader := io.LimitReader(file, limit+1)
	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("file exceeds %d byte limit", limit)
	}
	return data, nil
}

func installedPathMatches(pattern string) ([]string, error) {
	if strings.ContainsAny(pattern, "*?[") {
		return filepath.Glob(pattern)
	}
	if _, err := os.Stat(pattern); err == nil {
		return []string{pattern}, nil
	} else if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	} else {
		return nil, err
	}
}

func processNames(ctx context.Context, platform string) map[string]bool {
	result := map[string]bool{}
	if platform == "windows" {
		output, err := exec.CommandContext(ctx, "tasklist", "/FO", "CSV", "/NH").Output()
		if err != nil {
			return result
		}
		rows, err := csv.NewReader(strings.NewReader(string(output))).ReadAll()
		if err != nil {
			return result
		}
		for _, row := range rows {
			if len(row) > 0 {
				result[strings.ToLower(strings.TrimSuffix(row[0], ".exe"))] = true
			}
		}
		return result
	}
	output, err := exec.CommandContext(ctx, "ps", "-axo", "comm=").Output()
	if err != nil {
		return result
	}
	for _, line := range strings.Split(string(output), "\n") {
		command := strings.TrimSpace(line)
		name := strings.ToLower(filepath.Base(command))
		if name != "" {
			result[name] = true
		}
		if platform == "darwin" {
			if appName := macApplicationName(command); appName != "" {
				result[strings.ToLower(appName)] = true
			}
		}
	}
	return result
}

func macApplicationName(command string) string {
	normalized := filepath.ToSlash(strings.TrimSpace(command))
	marker := strings.Index(strings.ToLower(normalized), ".app/")
	if marker < 0 {
		return ""
	}
	bundle := normalized[:marker]
	return filepath.Base(bundle)
}

func anyProcessRunning(running map[string]bool, candidates []string) bool {
	for _, candidate := range candidates {
		if running[strings.ToLower(candidate)] {
			return true
		}
	}
	return false
}

func matchingListenerProcess(options Options, port int, candidates []string) (matched, ownershipVerified bool) {
	if owners := options.ListeningProcesses[port]; len(owners) > 0 {
		return anyProcessRunning(owners, candidates), true
	}
	return anyProcessRunning(options.ProcessNames, candidates), false
}

func executableNames(pack detector.Pack) map[string]bool {
	result := map[string]bool{}
	for _, signature := range pack.Runtimes {
		for _, name := range signature.Processes {
			if _, err := exec.LookPath(name); err == nil {
				result[strings.ToLower(name)] = true
			}
		}
	}
	return result
}

func listeningBindings(ctx context.Context, platform string) map[int]string {
	result := map[int]string{}
	commands := [][]string{}
	if platform == "windows" {
		commands = append(commands, []string{"netstat", "-an", "-p", "tcp"})
	} else {
		commands = append(commands, []string{"netstat", "-an"}, []string{"ss", "-ltnH"})
		if platform == "darwin" {
			commands = append(commands, []string{"lsof", "-nP", "-iTCP", "-sTCP:LISTEN"})
		}
	}
	var output []byte
	for _, arguments := range commands {
		candidate, err := exec.CommandContext(ctx, arguments[0], arguments[1:]...).Output()
		if err == nil {
			output = append(output, candidate...)
			output = append(output, '\n')
		}
	}
	for _, line := range strings.Split(string(output), "\n") {
		upper := strings.ToUpper(line)
		if !strings.Contains(upper, "LISTEN") {
			continue
		}
		for _, field := range strings.Fields(line) {
			field = strings.Trim(field, "[]")
			index := strings.LastIndex(field, ":")
			if index < 0 {
				index = strings.LastIndex(field, ".")
			}
			if index < 0 {
				continue
			}
			port, err := strconv.Atoi(strings.TrimSpace(field[index+1:]))
			if err == nil && port > 0 && port <= 65535 {
				binding := classifyBinding(strings.Trim(field[:index], "[]"))
				if bindingRank(binding) > bindingRank(result[port]) {
					result[port] = binding
				}
			}
		}
	}
	return result
}

func listeningProcessOwners(ctx context.Context, platform string) map[int]map[string]bool {
	switch platform {
	case "darwin":
		output, err := exec.CommandContext(ctx, "lsof", "-nP", "-iTCP", "-sTCP:LISTEN", "-Fpcn").Output()
		if err != nil {
			return map[int]map[string]bool{}
		}
		return parseDarwinListenerOwners(string(output))
	case "linux":
		output, err := exec.CommandContext(ctx, "ss", "-ltnpH").Output()
		if err != nil {
			return map[int]map[string]bool{}
		}
		return parseLinuxListenerOwners(string(output))
	case "windows":
		return windowsListenerOwners(ctx)
	default:
		return map[int]map[string]bool{}
	}
}

func parseDarwinListenerOwners(output string) map[int]map[string]bool {
	result := map[int]map[string]bool{}
	process := ""
	for _, line := range strings.Split(output, "\n") {
		if len(line) < 2 {
			continue
		}
		switch line[0] {
		case 'p':
			process = ""
		case 'c':
			process = strings.ToLower(strings.TrimSpace(line[1:]))
		case 'n':
			if process != "" {
				if port := listenerPort(line[1:]); port > 0 {
					addListenerOwner(result, port, process)
				}
			}
		}
	}
	return result
}

func parseLinuxListenerOwners(output string) map[int]map[string]bool {
	result := map[int]map[string]bool{}
	for _, line := range strings.Split(output, "\n") {
		marker := strings.Index(line, `users:(("`)
		if marker < 0 {
			continue
		}
		remainder := line[marker+len(`users:(("`):]
		end := strings.IndexByte(remainder, '"')
		if end < 1 {
			continue
		}
		process := strings.ToLower(strings.TrimSpace(remainder[:end]))
		fields := strings.Fields(line[:marker])
		for _, field := range fields {
			if port := listenerPort(field); port > 0 {
				addListenerOwner(result, port, process)
			}
		}
	}
	return result
}

func windowsListenerOwners(ctx context.Context) map[int]map[string]bool {
	result := map[int]map[string]bool{}
	taskOutput, err := exec.CommandContext(ctx, "tasklist", "/FO", "CSV", "/NH").Output()
	if err != nil {
		return result
	}
	rows, err := csv.NewReader(strings.NewReader(string(taskOutput))).ReadAll()
	if err != nil {
		return result
	}
	processByPID := map[string]string{}
	for _, row := range rows {
		if len(row) >= 2 {
			processByPID[strings.TrimSpace(row[1])] = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(row[0]), ".exe"))
		}
	}
	netstatOutput, err := exec.CommandContext(ctx, "netstat", "-ano", "-p", "tcp").Output()
	if err != nil {
		return result
	}
	for _, line := range strings.Split(string(netstatOutput), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 5 || !strings.EqualFold(fields[len(fields)-2], "LISTENING") {
			continue
		}
		port := listenerPort(fields[1])
		if process := processByPID[fields[len(fields)-1]]; port > 0 && process != "" {
			addListenerOwner(result, port, process)
		}
	}
	return result
}

func listenerPort(endpoint string) int {
	endpoint = strings.TrimSpace(strings.Trim(endpoint, "[]"))
	index := strings.LastIndex(endpoint, ":")
	if index < 0 {
		index = strings.LastIndex(endpoint, ".")
	}
	if index < 0 {
		return 0
	}
	value := strings.TrimSpace(strings.Trim(endpoint[index+1:], "()"))
	port, err := strconv.Atoi(value)
	if err != nil || port < 1 || port > 65535 {
		return 0
	}
	return port
}

func addListenerOwner(result map[int]map[string]bool, port int, process string) {
	if result[port] == nil {
		result[port] = map[string]bool{}
	}
	result[port][strings.ToLower(process)] = true
}

func classifyBinding(host string) string {
	host = strings.TrimSpace(strings.Trim(host, "[]"))
	switch host {
	case "", "*", "0.0.0.0", "::", ":::":
		return "all_interfaces"
	case "localhost", "127.0.0.1", "::1":
		return "loopback"
	}
	if ip := net.ParseIP(host); ip != nil {
		if ip.IsLoopback() {
			return "loopback"
		}
		return "interface"
	}
	return "unknown"
}

func bindingRank(binding string) int {
	switch binding {
	case "all_interfaces":
		return 4
	case "interface":
		return 3
	case "loopback":
		return 2
	case "unknown":
		return 1
	default:
		return 0
	}
}

// Version is overwritten at build time.
var Version = "2.0.0-dev"
