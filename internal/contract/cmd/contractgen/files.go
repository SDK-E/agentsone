// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"fmt"
	"go/format"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const spdxGo = "// SPDX-License-Identifier: MIT\n\n"

type repo struct{ root string }

func (r repo) path(rel string) string { return filepath.Join(r.root, filepath.FromSlash(rel)) }

// writeGo formats src, adds the SPDX line and writes it.
func (r repo) writeGo(rel string, src []byte) error {
	formatted, err := format.Source(src)
	if err != nil {
		return fmt.Errorf("format %s: %w", rel, err)
	}
	return r.write(rel, append([]byte(spdxGo), formatted...))
}

func (r repo) write(rel string, b []byte) error {
	p := r.path(rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		return fmt.Errorf("mkdir for %s: %w", rel, err)
	}
	if err := os.WriteFile(p, b, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", rel, err)
	}
	return nil
}

// removeGenerated deletes *.gen.go files under dir so stale output goes away.
func (r repo) removeGenerated(rel string) error {
	dir := r.path(rel)
	return filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(p, ".gen.go") {
			return os.Remove(p)
		}
		return nil
	})
}

// goTool runs a tool declared in go.mod and returns its standard output.
func (r repo) goTool(args ...string) ([]byte, error) {
	cmd := exec.Command("go", append([]string{"tool"}, args...)...) // #nosec G204 -- fixed tool names from go.mod
	cmd.Dir = r.root
	var out, errOut bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("go tool %s: %w: %s", args[0], err, errOut.String())
	}
	return out.Bytes(), nil
}
