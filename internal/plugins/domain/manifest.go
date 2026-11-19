// SPDX-License-Identifier: MIT

// Package domain holds the pure rules of the plugin context: the normalised
// manifest and its validation, the mapping from each source format to it, the
// install state machine, runner and theme rules. It does no I/O.
package domain

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"strings"
	"unicode/utf8"
)

// Kind is what a plugin provides, with the contract's PluginKind values.
type Kind string

// Plugin kinds.
const (
	KindMCPServer     Kind = "mcp-server"
	KindSkill         Kind = "skill"
	KindAgentTemplate Kind = "agent-template"
	KindRunner        Kind = "runner"
	KindPanel         Kind = "panel"
	KindTheme         Kind = "theme"
	KindEmitter       Kind = "emitter"
)

// SourceID names a plugin source adapter, with the contract's values.
type SourceID string

// Source ids.
const (
	SourceClaudeCode  SourceID = "claude-code"
	SourceAgentsOne   SourceID = "agentsone"
	SourceAgentSkills SourceID = "agent-skills"
	SourceMCPRegistry SourceID = "mcp-registry"
)

// Valid reports whether s is one of the four source ids.
func (s SourceID) Valid() bool {
	switch s {
	case SourceClaudeCode, SourceAgentsOne, SourceAgentSkills, SourceMCPRegistry:
		return true
	}
	return false
}

// Manifest is agentsone.plugin.json (plugin-manifest.schema.json).
type Manifest struct {
	Schema      string   `json:"$schema,omitempty"`
	Name        string   `json:"name"`
	Version     string   `json:"version"`
	Description string   `json:"description"`
	Author      Author   `json:"author"`
	License     string   `json:"license"`
	Homepage    string   `json:"homepage,omitempty"`
	Source      Source   `json:"source"`
	Compat      Compat   `json:"compat"`
	Contents    Contents `json:"contents"`
}

// Author is who wrote the plugin.
type Author struct {
	Name  string `json:"name"`
	Email string `json:"email,omitempty"`
	URL   string `json:"url,omitempty"`
}

// Source says where the plugin came from.
type Source struct {
	Kind   SourceID `json:"kind"`
	Ref    string   `json:"ref"`
	SHA256 string   `json:"sha256,omitempty"`
}

// Compat says which Agents One versions and architectures the plugin works with.
type Compat struct {
	AgentsOne      string   `json:"agentsone"`
	RunnerProtocol []string `json:"runnerProtocol,omitempty"`
	Arch           []string `json:"arch,omitempty"`
}

// Contents lists what the plugin provides.
type Contents struct {
	McpServers     []McpServer `json:"mcpServers"`
	Skills         []Skill     `json:"skills"`
	AgentTemplates []FileRef   `json:"agentTemplates"`
	Runners        []Runner    `json:"runners"`
	Panels         []Panel     `json:"panels"`
	Themes         []FileRef   `json:"themes"`
	Emitters       []Emitter   `json:"emitters"`
}

// FileRef is a file inside the plugin.
type FileRef struct {
	Path string `json:"path"`
}

// Transports of an MCP server.
const (
	TransportStdio = "stdio"
	TransportHTTP  = "streamable-http"
)

// McpServer is an MCP server a plugin provides: a child process (stdio) or a
// remote server (streamable-http).
type McpServer struct {
	Name      string
	Transport string
	// Command, Args and Env are for stdio servers.
	Command string
	Args    []string
	Env     map[string]string
	// URL and Headers are for streamable-http servers.
	URL     string
	Headers map[string]string
}

type mcpWire struct {
	Name      string             `json:"name"`
	Transport string             `json:"transport"`
	Command   *string            `json:"command,omitempty"`
	Args      *[]string          `json:"args,omitempty"`
	Env       *map[string]string `json:"env,omitempty"`
	URL       *string            `json:"url,omitempty"`
	Headers   map[string]string  `json:"headers,omitempty"`
}

// MarshalJSON writes the form of the server's transport.
func (s McpServer) MarshalJSON() ([]byte, error) {
	w := mcpWire{Name: s.Name, Transport: s.Transport}
	if s.Transport == TransportHTTP {
		w.URL = &s.URL
		w.Headers = s.Headers
	} else {
		args, env := s.Args, s.Env
		if args == nil {
			args = []string{}
		}
		if env == nil {
			env = map[string]string{}
		}
		w.Command, w.Args, w.Env = &s.Command, &args, &env
	}
	return json.Marshal(w)
}

