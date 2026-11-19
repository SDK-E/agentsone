// SPDX-License-Identifier: MIT

package claude

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// launchSpec is everything one engine start needs.
type launchSpec struct {
	// Weight is the welcome's default weight; the model is autoagents/one:<weight>.
	Weight       string
	SystemPrompt string
	// ProxyURL and Secret are the token proxy's; the engine never gets a real token.
	ProxyURL, Secret string
	SessionID        string
	// Resume continues SessionID after a kill or cancel instead of starting it.
	Resume bool
	IsRoot bool
	Rules  permissionRules
	// StateDir is the engine's CLAUDE_CONFIG_DIR, in the agent's home.
	StateDir string
}

// launch is the engine command: arguments after the binary, the whole
// environment, and the files to write (mode 0600) before the start.
type launch struct {
	Args  []string
	Env   []string
	Files map[string][]byte
}

// keptEnv are the only variables the engine inherits. Everything else is
// dropped, in particular every ANTHROPIC_*, CLAUDE_*, *_PROXY and token.
var keptEnv = []string{"PATH", "HOME", "USER", "LOGNAME", "LANG", "LC_ALL", "TZ", "TMPDIR"}

// The files buildLaunch asks for, in StateDir.
const (
	mcpConfigFile    = "agentsone-mcp.json"
	systemPromptFile = "agentsone-system-prompt.md"
)

// buildLaunch builds the engine command for s on top of parent, the
// runner's own environment (os.Environ()). The local secret goes only into
// the environment and a 0600 file: process arguments are readable by every
// user in the container through /proc. The system prompt goes into a file
// too, so a long summary never meets the argument size limit.
func buildLaunch(s launchSpec, parent []string) launch {
	mcpPath := filepath.Join(s.StateDir, mcpConfigFile)
	promptPath := filepath.Join(s.StateDir, systemPromptFile)
	args := []string{
		"-p", "--input-format", "stream-json", "--output-format", "stream-json", "--verbose",
		"--include-partial-messages",
		"--model", model(s.Weight),
		"--append-system-prompt-file", promptPath,
		"--mcp-config", mcpPath, "--strict-mcp-config",
		"--setting-sources", "", // no user, project or local settings: the runner sets everything
	}
	if s.Resume {
		args = append(args, "--resume", s.SessionID)
	} else {
		args = append(args, "--session-id", s.SessionID)
	}
	// WebFetch is always denied: its domain check calls api.anthropic.com
	// directly, outside the gateway.
	if s.IsRoot {
		args = append(args, "--permission-mode", "bypassPermissions", "--disallowedTools", "WebFetch")
	} else {
		allow := append([]string{"mcp__machine__*"}, s.Rules.Allowed...)
		deny := append([]string{"WebFetch"}, s.Rules.Disallowed...)
		args = append(args, "--permission-mode", "dontAsk", "--allowedTools")
		args = append(args, allow...)
		args = append(args, "--disallowedTools")
		args = append(args, deny...)
	}
	return launch{
		Args: args,
		Env:  launchEnv(s, parent),
		Files: map[string][]byte{
			mcpPath:    mcpConfig(s.ProxyURL, s.Secret),
			promptPath: []byte(s.SystemPrompt),
		},
	}
}

func launchEnv(s launchSpec, parent []string) []string {
	var env []string
	for _, kv := range parent {
		k, _, _ := strings.Cut(kv, "=")
		for _, keep := range keptEnv {
			if k == keep {
				env = append(env, kv)
			}
		}
	}
	env = append(env,
		"TERM=dumb",
		"ANTHROPIC_BASE_URL="+s.ProxyURL,
		// The engine sends this as Authorization: Bearer. It is the proxy's
		// local secret, never the gateway token. ANTHROPIC_API_KEY stays unset.
		"ANTHROPIC_AUTH_TOKEN="+s.Secret,
		"ANTHROPIC_DEFAULT_HAIKU_MODEL="+model("quick"),
		"ANTHROPIC_DEFAULT_SONNET_MODEL="+model("normal"),
		"ANTHROPIC_DEFAULT_OPUS_MODEL="+model("deep"),
		"ANTHROPIC_DEFAULT_FABLE_MODEL="+model("deep"),
		"CLAUDE_CODE_SUBAGENT_MODEL="+model("normal"),
		// No auto-update, telemetry, error reporting or feedback calls.
		"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1",
		"DISABLE_AUTOUPDATER=1", "DISABLE_TELEMETRY=1", "DISABLE_ERROR_REPORTING=1",
		// The terminal title costs a background model call.
		"CLAUDE_CODE_DISABLE_TERMINAL_TITLE=1",
		// The agent's memory is the daemon's: the sleep summary and the
		// memory MCP tools. The engine's own memory files would be a second,
		// unaudited store in the agent's home.
		"CLAUDE_CODE_DISABLE_AUTO_MEMORY=1",
		"CLAUDE_CONFIG_DIR="+s.StateDir,
	)
	if s.IsRoot {
		// The machine container is the sandbox; Claude Code refuses
		// bypassPermissions as root outside a recognised sandbox.
		env = append(env, "IS_SANDBOX=1")
	}
	return env
}

func model(weight string) string { return "autoagents/one:" + weight }

// mcpConfig is the engine's only MCP server: the machine endpoint, through
// the proxy, with the local secret.
func mcpConfig(proxyURL, secret string) []byte {
	b, _ := json.Marshal(map[string]any{ // a map of strings always marshals
		"mcpServers": map[string]any{
			"machine": map[string]any{
				"type": "http", "url": proxyURL + "/mcp",
				"headers": map[string]string{"Authorization": "Bearer " + secret},
			},
		},
	})
	return b
}

// permissionRules are a custom agent's extra Claude Code permission rules,
// from the template's runner.config.
type permissionRules struct {
	Allowed, Disallowed []string
}

// parseRunnerConfig reads welcome.config.runner:
// {"allowedTools": [rule], "disallowedTools": [rule]}. Both keys are
// optional; any other key is refused, so a typo never widens or narrows
// permissions silently.
func parseRunnerConfig(m map[string]any) (permissionRules, error) {
	var r permissionRules
	for k, v := range m {
		var dst *[]string
		switch k {
		case "allowedTools":
			dst = &r.Allowed
		case "disallowedTools":
			dst = &r.Disallowed
		default:
			return permissionRules{}, fmt.Errorf("the runner config has an unknown key %q; only allowedTools and disallowedTools are allowed", k)
		}
		rules, err := ruleList(k, v)
		if err != nil {
			return permissionRules{}, err
		}
		*dst = rules
	}
	return r, nil
}

func ruleList(key string, v any) ([]string, error) {
	list, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("the runner config %s must be a list of permission rules", key)
	}
	out := make([]string, 0, len(list))
	for _, item := range list {
		s, ok := item.(string)
		if !ok {
			return nil, fmt.Errorf("the runner config %s must hold only strings", key)
		}
		if err := checkRule(s); err != nil {
			return nil, fmt.Errorf("the runner config %s: %w", key, err)
		}
		out = append(out, s)
	}
	return out, nil
}

// checkRule refuses rules that are empty, carry control characters, or
// start with "-", which the engine would read as a flag.
func checkRule(s string) error {
	switch {
	case strings.TrimSpace(s) == "":
		return errors.New("a permission rule is empty")
	case strings.HasPrefix(s, "-"):
		return errors.New("a permission rule must not start with -")
	case strings.ContainsFunc(s, func(r rune) bool { return r < 0x20 || r == 0x7f }):
		return errors.New("a permission rule has a control character")
	}
	return nil
}
