package repository

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/barrikadelabs/barrikade-lens/internal/detector"
	"github.com/barrikadelabs/barrikade-lens/internal/scanner/builder"
	"github.com/barrikadelabs/barrikade-lens/pkg/discovery"
	"gopkg.in/yaml.v3"
)

const (
	maxRepositoryFiles = 100_000
	maxArtifactSize    = 4 << 20
)

var (
	manifestNames = map[string]bool{
		"package.json": true, "go.mod": true, "requirements.txt": true, "pyproject.toml": true,
		"poetry.lock": true, "uv.lock": true, "pipfile": true, "gemfile": true, "package-lock.json": true,
		"pnpm-lock.yaml": true, "yarn.lock": true, "go.sum": true, "cargo.toml": true, "cargo.lock": true,
	}
	sourceExtensions        = map[string]bool{".go": true, ".py": true, ".ts": true, ".tsx": true, ".js": true, ".jsx": true}
	agentFilePattern        = regexp.MustCompile(`(?i)(^|[._-])(agents?|crew|workflow)([._-]|$)`)
	kubernetesKindPattern   = regexp.MustCompile(`(?mi)^\s*kind:\s*([a-z][a-z0-9]*)\s*(?:#.*)?$`)
	kubernetesWorkloadKinds = map[string]bool{"deployment": true, "statefulset": true, "daemonset": true, "job": true, "cronjob": true, "pod": true}
)

type Options struct {
	OrganizationID string
	SourceID       string
	TargetID       string
	Root           string
	RepositoryURL  string
	CommitSHA      string
	Pack           detector.Pack
	MaxFiles       int
}

func Scan(ctx context.Context, options Options) (discovery.Snapshot, error) {
	if options.OrganizationID == "" {
		options.OrganizationID = "local"
	}
	if options.Root == "" {
		options.Root = "."
	}
	root, err := filepath.Abs(options.Root)
	if err != nil {
		return discovery.Snapshot{}, err
	}
	options.Root = root
	if options.Pack.ID == "" {
		options.Pack, err = detector.Builtin()
		if err != nil {
			return discovery.Snapshot{}, err
		}
	}
	if options.MaxFiles <= 0 {
		options.MaxFiles = maxRepositoryFiles
	}
	if options.RepositoryURL == "" {
		options.RepositoryURL = gitOutput(ctx, root, "config", "--get", "remote.origin.url")
	}
	options.RepositoryURL = normalizeRepositoryURL(options.RepositoryURL)
	if options.CommitSHA == "" {
		options.CommitSHA = gitOutput(ctx, root, "rev-parse", "HEAD")
	}
	canonical := options.RepositoryURL
	if canonical == "" {
		canonical = discovery.HashLocator(options.OrganizationID, root)
	}
	if options.TargetID == "" {
		options.TargetID = discovery.StableID(options.OrganizationID, discovery.KindRepository, canonical)
	}
	if options.SourceID == "" {
		options.SourceID = options.TargetID
	}

	snapshot := discovery.NewTargetSnapshot(options.OrganizationID, options.SourceID, options.TargetID, discovery.SourceRepository, discovery.Collector{
		ID: "barrikade-lens", Name: "Barrikade Lens", Version: Version, Mode: "repository",
	})
	snapshot.Scope = discovery.Scope{Name: filepath.Base(root)}
	b := builder.New(snapshot)
	repoEvidence := b.AddEvidence(builder.Observation{
		DetectorID: "lens.repository", DetectorVersion: Version, Method: "descriptor", Family: "repository", Specificity: "high",
		Locator: canonical,
	})
	attributes := map[string]any{"source_surface": "repository"}
	if options.RepositoryURL != "" {
		attributes["repository_url"] = options.RepositoryURL
	}
	if validCommit(options.CommitSHA) {
		attributes["commit_sha"] = strings.ToLower(options.CommitSHA)
	}
	repositoryID := b.AddEntity(discovery.KindRepository, canonical, filepath.Base(root), attributes, repoEvidence)

	state := scanState{options: options, builder: b, repositoryID: repositoryID, repositoryCanonical: canonical, frameworks: map[string]string{}, agentID: ""}
	err = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			b.Snapshot.Coverage.LocationsDenied++
			b.Snapshot.Coverage.Partial = true
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if entry.IsDir() && path != root && ignoredDirectory(entry.Name()) {
			return filepath.SkipDir
		}
		if entry.IsDir() {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		state.files++
		if state.files > options.MaxFiles {
			b.Snapshot.Coverage.Partial = true
			b.Snapshot.Coverage.Notes = append(b.Snapshot.Coverage.Notes, "repository file limit reached")
			return filepath.SkipAll
		}
		state.inspect(path)
		return nil
	})
	if err != nil {
		return discovery.Snapshot{}, err
	}
	b.Snapshot.Coverage.DetectorsRun = len(options.Pack.Frameworks) + 5
	return b.Finish()
}

