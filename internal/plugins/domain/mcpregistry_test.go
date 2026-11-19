// SPDX-License-Identifier: MIT

package domain_test

import (
	"errors"
	"testing"

	"github.com/SDK-E/agentsone/internal/plugins/domain"
)

func registryServer() domain.RegistryServer {
	return domain.RegistryServer{
		Name:        "io.github.Acme/Weather_Server",
		Version:     "1.2.0",
		Description: "Weather over MCP.",
		Title:       "Weather",
		WebsiteURL:  "https://acme.example/weather",
		Packages: []domain.RegistryPackage{{
			RegistryType: "npm", Identifier: "@acme/weather", Version: "1.2.0",
			Transport: domain.RegistryTransport{Type: "stdio"},
			PackageArguments: []domain.RegistryArgument{
				{Type: "positional", Value: "serve"},
				{Type: "named", Name: "--units", Value: "metric"},
			},
			EnvironmentVariables: []domain.RegistryEnv{
				{Name: "ACME_REGION", Value: "eu"},
				{Name: "ACME_KEY", IsSecret: true},
				{Name: "ACME_CACHE"},
			},
		}},
	}
}

func TestNormalizeRegistryServer_NPMStdioPackage(t *testing.T) {
	t.Parallel()

	got, err := domain.NormalizeRegistryServer(registryServer(), domain.RegistryOptions{})
	if err != nil {
		t.Fatalf("NormalizeRegistryServer() error = %v", err)
	}

	m := got.Manifest
	if m.Name != "weather-server" || m.Version != "1.2.0" || m.Description != "Weather over MCP." {
		t.Errorf("header = %+v", m)
	}
	if m.Source.Kind != domain.SourceMCPRegistry || m.Source.Ref != "io.github.Acme/Weather_Server@1.2.0" {
		t.Errorf("source = %+v", m.Source)
	}
	if m.License != "NOASSERTION" || m.Homepage != "https://acme.example/weather" {
		t.Errorf("license %q homepage %q", m.License, m.Homepage)
	}
	if len(m.Contents.McpServers) != 1 {
		t.Fatalf("servers = %+v", m.Contents.McpServers)
	}
	s := m.Contents.McpServers[0]
	wantArgs := []string{"-y", "@acme/weather@1.2.0", "serve", "--units", "metric"}
	if s.Transport != "stdio" || s.Command != "npx" || !equalStrings(s.Args, wantArgs) {
		t.Errorf("server = %+v, want npx %v", s, wantArgs)
	}
	if err := m.Validate(); err != nil {
		t.Errorf("manifest invalid: %v", err)
	}
}

func TestNormalizeRegistryServer_SecretsAreNeverWrittenIntoTheManifest(t *testing.T) {
	t.Parallel()
	s := registryServer()
	s.Packages[0].EnvironmentVariables[1].Value = "sk-live-123"

	got, err := domain.NormalizeRegistryServer(s, domain.RegistryOptions{})
	if err != nil {
		t.Fatal(err)
	}

	env := got.Manifest.Contents.McpServers[0].Env
	if _, ok := env["ACME_KEY"]; ok {
		t.Errorf("secret written into env: %v", env)
	}
	if env["ACME_REGION"] != "eu" {
		t.Errorf("plain value lost: %v", env)
	}
	if !reportMentions(got.Report.Notes, "ACME_KEY") || !reportMentions(got.Report.Notes, "ACME_CACHE") {
		t.Errorf("report does not ask for the missing settings: %v", got.Report.Notes)
	}
	raw, _ := got.Manifest.Encode()
	if contains(string(raw), "sk-live-123") {
		t.Error("secret value reached the encoded manifest")
	}
}

