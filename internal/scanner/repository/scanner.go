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
	slashpath "path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/barrikadelabs/barrikade-lens/internal/ard"
	"github.com/barrikadelabs/barrikade-lens/internal/detector"
	"github.com/barrikadelabs/barrikade-lens/internal/scanner/builder"
	"github.com/barrikadelabs/barrikade-lens/internal/scanner/mcpconfig"
	"github.com/barrikadelabs/barrikade-lens/internal/scanner/skillconfig"
	"github.com/barrikadelabs/barrikade-lens/pkg/discovery"
	"golang.org/x/net/html"
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
		"pom.xml": true, "build.gradle": true, "build.gradle.kts": true, "gradle.lockfile": true,
		"packages.lock.json": true, "directory.packages.props": true,
	}
	sourceExtensions = map[string]bool{
		".go": true, ".py": true, ".ts": true, ".tsx": true, ".js": true, ".jsx": true,
		".mjs": true, ".cjs": true, ".mts": true, ".cts": true, ".rs": true, ".java": true,
		".kt": true, ".kts": true, ".cs": true, ".rb": true,
	}
	agentFilePattern        = regexp.MustCompile(`(?i)(^|[._-])(agents?|crew)([._-]|$)`)
	kubernetesKindPattern   = regexp.MustCompile(`(?mi)^\s*kind:\s*([a-z][a-z0-9]*)\s*(?:#.*)?$`)
	openAPIVersionPattern   = regexp.MustCompile(`^(?:3|[4-9]|[1-9][0-9]+)\.[0-9]+(?:\.[0-9]+)?(?:[-+].*)?$`)
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
		Locator: canonical, Authoritative: true,
	})
	attributes := map[string]any{"source_surface": "repository"}
	if options.RepositoryURL != "" {
		attributes["repository_url"] = options.RepositoryURL
	}
	if validCommit(options.CommitSHA) {
		attributes["commit_sha"] = strings.ToLower(options.CommitSHA)
	}
	repositoryID := b.AddEntity(discovery.KindRepository, canonical, filepath.Base(root), attributes, repoEvidence)

	state := scanState{
		options: options, builder: b, repositoryID: repositoryID, repositoryCanonical: canonical,
		frameworks: map[string]string{}, frameworkRefs: map[string]string{}, agentIDs: map[string]string{},
		ardCatalogs: map[string]string{},
	}
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
	state.linkARDReferences()
	b.Snapshot.Coverage.DetectorsRun = len(options.Pack.Frameworks) + 5
	return b.Finish()
}

type ardReference struct {
	fromLocator string
	toLocator   string
	evidenceRef string
}

type scanState struct {
	options             Options
	builder             *builder.Builder
	repositoryID        string
	repositoryCanonical string
	frameworks          map[string]string
	frameworkRefs       map[string]string
	agentIDs            map[string]string
	ardCatalogs         map[string]string
	ardReferences       []ardReference
	files               int
}