type scanState struct {
	options             Options
	builder             *builder.Builder
	repositoryID        string
	repositoryCanonical string
	frameworks          map[string]string
	agentID             string
	files               int
}

func (s *scanState) inspect(path string) {
	base := strings.ToLower(filepath.Base(path))
	extension := strings.ToLower(filepath.Ext(path))
	isManifest := manifestNames[base]
	isSource := sourceExtensions[extension]
	isMCP := base == "mcp.json" || base == ".mcp.json" || strings.Contains(base, "mcp") && (extension == ".json" || extension == ".yaml" || extension == ".yml")
	isOpenAPI := strings.Contains(base, "openapi") || strings.Contains(base, "swagger")
	isArazzo := strings.Contains(base, "arazzo")
	isA2A := (base == "agent-card.json" || base == "agent.json" || strings.Contains(base, "a2a")) && (extension == ".json" || extension == ".yaml" || extension == ".yml")
	isAgent := agentFilePattern.MatchString(strings.TrimSuffix(base, extension)) && (extension == ".json" || extension == ".yaml" || extension == ".yml")
	if agentInstructionNames[base] {
		isAgent = true
	}
	if isA2A {
		isAgent = false
	}
	normalizedPath := strings.ToLower(filepath.ToSlash(path))
	isDeclarative := extension == ".yaml" || extension == ".yml" || extension == ".json" || extension == ".toml" || extension == ".tf" || extension == ".hcl"
	isDeploymentPath := strings.Contains(normalizedPath, "/k8s/") || strings.Contains(normalizedPath, "/kubernetes/") || strings.Contains(normalizedPath, "/helm/") || strings.Contains(normalizedPath, "/terraform/") || strings.Contains(normalizedPath, "/pulumi/") || strings.Contains(normalizedPath, "/cloudformation/")
	isDeployment := base == "dockerfile" || strings.HasPrefix(base, "docker-compose") || base == ".gitlab-ci.yml" || base == "bitbucket-pipelines.yml" || base == "azure-pipelines.yml" || base == "jenkinsfile" || strings.Contains(normalizedPath, "/.github/workflows/") || strings.Contains(normalizedPath, "/.circleci/") || strings.Contains(normalizedPath, "/.buildkite/") || isDeclarative && isDeploymentPath
	isOwners := base == "codeowners"
	if !isManifest && !isSource && !isMCP && !isOpenAPI && !isArazzo && !isA2A && !isAgent && !isDeployment && !isOwners {
		return
	}
	s.builder.Snapshot.Coverage.LocationsChecked++
	data, err := readLimited(path, maxArtifactSize)
	if err != nil {
		if errors.Is(err, os.ErrPermission) {
			s.builder.Snapshot.Coverage.LocationsDenied++
		}
		s.builder.Snapshot.Coverage.Partial = true
		return
	}
	relative, _ := filepath.Rel(s.options.Root, path)
	locator := filepath.ToSlash(relative)
	method := "manifest"
	if isOpenAPI || isArazzo || isA2A || isAgent {
		method = "descriptor"
	}
	ref := s.builder.AddEvidence(builder.Observation{
		DetectorID: "repo.artifacts", DetectorVersion: Version, Method: method, Family: evidenceFamily(isManifest, isSource, isMCP, isOpenAPI, isArazzo, isA2A, isAgent, isDeployment),
		Specificity: specificity(isSource), Locator: locator, ContentHash: discovery.ContentHash(data),
	})
	if isManifest || isSource {
		s.detectFrameworks(locator, string(data), ref, isManifest, isSource)
	}
	if isMCP {
		s.detectMCP(locator, data, ref)
	}
	if isOpenAPI {
		s.detectOpenAPI(locator, data, ref)
	}
	if isArazzo {
		s.detectArazzo(locator, data, ref)
	}
	if isA2A {
		s.detectA2A(locator, data, ref)
	}
	if isAgent {
		s.detectAgent(locator, data, ref)
	}
	if isDeployment {
		s.detectDeployment(locator, base, data, ref)
	}
	if isOwners {
		s.detectOwners(data, ref)
	}
}