// UnmarshalJSON reads either form and refuses unknown transports and fields.
func (s *McpServer) UnmarshalJSON(b []byte) error {
	var w mcpWire
	if err := strictDecode(b, &w); err != nil {
		return err
	}
	switch w.Transport {
	case TransportStdio:
		if w.URL != nil || w.Headers != nil {
			return fmt.Errorf("mcp server %q: stdio server with url or headers", w.Name)
		}
	case TransportHTTP:
		if w.Command != nil || w.Args != nil || w.Env != nil {
			return fmt.Errorf("mcp server %q: http server with command, args or env", w.Name)
		}
	default:
		return fmt.Errorf("mcp server %q: transport %q is not stdio or streamable-http", w.Name, w.Transport)
	}
	*s = McpServer{Name: w.Name, Transport: w.Transport, Headers: w.Headers}
	if w.Command != nil {
		s.Command = *w.Command
	}
	if w.Args != nil {
		s.Args = *w.Args
	}
	if w.Env != nil {
		s.Env = *w.Env
	}
	if w.URL != nil {
		s.URL = *w.URL
	}
	return nil
}

// Skill is an Agent Skills directory.
type Skill struct {
	Name        string `json:"name"`
	Path        string `json:"path"`
	Description string `json:"description"`
}

// Runner is a runner engine.
type Runner struct {
	Name             string            `json:"name"`
	Description      string            `json:"description,omitempty"`
	Command          string            `json:"command"`
	Args             []string          `json:"args"`
	Env              map[string]string `json:"env"`
	ProtocolVersions []string          `json:"protocolVersions"`
}

// Panel is an interface panel.
type Panel struct {
	Name   string   `json:"name"`
	Title  string   `json:"title"`
	Icon   string   `json:"icon"`
	Entry  string   `json:"entry"`
	Scopes []string `json:"scopes"`
}

// Emitter is an event source.
type Emitter struct {
	Name       string            `json:"name"`
	Command    string            `json:"command"`
	Args       []string          `json:"args"`
	Env        map[string]string `json:"env"`
	EventTypes []string          `json:"eventTypes"`
	Catalog    string            `json:"catalog,omitempty"`
}

var (
	pluginNameRe    = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)
	componentRe     = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)
	skillNameRe     = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
	semverRe        = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?(\+[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$`)
	protocolRe      = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)
	licenseRe       = regexp.MustCompile(`^[A-Za-z0-9.+:() -]+$`)
	scopeRe         = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}:(read|write)$`)
	envNameRe       = regexp.MustCompile(`^[A-Z_][A-Z0-9_]{0,127}$`)
	eventTypeRe     = regexp.MustCompile(`^agentsone\.[a-z][a-z0-9-]*(\.[a-z][a-z0-9-]*)+$`)
	sha256Re        = regexp.MustCompile(`^[0-9a-f]{64}$`)
	headerNameRe    = regexp.MustCompile(`^[A-Za-z0-9-]{1,64}$`)
	errorSentinelFn = func(format string, a ...any) error {
		return fmt.Errorf("%w: %s", ErrInvalidManifest, fmt.Sprintf(format, a...))
	}
)

// IsSemver reports whether v is a semantic version without a leading "v".
func IsSemver(v string) bool { return semverRe.MatchString(v) }

// ValidOneLine reports whether s is 1 to 255 characters with no line break.
func ValidOneLine(s string) bool {
	n := utf8.RuneCountInString(s)
	return n >= 1 && n <= 255 && !strings.ContainsAny(s, "\r\n")
}