func (s *scanState) inspect(path string) {
	base := strings.ToLower(filepath.Base(path))
	extension := strings.ToLower(filepath.Ext(path))
	isManifest := manifestNames[base] || extension == ".csproj" || extension == ".fsproj"
	isSource := sourceExtensions[extension]
	isMCP := base == "mcp.json" || base == ".mcp.json" || strings.Contains(base, "mcp") && (extension == ".json" || extension == ".yaml" || extension == ".yml")
	isOpenAPI := strings.Contains(base, "openapi") || strings.Contains(base, "swagger")
	isArazzo := strings.Contains(base, "arazzo")
	isA2A := (base == "agent-card.json" || base == "agent.json" || strings.Contains(base, "a2a")) && (extension == ".json" || extension == ".yaml" || extension == ".yml")
	isAgent := agentFilePattern.MatchString(strings.TrimSuffix(base, extension)) && (extension == ".json" || extension == ".yaml" || extension == ".yml")
	isAgentInstructions := agentInstructionNames[base]
	isSkill := base == "skill.md"
	isARD := extension == ".json" && strings.Contains(base, "catalog")
	isExplicitARD := base == "ai-catalog.json" || strings.Contains(base, "ai-catalog") || strings.Contains(base, "agent-catalog")
	isARDReference := base == "robots.txt" || extension == ".html" || extension == ".htm"
	normalizedPath := strings.ToLower(filepath.ToSlash(path))
	isCustomAgent := strings.HasSuffix(base, ".agent.md") || extension == ".md" && (strings.Contains(normalizedPath, "/.github/agents/") || strings.Contains(normalizedPath, "/.claude/agents/"))
	isDeclarative := extension == ".yaml" || extension == ".yml" || extension == ".json" || extension == ".toml" || extension == ".tf" || extension == ".hcl"
	isDeploymentPath := strings.Contains(normalizedPath, "/k8s/") || strings.Contains(normalizedPath, "/kubernetes/") || strings.Contains(normalizedPath, "/helm/") || strings.Contains(normalizedPath, "/terraform/") || strings.Contains(normalizedPath, "/pulumi/") || strings.Contains(normalizedPath, "/cloudformation/")
	isDeployment := base == "dockerfile" || strings.HasPrefix(base, "docker-compose") || base == ".gitlab-ci.yml" || base == "bitbucket-pipelines.yml" || base == "azure-pipelines.yml" || base == "jenkinsfile" || strings.Contains(normalizedPath, "/.github/workflows/") || strings.Contains(normalizedPath, "/.circleci/") || strings.Contains(normalizedPath, "/.buildkite/") || isDeclarative && isDeploymentPath
	isOwners := base == "codeowners"
	if !isManifest && !isSource && !isMCP && !isOpenAPI && !isArazzo && !isA2A && !isAgent && !isCustomAgent && !isAgentInstructions && !isSkill && !isARD && !isARDReference && !isDeployment && !isOwners {
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
	if isMCP || isOpenAPI || isArazzo || isA2A || isAgent || isCustomAgent || isAgentInstructions || isSkill || isARD || isARDReference {
		method = "descriptor"
	}
	family := evidenceFamily(isManifest, isSource, isMCP, isOpenAPI, isArazzo, isA2A, isAgent || isCustomAgent, isAgentInstructions, isSkill, isDeployment)
	validARD := false
	if isARD {
		_, validErr := ard.Parse(data)
		validARD = validErr == nil
		if validARD {
			family = "publisher_declaration"
		}
	}
	ref := s.builder.AddEvidence(builder.Observation{
		DetectorID: "repo.artifacts", DetectorVersion: Version, Method: method, Family: family,
		Specificity: specificity(isSource), Locator: locator, ContentHash: discovery.ContentHash(data),
		Authoritative: validARD || authoritativeRepositoryArtifact(locator, data, isManifest, isMCP, isOpenAPI, isArazzo, isA2A, isAgent, isCustomAgent, isAgentInstructions, isSkill, isDeployment, isOwners),
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
	if isCustomAgent {
		s.detectCustomAgent(locator, data, ref)
	}
	if isAgentInstructions {
		s.detectAgentInstructions(locator, ref)
	}
	if isSkill {
		s.detectSkill(locator, data, ref)
	}
	if isARD {
		s.detectARD(locator, data, ref, isExplicitARD)
	}
	if isARDReference {
		s.detectARDReferences(locator, data, ref)
	}
	if isDeployment {
		s.detectDeployment(locator, base, data, ref)
	}
	if isOwners {
		s.detectOwners(data, ref)
	}
}

func (s *scanState) detectARD(locator string, data []byte, ref string, explicit bool) {
	result, err := ard.Parse(data)
	if err != nil {
		if explicit {
			s.builder.Error("repo.ard", "invalid_ard_catalog", "ARD catalog could not be parsed at "+locator, false)
		}
		return
	}
	name := result.Catalog.Host.DisplayName
	if name == "" {
		name = "ARD Catalog"
	}
	catalogID := s.builder.AddEntity(discovery.KindCatalog, "ard:catalog:"+s.repositoryCanonical+":"+locator, name, map[string]any{
		"ard_spec_version": result.Catalog.SpecVersion, "source_surface": "repository", "defined": true, "locator": locator,
	}, ref)
	s.ardCatalogs[slashpath.Clean(locator)] = catalogID
	s.builder.AddRelationship(discovery.RelationshipDefinedIn, catalogID, s.repositoryID, map[string]any{"locator": locator}, ref)
	ard.AddToBuilder(s.builder, catalogID, s.repositoryID, result.Catalog, ref, "repository")
	for _, warning := range result.Warnings {
		s.builder.Error("repo.ard", "invalid_ard_entry", warning, false)
	}
}

func (s *scanState) detectARDReferences(locator string, data []byte, ref string) {
	references := []string{}
	if strings.EqualFold(slashpath.Base(locator), "robots.txt") {
		scanner := bufio.NewScanner(strings.NewReader(string(data)))
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			key, value, found := strings.Cut(line, ":")
			if found && strings.EqualFold(strings.TrimSpace(key), "Agentmap") {
				references = append(references, strings.TrimSpace(value))
			}
		}
	} else {
		tokenizer := html.NewTokenizer(strings.NewReader(string(data)))
		for {
			tokenType := tokenizer.Next()
			if tokenType == html.ErrorToken {
				break
			}
			if tokenType != html.StartTagToken && tokenType != html.SelfClosingTagToken {
				continue
			}
			token := tokenizer.Token()
			if !strings.EqualFold(token.Data, "link") {
				continue
			}
			rel, href := "", ""
			for _, attribute := range token.Attr {
				switch strings.ToLower(attribute.Key) {
				case "rel":
					rel = attribute.Val
				case "href":
					href = attribute.Val
				}
			}
			for _, value := range strings.Fields(rel) {
				if strings.EqualFold(value, "ai-catalog") {
					references = append(references, href)
					break
				}
			}
		}
	}
	for _, raw := range references {
		if target, ok := localARDReference(locator, raw); ok {
			s.ardReferences = append(s.ardReferences, ardReference{fromLocator: locator, toLocator: target, evidenceRef: ref})
		}
	}
}

func localARDReference(fromLocator, raw string) (string, bool) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme != "" || parsed.Host != "" || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Path == "" {
		return "", false
	}
	var target string
	if strings.HasPrefix(parsed.Path, "/") {
		target = strings.TrimPrefix(parsed.Path, "/")
	} else {
		target = slashpath.Join(slashpath.Dir(fromLocator), parsed.Path)
	}
	target = slashpath.Clean(target)
	if target == "." || target == ".." || strings.HasPrefix(target, "../") {
		return "", false
	}
	return target, true
}

func (s *scanState) linkARDReferences() {
	for _, reference := range s.ardReferences {
		catalogID, exists := s.ardCatalogs[reference.toLocator]
		if !exists {
			continue
		}
		s.builder.AddRelationship(discovery.RelationshipReferences, s.repositoryID, catalogID, map[string]any{
			"source_surface": "repository", "locator": reference.fromLocator, "target_locator": reference.toLocator,
		}, reference.evidenceRef)
	}
}

var agentInstructionNames = map[string]bool{"agents.md": true, "claude.md": true, "gemini.md": true, "antigravity.md": true, ".clinerules": true, ".windsurfrules": true, ".cursorrules": true, ".roomodes": true}

func authoritativeRepositoryArtifact(locator string, data []byte, manifest, mcp, openapi, arazzo, a2a, agent, customAgent, instructions, skill, deployment, owners bool) bool {
	if manifest || instructions || deployment || owners {
		return true
	}
	if customAgent {
		_, valid := customAgentDefinition(locator, data)
		return valid
	}
	if skill {
		return skillconfig.Parse(data, filepath.Base(filepath.Dir(locator))).Valid
	}
	document, err := structuredDocument(data)
	if err != nil {
		return false
	}
	if mcp && len(mcpconfig.Find(document)) > 0 {
		return true
	}
	if openapi && isOpenAPIDocument(document) {
		return true
	}
	if arazzo && strings.HasPrefix(firstString(document, "arazzo"), "1.") {
		return true
	}
	if a2a && isA2AAgentCard(document) {
		return true
	}
	return agent && !isA2AAgentCard(document) && isAgentDefinition(document)
}

func (s *scanState) detectFrameworks(locator, content, ref string, isManifest, isSource bool) {
	lower := strings.ToLower(content)
	for _, signature := range s.options.Pack.Frameworks {
		matched := ""
		if isManifest {
			candidates := append([]string{}, signature.Packages...)
			candidates = append(candidates, signature.LanguagePackages[manifestLanguage(locator)]...)
			for _, packageName := range candidates {
				if manifestPackageMatch(locator, content, packageName) {
					matched = packageName
					break
				}
			}
		}
		if matched == "" && isSource {
			candidates := append([]string{}, signature.Imports...)
			candidates = append(candidates, signature.LanguageImports[sourceLanguageForExtension(strings.ToLower(filepath.Ext(locator)))]...)
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
			frameworkID = s.builder.AddEntity(discovery.KindFramework, "framework:"+signature.ID, signature.Name, map[string]any{"detected_in_repository": true, "source_surface": "repository"}, ref)
			s.frameworks[signature.ID] = frameworkID
		}
		s.frameworkRefs[signature.ID] = ref
		s.builder.AddRelationship(discovery.RelationshipUses, s.repositoryID, frameworkID, map[string]any{"locator": locator}, ref)
		for _, agentID := range s.agentIDs {
			s.builder.AddRelationship(discovery.RelationshipUses, agentID, frameworkID, map[string]any{"source_surface": "repository"}, ref)
		}
	}
}

func sourceLanguageForExtension(extension string) string {
	switch extension {
	case ".py":
		return "python"
	case ".js", ".jsx", ".ts", ".tsx", ".mjs", ".cjs", ".mts", ".cts":
		return "javascript"
	case ".go":
		return "go"
	case ".rs":
		return "rust"
	case ".java", ".kt", ".kts":
		return "jvm"
	case ".cs":
		return "dotnet"
	case ".rb":
		return "ruby"
	default:
		return ""
	}
}

func manifestLanguage(locator string) string {
	base := strings.ToLower(filepath.Base(locator))
	extension := strings.ToLower(filepath.Ext(locator))
	switch {
	case base == "package.json", base == "package-lock.json", base == "pnpm-lock.yaml", base == "yarn.lock":
		return "javascript"
	case base == "requirements.txt", base == "pyproject.toml", base == "poetry.lock", base == "uv.lock", base == "pipfile":
		return "python"
	case base == "go.mod", base == "go.sum":
		return "go"
	case base == "cargo.toml", base == "cargo.lock":
		return "rust"
	case base == "gemfile":
		return "ruby"
	case base == "pom.xml", base == "build.gradle", base == "build.gradle.kts", base == "gradle.lockfile":
		return "jvm"
	case base == "packages.lock.json", base == "directory.packages.props", extension == ".csproj", extension == ".fsproj":
		return "dotnet"
	default:
		return ""
	}
}

func manifestPackageMatch(locator, content, name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	switch strings.ToLower(filepath.Base(locator)) {
	case "package.json":
		return npmManifestDependency(content, name)
	case "package-lock.json":
		return npmLockDependency(content, name)
	default:
		return packageMatch(strings.ToLower(content), name)
	}
}

func npmManifestDependency(content, name string) bool {
	var document map[string]any
	if json.Unmarshal([]byte(content), &document) != nil {
		return false
	}
	for _, key := range []string{"dependencies", "devDependencies", "peerDependencies", "optionalDependencies", "bundledDependencies", "bundleDependencies"} {
		if dependencyCollectionContains(document[key], name) {
			return true
		}
	}
	return false
}

func npmLockDependency(content, name string) bool {
	var document map[string]any
	if json.Unmarshal([]byte(content), &document) != nil {
		return false
	}
	var walk func(any) bool
	walk = func(value any) bool {
		switch typed := value.(type) {
		case map[string]any:
			for key, child := range typed {
				normalized := strings.ToLower(key)
				if (normalized == "dependencies" || normalized == "devdependencies" || normalized == "peerdependencies" || normalized == "optionaldependencies") && dependencyCollectionContains(child, name) {
					return true
				}
				if strings.HasSuffix(normalized, "node_modules/"+name) {
					return true
				}
				if walk(child) {
					return true
				}
			}
		case []any:
			for _, child := range typed {
				if walk(child) {
					return true
				}
			}
		}
		return false
	}
	return walk(document)
}

func dependencyCollectionContains(value any, name string) bool {
	switch typed := value.(type) {
	case map[string]any:
		for candidate := range typed {
			if strings.EqualFold(strings.TrimSpace(candidate), name) {
				return true
			}
		}
	case []any:
		for _, candidate := range typed {
			if text, ok := candidate.(string); ok && strings.EqualFold(strings.TrimSpace(text), name) {
				return true
			}
		}
	}
	return false
}

func (s *scanState) detectAgent(locator string, data []byte, ref string) {
	document, err := structuredDocument(data)
	if err != nil || isA2AAgentCard(document) || !isAgentDefinition(document) {
		return
	}
	name := firstString(document, "name", "agent_name", "id")
	if name == "" || len(name) > 200 {
		name = strings.TrimSuffix(filepath.Base(locator), filepath.Ext(locator))
	}
	attributes := s.repositoryArtifactAttributes(locator, map[string]any{"defined": true, "descriptor": locator, "source_surface": "repository"})
	id := s.builder.AddEntity(discovery.KindAgent, "target:"+s.options.TargetID+":agent:"+locator, name, attributes, ref)
	s.agentIDs[id] = id
	s.builder.AddRelationship(discovery.RelationshipDefinedIn, id, s.repositoryID, nil, ref)
	for signatureID, frameworkID := range s.frameworks {
		s.builder.AddRelationship(discovery.RelationshipUses, id, frameworkID, map[string]any{"source_surface": "repository"}, ref, s.frameworkRefs[signatureID])
	}
}

func (s *scanState) detectCustomAgent(locator string, data []byte, ref string) {
	name, valid := customAgentDefinition(locator, data)
	if !valid {
		return
	}
	attributes := s.repositoryArtifactAttributes(locator, map[string]any{
		"defined": true, "descriptor": locator, "definition_format": "agent_markdown", "source_surface": "repository",
	})
	id := s.builder.AddEntity(discovery.KindAgent, "target:"+s.options.TargetID+":custom-agent:"+strings.ToLower(locator), name, attributes, ref)
	s.agentIDs[id] = id
	s.builder.AddRelationship(discovery.RelationshipDefinedIn, id, s.repositoryID, nil, ref)
	for signatureID, frameworkID := range s.frameworks {
		s.builder.AddRelationship(discovery.RelationshipUses, id, frameworkID, map[string]any{"source_surface": "repository"}, ref, s.frameworkRefs[signatureID])
	}
}

func customAgentDefinition(locator string, data []byte) (string, bool) {
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	body := strings.TrimSpace(text)
	declared := ""
	if strings.HasPrefix(text, "---\n") {
		end := strings.Index(text[4:], "\n---")
		if end < 0 {
			return "", false
		}
		var metadata map[string]any
		if yaml.Unmarshal([]byte(text[4:4+end]), &metadata) != nil {
			return "", false
		}
		declared, _ = metadata["name"].(string)
		declared = strings.TrimSpace(declared)
		body = strings.TrimSpace(text[4+end+4:])
	}
	name := declared
	if name == "" {
		if !strings.HasSuffix(strings.ToLower(filepath.Base(locator)), ".agent.md") {
			return "", false
		}
		name = strings.TrimSuffix(filepath.Base(locator), ".agent.md")
	}
	return name, name != "" && len(name) <= 200 && !strings.ContainsAny(name, "\r\n\x00") && body != ""
}

func (s *scanState) detectAgentInstructions(locator, ref string) {
	s.builder.AddEntity(discovery.KindRepository, s.repositoryCanonical, filepath.Base(s.options.Root), map[string]any{
		"agent_instructions_present": true,
		"agent_instruction_files":    []string{locator},
	}, ref)
}

func (s *scanState) detectSkill(locator string, data []byte, ref string) {
	directory := filepath.Base(filepath.Dir(locator))
	metadata := skillconfig.Parse(data, directory)
	if !metadata.Valid {
		return
	}
	attributes := s.repositoryArtifactAttributes(locator, map[string]any{"declared": true, "descriptor": locator, "descriptor_valid": true, "source_surface": "repository"})
	id := s.builder.AddEntity(discovery.KindSkill, "target:"+s.options.TargetID+":skill:"+strings.ToLower(locator), metadata.Name, attributes, ref)
	s.builder.AddRelationship(discovery.RelationshipDefinedIn, id, s.repositoryID, nil, ref)
}

func (s *scanState) detectMCP(locator string, data []byte, ref string) {
	document, err := structuredDocument(data)
	if err != nil {
		return
	}
	for _, server := range mcpconfig.Find(document) {
		attributes := s.repositoryArtifactAttributes(locator, map[string]any{"configured": true, "transport": server.Transport, "descriptor": locator, "source_surface": "repository"})
		canonical := "target:" + s.options.TargetID + ":mcp:" + strings.ToLower(server.Name)
		if server.URL != "" {
			if sanitized, sanitizeErr := discovery.SanitizeURL(server.URL); sanitizeErr == nil {
				attributes["endpoint"] = sanitized
				attributes["host"] = discovery.URLHost(sanitized)
				attributes["protocol_identity"] = sanitized
				canonical = "target:" + s.options.TargetID + ":mcp-url:" + sanitized
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
		id := s.builder.AddEntity(discovery.KindMCPServer, canonical, server.Name, attributes, ref)
		s.builder.AddRelationship(discovery.RelationshipConfiguredBy, id, s.repositoryID, nil, ref)
	}
}

func (s *scanState) detectOpenAPI(locator string, data []byte, ref string) {
	document, err := structuredDocument(data)
	if err != nil || !isOpenAPIDocument(document) {
		return
	}
	version := firstString(document, "openapi", "swagger")
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
	attributes := s.repositoryArtifactAttributes(locator, map[string]any{"document": locator, "openapi_version": version, "source_surface": "repository"})
	if host != "" {
		attributes["host"] = host
	}
	if len(servers) > 0 {
		attributes["servers"] = servers
		attributes["protocol_identity"] = servers[0]
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

func isOpenAPIDocument(document map[string]any) bool {
	if version := firstString(document, "openapi"); openAPIVersionPattern.MatchString(version) {
		return true
	}
	return firstString(document, "swagger") == "2.0"
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
		attributes := s.repositoryArtifactAttributes(locator, map[string]any{"document": locator, "arazzo_version": version, "source_surface": "repository"})
		id := s.builder.AddEntity(discovery.KindWorkflow, "target:"+s.options.TargetID+":workflow:"+locator+":"+name, name, attributes, ref)
		s.builder.AddRelationship(discovery.RelationshipDefinedIn, id, s.repositoryID, nil, ref)
	}
}

func (s *scanState) detectA2A(locator string, data []byte, ref string) {
	document, err := structuredDocument(data)
	if err != nil || !isA2AAgentCard(document) {
		return
	}
	name := firstString(document, "name")
	if name == "" || len(name) > 500 {
		name = "A2A agent at " + locator
	}
	attributes := s.repositoryArtifactAttributes(locator, map[string]any{"defined": true, "protocol": "a2a", "agent_card": true, "descriptor": locator, "source_surface": "repository"})
	canonical := "target:" + s.options.TargetID + ":a2a:" + locator
	if endpoint := a2aEndpoint(document); endpoint != "" {
		if sanitized, sanitizeErr := discovery.SanitizeURL(endpoint); sanitizeErr == nil {
			attributes["endpoint"] = sanitized
			attributes["host"] = discovery.URLHost(sanitized)
			attributes["protocol_identity"] = sanitized
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

func (s *scanState) repositoryArtifactAttributes(locator string, attributes map[string]any) map[string]any {
	attributes["repository_path"] = locator
	if s.options.RepositoryURL != "" {
		attributes["repository_url"] = s.options.RepositoryURL
	}
	return attributes
}

func isAgentDefinition(document map[string]any) bool {
	if len(document) == 0 {
		return false
	}
	if nested, ok := document["agent"].(map[string]any); ok {
		document = nested
	}
	for key := range document {
		switch normalizeDocumentKey(key) {
		case "model", "modelid", "llm", "tools", "instructions", "systemprompt", "role", "goal", "backstory", "memory":
			return true
		}
	}
	return false
}

func isA2AAgentCard(document map[string]any) bool {
	if firstString(document, "name") == "" || a2aEndpoint(document) == "" {
		return false
	}
	_, capabilities := document["capabilities"].(map[string]any)
	skills, hasSkills := document["skills"].([]any)
	return capabilities || hasSkills && len(skills) > 0 || firstString(document, "protocolVersion") != ""
}

func a2aEndpoint(document map[string]any) string {
	if endpoint := firstString(document, "url"); endpoint != "" {
		return endpoint
	}
	if interfaces, ok := document["supportedInterfaces"].([]any); ok {
		for _, raw := range interfaces {
			if item, ok := raw.(map[string]any); ok {
				if endpoint := firstString(item, "url"); endpoint != "" {
					return endpoint
				}
			}
		}
	}
	return ""
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

func normalizeDocumentKey(value string) string {
	value = strings.ToLower(value)
	value = strings.ReplaceAll(value, "_", "")
	value = strings.ReplaceAll(value, "-", "")
	return value
}

func packageMatch(content, name string) bool {
	if name == "" {
		return false
	}
	for offset := 0; offset < len(content); {
		index := strings.Index(content[offset:], name)
		if index < 0 {
			return false
		}
		index += offset
		beforeOK := index == 0 || !packageNameCharacter(content[index-1])
		after := index + len(name)
		afterOK := after == len(content) || !packageNameCharacter(content[after])
		if beforeOK && afterOK && dependencyDelimiter(content, after) {
			return true
		}
		offset = index + 1
	}
	return false
}

func packageNameCharacter(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= '0' && value <= '9' || value == '-' || value == '_' || value == '.' || value == '/'
}

func dependencyDelimiter(content string, index int) bool {
	for index < len(content) && (content[index] == ' ' || content[index] == '\t') {
		index++
	}
	if index >= len(content) {
		return true
	}
	switch content[index] {
	case '"', '\'', ':', '=', '<', '>', '~', '!', '[', ']', ',', ';', '@', '\n', '\r':
		return true
	}
	return false
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
		case ".js", ".jsx", ".ts", ".tsx", ".mjs", ".cjs", ".mts", ".cts":
			if (strings.HasPrefix(line, "import ") || strings.HasPrefix(line, "export ")) && containsModuleLiteral(line, name) {
				return true
			}
			if (tokenOutsideString(line, "require") || tokenOutsideString(line, "import")) && containsModuleLiteral(line, name) {
				return true
			}
		case ".go":
			if containsModuleLiteral(line, name) && (strings.HasPrefix(line, "import ") || strings.HasPrefix(line, "\"") || strings.HasPrefix(line, "`")) {
				return true
			}
		case ".rs":
			if strings.HasPrefix(line, "use "+name) || strings.HasPrefix(line, "extern crate "+name) {
				return true
			}
		case ".java", ".kt", ".kts":
			if strings.HasPrefix(line, "import "+name) {
				return true
			}
		case ".cs":
			if strings.HasPrefix(line, "using "+name) {
				return true
			}
		case ".rb":
			if strings.HasPrefix(line, "require ") && containsModuleLiteral(line, name) {
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

func evidenceFamily(manifest, source, mcp, openapi, arazzo, a2a, agent, instructions, skill, deployment bool) string {
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
	case instructions:
		return "agent_instructions"
	case skill:
		return "skill_descriptor"
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
