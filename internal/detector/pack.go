package detector

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

type Pack struct {
	SchemaVersion  string             `yaml:"schema_version" json:"schema_version"`
	ID             string             `yaml:"id" json:"id"`
	Version        string             `yaml:"version" json:"version"`
	Checksum       string             `yaml:"checksum,omitempty" json:"checksum,omitempty"`
	Runtimes       []RuntimeSignature `yaml:"runtimes" json:"runtimes"`
	Frameworks     []PackageSignature `yaml:"frameworks" json:"frameworks"`
	ModelCaches    []ModelCache       `yaml:"model_caches,omitempty" json:"model_caches,omitempty"`
	SkillRoots     []SkillRoot        `yaml:"skill_roots,omitempty" json:"skill_roots,omitempty"`
	ExtensionRoots []string           `yaml:"extension_roots,omitempty" json:"extension_roots,omitempty"`
	Listeners      []Listener         `yaml:"listeners,omitempty" json:"listeners,omitempty"`
}

type RuntimeSignature struct {
	ID              string   `yaml:"id" json:"id"`
	Name            string   `yaml:"name" json:"name"`
	Category        string   `yaml:"category" json:"category"`
	Processes       []string `yaml:"processes,omitempty" json:"processes,omitempty"`
	Images          []string `yaml:"images,omitempty" json:"images,omitempty"`
	EnvironmentKeys []string `yaml:"environment_keys,omitempty" json:"environment_keys,omitempty"`
	ExtensionIDs    []string `yaml:"extension_ids,omitempty" json:"extension_ids,omitempty"`
	InstallPaths    []string `yaml:"install_paths,omitempty" json:"install_paths,omitempty"`
	Paths           []string `yaml:"paths,omitempty" json:"paths,omitempty"`
	Configs         []Config `yaml:"configs,omitempty" json:"configs,omitempty"`
	SkillRoots      []string `yaml:"skill_roots,omitempty" json:"skill_roots,omitempty"`
	AgentRoots      []string `yaml:"agent_roots,omitempty" json:"agent_roots,omitempty"`
	ModelServers    []Port   `yaml:"model_servers,omitempty" json:"model_servers,omitempty"`
}

type Config struct {
	Path   string `yaml:"path" json:"path"`
	Format string `yaml:"format" json:"format"`
	Scope  string `yaml:"scope" json:"scope"`
}

type Port struct {
	Name string `yaml:"name" json:"name"`
	Port int    `yaml:"port" json:"port"`
}

type PackageSignature struct {
	ID               string              `yaml:"id" json:"id"`
	Name             string              `yaml:"name" json:"name"`
	Packages         []string            `yaml:"packages,omitempty" json:"packages,omitempty"`
	LanguagePackages map[string][]string `yaml:"language_packages,omitempty" json:"language_packages,omitempty"`
	Imports          []string            `yaml:"imports,omitempty" json:"imports,omitempty"`
	LanguageImports  map[string][]string `yaml:"language_imports,omitempty" json:"language_imports,omitempty"`
}

// ModelCache describes a known, local model layout. Layout selects a built-in,
// non-executable parser; detector packs can only provide paths and metadata.
type ModelCache struct {
	ID     string   `yaml:"id" json:"id"`
	Name   string   `yaml:"name" json:"name"`
	Paths  []string `yaml:"paths" json:"paths"`
	Layout string   `yaml:"layout" json:"layout"`
}

// SkillRoot describes a non-vendor-specific location containing open
// Agent Skills descriptors. The collector reads only SKILL.md metadata.
type SkillRoot struct {
	ID    string   `yaml:"id" json:"id"`
	Name  string   `yaml:"name" json:"name"`
	Paths []string `yaml:"paths" json:"paths"`
	Scope string   `yaml:"scope" json:"scope"`
}

type Listener struct {
	ID        string   `yaml:"id" json:"id"`
	Name      string   `yaml:"name" json:"name"`
	Kind      string   `yaml:"kind" json:"kind"`
	Port      int      `yaml:"port" json:"port"`
	Processes []string `yaml:"processes" json:"processes"`
}

func Load(path string) (Pack, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Pack{}, err
	}
	var pack Pack
	if err := yaml.Unmarshal(data, &pack); err != nil {
		return Pack{}, err
	}
	if err := pack.Validate(); err != nil {
		return Pack{}, err
	}
	return pack, nil
}

