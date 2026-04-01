package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// AuthStack identifies which authentication stack the container uses.
type AuthStack string

const (
	AuthStackIntune     AuthStack = "intune"
	AuthStackHimmelblau AuthStack = "himmelblau"
)

// ValidAuthStacks returns the list of recognised auth stack values.
func ValidAuthStacks() []AuthStack {
	return []AuthStack{AuthStackIntune, AuthStackHimmelblau}
}

// IsValid returns true if the auth stack value is recognised.
func (a AuthStack) IsValid() bool {
	for _, v := range ValidAuthStacks() {
		if a == v {
			return true
		}
	}
	return false
}

type Config struct {
	MachineName      string    `toml:"machine_name"`
	RootfsPath       string    `toml:"rootfs_path"`
	HostUID          int       `toml:"host_uid"`
	HostUser         string    `toml:"host_user"`
	BrokerProxy      bool      `toml:"broker_proxy"`
	Insiders         bool      `toml:"insiders"`
	AuthStack        AuthStack `toml:"auth_stack"`
	HimmelblauDomain string    `toml:"himmelblau_domain,omitempty"`
	HimmelblauEmail  string    `toml:"himmelblau_email,omitempty"`
}

func DefaultRoot() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot determine home directory: %w", err)
	}
	return filepath.Join(home, ".local", "share", "intuneme"), nil
}

func Load(root string) (*Config, error) {
	cfg := &Config{
		MachineName: "intuneme",
		RootfsPath:  filepath.Join(root, "rootfs"),
		HostUID:     os.Getuid(),
		HostUser:    os.Getenv("USER"),
		AuthStack:   AuthStackIntune,
	}

	path := filepath.Join(root, "config.toml")
	if _, err := os.Stat(path); err == nil {
		if _, err := toml.DecodeFile(path, cfg); err != nil {
			return nil, err
		}
		// Ensure rootfs_path default if not in file
		if cfg.RootfsPath == "" {
			cfg.RootfsPath = filepath.Join(root, "rootfs")
		}
		// Default to intune for existing configs that predate the auth_stack field.
		if cfg.AuthStack == "" {
			cfg.AuthStack = AuthStackIntune
		}
	}

	return cfg, nil
}

func (c *Config) Save(root string) error {
	path := filepath.Join(root, "config.toml")
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	return toml.NewEncoder(f).Encode(c)
}
