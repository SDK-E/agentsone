// SPDX-License-Identifier: MIT

package claude

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const secret = "local-secret-0123456789abcdef0123456789abcdef0123456789abcdef0123"

func spec(root bool, rules permissionRules) launchSpec {
	return launchSpec{
		Weight: "normal", SystemPrompt: "You are ada.", ProxyURL: "http://127.0.0.1:4100", Secret: secret,
		SessionID: "0b7f9a52-6d0e-4c8e-9f3c-5a1d2e3f4a5b", IsRoot: root, Rules: rules, StateDir: "/home/ada/.local/state/agentsone/claude",
	}
}

// flag returns the values after name, up to the next flag.
func flag(args []string, name string) ([]string, bool) {
	i := slices.Index(args, name)
	if i < 0 {
		return nil, false
	}
	var out []string
	for _, a := range args[i+1:] {
		if strings.HasPrefix(a, "--") {
			break
		}
		out = append(out, a)
	}
	return out, true
}

func envMap(env []string) map[string]string {
	m := map[string]string{}
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		m[k] = v
	}
	return m
}

func TestLaunchSpeaksStreamJSONToTheProxy(t *testing.T) {
	l := buildLaunch(spec(false, permissionRules{}), nil)
	for _, want := range [][]string{
		{"-p"}, {"--input-format", "stream-json"}, {"--output-format", "stream-json"}, {"--verbose"},
		{"--include-partial-messages"}, {"--strict-mcp-config"}, {"--model", "autoagents/one:normal"},
		{"--session-id", "0b7f9a52-6d0e-4c8e-9f3c-5a1d2e3f4a5b"},
	} {
		i := slices.Index(l.Args, want[0])
		if i < 0 || !slices.Equal(l.Args[i:i+len(want)], want) {
			t.Errorf("args %q miss %q", l.Args, want)
		}
	}
	if got, _ := flag(l.Args, "--setting-sources"); !slices.Equal(got, []string{""}) {
		t.Errorf("--setting-sources = %q, want one empty value", got)
	}
	mcpPath, _ := flag(l.Args, "--mcp-config")
	promptPath, _ := flag(l.Args, "--append-system-prompt-file")
	if len(mcpPath) != 1 || len(promptPath) != 1 {
		t.Fatalf("--mcp-config %q, --append-system-prompt-file %q, want one file each", mcpPath, promptPath)
	}
	var cfg struct {
		MCPServers map[string]struct {
			Type    string            `json:"type"`
			URL     string            `json:"url"`
			Headers map[string]string `json:"headers"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(l.Files[mcpPath[0]], &cfg); err != nil {
		t.Fatalf("MCP config: %v", err)
	}
	m := cfg.MCPServers["machine"]
	if len(cfg.MCPServers) != 1 || m.Type != "http" || m.URL != "http://127.0.0.1:4100/mcp" || m.Headers["Authorization"] != "Bearer "+secret {
		t.Errorf("MCP config = %+v, want one http server \"machine\" on the proxy with the local secret", cfg)
	}
	if got := string(l.Files[promptPath[0]]); got != "You are ada." {
		t.Errorf("system prompt file = %q", got)
	}
	for path := range l.Files {
		if filepath.Dir(path) != "/home/ada/.local/state/agentsone/claude" {
			t.Errorf("file %s is outside the state folder", path)
		}
	}
}

func TestLaunchKeepsTheSecretOutOfTheArguments(t *testing.T) {
	// /proc/<pid>/cmdline is readable by every user in the container.
	l := buildLaunch(spec(true, permissionRules{}), nil)
	if strings.Contains(strings.Join(l.Args, " "), secret) {
		t.Errorf("args carry the local secret: %q", l.Args)
	}
}

func TestLaunchResumesAKnownSession(t *testing.T) {
	s := spec(false, permissionRules{})
	s.Resume = true
	l := buildLaunch(s, nil)
	if got, _ := flag(l.Args, "--resume"); !slices.Equal(got, []string{s.SessionID}) {
		t.Errorf("--resume = %q, want the session id", got)
	}
	if _, ok := flag(l.Args, "--session-id"); ok {
		t.Error("a resumed start also passes --session-id")
	}
}

func TestRootAgentsBypassPermissionsInTheSandbox(t *testing.T) {
	l := buildLaunch(spec(true, permissionRules{Allowed: []string{"Read"}}), nil)
	if got, _ := flag(l.Args, "--permission-mode"); !slices.Equal(got, []string{"bypassPermissions"}) {
		t.Errorf("--permission-mode = %q, want bypassPermissions", got)
	}
	if got, _ := flag(l.Args, "--disallowedTools"); !slices.Equal(got, []string{"WebFetch"}) {
		t.Errorf("--disallowedTools = %q, want only WebFetch", got)
	}
	if _, ok := flag(l.Args, "--allowedTools"); ok {
		t.Error("a root agent gets --allowedTools, want none")
	}
	if envMap(l.Env)["IS_SANDBOX"] != "1" {
		t.Error("IS_SANDBOX is not 1 for a root agent")
	}
}

func TestCustomAgentsAskNothingAndKeepTheirRules(t *testing.T) {
	l := buildLaunch(spec(false, permissionRules{Allowed: []string{"Bash(git *)", "Read"}, Disallowed: []string{"Bash(rm *)"}}), nil)
	if got, _ := flag(l.Args, "--permission-mode"); !slices.Equal(got, []string{"dontAsk"}) {
		t.Errorf("--permission-mode = %q, want dontAsk", got)
	}
	if got, _ := flag(l.Args, "--allowedTools"); !slices.Equal(got, []string{"mcp__machine__*", "Bash(git *)", "Read"}) {
		t.Errorf("--allowedTools = %q", got)
	}
	if got, _ := flag(l.Args, "--disallowedTools"); !slices.Equal(got, []string{"WebFetch", "Bash(rm *)"}) {
		t.Errorf("--disallowedTools = %q", got)
	}
	if _, ok := envMap(l.Env)["IS_SANDBOX"]; ok {
		t.Error("IS_SANDBOX is set for a custom agent")
	}
}

func TestLaunchEnvironmentIsBuiltFromAnAllowList(t *testing.T) {
	parent := []string{
		"PATH=/usr/bin", "HOME=/home/ada", "USER=ada", "LOGNAME=ada", "LANG=C.UTF-8", "LC_ALL=C.UTF-8", "TZ=UTC", "TMPDIR=/tmp",
		"TERM=xterm-256color", "ANTHROPIC_API_KEY=sk-ant-real", "ANTHROPIC_BASE_URL=https://api.anthropic.com",
		"CLAUDE_CODE_USE_BEDROCK=1", "HTTP_PROXY=http://trap", "HTTPS_PROXY=http://trap", "http_proxy=http://trap",
		"NO_PROXY=*", "ALL_PROXY=socks5://trap", "AGENTSONE_TOKEN=mcp-real", "AWS_SECRET_ACCESS_KEY=x", "IS_SANDBOX=1",
	}
	env := envMap(buildLaunch(spec(false, permissionRules{}), parent).Env)
	want := map[string]string{
		"PATH": "/usr/bin", "HOME": "/home/ada", "USER": "ada", "LOGNAME": "ada", "LANG": "C.UTF-8", "LC_ALL": "C.UTF-8",
		"TZ": "UTC", "TMPDIR": "/tmp", "TERM": "dumb",
		"ANTHROPIC_BASE_URL":                       "http://127.0.0.1:4100",
		"ANTHROPIC_AUTH_TOKEN":                     secret,
		"ANTHROPIC_DEFAULT_HAIKU_MODEL":            "autoagents/one:quick",
		"ANTHROPIC_DEFAULT_SONNET_MODEL":           "autoagents/one:normal",
		"ANTHROPIC_DEFAULT_OPUS_MODEL":             "autoagents/one:deep",
		"ANTHROPIC_DEFAULT_FABLE_MODEL":            "autoagents/one:deep",
		"CLAUDE_CODE_SUBAGENT_MODEL":               "autoagents/one:normal",
		"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1",
		"DISABLE_AUTOUPDATER":                      "1",
		"DISABLE_TELEMETRY":                        "1",
		"DISABLE_ERROR_REPORTING":                  "1",
		"CLAUDE_CODE_DISABLE_TERMINAL_TITLE":       "1",
		"CLAUDE_CODE_DISABLE_AUTO_MEMORY":          "1",
		"CLAUDE_CONFIG_DIR":                        "/home/ada/.local/state/agentsone/claude",
	}
	for k, v := range want {
		if env[k] != v {
			t.Errorf("%s = %q, want %q", k, env[k], v)
		}
	}
	for k := range env {
		if _, ok := want[k]; !ok {
			t.Errorf("unexpected variable %s reaches the engine", k)
		}
	}
}

func TestParseRunnerConfig(t *testing.T) {
	got, err := parseRunnerConfig(map[string]any{"allowedTools": []any{"Read", "Bash(git *)"}, "disallowedTools": []any{"Edit"}})
	if err != nil || !slices.Equal(got.Allowed, []string{"Read", "Bash(git *)"}) || !slices.Equal(got.Disallowed, []string{"Edit"}) {
		t.Errorf("parseRunnerConfig() = %+v, %v", got, err)
	}
	if got, err := parseRunnerConfig(nil); err != nil || len(got.Allowed)+len(got.Disallowed) != 0 {
		t.Errorf("parseRunnerConfig(nil) = %+v, %v, want empty rules", got, err)
	}
	for name, bad := range map[string]map[string]any{
		"unknown key":     {"allowedTools": []any{"Read"}, "tools": []any{"Edit"}},
		"not a list":      {"allowedTools": "Read"},
		"not strings":     {"disallowedTools": []any{"Read", 3}},
		"empty rule":      {"allowedTools": []any{" "}},
		"rule with a nul": {"allowedTools": []any{"Read\x00"}},
		"flag-like rule":  {"allowedTools": []any{"--dangerously-skip-permissions"}},
	} {
		if _, err := parseRunnerConfig(bad); err == nil {
			t.Errorf("%s: parseRunnerConfig() error = nil, want a refusal", name)
		}
	}
}
