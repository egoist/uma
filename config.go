package main

import (
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// config is what the app remembers between launches. The API key is kept
// in the macOS keychain, elsewhere in the file, readable by the user alone.
type config struct {
	Endpoint string   `json:"endpoint"`
	Key      string   `json:"key,omitempty"`
	Range    int      `json:"range"`
	HideLive bool     `json:"hideLive,omitempty"` // no count of live visitors in the menu bar
	Hidden   []string `json:"hidden,omitempty"`   // IDs of websites not shown
	// The user's order of websites, by ID, used while CustomOrder is on;
	// off, those with the most visitors come first.
	Order       []string `json:"order,omitempty"`
	CustomOrder bool     `json:"customOrder,omitempty"`
}

const keychainService = "uma: Umami API key"

func loadConfig(dir string) config {
	cfg := config{Endpoint: cloudEndpoint, Range: 0}
	if b, err := os.ReadFile(filepath.Join(dir, "config.json")); err == nil {
		_ = json.Unmarshal(b, &cfg)
	}
	if runtime.GOOS == "darwin" && cfg.Key == "" {
		out, err := exec.Command("security", "find-generic-password", "-s", keychainService, "-w").Output()
		if err == nil {
			cfg.Key = strings.TrimSpace(string(out))
		}
	}
	return cfg
}

func saveConfig(dir string, cfg config) error {
	if runtime.GOOS == "darwin" {
		if err := keychainStore(cfg.Key); err == nil {
			cfg.Key = ""
		}
	}
	return writeConfig(dir, cfg)
}

// writeConfig writes the file alone.
func writeConfig(dir string, cfg config) error {
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp := filepath.Join(dir, "config.json.tmp")
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, "config.json"))
}

func keychainStore(key string) error {
	if key == "" {
		err := exec.Command("security", "delete-generic-password", "-s", keychainService).Run()
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return nil // there was none
		}
		return err
	}
	return exec.Command("security", "add-generic-password", "-U", "-a", os.Getenv("USER"),
		"-s", keychainService, "-w", key).Run()
}

// normalizeEndpoint completes what the user typed: a scheme, and the /api
// of a server of their own given by its address alone.
func normalizeEndpoint(s string) string {
	s = strings.TrimRight(strings.TrimSpace(s), "/")
	if s == "" {
		return cloudEndpoint
	}
	if !strings.Contains(s, "://") {
		s = "https://" + s
	}
	u, err := url.Parse(s)
	if err != nil || u.Host == "" {
		return s
	}
	if u.Host == "cloud.umami.is" {
		return cloudEndpoint
	}
	if u.Host != "api.umami.is" && u.Path == "" {
		u.Path = "/api"
	}
	return u.String()
}
