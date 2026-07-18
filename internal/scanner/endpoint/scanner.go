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
	"sort"
	"strconv"
	"strings"

	"github.com/barrikadelabs/barrikade-lens/internal/detector"
	"github.com/barrikadelabs/barrikade-lens/internal/scanner/builder"
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
	HomeDir                 string
	Hostname                string
	Username                string
	Platform                string
	Pack                    detector.Pack
	ProcessNames            map[string]bool
	ExecutableNames         map[string]bool
	ListeningPorts          map[int]bool
	ListeningBindings       map[int]string
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
	if options.SourceID == "" {
		options.SourceID = discovery.StableID(options.OrganizationID, discovery.KindEndpoint, strings.ToLower(options.Hostname))
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
		if options.ListeningPorts == nil {
			options.ListeningPorts = map[int]bool{}
			for port := range options.ListeningBindings {
				options.ListeningPorts[port] = true
			}
		}
	}

	snapshot := discovery.NewSnapshot(options.OrganizationID, options.SourceID, discovery.SourceEndpoint, discovery.Collector{
		ID: "barrikade-lens", Name: "Barrikade Lens", Version: Version, Mode: "standalone",
	})
	snapshot.Scope = discovery.Scope{Name: options.Hostname, Attributes: map[string]string{"platform": options.Platform}}
	b := builder.New(snapshot)

	systemEvidence := b.AddEvidence(builder.Observation{
		DetectorID: "lens.endpoint", DetectorVersion: Version, Method: "system", Family: "identity", Specificity: "high",
		Locator: discovery.HashLocator(options.OrganizationID, options.SourceID),
	})
	endpointID := b.AddEntity(discovery.KindEndpoint, options.SourceID, options.Hostname, map[string]any{
		"hostname": options.Hostname, "os": options.Platform, "architecture": runtime.GOARCH,
	}, systemEvidence)
	userID := ""
	if options.Username != "" {
		userID = b.AddEntity(discovery.KindUser, "endpoint:"+options.SourceID+":user:"+options.Username, options.Username, map[string]any{
			"os_user": true,
		}, systemEvidence)
		b.AddRelationship(discovery.RelationshipOwnedBy, endpointID, userID, nil, systemEvidence)
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

func scanRuntime(b *builder.Builder, options Options, signature detector.RuntimeSignature, endpointID, userID string) {
	canonical := "endpoint:" + options.SourceID + ":runtime:" + signature.ID
	runtimeID := ""
	addRuntime := func(attributes map[string]any, ref string) string {
		runtimeID = b.AddEntity(discovery.KindRuntime, canonical, signature.Name, attributes, ref)
		b.AddRelationship(discovery.RelationshipRunsOn, runtimeID, endpointID, nil, ref)
		if userID != "" {
			b.AddRelationship(discovery.RelationshipOwnedBy, runtimeID, userID, map[string]any{"scope": "user"}, ref)
		}
		return runtimeID
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
				Family: "installation", Specificity: "high", Locator: discovery.SafeLocator(options.OrganizationID, "", path),
			})
			addRuntime(map[string]any{"installed": true}, ref)
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
		ref := b.AddEvidence(builder.Observation{
			DetectorID: signature.ID, DetectorVersion: options.Pack.Version, Method: "descriptor",
			Family: "configuration", Specificity: "high", Locator: discovery.SafeLocator(options.OrganizationID, "", path),
			ContentHash: discovery.ContentHash(data),
		})
		currentRuntimeID := addRuntime(map[string]any{"configured": true, "configuration_scope": config.Scope}, ref)
		document, err := parseConfig(config.Format, data)
		if err != nil {
			b.Error(signature.ID, "config_malformed", "A known configuration file was present but malformed", false)
			continue
		}
		addMCPServers(b, options, signature, currentRuntimeID, document, ref)
		addModels(b, options, currentRuntimeID, document, ref)
	}

	for _, root := range signature.SkillRoots {
		path := expandPath(root, options.HomeDir)
		if path != "" {
			scanSkills(b, options, signature, addRuntime, path)
		}
	}

	for _, process := range signature.Processes {
		if options.ExecutableNames[strings.ToLower(process)] {
			ref := b.AddEvidence(builder.Observation{
				DetectorID: signature.ID, DetectorVersion: options.Pack.Version, Method: "executable",
				Family: "installation", Specificity: "high", Locator: discovery.HashLocator(options.OrganizationID, strings.ToLower(process)),
			})
			addRuntime(map[string]any{"installed": true, "installation_method": "executable_path"}, ref)
			break
		}
	}
	for _, process := range signature.Processes {
		if options.ProcessNames[strings.ToLower(process)] {
			ref := b.AddEvidence(builder.Observation{
				DetectorID: signature.ID, DetectorVersion: options.Pack.Version, Method: "process",
				Family: "process", Specificity: "high", Locator: discovery.HashLocator(options.OrganizationID, strings.ToLower(process)),
			})
			addRuntime(map[string]any{"running_at_scan": true}, ref)
			break
		}
	}
	for _, modelServer := range signature.ModelServers {
		if !options.ListeningPorts[modelServer.Port] {
			continue
		}
		ref := b.AddEvidence(builder.Observation{
			DetectorID: signature.ID, DetectorVersion: options.Pack.Version, Method: "listener",
			Family: "network_listener", Specificity: "high", Locator: fmt.Sprintf("tcp://127.0.0.1:%d", modelServer.Port),
		})
		currentRuntimeID := addRuntime(map[string]any{"running_at_scan": true}, ref)
		serverID := b.AddEntity(discovery.KindModelServer, fmt.Sprintf("endpoint:%s:model-server:%d", options.SourceID, modelServer.Port), modelServer.Name, map[string]any{
			"running_at_scan": true, "transport": "http", "port": modelServer.Port,
		}, ref)
		if binding := options.ListeningBindings[modelServer.Port]; binding != "" {
			b.AddEntity(discovery.KindModelServer, fmt.Sprintf("endpoint:%s:model-server:%d", options.SourceID, modelServer.Port), modelServer.Name, map[string]any{"binding": binding}, ref)
		}
		b.AddRelationship(discovery.RelationshipProvides, currentRuntimeID, serverID, nil, ref)
		b.AddRelationship(discovery.RelationshipRunsOn, serverID, endpointID, nil, ref)
	}
}