var agentInstructionNames = map[string]bool{"agents.md": true, "claude.md": true, "gemini.md": true, "antigravity.md": true, ".clinerules": true, ".windsurfrules": true, ".cursorrules": true, ".roomodes": true}

func (s *scanState) agent(ref string) string {
	if s.agentID == "" {
		s.agentID = s.builder.AddEntity(discovery.KindAgent, "target:"+s.options.TargetID+":application", filepath.Base(s.options.Root), map[string]any{"defined": true, "source_surface": "repository"}, ref)
		s.builder.AddRelationship(discovery.RelationshipDefinedIn, s.agentID, s.repositoryID, nil, ref)
	} else {
		s.builder.AddEntity(discovery.KindAgent, "target:"+s.options.TargetID+":application", filepath.Base(s.options.Root), nil, ref)
	}
	return s.agentID
}

func (s *scanState) detectFrameworks(locator, content, ref string, isManifest, isSource bool) {
	lower := strings.ToLower(content)
	for _, signature := range s.options.Pack.Frameworks {
		matched := ""
		if isManifest {
			for _, packageName := range signature.Packages {
				if packageMatch(lower, strings.ToLower(packageName)) {
					matched = packageName
					break
				}
			}
		}
		if matched == "" && isSource {
			candidates := append(append([]string{}, signature.Imports...), signature.Packages...)
			for _, importName := range candidates {
				if importMatch(lower, strings.ToLower(importName), strings.ToLower(filepath.Ext(locator))) {
					matched = importName
					break
				}
			}
		}
		if matched == "" {
			continue
		}
		frameworkID, exists := s.frameworks[signature.ID]
		if !exists {
			frameworkID = s.builder.AddEntity(discovery.KindFramework, "framework:"+signature.ID, signature.Name, map[string]any{"detected_in_repository": true}, ref)
			s.frameworks[signature.ID] = frameworkID
		}
		s.builder.AddRelationship(discovery.RelationshipUses, s.agent(ref), frameworkID, map[string]any{"locator": locator}, ref)
	}
}

func (s *scanState) detectAgent(locator string, data []byte, ref string) {
	document, _ := structuredDocument(data)
	name := firstString(document, "name", "agent_name", "id")
	if name == "" || len(name) > 200 {
		name = strings.TrimSuffix(filepath.Base(locator), filepath.Ext(locator))
	}
	id := s.builder.AddEntity(discovery.KindAgent, "target:"+s.options.TargetID+":agent:"+locator, name, map[string]any{"defined": true, "descriptor": locator, "source_surface": "repository"}, ref)
	s.builder.AddRelationship(discovery.RelationshipDefinedIn, id, s.repositoryID, nil, ref)
}

func (s *scanState) detectMCP(locator string, data []byte, ref string) {
	document, err := structuredDocument(data)
	if err != nil {
		return
	}
	servers := findObjectByNormalizedKey(document, "mcpservers")
	for name, raw := range servers {
		config, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		attributes := map[string]any{"configured": true, "transport": "stdio", "descriptor": locator, "source_surface": "repository"}
		canonical := "target:" + s.options.TargetID + ":mcp:" + name
		if rawURL := firstString(config, "url", "endpoint", "serverUrl"); rawURL != "" {
			sanitized, err := discovery.SanitizeURL(rawURL)
			if err != nil {
				continue
			}
			attributes["endpoint"] = sanitized
			attributes["host"] = discovery.URLHost(sanitized)
			attributes["transport"] = "http"
			canonical = "target:" + s.options.TargetID + ":mcp-url:" + sanitized
		}
		id := s.builder.AddEntity(discovery.KindMCPServer, canonical, name, attributes, ref)
		s.builder.AddRelationship(discovery.RelationshipConfiguredBy, id, s.repositoryID, nil, ref)
	}
}

