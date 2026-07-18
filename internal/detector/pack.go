package detector

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

type Pack struct {
	SchemaVersion string             `yaml:"schema_version" json:"schema_version"`
	ID            string             `yaml:"id" json:"id"`
	Version       string             `yaml:"version" json:"version"`
	Checksum      string             `yaml:"checksum,omitempty" json:"checksum,omitempty"`
	Runtimes      []RuntimeSignature `yaml:"runtimes" json:"runtimes"`
	Frameworks    []PackageSignature `yaml:"frameworks" json:"frameworks"`
	ModelCaches   []ModelCache       `yaml:"model_caches,omitempty" json:"model_caches,omitempty"`
	Listeners     []Listener         `yaml:"listeners,omitempty" json:"listeners,omitempty"`
}

type RuntimeSignature struct {
	ID              string   `yaml:"id" json:"id"`
	Name            string   `yaml:"name" json:"name"`
	Processes       []string `yaml:"processes,omitempty" json:"processes,omitempty"`
	Images          []string `yaml:"images,omitempty" json:"images,omitempty"`
	EnvironmentKeys []string `yaml:"environment_keys,omitempty" json:"environment_keys,omitempty"`
	Paths           []string `yaml:"paths,omitempty" json:"paths,omitempty"`
	Configs         []Config `yaml:"configs,omitempty" json:"configs,omitempty"`
	SkillRoots      []string `yaml:"skill_roots,omitempty" json:"skill_roots,omitempty"`
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
	ID       string   `yaml:"id" json:"id"`
	Name     string   `yaml:"name" json:"name"`
	Packages []string `yaml:"packages" json:"packages"`
	Imports  []string `yaml:"imports,omitempty" json:"imports,omitempty"`
}

// ModelCache describes a known, local model layout. Layout selects a built-in,
// non-executable parser; detector packs can only provide paths and metadata.
type ModelCache struct {
	ID     string   `yaml:"id" json:"id"`
	Name   string   `yaml:"name" json:"name"`
	Paths  []string `yaml:"paths" json:"paths"`
	Layout string   `yaml:"layout" json:"layout"`
}

type Listener struct {
	ID   string `yaml:"id" json:"id"`
	Name string `yaml:"name" json:"name"`
	Kind string `yaml:"kind" json:"kind"`
	Port int    `yaml:"port" json:"port"`
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
	if p.SchemaVersion != "1" || p.ID == "" || p.Version == "" {
		return fmt.Errorf("detector pack schema_version=1, id, and version are required")
	}
	seen := map[string]struct{}{}
	for _, runtime := range p.Runtimes {
		if runtime.ID == "" || runtime.Name == "" {
			return fmt.Errorf("runtime id and name are required")
		}
		if _, exists := seen[runtime.ID]; exists {
			return fmt.Errorf("duplicate detector id %q", runtime.ID)
		}
		seen[runtime.ID] = struct{}{}
		for _, config := range runtime.Configs {
			if config.Format != "json" && config.Format != "yaml" && config.Format != "yml" && config.Format != "toml" {
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
		if framework.ID == "" || framework.Name == "" || len(framework.Packages) == 0 {
			return fmt.Errorf("framework id, name, and packages are required")
		}
		if _, exists := seen[framework.ID]; exists {
			return fmt.Errorf("duplicate detector id %q", framework.ID)
		}
		seen[framework.ID] = struct{}{}
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
	for _, listener := range p.Listeners {
		if listener.ID == "" || listener.Name == "" || listener.Port < 1 || listener.Port > 65535 {
			return fmt.Errorf("listener id, name, and a valid port are required")
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

func (p Pack) CalculatedChecksum() string {
	p.Checksum = ""
	data, err := json.Marshal(p)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}
