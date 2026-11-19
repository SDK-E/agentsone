// SPDX-License-Identifier: MIT

package domain

import (
	"fmt"
	"strings"
)

// RegistryServer is a server.json of the MCP registry, the parts Agents One
// reads.
type RegistryServer struct {
	Name        string            `json:"name"`
	Version     string            `json:"version"`
	Description string            `json:"description"`
	Title       string            `json:"title"`
	WebsiteURL  string            `json:"websiteUrl"`
	Repository  RegistryRepo      `json:"repository"`
	Packages    []RegistryPackage `json:"packages"`
	Remotes     []RegistryRemote  `json:"remotes"`
}

// RegistryRepo is a server's source repository.
type RegistryRepo struct {
	URL string `json:"url"`
}

// RegistryPackage is one installable package of a server.
type RegistryPackage struct {
	RegistryType         string             `json:"registryType"`
	Identifier           string             `json:"identifier"`
	Version              string             `json:"version"`
	Transport            RegistryTransport  `json:"transport"`
	PackageArguments     []RegistryArgument `json:"packageArguments"`
	EnvironmentVariables []RegistryEnv      `json:"environmentVariables"`
}

// RegistryTransport is how a package talks MCP.
type RegistryTransport struct {
	Type string `json:"type"`
}

// RegistryArgument is a command line argument of a package.
type RegistryArgument struct {
	Type  string `json:"type"` // positional or named
	Name  string `json:"name"`
	Value string `json:"value"`
}

// RegistryEnv is an environment variable of a package.
type RegistryEnv struct {
	Name     string `json:"name"`
	Value    string `json:"value"`
	Default  string `json:"default"`
	IsSecret bool   `json:"isSecret"`
}

// RegistryRemote is a hosted endpoint of a server.
type RegistryRemote struct {
	Type    string           `json:"type"`
	URL     string           `json:"url"`
	Headers []RegistryHeader `json:"headers"`
}

// RegistryHeader is a request header of a remote.
type RegistryHeader struct {
	Name     string `json:"name"`
	Value    string `json:"value"`
	IsSecret bool   `json:"isSecret"`
}

// RegistryOptions are facts about the machine the mapping depends on.
type RegistryOptions struct {
	// ContainerEngine is true when podman is installed, so OCI packages work.
	ContainerEngine bool
}

// RegistryPluginName is the plugin name of a registry server name: the part
// after the last "/", lowercased, other characters replaced by "-".
func RegistryPluginName(serverName string) string {
	base := serverName
	if i := strings.LastIndex(serverName, "/"); i >= 0 {
		base = serverName[i+1:]
	}
	return SanitizeName(base)
}

// RegistryRef is source.ref of a registry plugin.
func RegistryRef(name, version string) string { return name + "@" + version }

// NormalizeRegistryServer maps a registry server to a plugin with MCP
// servers (plugins/SOURCES.md). Secrets are never written into the manifest.
func NormalizeRegistryServer(s RegistryServer, opt RegistryOptions) (Normalized, error) {
	if !IsSemver(s.Version) {
		return Normalized{}, errNotSemantic(s.Version)
	}
	var out Normalized
	name := RegistryPluginName(s.Name)
	used := map[string]bool{}
	var servers []McpServer
	add := func(srv McpServer, suffix string) {
		srv.Name = uniqueName(name, suffix, used)
		servers = append(servers, srv)
	}
	for _, p := range s.Packages {
		srv, ok := mapPackage(p, opt, &out.Report)
		if ok {
			add(srv, p.RegistryType)
		}
	}
	for _, r := range s.Remotes {
		srv, ok := mapRemote(r, &out.Report)
		if ok {
			add(srv, "remote")
		}
	}
	if len(servers) == 0 {
		return Normalized{}, fmt.Errorf("%s has no package or remote Agents One can run: %w", s.Name, ErrNothingToInstall)
	}
	homepage := s.WebsiteURL
	if homepage == "" {
		homepage = s.Repository.URL
	}
	m := Manifest{
		Name: name, Version: s.Version, Description: cut(oneLine(s.Description), 255),
		Author:   Author{Name: authorFromRegistry(s.Name)},
		License:  noAssertion,
		Homepage: homepage,
		Source:   Source{Kind: SourceMCPRegistry, Ref: RegistryRef(s.Name, s.Version)},
		Compat:   Compat{AgentsOne: DefaultCompat},
		Contents: Contents{McpServers: servers},
	}
	if err := m.Validate(); err != nil {
		return Normalized{}, err
	}
	out.Manifest = m
	return out, nil
}