// ParseManifest decodes and validates agentsone.plugin.json. Unknown fields
// and missing contents lists are refused.
func ParseManifest(raw []byte) (Manifest, error) {
	var probe struct {
		Contents map[string]json.RawMessage `json:"contents"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return Manifest{}, errorSentinelFn("not JSON: %v", err)
	}
	for _, key := range []string{"mcpServers", "skills", "agentTemplates", "runners", "panels", "themes", "emitters"} {
		if _, ok := probe.Contents[key]; !ok {
			return Manifest{}, errorSentinelFn("contents.%s is missing", key)
		}
	}
	var m Manifest
	if err := strictDecode(raw, &m); err != nil {
		return Manifest{}, errorSentinelFn("%v", err)
	}
	if err := m.Validate(); err != nil {
		return Manifest{}, err
	}
	return m, nil
}

func strictDecode(raw []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	return nil
}

// Encode writes the manifest as indented JSON with every contents list
// present, empty ones as [].
func (m Manifest) Encode() ([]byte, error) {
	m.Contents = m.Contents.withLists()
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode manifest: %w", err)
	}
	return append(b, '\n'), nil
}

func (c Contents) withLists() Contents {
	if c.McpServers == nil {
		c.McpServers = []McpServer{}
	}
	if c.Skills == nil {
		c.Skills = []Skill{}
	}
	if c.AgentTemplates == nil {
		c.AgentTemplates = []FileRef{}
	}
	if c.Runners == nil {
		c.Runners = []Runner{}
	}
	if c.Panels == nil {
		c.Panels = []Panel{}
	}
	if c.Themes == nil {
		c.Themes = []FileRef{}
	}
	if c.Emitters == nil {
		c.Emitters = []Emitter{}
	}
	return c
}

// Kinds lists the kinds the plugin provides, in a fixed order.
func (m Manifest) Kinds() []Kind {
	var kinds []Kind
	c := m.Contents
	for _, k := range []struct {
		kind Kind
		n    int
	}{
		{KindMCPServer, len(c.McpServers)}, {KindSkill, len(c.Skills)},
		{KindAgentTemplate, len(c.AgentTemplates)}, {KindRunner, len(c.Runners)},
		{KindPanel, len(c.Panels)}, {KindTheme, len(c.Themes)}, {KindEmitter, len(c.Emitters)},
	} {
		if k.n > 0 {
			kinds = append(kinds, k.kind)
		}
	}
	return kinds
}

// Validate checks the manifest against the contract's rules and the plugin
// manager's extra rule that a plugin provides something.
func (m Manifest) Validate() error {
	if err := m.validateHeader(); err != nil {
		return err
	}
	if err := m.Contents.validate(); err != nil {
		return err
	}
	if len(m.Kinds()) == 0 {
		return fmt.Errorf("%w: %w", ErrInvalidManifest, ErrEmptyPlugin)
	}
	return nil
}

func (m Manifest) validateHeader() error {
	switch {
	case !pluginNameRe.MatchString(m.Name):
		return errorSentinelFn("name %q is not kebab-case up to 64 characters", m.Name)
	case !IsSemver(m.Version):
		return fmt.Errorf("%w: version %q: %w", ErrInvalidManifest, m.Version, ErrNotSemantic)
	case !ValidOneLine(m.Description):
		return errorSentinelFn("description must be one line of 1 to 255 characters")
	case m.Author.Name == "" || len(m.Author.Name) > 128:
		return errorSentinelFn("author.name must be 1 to 128 characters")
	case m.License == "" || len(m.License) > 128 || !licenseRe.MatchString(m.License):
		return errorSentinelFn("license %q is not an SPDX expression", m.License)
	case !m.Source.Kind.Valid():
		return errorSentinelFn("source.kind %q is unknown", m.Source.Kind)
	case m.Source.Ref == "" || len(m.Source.Ref) > 1024:
		return errorSentinelFn("source.ref must be 1 to 1024 characters")
	case m.Source.SHA256 != "" && !sha256Re.MatchString(m.Source.SHA256):
		return errorSentinelFn("source.sha256 is not 64 lowercase hex characters")
	case m.Compat.AgentsOne == "":
		return errorSentinelFn("compat.agentsone is empty")
	case m.Homepage != "" && !validURL(m.Homepage):
		return errorSentinelFn("homepage %q is not a URL", m.Homepage)
	}
	for _, p := range m.Compat.RunnerProtocol {
		if !protocolRe.MatchString(p) {
			return errorSentinelFn("compat.runnerProtocol %q is not <major>.<minor>", p)
		}
	}
	for _, a := range m.Compat.Arch {
		if a != "amd64" && a != "arm64" {
			return errorSentinelFn("compat.arch %q is not amd64 or arm64", a)
		}
	}
	return nil
}

// placeholderRe matches {variable}, ${PLUGIN_ROOT} and ${user_config.KEY}
// placeholders that a remote server URL may carry.
var placeholderRe = regexp.MustCompile(`\$?\{[A-Za-z0-9_.-]{1,64}\}`)

func validURL(s string) bool {
	u, err := url.Parse(placeholderRe.ReplaceAllString(s, "x"))
	return err == nil && u.Scheme != "" && u.Host != "" && len(s) <= 1024
}

func (c Contents) validate() error {
	seen := map[string]bool{}
	unique := func(kind, name string) error {
		key := kind + "/" + name
		if seen[key] {
			return errorSentinelFn("duplicate %s %q", kind, name)
		}
		seen[key] = true
		return nil
	}
	for _, s := range c.McpServers {
		if err := s.validate(); err != nil {
			return err
		}
		if err := unique("mcp server", s.Name); err != nil {
			return err
		}
	}
	for _, s := range c.Skills {
		if err := s.validate(); err != nil {
			return err
		}
		if err := unique("skill", s.Name); err != nil {
			return err
		}
	}
	for _, kind := range []struct {
		name string
		refs []FileRef
	}{{"agentTemplates", c.AgentTemplates}, {"themes", c.Themes}} {
		for _, f := range kind.refs {
			if err := ValidatePluginPath(f.Path); err != nil {
				return fmt.Errorf("%w: %s: %w", ErrInvalidManifest, kind.name, err)
			}
		}
	}
	for _, r := range c.Runners {
		if err := r.validate(); err != nil {
			return err
		}
		if err := unique("runner", r.Name); err != nil {
			return err
		}
	}
	for _, p := range c.Panels {
		if err := p.validate(); err != nil {
			return err
		}
		if err := unique("panel", p.Name); err != nil {
			return err
		}
	}
	for _, e := range c.Emitters {
		if err := e.validate(); err != nil {
			return err
		}
		if err := unique("emitter", e.Name); err != nil {
			return err
		}
	}
	return nil
}

func (s McpServer) validate() error {
	if !componentRe.MatchString(s.Name) {
		return errorSentinelFn("mcp server name %q is invalid", s.Name)
	}
	switch s.Transport {
	case TransportStdio:
		if s.Command == "" {
			return errorSentinelFn("mcp server %q has no command", s.Name)
		}
		return validateProcess("mcp server "+s.Name, s.Command, s.Args, s.Env)
	case TransportHTTP:
		if !validURL(s.URL) {
			return errorSentinelFn("mcp server %q needs a url", s.Name)
		}
		for k := range s.Headers {
			if !headerNameRe.MatchString(k) {
				return errorSentinelFn("mcp server %q header %q is invalid", s.Name, k)
			}
		}
		return nil
	}
	return errorSentinelFn("mcp server %q transport %q is not supported", s.Name, s.Transport)
}

func validateProcess(what, command string, args []string, env map[string]string) error {
	if command == "" || len(command) > 4096 {
		return errorSentinelFn("%s command must be 1 to 4096 characters", what)
	}
	if len(args) > 64 {
		return errorSentinelFn("%s has more than 64 arguments", what)
	}
	for k := range env {
		if !envNameRe.MatchString(k) {
			return errorSentinelFn("%s environment name %q is invalid", what, k)
		}
	}
	return nil
}

func (s Skill) validate() error {
	if !skillNameRe.MatchString(s.Name) || len(s.Name) > 64 {
		return errorSentinelFn("skill name %q is invalid", s.Name)
	}
	rel, err := RelPath(s.Path)
	if err != nil {
		return fmt.Errorf("%w: skill %q: %w", ErrInvalidManifest, s.Name, err)
	}
	if path.Base(rel) != s.Name {
		return errorSentinelFn("skill %q must equal its directory name %q", s.Name, path.Base(rel))
	}
	if n := utf8.RuneCountInString(s.Description); n < 1 || n > 1024 {
		return errorSentinelFn("skill %q description must be 1 to 1024 characters", s.Name)
	}
	return nil
}

func (r Runner) validate() error {
	if !componentRe.MatchString(r.Name) {
		return errorSentinelFn("runner name %q is invalid", r.Name)
	}
	if err := validateProcess("runner "+r.Name, r.Command, r.Args, r.Env); err != nil {
		return err
	}
	if len(r.ProtocolVersions) == 0 {
		return errorSentinelFn("runner %q lists no protocol version", r.Name)
	}
	for _, v := range r.ProtocolVersions {
		if !protocolRe.MatchString(v) {
			return errorSentinelFn("runner %q protocol version %q is invalid", r.Name, v)
		}
	}
	return nil
}

func (p Panel) validate() error {
	if !componentRe.MatchString(p.Name) {
		return errorSentinelFn("panel name %q is invalid", p.Name)
	}
	if n := utf8.RuneCountInString(p.Title); n < 1 || n > 48 {
		return errorSentinelFn("panel %q title must be 1 to 48 characters", p.Name)
	}
	for _, f := range []string{p.Icon, p.Entry} {
		if err := ValidatePluginPath(f); err != nil {
			return fmt.Errorf("%w: panel %q: %w", ErrInvalidManifest, p.Name, err)
		}
	}
	for _, s := range p.Scopes {
		if !scopeRe.MatchString(s) {
			return errorSentinelFn("panel %q scope %q is not <group>:read or <group>:write", p.Name, s)
		}
	}
	return nil
}

func (e Emitter) validate() error {
	if !componentRe.MatchString(e.Name) {
		return errorSentinelFn("emitter name %q is invalid", e.Name)
	}
	if err := validateProcess("emitter "+e.Name, e.Command, e.Args, e.Env); err != nil {
		return err
	}
	if len(e.EventTypes) == 0 {
		return errorSentinelFn("emitter %q lists no event type", e.Name)
	}
	for _, t := range e.EventTypes {
		if len(t) > 128 || !eventTypeRe.MatchString(t) {
			return errorSentinelFn("emitter %q event type %q is invalid", e.Name, t)
		}
	}
	if e.Catalog != "" {
		if err := ValidatePluginPath(e.Catalog); err != nil {
			return fmt.Errorf("%w: emitter %q: %w", ErrInvalidManifest, e.Name, err)
		}
	}
	return nil
}
