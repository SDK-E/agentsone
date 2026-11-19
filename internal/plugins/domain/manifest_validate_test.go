// SPDX-License-Identifier: MIT

package domain_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/SDK-E/agentsone/internal/plugins/domain"
)

func validRunner() domain.Runner {
	return domain.Runner{Name: "r", Command: "bin/r", ProtocolVersions: []string{"1.0"}}
}

func validPanel() domain.Panel {
	return domain.Panel{Name: "p", Title: "Panel", Icon: "./i.svg", Entry: "./i.html", Scopes: []string{"agents:read"}}
}

func validEmitter() domain.Emitter {
	return domain.Emitter{Name: "e", Command: "bin/e", EventTypes: []string{"agentsone.plugin.ping"}}
}

func TestManifest_Validate_RefusesBadHeaderAndComponents(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		change func(*domain.Manifest)
	}{
		{"author name empty", func(m *domain.Manifest) { m.Author.Name = "" }},
		{"author name over 128", func(m *domain.Manifest) { m.Author.Name = strings.Repeat("a", 129) }},
		{"license with bad characters", func(m *domain.Manifest) { m.License = "MIT; rm -rf" }},
		{"license over 128", func(m *domain.Manifest) { m.License = strings.Repeat("M", 129) }},
		{"source ref over 1024", func(m *domain.Manifest) { m.Source.Ref = strings.Repeat("r", 1025) }},
		{"sha256 not hex", func(m *domain.Manifest) { m.Source.SHA256 = "XYZ" }},
		{"compat empty", func(m *domain.Manifest) { m.Compat.AgentsOne = "" }},
		{"homepage not a URL", func(m *domain.Manifest) { m.Homepage = "not a url" }},
		{"homepage without host", func(m *domain.Manifest) { m.Homepage = "https://" }},
		{"runner protocol malformed", func(m *domain.Manifest) { m.Compat.RunnerProtocol = []string{"1"} }},
		{"arch unknown", func(m *domain.Manifest) { m.Compat.Arch = []string{"riscv"} }},

		{"mcp server name invalid", func(m *domain.Manifest) {
			m.Contents.McpServers = []domain.McpServer{{Name: "Bad", Transport: domain.TransportStdio, Command: "x"}}
		}},
		{"mcp server transport unknown", func(m *domain.Manifest) {
			m.Contents.McpServers = []domain.McpServer{{Name: "x", Transport: "sse", URL: "https://a.example/sse"}}
		}},
		{"mcp stdio bad env name", func(m *domain.Manifest) {
			m.Contents.McpServers = []domain.McpServer{{Name: "x", Transport: domain.TransportStdio, Command: "c", Env: map[string]string{"bad-name": "1"}}}
		}},
		{"mcp http bad header", func(m *domain.Manifest) {
			m.Contents.McpServers = []domain.McpServer{{Name: "x", Transport: domain.TransportHTTP, URL: "https://a.example/mcp", Headers: map[string]string{"bad header": "1"}}}
		}},
		{"mcp duplicate", func(m *domain.Manifest) {
			s := domain.McpServer{Name: "x", Transport: domain.TransportStdio, Command: "c"}
			m.Contents.McpServers = []domain.McpServer{s, s}
		}},
		{"mcp command over 4096", func(m *domain.Manifest) {
			m.Contents.McpServers = []domain.McpServer{{Name: "x", Transport: domain.TransportStdio, Command: strings.Repeat("c", 4097)}}
		}},
		{"mcp more than 64 arguments", func(m *domain.Manifest) {
			m.Contents.McpServers = []domain.McpServer{{Name: "x", Transport: domain.TransportStdio, Command: "c", Args: make([]string, 65)}}
		}},

		{"skill name invalid", func(m *domain.Manifest) { m.Contents.Skills[0].Name = "Bad_Name" }},
		{"skill description empty", func(m *domain.Manifest) { m.Contents.Skills[0].Description = "" }},
		{"skill description over 1024", func(m *domain.Manifest) { m.Contents.Skills[0].Description = strings.Repeat("d", 1025) }},

		{"agent template path unsafe", func(m *domain.Manifest) { m.Contents.AgentTemplates = []domain.FileRef{{Path: "../x.json"}} }},
		{"theme path unsafe", func(m *domain.Manifest) { m.Contents.Themes = []domain.FileRef{{Path: "/etc/x.json"}} }},

		{"runner name invalid", func(m *domain.Manifest) {
			r := validRunner()
			r.Name = "Bad"
			m.Contents.Runners = []domain.Runner{r}
		}},
		{"runner command empty", func(m *domain.Manifest) {
			r := validRunner()
			r.Command = ""
			m.Contents.Runners = []domain.Runner{r}
		}},
		{"runner no protocol versions", func(m *domain.Manifest) {
			r := validRunner()
			r.ProtocolVersions = nil
			m.Contents.Runners = []domain.Runner{r}
		}},
		{"runner protocol version malformed", func(m *domain.Manifest) {
			r := validRunner()
			r.ProtocolVersions = []string{"one.zero"}
			m.Contents.Runners = []domain.Runner{r}
		}},
		{"runner bad env name", func(m *domain.Manifest) {
			r := validRunner()
			r.Env = map[string]string{"lower": "1"}
			m.Contents.Runners = []domain.Runner{r}
		}},
		{"runner duplicate", func(m *domain.Manifest) {
			r := validRunner()
			m.Contents.Runners = []domain.Runner{r, r}
		}},

		{"panel name invalid", func(m *domain.Manifest) {
			p := validPanel()
			p.Name = "Bad"
			m.Contents.Panels = []domain.Panel{p}
		}},
		{"panel title empty", func(m *domain.Manifest) {
			p := validPanel()
			p.Title = ""
			m.Contents.Panels = []domain.Panel{p}
		}},
		{"panel title over 48", func(m *domain.Manifest) {
			p := validPanel()
			p.Title = strings.Repeat("t", 49)
			m.Contents.Panels = []domain.Panel{p}
		}},
		{"panel icon unsafe", func(m *domain.Manifest) {
			p := validPanel()
			p.Icon = "board"
			m.Contents.Panels = []domain.Panel{p}
		}},
		{"panel entry unsafe", func(m *domain.Manifest) {
			p := validPanel()
			p.Entry = "./../entry.html"
			m.Contents.Panels = []domain.Panel{p}
		}},
		{"panel duplicate", func(m *domain.Manifest) {
			p := validPanel()
			m.Contents.Panels = []domain.Panel{p, p}
		}},

		{"emitter name invalid", func(m *domain.Manifest) {
			e := validEmitter()
			e.Name = "Bad"
			m.Contents.Emitters = []domain.Emitter{e}
		}},
		{"emitter command empty", func(m *domain.Manifest) {
			e := validEmitter()
			e.Command = ""
			m.Contents.Emitters = []domain.Emitter{e}
		}},
		{"emitter no event types", func(m *domain.Manifest) {
			e := validEmitter()
			e.EventTypes = nil
			m.Contents.Emitters = []domain.Emitter{e}
		}},
		{"emitter event type outside agentsone", func(m *domain.Manifest) {
			e := validEmitter()
			e.EventTypes = []string{"other.thing.happened"}
			m.Contents.Emitters = []domain.Emitter{e}
		}},
		{"emitter event type over 128", func(m *domain.Manifest) {
			e := validEmitter()
			e.EventTypes = []string{"agentsone." + strings.Repeat("a", 120) + ".b"}
			m.Contents.Emitters = []domain.Emitter{e}
		}},
		{"emitter catalog unsafe", func(m *domain.Manifest) {
			e := validEmitter()
			e.Catalog = "../catalog.json"
			m.Contents.Emitters = []domain.Emitter{e}
		}},
		{"emitter duplicate", func(m *domain.Manifest) {
			e := validEmitter()
			m.Contents.Emitters = []domain.Emitter{e, e}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			m := validManifest()
			tt.change(&m)
			if err := m.Validate(); !errors.Is(err, domain.ErrInvalidManifest) {
				t.Fatalf("Validate() error = %v, want ErrInvalidManifest", err)
			}
		})
	}
}