// authorFromRegistry uses the reverse-DNS namespace as the publisher.
func authorFromRegistry(serverName string) string {
	ns, _, ok := strings.Cut(serverName, "/")
	if !ok || ns == "" {
		return "Unknown"
	}
	return ns
}

func uniqueName(base, suffix string, used map[string]bool) string {
	name := base
	if used[name] {
		name = SanitizeName(base + "-" + suffix)
	}
	for n := 2; used[name]; n++ {
		name = SanitizeName(fmt.Sprintf("%s-%d", base, n))
	}
	used[name] = true
	return name
}

func mapPackage(p RegistryPackage, opt RegistryOptions, rep *Report) (McpServer, bool) {
	if p.Transport.Type != "stdio" {
		rep.skip("package %s: transport %q is not stdio", p.Identifier, p.Transport.Type)
		return McpServer{}, false
	}
	args := packageArgs(p.PackageArguments)
	env := map[string]string{}
	var envFlags []string
	for _, e := range p.EnvironmentVariables {
		value := e.Value
		if value == "" {
			value = e.Default
		}
		switch {
		case !envNameRe.MatchString(e.Name):
			rep.skip("package %s: environment variable %q has an invalid name", p.Identifier, e.Name)
		case e.IsSecret:
			rep.note("%s asks for a secret, %s, when the server is enabled", p.Identifier, e.Name)
		case value == "":
			rep.note("%s needs a value for %s when the server is enabled", p.Identifier, e.Name)
		default:
			env[e.Name] = value
			envFlags = append(envFlags, "-e", e.Name+"="+value)
		}
	}
	switch p.RegistryType {
	case "npm":
		return McpServer{Transport: TransportStdio, Command: "npx", Args: append([]string{"-y", p.Identifier + "@" + p.Version}, args...), Env: env}, true
	case "pypi":
		return McpServer{Transport: TransportStdio, Command: "uvx", Args: append([]string{p.Identifier + "==" + p.Version}, args...), Env: env}, true
	case "oci":
		if !opt.ContainerEngine {
			rep.skip("package %s: needs a container engine, none is installed", p.Identifier)
			return McpServer{}, false
		}
		run := append([]string{"run", "-i", "--rm"}, envFlags...)
		run = append(run, p.Identifier)
		return McpServer{Transport: TransportStdio, Command: "podman", Args: append(run, args...), Env: map[string]string{}}, true
	}
	rep.skip("package %s: registry type %q is not mapped in version 1", p.Identifier, p.RegistryType)
	return McpServer{}, false
}

func packageArgs(in []RegistryArgument) []string {
	var out []string
	for _, a := range in {
		if a.Type == "named" && a.Name != "" {
			out = append(out, a.Name)
		}
		if a.Value != "" {
			out = append(out, a.Value)
		}
	}
	return out
}

func mapRemote(r RegistryRemote, rep *Report) (McpServer, bool) {
	if r.Type != "streamable-http" {
		rep.skip("remote %s: type %q (sse) is not mapped", r.URL, r.Type)
		return McpServer{}, false
	}
	headers := map[string]string{}
	for _, h := range r.Headers {
		if !headerNameRe.MatchString(h.Name) {
			rep.skip("remote %s: header %q is invalid", r.URL, h.Name)
			continue
		}
		switch {
		case h.IsSecret:
			headers[h.Name] = "${user_config." + strings.ToUpper(strings.ReplaceAll(h.Name, "-", "_")) + "}"
			rep.note("%s asks for %s when the server is enabled", r.URL, h.Name)
		case h.Value != "":
			headers[h.Name] = h.Value
		}
	}
	if len(headers) == 0 {
		headers = nil
	}
	return McpServer{Transport: TransportHTTP, URL: r.URL, Headers: headers}, true
}
