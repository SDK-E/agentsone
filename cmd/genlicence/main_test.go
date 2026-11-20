// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"

	"github.com/SDK-E/agentsone/internal/auth/adapters/licencefile"
	"github.com/SDK-E/agentsone/internal/enginefetch/domain"
)

func TestRun_WritesAFileTheAuthAdapterReadsWithThePinnedEngine(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "engine", "licence.json")
	var stderr bytes.Buffer
	if code := run([]string{path}, &stderr); code != 0 {
		t.Fatalf("run() = %d, stderr %q", code, stderr.String())
	}
	pin, err := domain.Pinned()
	if err != nil {
		t.Fatal(err)
	}
	engine, version, text, err := licencefile.New(path).EngineLicence(context.Background())
	if err != nil {
		t.Fatalf("EngineLicence() error = %v", err)
	}
	if engine != pin.Engine || version != pin.Version || text != domain.LicenceNotice {
		t.Fatalf("file = %q %q %q, want the pin and the notice", engine, version, text)
	}
}

func TestRun_NeedsExactlyOneFile(t *testing.T) {
	t.Parallel()
	var stderr bytes.Buffer
	if code := run(nil, &stderr); code != 2 {
		t.Fatalf("run() = %d, want 2", code)
	}
	if stderr.Len() == 0 {
		t.Fatal("want a usage line")
	}
}

func TestRun_FailsWhenTheFolderCannotBeCreated(t *testing.T) {
	t.Parallel()
	var stderr bytes.Buffer
	if code := run([]string{"/dev/null/engine/licence.json"}, &stderr); code != 1 {
		t.Fatalf("run() = %d, want 1", code)
	}
}
