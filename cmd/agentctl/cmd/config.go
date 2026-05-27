// Config persistence + the `login` command.
//
// The file lives at $XDG_CONFIG_HOME/ailab/config.json (or ~/.config/ailab/...).
// It's a tiny JSON document; we deliberately don't depend on viper here.

package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

// Config is the on-disk shape.
type Config struct {
	API   string `json:"api"`
	Token string `json:"token"`
}

func configPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "ailab", "config.json"), nil
}

// LoadConfig reads ~/.config/ailab/config.json. Missing file returns a zero Config.
func LoadConfig() (Config, error) {
	path, err := configPath()
	if err != nil {
		return Config{}, err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Config{}, nil
		}
		return Config{}, err
	}
	var c Config
	if err := json.Unmarshal(b, &c); err != nil {
		return Config{}, err
	}
	return c, nil
}

// SaveConfig writes ~/.config/ailab/config.json with permissions 0600.
func SaveConfig(c Config) error {
	path, err := configPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o600)
}

func loginCmd() *cobra.Command {
	var api, token string
	c := &cobra.Command{
		Use:   "login",
		Short: "Save API URL and bearer token to the local config",
		RunE: func(cmd *cobra.Command, args []string) error {
			if token == "" {
				return fmt.Errorf("--token is required")
			}
			if api == "" {
				api = "http://localhost:8080"
			}
			cfg := Config{API: api, Token: token}
			if err := SaveConfig(cfg); err != nil {
				return err
			}
			path, _ := configPath()
			fmt.Fprintf(cmd.OutOrStdout(), "saved to %s\n", path)
			return nil
		},
	}
	c.Flags().StringVar(&api, "api", "", "platform API base URL")
	c.Flags().StringVar(&token, "token", "", "bearer token (required)")
	return c
}