func TestManifest_Validate_AcceptsEveryKindAndPlaceholders(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		change func(*domain.Manifest)
	}{
		{"every kind", func(m *domain.Manifest) {
			m.Contents.McpServers = []domain.McpServer{
				{Name: "local", Transport: domain.TransportStdio, Command: "${PLUGIN_ROOT}/bin/x", Args: []string{"-v"}, Env: map[string]string{"LOG_LEVEL": "info"}},
				{Name: "remote", Transport: domain.TransportHTTP, URL: "https://a.example/mcp", Headers: map[string]string{"X-Api-Key": "${user_config.KEY}"}},
			}
			m.Contents.AgentTemplates = []domain.FileRef{{Path: "./templates/a.json"}}
			m.Contents.Themes = []domain.FileRef{{Path: "${PLUGIN_ROOT}/themes/t.json"}}
			m.Contents.Runners = []domain.Runner{validRunner()}
			m.Contents.Panels = []domain.Panel{validPanel()}
			e := validEmitter()
			e.Catalog = "./events.json"
			m.Contents.Emitters = []domain.Emitter{e}
			m.Compat.Arch = []string{"amd64", "arm64"}
			m.Compat.RunnerProtocol = []string{"1.0"}
			m.Source.SHA256 = strings.Repeat("a", 64)
			m.Homepage = "https://example.com/plugin"
		}},
		{"remote url with placeholders", func(m *domain.Manifest) {
			m.Contents.McpServers = []domain.McpServer{{Name: "r", Transport: domain.TransportHTTP, URL: "https://{tenant}.example.com/${user_config.PATH}/mcp"}}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			m := validManifest()
			tt.change(&m)
			if err := m.Validate(); err != nil {
				t.Fatalf("Validate() error = %v", err)
			}
			if _, err := m.Encode(); err != nil {
				t.Fatalf("Encode() error = %v", err)
			}
		})
	}
}

