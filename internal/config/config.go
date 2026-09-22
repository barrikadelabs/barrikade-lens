package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

type Config struct {
	ConfigVersion        int    `json:"config_version"`
	HubURL               string `json:"hub_url"`
	OrganizationID       string `json:"organization_id"`
	SourceID             string `json:"source_id"`
	TargetID             string `json:"target_id"`
	AccessToken          string `json:"access_token,omitempty"`
	AccessTokenExpiresAt string `json:"access_token_expires_at,omitempty"`
	RefreshToken         string `json:"refresh_token,omitempty"`
	Sequence             uint64 `json:"sequence,omitempty"`
}

func Path() (string, error) {
	if explicit := os.Getenv("BARRIKADE_LENS_CONFIG"); explicit != "" {
		return explicit, nil
	}
	if runtime.GOOS == "windows" {
		root := os.Getenv("ProgramData")
		if root == "" {
			root = os.Getenv("APPDATA")
		}
		if root == "" {
			return "", fmt.Errorf("ProgramData and APPDATA are unavailable")
		}
		return filepath.Join(root, "Barrikade", "Lens", "config.json"), nil
	}
	root, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "barrikade-lens", "config.json"), nil
}

func Load(path string) (Config, error) {
	if path == "" {
		var err error
		path, err = Path()
		if err != nil {
			return Config{}, err
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	var value Config
	if err := json.Unmarshal(data, &value); err != nil {
		return Config{}, err
	}
	return value, nil
}

func Save(path string, value Config) error {
	if path == "" {
		var err error
		path, err = Path()
		if err != nil {
			return err
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".config-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(append(data, '\n')); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := protectFile(temporaryPath); err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}