func (s *scanState) detectOpenAPI(locator string, data []byte, ref string) {
	document, err := structuredDocument(data)
	if err != nil {
		return
	}
	version := firstString(document, "openapi", "swagger")
	if version == "" {
		return
	}
	name := locator
	if info, ok := document["info"].(map[string]any); ok {
		if title := firstString(info, "title"); title != "" {
			name = title
		}
	}
	host := ""
	servers := []string{}
	if list, ok := document["servers"].([]any); ok {
		for _, raw := range list {
			if server, ok := raw.(map[string]any); ok {
				sanitized, err := discovery.SanitizeURL(firstString(server, "url"))
				if err == nil {
					servers = append(servers, sanitized)
					if host == "" {
						host = discovery.URLHost(sanitized)
					}
				}
			}
		}
	}
	canonical := "target:" + s.options.TargetID + ":api:" + locator
	if host != "" {
		canonical = "api-host:" + host
	}
	attributes := map[string]any{"document": locator, "openapi_version": version}
	if host != "" {
		attributes["host"] = host
	}
	if len(servers) > 0 {
		attributes["servers"] = servers
	}
	apiID := s.builder.AddEntity(discovery.KindAPIService, canonical, name, attributes, ref)
	s.builder.AddRelationship(discovery.RelationshipDefinedIn, apiID, s.repositoryID, nil, ref)
	paths, _ := document["paths"].(map[string]any)
	for path, rawPath := range paths {
		operations, _ := rawPath.(map[string]any)
		for method, rawOperation := range operations {
			if !isHTTPMethod(method) {
				continue
			}
			operation, _ := rawOperation.(map[string]any)
			operationName := firstString(operation, "operationId", "summary")
			if operationName == "" {
				operationName = strings.ToUpper(method) + " " + path
			}
			operationID := s.builder.AddEntity(discovery.KindAPIOperation, canonical+":"+strings.ToLower(method)+":"+path, operationName, map[string]any{"method": strings.ToUpper(method), "path": path}, ref)
			s.builder.AddRelationship(discovery.RelationshipProvides, apiID, operationID, nil, ref)
		}
	}
}

func (s *scanState) detectArazzo(locator string, data []byte, ref string) {
	document, err := structuredDocument(data)
	version := firstString(document, "arazzo")
	if err != nil || !strings.HasPrefix(version, "1.") {
		return
	}
	workflows, _ := document["workflows"].([]any)
	for index, raw := range workflows {
		workflow, _ := raw.(map[string]any)
		name := firstString(workflow, "workflowId", "summary")
		if name == "" {
			name = fmt.Sprintf("workflow-%d", index+1)
		}
		id := s.builder.AddEntity(discovery.KindWorkflow, "target:"+s.options.TargetID+":workflow:"+locator+":"+name, name, map[string]any{"document": locator, "arazzo_version": version, "source_surface": "repository"}, ref)
		s.builder.AddRelationship(discovery.RelationshipDefinedIn, id, s.repositoryID, nil, ref)
	}
}

func (s *scanState) detectA2A(locator string, data []byte, ref string) {
	document, err := structuredDocument(data)
	if err != nil {
		return
	}
	name := firstString(document, "name")
	if name == "" || len(name) > 500 {
		name = "A2A agent at " + locator
	}
	attributes := map[string]any{"defined": true, "protocol": "a2a", "agent_card": true, "descriptor": locator}
	canonical := "target:" + s.options.TargetID + ":a2a:" + locator
	if endpoint := firstString(document, "url"); endpoint != "" {
		if sanitized, sanitizeErr := discovery.SanitizeURL(endpoint); sanitizeErr == nil {
			attributes["endpoint"] = sanitized
			attributes["host"] = discovery.URLHost(sanitized)
			canonical = "a2a-endpoint:" + sanitized
		}
	}
	agentID := s.builder.AddEntity(discovery.KindAgent, canonical, name, attributes, ref)
	s.builder.AddRelationship(discovery.RelationshipDefinedIn, agentID, s.repositoryID, nil, ref)
	skills, _ := document["skills"].([]any)
	for index, raw := range skills {
		skill, _ := raw.(map[string]any)
		skillName := firstString(skill, "name", "id")
		if skillName == "" || len(skillName) > 500 {
			skillName = fmt.Sprintf("skill-%d", index+1)
		}
		skillID := s.builder.AddEntity(discovery.KindSkill, canonical+":skill:"+skillName, skillName, map[string]any{"declared": true, "protocol": "a2a"}, ref)
		s.builder.AddRelationship(discovery.RelationshipProvides, agentID, skillID, nil, ref)
	}
}

