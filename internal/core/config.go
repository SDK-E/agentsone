// SPDX-License-Identifier: MIT

package core

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/pelletier/go-toml/v2"
)

// Fixed paths inside the VM (Linux file layout and systemd conventions).
const (
	ConfigPath  = "/etc/agentsone/config.toml"
	StateDir    = "/var/lib/agentsone"
	LibDir      = "/usr/lib/agentsone"
	RuntimeDir  = "/run/agentsone"
	AgentsHome  = "/home/agents"
	DefaultAddr = "127.0.0.1:7700"
)

// Config is /etc/agentsone/config.toml. Every field has a default, so an
// absent file is valid. Work packages add their own section struct here
// only through a contract change.
type Config struct {
	// StateDir holds the databases and plugins.
	StateDir string `toml:"state_dir"`
	// RuntimeDir holds the API socket and runner sockets.
	RuntimeDir string `toml:"runtime_dir"`
	// LibDir holds binaries and the interface.
	LibDir string `toml:"lib_dir"`
	// AgentsHome holds custom agents' home directories.
	AgentsHome string `toml:"agents_home"`

	API    APIConfig    `toml:"api"`
	Log    LogConfig    `toml:"log"`
	Budget BudgetConfig `toml:"budget"`

	// sections holds module tables, read with Section.
	sections map[string]any
}

// APIConfig configures listeners.
type APIConfig struct {
	// Addr is the loopback TCP listener for the interface.
	Addr string `toml:"addr"`
	// Socket overrides the Unix socket path; empty means RuntimeDir/api.sock.
	Socket string `toml:"socket"`
	// PublicURL is the origin humans open the interface at, such as
	// https://agents.example.com (scheme and host, no path). When set, only
	// this origin may send state-changing browser requests. Empty means the
	// address the web door was reached at, as the edge reports it.
	PublicURL string `toml:"public_url"`
}

// LogConfig configures logging.
type LogConfig struct {
	// Level is debug, info, warn or error.
	Level string `toml:"level"`
	// Format is text or json.
	Format string `toml:"format"`
}

// BudgetConfig configures budget checks.
type BudgetConfig struct {
	// CheckInterval is how often usage is checked against allocations.
	CheckInterval Duration `toml:"check_interval"`
}

// Duration is a time.Duration written as a Go duration string ("30s").
type Duration struct{ time.Duration }

// MarshalText writes the duration string.
func (d Duration) MarshalText() ([]byte, error) { return []byte(d.String()), nil }

// UnmarshalText parses a duration string.
func (d *Duration) UnmarshalText(b []byte) error {
	v, err := time.ParseDuration(string(b))
	if err != nil {
		return fmt.Errorf("duration %q: %w", b, err)
	}
	d.Duration = v
	return nil
}

// DefaultConfig returns the configuration used when no file exists.
func DefaultConfig() Config {
	return Config{
		StateDir:   StateDir,
		RuntimeDir: RuntimeDir,
		LibDir:     LibDir,
		AgentsHome: AgentsHome,
		API:        APIConfig{Addr: DefaultAddr},
		Log:        LogConfig{Level: "info", Format: "text"},
		Budget:     BudgetConfig{CheckInterval: Duration{30 * time.Second}},
	}
}

// LoadConfig reads path over the defaults. A missing file gives defaults.
// Unknown keys are an error so typos do not pass silently.
func LoadConfig(path string) (Config, error) {
	b, err := os.ReadFile(path) // #nosec G304 -- the path is the daemon's own config file
	if errors.Is(err, fs.ErrNotExist) {
		return DefaultConfig(), nil
	}
	if err != nil {
		return DefaultConfig(), fmt.Errorf("read config: %w", err)
	}
	c, err := ParseConfig(b)
	if err != nil {
		return c, fmt.Errorf("config %s: %w", path, err)
	}
	return c, nil
}

// coreKeys are the top-level keys Config itself decodes. Every other
// top-level key must be a table that a module reads with Section.
var coreKeys = map[string]bool{
	"state_dir": true, "runtime_dir": true, "lib_dir": true, "agents_home": true,
	"api": true, "log": true, "budget": true,
}

// ParseConfig decodes TOML over the defaults. Core keys are checked here;
// module tables are kept for Section.
func ParseConfig(b []byte) (Config, error) {
	c := DefaultConfig()
	var all map[string]any
	if err := toml.Unmarshal(b, &all); err != nil {
		return c, fmt.Errorf("parse: %w", err)
	}
	own := map[string]any{}
	c.sections = map[string]any{}
	for k, v := range all {
		switch _, table := v.(map[string]any); {
		case coreKeys[k]:
			own[k] = v
		case table:
			c.sections[k] = v
		default:
			return c, fmt.Errorf("unknown key %q", k)
		}
	}
	if err := decodeStrict(own, &c); err != nil {
		return c, err
	}
	return c, nil
}

// Section decodes the module table [name] into v. Fields of v keep their
// values when the table or a key is absent; unknown keys are an error.
func (c Config) Section(name string, v any) error {
	table, ok := c.sections[name]
	if !ok {
		return nil
	}
	if err := decodeStrict(table, v); err != nil {
		return fmt.Errorf("[%s]: %w", name, err)
	}
	return nil
}

func decodeStrict(table any, v any) error {
	b, err := toml.Marshal(table)
	if err != nil {
		return fmt.Errorf("re-encode: %w", err)
	}
	if err := toml.NewDecoder(bytes.NewReader(b)).DisallowUnknownFields().Decode(v); err != nil {
		return fmt.Errorf("decode: %w", err)
	}
	return nil
}

// SocketPath is the API Unix socket.
func (c Config) SocketPath() string {
	if c.API.Socket != "" {
		return c.API.Socket
	}
	return filepath.Join(c.RuntimeDir, "api.sock")
}

// RunnerSocket is the Unix socket for the named agent's runner.
func (c Config) RunnerSocket(agent string) string {
	return filepath.Join(c.RuntimeDir, "runners", agent+".sock")
}

// DBPath is the path of one database: core, bus, memory or audit.
func (c Config) DBPath(name string) string {
	return filepath.Join(c.StateDir, name+".db")
}

// PluginsDir holds installed plugins.
func (c Config) PluginsDir() string { return filepath.Join(c.StateDir, "plugins") }

// AgentHome is a custom agent's home directory.
func (c Config) AgentHome(agent string) string { return filepath.Join(c.AgentsHome, agent) }
