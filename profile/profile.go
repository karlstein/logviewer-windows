// Package profile manages SSH connection profiles.
package profile

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const maxProfiles = 50

// Profile represents an SSH connection profile.
type Profile struct {
	Name     string `json:"name"`
	Host     string `json:"host"`
	Port     string `json:"port"`
	User     string `json:"user"`
	Password string `json:"password,omitempty"`
	KeyPath  string `json:"key_path,omitempty"`
}

// Validate checks that the profile has the minimum required fields.
func (p Profile) Validate() error {
	if p.Name == "" {
		return fmt.Errorf("profile name is required")
	}
	if p.Host == "" {
		return fmt.Errorf("host is required")
	}
	if p.Port == "" {
		return fmt.Errorf("port is required")
	}
	if p.User == "" {
		return fmt.Errorf("user is required")
	}
	if p.Password == "" && p.KeyPath == "" {
		return fmt.Errorf("password or SSH key path is required")
	}
	return nil
}

// profilesPath returns the path to the profiles JSON file.
func profilesPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot determine home directory: %w", err)
	}
	dir := filepath.Join(home, ".logviewer")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", fmt.Errorf("cannot create config directory: %w", err)
	}
	return filepath.Join(dir, "profiles.json"), nil
}

// Load reads all profiles from disk.
func Load() ([]Profile, error) {
	path, err := profilesPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return []Profile{}, nil
		}
		return nil, fmt.Errorf("cannot read profiles: %w", err)
	}
	var profiles []Profile
	if err := json.Unmarshal(data, &profiles); err != nil {
		return nil, fmt.Errorf("cannot parse profiles: %w", err)
	}
	return profiles, nil
}

// Save writes all profiles to disk.
func Save(profiles []Profile) error {
	if len(profiles) > maxProfiles {
		return fmt.Errorf("too many profiles (max %d)", maxProfiles)
	}
	path, err := profilesPath()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(profiles, "", "  ")
	if err != nil {
		return fmt.Errorf("cannot marshal profiles: %w", err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("cannot write profiles: %w", err)
	}
	return nil
}

// Add appends a new profile and persists. Returns an error if a profile
// with the same name already exists.
func Add(profiles []Profile, p Profile) ([]Profile, error) {
	for _, existing := range profiles {
		if existing.Name == p.Name {
			return nil, fmt.Errorf("profile %q already exists", p.Name)
		}
	}
	profiles = append(profiles, p)
	if err := Save(profiles); err != nil {
		return nil, err
	}
	return profiles, nil
}

// Update replaces a profile at the given index and persists.
func Update(profiles []Profile, idx int, p Profile) ([]Profile, error) {
	if idx < 0 || idx >= len(profiles) {
		return nil, fmt.Errorf("invalid profile index %d", idx)
	}
	// Check name uniqueness (skip self)
	for i, existing := range profiles {
		if i != idx && existing.Name == p.Name {
			return nil, fmt.Errorf("profile %q already exists", p.Name)
		}
	}
	profiles[idx] = p
	if err := Save(profiles); err != nil {
		return nil, err
	}
	return profiles, nil
}

// Delete removes a profile at the given index and persists.
func Delete(profiles []Profile, idx int) ([]Profile, error) {
	if idx < 0 || idx >= len(profiles) {
		return nil, fmt.Errorf("invalid profile index %d", idx)
	}
	profiles = append(profiles[:idx], profiles[idx+1:]...)
	if err := Save(profiles); err != nil {
		return nil, err
	}
	return profiles, nil
}

// SSHAddr returns the host:port string for SSH dialing.
func (p Profile) SSHAddr() string {
	if p.Port == "" {
		return p.Host + ":22"
	}
	return p.Host + ":" + p.Port
}
