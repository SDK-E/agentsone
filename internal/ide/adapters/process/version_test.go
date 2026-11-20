// SPDX-License-Identifier: MIT

package process_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/SDK-E/agentsone/internal/ide/adapters/process"
)

func TestVersionReadsTheBakedInServer(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"name":"code-oss-dev","version":"1.143.2"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if v := process.Version(dir); v != "1.143.2" {
		t.Fatalf("version %q", v)
	}
}

func TestVersionIsUnknownWithoutAServer(t *testing.T) {
	if v := process.Version(t.TempDir()); v != "unknown" {
		t.Fatalf("version %q", v)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`not json`), 0o600); err != nil {
		t.Fatal(err)
	}
	if v := process.Version(dir); v != "unknown" {
		t.Fatalf("version of a broken file %q", v)
	}
}