func (s *scanState) detectDeployment(locator, base string, data []byte, ref string) {
	normalized := strings.ToLower(filepath.ToSlash(locator))
	if base == ".gitlab-ci.yml" || base == "bitbucket-pipelines.yml" || base == "azure-pipelines.yml" || base == "jenkinsfile" || strings.HasPrefix(normalized, ".github/workflows/") || strings.Contains(normalized, "/.github/workflows/") || strings.HasPrefix(normalized, ".circleci/") || strings.Contains(normalized, "/.circleci/") || strings.HasPrefix(normalized, ".buildkite/") || strings.Contains(normalized, "/.buildkite/") {
		name := strings.TrimSuffix(filepath.Base(locator), filepath.Ext(locator))
		if document, err := structuredDocument(data); err == nil {
			if declared := firstString(document, "name"); declared != "" && len(declared) <= 200 {
				name = declared
			}
		}
		id := s.builder.AddEntity(discovery.KindWorkflow, "target:"+s.options.TargetID+":ci:"+locator, name, map[string]any{"declared": true, "workflow_type": "ci", "locator": locator, "source_surface": "repository"}, ref)
		s.builder.AddRelationship(discovery.RelationshipDefinedIn, id, s.repositoryID, nil, ref)
		return
	}
	if strings.HasPrefix(base, "docker-compose") {
		document, err := structuredDocument(data)
		if err == nil {
			if services, ok := document["services"].(map[string]any); ok {
				for name := range services {
					if strings.TrimSpace(name) == "" || len(name) > 200 {
						continue
					}
					id := s.builder.AddEntity(discovery.KindWorkload, "target:"+s.options.TargetID+":compose:"+locator+":"+name, name, map[string]any{"deployment_reference": true, "type": "compose_service", "locator": locator, "source_surface": "repository"}, ref)
					s.builder.AddRelationship(discovery.RelationshipDefinedIn, id, s.repositoryID, nil, ref)
				}
			}
		}
		s.markRepositoryDeployment(ref, "compose_present")
		return
	}
	if base == "dockerfile" {
		s.markRepositoryDeployment(ref, "container_build_present")
		return
	}

	workloadIndex := 0
	for _, match := range kubernetesKindPattern.FindAllSubmatch(data, -1) {
		kind := strings.ToLower(string(match[1]))
		if !kubernetesWorkloadKinds[kind] {
			continue
		}
		workloadIndex++
		name := strings.ToUpper(kind[:1]) + kind[1:] + " at " + filepath.Base(locator)
		id := s.builder.AddEntity(discovery.KindWorkload, fmt.Sprintf("target:%s:deployment:%s:%s:%d", s.options.TargetID, locator, kind, workloadIndex), name, map[string]any{"deployment_reference": true, "type": "kubernetes_" + kind, "locator": locator, "source_surface": "repository"}, ref)
		s.builder.AddRelationship(discovery.RelationshipDefinedIn, id, s.repositoryID, nil, ref)
	}
	s.markRepositoryDeployment(ref, "deployment_configuration_present")
}

func (s *scanState) markRepositoryDeployment(ref, attribute string) {
	s.builder.AddEntity(discovery.KindRepository, s.repositoryCanonical, filepath.Base(s.options.Root), map[string]any{attribute: true}, ref)
}

func (s *scanState) detectOwners(data []byte, ref string) {
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	owners := map[string]struct{}{}
	for scanner.Scan() {
		line := strings.TrimSpace(strings.SplitN(scanner.Text(), "#", 2)[0])
		fields := strings.Fields(line)
		for _, field := range fields[1:] {
			if strings.HasPrefix(field, "@") && len(field) <= 200 {
				owners[field] = struct{}{}
			}
		}
		if len(owners) >= 100 {
			break
		}
	}
	for owner := range owners {
		ownerID := s.builder.AddEntity(discovery.KindUser, "scm-owner:"+strings.ToLower(owner), owner, map[string]any{"scm_owner": true}, ref)
		s.builder.AddRelationship(discovery.RelationshipOwnedBy, s.repositoryID, ownerID, map[string]any{"attribution": "observed_scm_namespace", "authoritative": false}, ref)
	}
}

func structuredDocument(data []byte) (map[string]any, error) {
	var document map[string]any
	if err := json.Unmarshal(data, &document); err == nil {
		return document, nil
	}
	if err := yaml.Unmarshal(data, &document); err != nil {
		return nil, err
	}
	return document, nil
}

func findObjectByNormalizedKey(value any, target string) map[string]any {
	object, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	for key, child := range object {
		normalized := strings.ToLower(strings.ReplaceAll(key, "_", ""))
		if normalized == target {
			if result, ok := child.(map[string]any); ok {
				return result
			}
		}
		if result := findObjectByNormalizedKey(child, target); result != nil {
			return result
		}
	}
	return nil
}