func TestManifest_Kinds_NamesEveryKind(t *testing.T) {
	t.Parallel()
	m := validManifest()
	m.Contents.McpServers = []domain.McpServer{{Name: "x", Transport: domain.TransportStdio, Command: "c"}}
	m.Contents.AgentTemplates = []domain.FileRef{{Path: "./a.json"}}
	m.Contents.Themes = []domain.FileRef{{Path: "./t.json"}}
	m.Contents.Runners = []domain.Runner{validRunner()}
	m.Contents.Panels = []domain.Panel{validPanel()}
	m.Contents.Emitters = []domain.Emitter{validEmitter()}
	if got := len(m.Kinds()); got != 7 {
		t.Fatalf("Kinds() = %v, want seven kinds", m.Kinds())
	}
}

func TestParseManifest_Refusals(t *testing.T) {
	t.Parallel()
	base := func(contents string) string {
		return `{"name":"demo","version":"1.0.0","description":"d","author":{"name":"a"},"license":"MIT",` +
			`"source":{"kind":"agentsone","ref":"./x"},"compat":{"agentsone":">=0.1.0"},"contents":` + contents + `}`
	}
	full := func(mcp string) string {
		return `{"mcpServers":` + mcp + `,"skills":[],"agentTemplates":[],"runners":[],"panels":[],"themes":[],"emitters":[]}`
	}
	tests := []struct {
		name string
		raw  string
	}{
		{"not JSON", `{`},
		{"contents list missing", base(`{"mcpServers":[]}`)},
		{"unknown field", strings.Replace(base(full(`[]`)), `"name":"demo"`, `"name":"demo","extra":1`, 1)},
		{"nothing in it", base(full(`[]`))},
		{"mcp server with unknown transport", base(full(`[{"name":"x","transport":"sse","url":"https://a.example"}]`))},
		{"mcp stdio with url", base(full(`[{"name":"x","transport":"stdio","command":"c","args":[],"env":{},"url":"https://a.example"}]`))},
		{"mcp http with command", base(full(`[{"name":"x","transport":"streamable-http","url":"https://a.example","command":"c"}]`))},
		{"mcp server with unknown field", base(full(`[{"name":"x","transport":"stdio","command":"c","bogus":1}]`))},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if _, err := domain.ParseManifest([]byte(tt.raw)); !errors.Is(err, domain.ErrInvalidManifest) {
				t.Fatalf("ParseManifest() error = %v, want ErrInvalidManifest", err)
			}
		})
	}
}

func TestMcpServer_JSONRoundTrip(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   domain.McpServer
		want []string
	}{
		{"stdio with nil args and env writes empty ones", domain.McpServer{Name: "a", Transport: domain.TransportStdio, Command: "c"},
			[]string{`"args":[]`, `"env":{}`, `"command":"c"`}},
		{"stdio keeps args and env", domain.McpServer{Name: "a", Transport: domain.TransportStdio, Command: "c", Args: []string{"-x"}, Env: map[string]string{"K": "v"}},
			[]string{`"args":["-x"]`, `"env":{"K":"v"}`}},
		{"http writes url and headers", domain.McpServer{Name: "b", Transport: domain.TransportHTTP, URL: "https://a.example/mcp", Headers: map[string]string{"X-A": "1"}},
			[]string{`"url":"https://a.example/mcp"`, `"headers":{"X-A":"1"}`}},
		{"http without headers", domain.McpServer{Name: "b", Transport: domain.TransportHTTP, URL: "https://a.example/mcp"},
			[]string{`"url":"https://a.example/mcp"`}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			raw, err := json.Marshal(tt.in)
			if err != nil {
				t.Fatalf("Marshal() error = %v", err)
			}
			for _, w := range tt.want {
				if !strings.Contains(string(raw), w) {
					t.Errorf("JSON %s lacks %s", raw, w)
				}
			}
			var back domain.McpServer
			if err := json.Unmarshal(raw, &back); err != nil {
				t.Fatalf("Unmarshal() error = %v", err)
			}
			if back.Name != tt.in.Name || back.Transport != tt.in.Transport || back.Command != tt.in.Command || back.URL != tt.in.URL {
				t.Errorf("round trip = %+v, want %+v", back, tt.in)
			}
		})
	}
}

func TestMcpServer_UnmarshalJSON_Refusals(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		raw  string
	}{
		{"not an object", `[]`},
		{"unknown field", `{"name":"x","transport":"stdio","command":"c","bogus":1}`},
		{"unknown transport", `{"name":"x","transport":"sse"}`},
		{"missing transport", `{"name":"x"}`},
		{"stdio with url", `{"name":"x","transport":"stdio","url":"https://a.example"}`},
		{"stdio with headers", `{"name":"x","transport":"stdio","headers":{"A":"b"}}`},
		{"http with command", `{"name":"x","transport":"streamable-http","command":"c"}`},
		{"http with args", `{"name":"x","transport":"streamable-http","args":[]}`},
		{"http with env", `{"name":"x","transport":"streamable-http","env":{}}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var s domain.McpServer
			if err := json.Unmarshal([]byte(tt.raw), &s); err == nil {
				t.Fatalf("Unmarshal(%s) = %+v, want error", tt.raw, s)
			}
		})
	}
}
