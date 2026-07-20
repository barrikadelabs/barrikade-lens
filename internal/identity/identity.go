package identity

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const Version = 1

type State struct {
	Version    int    `json:"version"`
	HubOrigin  string `json:"hub_origin"`
	PublicKey  string `json:"public_key"`
	PrivateKey string `json:"private_key"`
}

func Path(configPath string) string {
	return filepath.Join(filepath.Dir(configPath), "identity.json")
}

func LoadOrCreate(path, hubOrigin string) (State, error) {
	if state, err := Load(path); err == nil {
		if subtle.ConstantTimeCompare([]byte(state.HubOrigin), []byte(hubOrigin)) != 1 {
			return State{}, fmt.Errorf("endpoint identity belongs to a different Lens Hub")
		}
		return state, nil
	} else if !os.IsNotExist(err) {
		return State{}, err
	}
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return State{}, err
	}
	state := State{Version: Version, HubOrigin: hubOrigin, PublicKey: base64.RawURLEncoding.EncodeToString(publicKey), PrivateKey: base64.RawURLEncoding.EncodeToString(privateKey)}
	if err := Save(path, state); err != nil {
		return State{}, err
	}
	return state, nil
}

func Load(path string) (State, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return State{}, err
	}
	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return State{}, err
	}
	if state.Version != Version || state.HubOrigin == "" {
		return State{}, fmt.Errorf("unsupported endpoint identity format")
	}
	publicKey, privateKey, err := state.Keys()
	if err != nil || !publicKey.Equal(privateKey.Public()) {
		return State{}, fmt.Errorf("endpoint identity keypair is invalid")
	}
	return state, nil
}

func Save(path string, state State) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, append(data, '\n'), 0o600); err != nil {
		return err
	}
	if err := protectFile(temporary); err != nil {
		return err
	}
	return os.Rename(temporary, path)
}

func (s State) Keys() (ed25519.PublicKey, ed25519.PrivateKey, error) {
	publicKey, err := base64.RawURLEncoding.DecodeString(s.PublicKey)
	if err != nil || len(publicKey) != ed25519.PublicKeySize {
		return nil, nil, fmt.Errorf("invalid endpoint public key")
	}
	privateKey, err := base64.RawURLEncoding.DecodeString(s.PrivateKey)
	if err != nil || len(privateKey) != ed25519.PrivateKeySize {
		return nil, nil, fmt.Errorf("invalid endpoint private key")
	}
	return ed25519.PublicKey(publicKey), ed25519.PrivateKey(privateKey), nil
}

func (s State) Sign(code, hostname, platform, architecture, collectorVersion string) (string, error) {
	_, privateKey, err := s.Keys()
	if err != nil {
		return "", err
	}
	signature := ed25519.Sign(privateKey, ProofMessage(code, hostname, platform, architecture, collectorVersion))
	return base64.RawURLEncoding.EncodeToString(signature), nil
}

func Verify(publicKeyEncoded, proof, code, hostname, platform, architecture, collectorVersion string) error {
	publicKey, err := base64.RawURLEncoding.DecodeString(publicKeyEncoded)
	if err != nil || len(publicKey) != ed25519.PublicKeySize {
		return fmt.Errorf("invalid endpoint public key")
	}
	signature, err := base64.RawURLEncoding.DecodeString(proof)
	if err != nil || len(signature) != ed25519.SignatureSize || !ed25519.Verify(ed25519.PublicKey(publicKey), ProofMessage(code, hostname, platform, architecture, collectorVersion), signature) {
		return fmt.Errorf("invalid endpoint identity proof")
	}
	return nil
}

func ProofMessage(code, hostname, platform, architecture, collectorVersion string) []byte {
	return []byte(strings.Join([]string{"lens-enrollment-v1", strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(code), "-", "")), hostname, platform, architecture, collectorVersion}, "\x00"))
}

func Fingerprint(publicKeyEncoded string) (string, error) {
	publicKey, err := base64.RawURLEncoding.DecodeString(publicKeyEncoded)
	if err != nil || len(publicKey) != ed25519.PublicKeySize {
		return "", fmt.Errorf("invalid endpoint public key")
	}
	digest := sha256.Sum256(publicKey)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}