func TestNormalizeRegistryServer_PypiAndOCI(t *testing.T) {
	t.Parallel()
	s := domain.RegistryServer{
		Name: "io.example/multi", Version: "2.0.0", Description: "Two packages.",
		Packages: []domain.RegistryPackage{
			{RegistryType: "pypi", Identifier: "multi-mcp", Version: "2.0.0", Transport: domain.RegistryTransport{Type: "stdio"}},
			{RegistryType: "oci", Identifier: "ghcr.io/example/multi:2.0.0", Transport: domain.RegistryTransport{Type: "stdio"}},
			{RegistryType: "nuget", Identifier: "Multi", Version: "2.0.0", Transport: domain.RegistryTransport{Type: "stdio"}},
		},
	}

	withEngine, err := domain.NormalizeRegistryServer(s, domain.RegistryOptions{ContainerEngine: true})
	if err != nil {
		t.Fatal(err)
	}
	servers := withEngine.Manifest.Contents.McpServers
	if len(servers) != 2 {
		t.Fatalf("servers = %+v", servers)
	}
	if servers[0].Command != "uvx" || servers[0].Args[0] != "multi-mcp==2.0.0" {
		t.Errorf("pypi server = %+v", servers[0])
	}
	if servers[1].Command != "podman" || !equalStrings(servers[1].Args[:4], []string{"run", "-i", "--rm", "ghcr.io/example/multi:2.0.0"}) {
		t.Errorf("oci server = %+v", servers[1])
	}
	if !reportMentions(withEngine.Report.NotMapped, "nuget") {
		t.Errorf("nuget not reported: %v", withEngine.Report.NotMapped)
	}
	if servers[0].Name == servers[1].Name {
		t.Errorf("both servers are named %q", servers[0].Name)
	}

	without, err := domain.NormalizeRegistryServer(s, domain.RegistryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(without.Manifest.Contents.McpServers) != 1 || !reportMentions(without.Report.NotMapped, "container engine") {
		t.Errorf("without engine: servers %+v report %v", without.Manifest.Contents.McpServers, without.Report.NotMapped)
	}
}

func TestNormalizeRegistryServer_RemoteBecomesStreamableHTTPWithSettings(t *testing.T) {
	t.Parallel()
	s := domain.RegistryServer{
		Name: "io.example/remote", Version: "1.0.0", Description: "Remote.",
		Remotes: []domain.RegistryRemote{
			{Type: "streamable-http", URL: "https://{tenant}.example.com/mcp", Headers: []domain.RegistryHeader{
				{Name: "X-Region", Value: "eu"}, {Name: "Authorization", IsSecret: true},
			}},
			{Type: "sse", URL: "https://old.example.com/sse"},
		},
	}

	got, err := domain.NormalizeRegistryServer(s, domain.RegistryOptions{})
	if err != nil {
		t.Fatal(err)
	}

	if len(got.Manifest.Contents.McpServers) != 1 {
		t.Fatalf("servers = %+v", got.Manifest.Contents.McpServers)
	}
	srv := got.Manifest.Contents.McpServers[0]
	if srv.Transport != "streamable-http" || srv.URL != "https://{tenant}.example.com/mcp" {
		t.Errorf("server = %+v", srv)
	}
	if srv.Headers["X-Region"] != "eu" || srv.Headers["Authorization"] != "${user_config.AUTHORIZATION}" {
		t.Errorf("headers = %v", srv.Headers)
	}
	if !reportMentions(got.Report.NotMapped, "sse") {
		t.Errorf("sse not reported: %v", got.Report.NotMapped)
	}
}

func TestNormalizeRegistryServer_Refusals(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		change func(*domain.RegistryServer)
		want   error
	}{
		{"version not semantic", func(s *domain.RegistryServer) { s.Version = "latest" }, domain.ErrNotSemantic},
		{"no usable package or remote", func(s *domain.RegistryServer) { s.Packages = nil }, domain.ErrNothingToInstall},
		{"empty description", func(s *domain.RegistryServer) { s.Description = "" }, domain.ErrInvalidManifest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s := registryServer()
			tt.change(&s)
			if _, err := domain.NormalizeRegistryServer(s, domain.RegistryOptions{}); !errors.Is(err, tt.want) {
				t.Fatalf("error = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestRegistryPluginName_FollowsTheSpecRule(t *testing.T) {
	t.Parallel()
	tests := map[string]string{
		"io.github.owner/name":     "name",
		"io.github.owner/Some_Mcp": "some-mcp",
		"com.example/9lives":       "p-9lives",
		"plain":                    "plain",
	}
	for in, want := range tests {
		if got := domain.RegistryPluginName(in); got != want {
			t.Errorf("RegistryPluginName(%q) = %q, want %q", in, got, want)
		}
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
