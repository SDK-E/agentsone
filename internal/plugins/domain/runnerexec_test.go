// SPDX-License-Identifier: MIT

package domain_test

import (
	"reflect"
	"sort"
	"testing"

	"github.com/SDK-E/agentsone/internal/plugins/domain"
)

func TestRunnerExec_ExpandsPluginRootAndPassesTheSocketInTheEnvironment(t *testing.T) {
	t.Parallel()
	r := domain.Runner{
		Name:    "acme",
		Command: "${PLUGIN_ROOT}/bin/acme",
		Args:    []string{"--config", "${PLUGIN_ROOT}/etc/acme.toml", "serve"},
		Env:     map[string]string{"ACME_HOME": "${PLUGIN_ROOT}", "LEVEL": "info"},
	}

	got := domain.RunnerExec("/var/lib/agentsone/plugins/acme/1.0.0", r, "/run/agentsone/runners/ada.sock")

	if got.Path != "/var/lib/agentsone/plugins/acme/1.0.0/bin/acme" || got.Dir != "/var/lib/agentsone/plugins/acme/1.0.0" {
		t.Errorf("path %q dir %q", got.Path, got.Dir)
	}
	wantArgs := []string{"--config", "/var/lib/agentsone/plugins/acme/1.0.0/etc/acme.toml", "serve"}
	if !reflect.DeepEqual(got.Args, wantArgs) {
		t.Errorf("args = %v, want %v", got.Args, wantArgs)
	}
	sort.Strings(got.Env)
	wantEnv := []string{
		"ACME_HOME=/var/lib/agentsone/plugins/acme/1.0.0",
		"AGENTSONE_RUNNER_SOCKET=/run/agentsone/runners/ada.sock",
		"LEVEL=info",
	}
	if !reflect.DeepEqual(got.Env, wantEnv) {
		t.Errorf("env = %v, want %v", got.Env, wantEnv)
	}
}

func TestRunnerExec_ACommandWithoutPluginRootIsLeftAlone(t *testing.T) {
	t.Parallel()
	got := domain.RunnerExec("/p/x/1.0.0", domain.Runner{Name: "x", Command: "python3"}, "/s.sock")
	if got.Path != "python3" {
		t.Errorf("path = %q, want python3", got.Path)
	}
}

func TestBuiltinRunners_AreEchoAndOpenAIUnderTheLibDir(t *testing.T) {
	t.Parallel()
	got := domain.BuiltinRunners("/usr/lib/agentsone")
	if len(got) != 2 || got[0].Name != "echo" || got[1].Name != "openai" {
		t.Fatalf("builtins = %+v", got)
	}
	if got[0].Command != "/usr/lib/agentsone/runners/echo" || got[1].Command != "/usr/lib/agentsone/runners/openai" {
		t.Errorf("commands = %q, %q", got[0].Command, got[1].Command)
	}
	if got[1].Model != "autoagents/one" || got[0].Model != "" {
		t.Errorf("models = %q, %q", got[0].Model, got[1].Model)
	}
}
