// SPDX-License-Identifier: MIT

package main

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	schemasDir     = "contract/schemas"
	contractPrefix = "github.com/SDK-E/agentsone/internal/contract/"
	protocolDir    = "runners/sdk/protocol"
	protocolImport = "github.com/SDK-E/agentsone/runners/sdk/protocol"
)

// schemaPackages maps a schema file name (without .schema.json) to its Go
// package directory. Runner schemas all go to the public protocol package.
var schemaPackages = map[string]string{
	"common":          "internal/contract/common",
	"event":           "internal/contract/event",
	"events-catalog":  "internal/contract/event",
	"plugin-manifest": "internal/contract/plugin",
	"theme":           "internal/contract/theme",
	"memory-record":   "internal/contract/memory",
	"audit-record":    "internal/contract/audit",
	"agent-template":  "internal/contract/agenttemplate",
}

type schemaFile struct {
	rel string // path relative to the repository root
	id  string
	dir string // output package directory
}

func generateSchemas(r repo) error {
	files, err := findSchemas(r)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return nil
	}
	args := []string{"go-jsonschema", "--only-models", "--tags", "json", "--capitalization", "ID",
		"--struct-name-from-title", "--resolve-extension", "json"}
	dirs := map[string]bool{}
	for _, f := range files {
		args = append(args,
			"--schema-package", f.id+"="+importPath(f.dir),
			"--schema-output", f.id+"="+r.path(f.dir+"/"+outputName(f.rel)))
		dirs[f.dir] = true
	}
	for dir := range dirs {
		if err := r.removeGenerated(dir); err != nil {
			return err
		}
	}
	for _, f := range files {
		args = append(args, r.path(f.rel))
	}
	if _, err := r.goTool(args...); err != nil {
		return err
	}
	return addHeaders(r, files)
}

func findSchemas(r repo) ([]schemaFile, error) {
	var files []schemaFile
	err := filepath.WalkDir(r.path(schemasDir), func(p string, d fs.DirEntry, err error) error {
		if os.IsNotExist(err) {
			return fs.SkipDir
		}
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".schema.json") {
			return err
		}
		rel, err := filepath.Rel(r.root, p)
		if err != nil {
			return fmt.Errorf("relative path of %s: %w", p, err)
		}
		f, err := newSchemaFile(filepath.ToSlash(rel), p)
		if err != nil {
			return err
		}
		files = append(files, f)
		return nil
	})
	sort.Slice(files, func(i, j int) bool { return files[i].rel < files[j].rel })
	return files, err
}

func newSchemaFile(rel, abs string) (schemaFile, error) {
	raw, err := os.ReadFile(abs) // #nosec G304 -- schema files inside the repository
	if err != nil {
		return schemaFile{}, fmt.Errorf("read %s: %w", rel, err)
	}
	var head struct {
		ID string `json:"$id"`
	}
	if err := json.Unmarshal(raw, &head); err != nil {
		return schemaFile{}, fmt.Errorf("parse %s: %w", rel, err)
	}
	if head.ID == "" {
		return schemaFile{}, fmt.Errorf("%s has no $id", rel)
	}
	dir, err := packageDirFor(rel)
	if err != nil {
		return schemaFile{}, err
	}
	return schemaFile{rel: rel, id: head.ID, dir: dir}, nil
}

func packageDirFor(rel string) (string, error) {
	inSchemas := strings.TrimPrefix(rel, schemasDir+"/")
	if strings.HasPrefix(inSchemas, "runner/") {
		return protocolDir, nil
	}
	name := strings.TrimSuffix(filepath.Base(rel), ".schema.json")
	if strings.Contains(inSchemas, "/") {
		name = strings.SplitN(inSchemas, "/", 2)[0]
	}
	dir, ok := schemaPackages[name]
	if !ok {
		return "", fmt.Errorf("%s has no Go package; add it to schemaPackages in contractgen (a contract change)", rel)
	}
	return dir, nil
}

func importPath(dir string) string {
	if dir == protocolDir {
		return protocolImport
	}
	return contractPrefix + strings.TrimPrefix(dir, "internal/contract/")
}

func outputName(rel string) string {
	base := strings.TrimSuffix(filepath.Base(rel), ".schema.json")
	return strings.ReplaceAll(base, "-", "_") + ".gen.go"
}

// addHeaders puts the SPDX line on files written by go-jsonschema.
func addHeaders(r repo, files []schemaFile) error {
	for _, f := range files {
		rel := f.dir + "/" + outputName(f.rel)
		b, err := os.ReadFile(r.path(rel))
		if os.IsNotExist(err) {
			continue // a schema with only $defs used elsewhere may produce no file
		}
		if err != nil {
			return fmt.Errorf("read %s: %w", rel, err)
		}
		if err := r.writeGo(rel, b); err != nil {
			return err
		}
	}
	return nil
}