func (p Pack) Validate() error {
	if p.SchemaVersion != "2" || p.ID == "" || p.Version == "" {
		return fmt.Errorf("detector pack schema_version=2, id, and version are required")
	}
	seen := map[string]struct{}{}
	extensionOwners := map[string]string{}
	for _, runtime := range p.Runtimes {
		if runtime.ID == "" || runtime.Name == "" || !runtimeCategory(runtime.Category) {
			return fmt.Errorf("runtime id, name, and a supported category are required")
		}
		if _, exists := seen[runtime.ID]; exists {
			return fmt.Errorf("duplicate detector id %q", runtime.ID)
		}
		seen[runtime.ID] = struct{}{}
		for _, rawID := range runtime.ExtensionIDs {
			extensionID := strings.ToLower(strings.TrimSpace(rawID))
			if extensionID == "" {
				return fmt.Errorf("runtime %q has an empty extension id", runtime.ID)
			}
			if owner, exists := extensionOwners[extensionID]; exists && owner != runtime.ID {
				return fmt.Errorf("extension id %q belongs to both %q and %q", extensionID, owner, runtime.ID)
			}
			extensionOwners[extensionID] = runtime.ID
		}
		for _, config := range runtime.Configs {
			if config.Format != "json" && config.Format != "jsonc" && config.Format != "yaml" && config.Format != "yml" && config.Format != "toml" {
				return fmt.Errorf("runtime %q has unsupported configuration format %q", runtime.ID, config.Format)
			}
			if config.Scope != "user" && config.Scope != "project" && config.Scope != "system" {
				return fmt.Errorf("runtime %q has unsupported configuration scope %q", runtime.ID, config.Scope)
			}
		}
		for _, server := range runtime.ModelServers {
			if server.Name == "" || server.Port < 1 || server.Port > 65535 {
				return fmt.Errorf("runtime %q has an invalid model server", runtime.ID)
			}
		}
	}
	for _, framework := range p.Frameworks {
		if framework.ID == "" || framework.Name == "" || len(framework.Packages) == 0 && len(framework.LanguagePackages) == 0 {
			return fmt.Errorf("framework id, name, and package signatures are required")
		}
		if _, exists := seen[framework.ID]; exists {
			return fmt.Errorf("duplicate detector id %q", framework.ID)
		}
		seen[framework.ID] = struct{}{}
		for language, packages := range framework.LanguagePackages {
			if !sourceLanguage(language) || len(packages) == 0 {
				return fmt.Errorf("framework %q has unsupported or empty language packages for %q", framework.ID, language)
			}
		}
		for language, imports := range framework.LanguageImports {
			if !sourceLanguage(language) || len(imports) == 0 {
				return fmt.Errorf("framework %q has unsupported or empty language imports for %q", framework.ID, language)
			}
		}
	}
	for _, cache := range p.ModelCaches {
		if cache.ID == "" || cache.Name == "" || len(cache.Paths) == 0 {
			return fmt.Errorf("model cache id, name, and paths are required")
		}
		if cache.Layout != "huggingface" && cache.Layout != "ollama" && cache.Layout != "lm-studio" && cache.Layout != "directory" {
			return fmt.Errorf("model cache %q has unsupported layout %q", cache.ID, cache.Layout)
		}
		if _, exists := seen[cache.ID]; exists {
			return fmt.Errorf("duplicate detector id %q", cache.ID)
		}
		seen[cache.ID] = struct{}{}
	}
	for _, root := range p.SkillRoots {
		if root.ID == "" || root.Name == "" || len(root.Paths) == 0 {
			return fmt.Errorf("skill root id, name, and paths are required")
		}
		if root.Scope != "user" && root.Scope != "project" && root.Scope != "system" {
			return fmt.Errorf("skill root %q has unsupported scope %q", root.ID, root.Scope)
		}
		if _, exists := seen[root.ID]; exists {
			return fmt.Errorf("duplicate detector id %q", root.ID)
		}
		seen[root.ID] = struct{}{}
	}
	for _, root := range p.ExtensionRoots {
		if strings.TrimSpace(root) == "" {
			return fmt.Errorf("extension roots cannot contain an empty path")
		}
	}
	for _, listener := range p.Listeners {
		if listener.ID == "" || listener.Name == "" || listener.Port < 1 || listener.Port > 65535 || len(listener.Processes) == 0 {
			return fmt.Errorf("listener id, name, matching processes, and a valid port are required")
		}
		if listener.Kind != "model_server" && listener.Kind != "mcp_server" && listener.Kind != "api_service" {
			return fmt.Errorf("listener %q has unsupported kind %q", listener.ID, listener.Kind)
		}
		if _, exists := seen[listener.ID]; exists {
			return fmt.Errorf("duplicate detector id %q", listener.ID)
		}
		seen[listener.ID] = struct{}{}
	}
	if p.Checksum != "" && p.Checksum != p.CalculatedChecksum() {
		return fmt.Errorf("detector pack checksum does not match its contents")
	}
	return nil
}

func runtimeCategory(value string) bool {
	switch value {
	case "agent_tool", "model_runtime", "host_application", "development_runtime", "unclassified":
		return true
	default:
		return false
	}
}

func sourceLanguage(value string) bool {
	switch value {
	case "python", "javascript", "go", "rust", "jvm", "dotnet", "ruby":
		return true
	default:
		return false
	}
}

func (p Pack) CalculatedChecksum() string {
	p.Checksum = ""
	data, err := json.Marshal(p)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}
