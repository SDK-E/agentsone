// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/SDK-E/agentsone/internal/mcp/adapters/cli"
)

func execute(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code = run(context.Background(), args, cli.IO{In: strings.NewReader(""), Out: &out, Err: &errOut})
	return code, out.String(), errOut.String()
}

func TestVersionCommandPrintsVersion(t *testing.T) {
	code, out, _ := execute(t, "version")

	if code != 0 || strings.TrimSpace(out) != version {
		t.Fatalf("exit %d, got %q", code, out)
	}
}

func TestCommandsComeFromTheEmbeddedContract(t *testing.T) {
	code, out, errOut := execute(t, "agents", "--help")

	if code != 0 {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}
	if !strings.Contains(out, "hire") {
		t.Fatalf("agents help = %q, want the hire command", out)
	}
}

func TestUnknownCommandExitsWithUsageCode(t *testing.T) {
	code, _, errOut := execute(t, "nonsense")

	if code != cli.ExitUsage || !strings.Contains(errOut, "nonsense") {
		t.Fatalf("exit %d stderr %q", code, errOut)
	}
}