func firstString(value any, keys ...string) string {
	object, ok := value.(map[string]any)
	if !ok {
		return ""
	}
	for _, key := range keys {
		if item, ok := object[key].(string); ok {
			return strings.TrimSpace(item)
		}
	}
	return ""
}

func packageMatch(content, name string) bool {
	for _, quote := range []string{"\"", "'"} {
		if strings.Contains(content, quote+name+quote) {
			return true
		}
	}
	return strings.Contains(content, "\n"+name+"==") || strings.Contains(content, "\n"+name+">=") || strings.HasPrefix(content, name+"==")
}

func importMatch(content, name, extension string) bool {
	for _, rawLine := range strings.Split(content, "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "//") || strings.HasPrefix(line, "*") {
			continue
		}
		switch extension {
		case ".py":
			if pythonImportLine(line, "import ", name) || pythonImportLine(line, "from ", name) {
				return true
			}
		case ".js", ".jsx", ".ts", ".tsx":
			if (strings.HasPrefix(line, "import ") || strings.HasPrefix(line, "export ")) && containsModuleLiteral(line, name) {
				return true
			}
			if tokenOutsideString(line, "require") && containsModuleLiteral(line, name) {
				return true
			}
		}
	}
	return false
}

func pythonImportLine(line, prefix, name string) bool {
	if !strings.HasPrefix(line, prefix) {
		return false
	}
	remainder := strings.TrimSpace(strings.TrimPrefix(line, prefix))
	if !strings.HasPrefix(remainder, name) {
		return false
	}
	if len(remainder) == len(name) {
		return true
	}
	switch remainder[len(name)] {
	case '.', ' ', '\t', ',', ';':
		return true
	}
	return false
}

func containsModuleLiteral(line, name string) bool {
	for _, quote := range []string{"\"", "'", "`"} {
		if strings.Contains(line, quote+name+quote) || strings.Contains(line, quote+name+"/") {
			return true
		}
	}
	return false
}

func tokenOutsideString(line, token string) bool {
	inString := byte(0)
	escaped := false
	for index := 0; index+len(token) <= len(line); index++ {
		current := line[index]
		if inString != 0 {
			if escaped {
				escaped = false
			} else if current == '\\' {
				escaped = true
			} else if current == inString {
				inString = 0
			}
			continue
		}
		if current == '\'' || current == '"' || current == '`' {
			inString = current
			continue
		}
		if strings.HasPrefix(line[index:], token) {
			return true
		}
	}
	return false
}

func evidenceFamily(manifest, source, mcp, openapi, arazzo, a2a, agent, deployment bool) string {
	switch {
	case openapi:
		return "api_descriptor"
	case arazzo:
		return "workflow_descriptor"
	case a2a:
		return "a2a_agent_card"
	case mcp:
		return "mcp_configuration"
	case agent:
		return "agent_descriptor"
	case deployment:
		return "deployment"
	case manifest:
		return "package_manifest"
	case source:
		return "source_import"
	}
	return "repository_artifact"
}

func specificity(source bool) string {
	if source {
		return "medium"
	}
	return "high"
}
func ignoredDirectory(name string) bool {
	return name == ".git" || name == "node_modules" || name == "vendor" || name == ".venv" || name == "dist" || name == "build" || name == ".next"
}
func isHTTPMethod(value string) bool {
	switch strings.ToLower(value) {
	case "get", "put", "post", "delete", "patch", "options", "head", "trace":
		return true
	}
	return false
}
func validCommit(value string) bool {
	value = strings.TrimSpace(value)
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	for _, character := range value {
		if !strings.ContainsRune("0123456789abcdefABCDEF", character) {
			return false
		}
	}
	return true
}

func normalizeRepositoryURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if strings.HasPrefix(raw, "git@") {
		parts := strings.SplitN(strings.TrimPrefix(raw, "git@"), ":", 2)
		if len(parts) == 2 {
			raw = "https://" + parts[0] + "/" + parts[1]
		}
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return ""
	}
	u.User = nil
	u.RawQuery = ""
	u.Fragment = ""
	u.Path = strings.TrimSuffix(u.Path, ".git")
	if u.Scheme != "http" && u.Scheme != "https" && u.Scheme != "ssh" {
		return ""
	}
	return u.String()
}

func gitOutput(ctx context.Context, root string, args ...string) string {
	commandArgs := append([]string{"-C", root}, args...)
	output, err := exec.CommandContext(ctx, "git", commandArgs...).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}

func readLimited(path string, limit int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("artifact exceeds size limit")
	}
	return data, nil
}

var Version = "2.0.0-dev"
