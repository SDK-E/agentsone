// SPDX-License-Identifier: MIT

package contracttest_test

import (
	"errors"
	"io/fs"
	"path/filepath"
	"testing"

	"github.com/SDK-E/agentsone/internal/contract/contracttest"
)

func TestLoadAPIFixtures(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "getThing.json"), `{"status": 200, "body": {"name": "a"}}`)
	writeFile(t, filepath.Join(dir, "getThing.missing.json"), `{"status": 404, "body": {"type": "t", "title": "x", "status": 404}}`)
	writeFile(t, filepath.Join(dir, "makeThing.json"), `{"status": 204}`)
	writeFile(t, filepath.Join(dir, "README.md"), `ignored`)

	set, err := contracttest.LoadAPIFixtures(dir)
	if err != nil {
		t.Fatalf("LoadAPIFixtures: %v", err)
	}
	if len(set) != 2 || len(set["getThing"]) != 2 {
		t.Fatalf("fixtures %v", set)
	}
	missing := set["getThing"]["missing"]
	if missing.Status != 404 || missing.OperationID != "getThing" || missing.Variant != "missing" {
		t.Fatalf("variant fixture %+v", missing)
	}
	if def := set["getThing"][""]; def.Status != 200 || string(def.Body) != `{"name": "a"}` {
		t.Fatalf("default fixture %+v body %s", def, def.Body)
	}
	if empty := set["makeThing"][""]; len(empty.Body) != 0 {
		t.Fatalf("no-body fixture has body %s", empty.Body)
	}
}

func TestLoadAPIFixturesRejectsMissingStatus(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "getThing.json"), `{"body": {}}`)
	if _, err := contracttest.LoadAPIFixtures(dir); err == nil {
		t.Fatal("fixture without status accepted")
	}
}

func TestLoadAPIFixturesMissingDir(t *testing.T) {
	_, err := contracttest.LoadAPIFixtures(filepath.Join(t.TempDir(), "absent"))
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("err = %v, want fs.ErrNotExist", err)
	}
}