func scanKnownListener(b *builder.Builder, options Options, listener detector.Listener, endpointID string) {
	if !options.ListeningPorts[listener.Port] {
		return
	}
	ref := b.AddEvidence(builder.Observation{
		DetectorID: listener.ID, DetectorVersion: options.Pack.Version, Method: "listener",
		Family: "network_listener", Specificity: "high", Locator: fmt.Sprintf("tcp-listener:%d", listener.Port),
	})
	attributes := map[string]any{"running_at_scan": true, "transport": "tcp", "port": listener.Port}
	if binding := options.ListeningBindings[listener.Port]; binding != "" {
		attributes["binding"] = binding
	}
	kind := discovery.EntityKind(listener.Kind)
	entityID := b.AddEntity(kind, fmt.Sprintf("endpoint:%s:%s:%d", options.SourceID, listener.Kind, listener.Port), listener.Name, attributes, ref)
	b.AddRelationship(discovery.RelationshipRunsOn, entityID, endpointID, nil, ref)
}

func scanSkills(b *builder.Builder, options Options, signature detector.RuntimeSignature, addRuntime func(map[string]any, string) string, root string) {
	b.Snapshot.Coverage.LocationsChecked++
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err != nil {
		b.Snapshot.Coverage.LocationsDenied++
		b.Snapshot.Coverage.Partial = true
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		descriptor := filepath.Join(root, entry.Name(), "SKILL.md")
		data, err := readLimited(descriptor, maxConfigSize)
		method := "path"
		contentHash := ""
		if err == nil {
			method = "descriptor"
			contentHash = discovery.ContentHash(data)
		}
		ref := b.AddEvidence(builder.Observation{
			DetectorID: signature.ID + ".skills", DetectorVersion: options.Pack.Version, Method: method,
			Family: "skill", Specificity: "high", Locator: discovery.SafeLocator(options.OrganizationID, "", filepath.Join(root, entry.Name())),
			ContentHash: contentHash,
		})
		runtimeID := addRuntime(map[string]any{"skills_configured": true}, ref)
		skillID := b.AddEntity(discovery.KindSkill, "endpoint:"+options.SourceID+":skill:"+strings.ToLower(options.Username)+":"+signature.ID+":"+entry.Name(), entry.Name(), map[string]any{
			"configured": true,
		}, ref)
		b.AddRelationship(discovery.RelationshipProvides, runtimeID, skillID, nil, ref)
	}
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
				modelID := b.AddEntity(discovery.KindModel, "endpoint:"+options.SourceID+":model-cache:"+cache.ID, cache.Name, map[string]any{"cached": true, "cache_only": true, "cache_provider": cache.Name, "cache_id": cache.ID}, ref)
				b.AddRelationship(discovery.RelationshipRunsOn, modelID, endpointID, map[string]any{"cached": true}, ref)
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
			modelID := b.AddEntity(discovery.KindModel, "model:"+key, name, map[string]any{
				"cached": true, "cache_provider": cache.Name, "cache_id": cache.ID,
			}, ref)
			b.AddRelationship(discovery.RelationshipRunsOn, modelID, endpointID, map[string]any{"cached": true}, ref)
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

type mcpServer struct {
	Name              string
	Transport         string
	URL               string
	Enabled           *bool
	EnvironmentKeys   []string
	CredentialPresent bool
}

func addMCPServers(b *builder.Builder, options Options, signature detector.RuntimeSignature, runtimeID string, document any, ref string) {
	for _, server := range findMCPServers(document) {
		attributes := map[string]any{"configured": true, "transport": server.Transport}
		canonical := "endpoint:" + options.SourceID + ":mcp:" + strings.ToLower(options.Username) + ":" + signature.ID + ":" + server.Name
		if server.URL != "" {
			sanitized, err := discovery.SanitizeURL(server.URL)
			if err != nil {
				continue
			}
			attributes["endpoint"] = sanitized
			attributes["host"] = discovery.URLHost(sanitized)
			canonical = "endpoint:" + options.SourceID + ":mcp-url:" + sanitized
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

func findMCPServers(document any) []mcpServer {
	result := []mcpServer{}
	var walk func(any)
	walk = func(value any) {
		object, ok := value.(map[string]any)
		if !ok {
			if list, ok := value.([]any); ok {
				for _, item := range list {
					walk(item)
				}
			}
			return
		}
		for key, child := range object {
			normalized := strings.ToLower(strings.ReplaceAll(key, "_", ""))
			if normalized == "mcpservers" {
				if servers, ok := child.(map[string]any); ok {
					for name, raw := range servers {
						config, ok := raw.(map[string]any)
						if !ok {
							continue
						}
						server := mcpServer{Name: name, Transport: "stdio"}
						if endpoint, ok := stringValue(config, "url", "endpoint", "serverUrl"); ok {
							server.URL = endpoint
							server.Transport = "http"
						}
						if transport, ok := stringValue(config, "transport", "type"); ok {
							server.Transport = strings.ToLower(transport)
						}
						if enabled, ok := boolValue(config, "enabled"); ok {
							server.Enabled = &enabled
						}
						if disabled, ok := boolValue(config, "disabled"); ok {
							enabled := !disabled
							server.Enabled = &enabled
						}
						if env, ok := config["env"].(map[string]any); ok {
							for envKey, envValue := range env {
								server.EnvironmentKeys = append(server.EnvironmentKeys, envKey)
								if fmt.Sprint(envValue) != "" {
									server.CredentialPresent = true
								}
							}
							sort.Strings(server.EnvironmentKeys)
						}
						result = append(result, server)
					}
				}
				continue
			}
			walk(child)
		}
	}
	walk(document)
	return result
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
			b.AddRelationship(discovery.RelationshipUses, runtimeID, modelID, nil, ref)
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
	case "yaml", "yml":
		err = yaml.Unmarshal(data, &value)
	case "toml":
		err = toml.Unmarshal(data, &value)
	default:
		return nil, fmt.Errorf("unsupported config format %q", format)
	}
	return value, err
}

func stringValue(object map[string]any, keys ...string) (string, bool) {
	for _, key := range keys {
		if value, ok := object[key].(string); ok && strings.TrimSpace(value) != "" {
			return value, true
		}
	}
	return "", false
}

func boolValue(object map[string]any, keys ...string) (bool, bool) {
	for _, key := range keys {
		if value, ok := object[key].(bool); ok {
			return value, true
		}
	}
	return false, false
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
		name := strings.ToLower(filepath.Base(strings.TrimSpace(line)))
		if name != "" {
			result[name] = true
		}
	}
	return result
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
