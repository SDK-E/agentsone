// SPDX-License-Identifier: MIT

package contract_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/SDK-E/agentsone/internal/auditchain"
	"github.com/SDK-E/agentsone/internal/contract/api"
	"github.com/SDK-E/agentsone/internal/contract/contracttest"
	"github.com/SDK-E/agentsone/internal/core"
)

const (
	auditSchema = "audit-record"
	eventSchema = "event"
)

// contractDirEnv points the tests at another contract folder, to try
// fixtures before they are committed.
const contractDirEnv = "AGENTSONE_CONTRACT_DIR"

// contractPath resolves a path inside the contract folder (rel has no
// leading "contract/").
func contractPath(t *testing.T, rel string) string {
	t.Helper()
	if dir := os.Getenv(contractDirEnv); dir != "" {
		return filepath.Join(dir, filepath.FromSlash(rel))
	}
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate the test file")
	}
	return filepath.Join(filepath.Dir(file), "..", "..", "contract", filepath.FromSlash(rel))
}

// requireDir skips the test until the contract owner adds files there.
func requireDir(t *testing.T, rel string) string {
	t.Helper()
	dir := contractPath(t, rel)
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) || (err == nil && len(entries) == 0) {
		t.Skipf("contract/%s has no files yet", rel)
	}
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func schemaSet(t *testing.T) *contracttest.SchemaSet {
	t.Helper()
	set, err := contracttest.NewSchemaSet(requireDir(t, "schemas"))
	if err != nil {
		t.Fatal(err)
	}
	return set
}

func readJSONFiles(t *testing.T, dir string) map[string][]byte {
	t.Helper()
	files := map[string][]byte{}
	matches, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range matches {
		b, err := os.ReadFile(m) // #nosec G304 -- contract fixtures
		if err != nil {
			t.Fatal(err)
		}
		files[filepath.Base(m)] = b
	}
	return files
}

// documents splits a fixture into the documents it holds: a JSON array is a
// list (for example an audit chain), anything else is one document.
func documents(raw []byte) ([]json.RawMessage, bool) {
	var list []json.RawMessage
	if bytes.HasPrefix(bytes.TrimSpace(raw), []byte("[")) && json.Unmarshal(raw, &list) == nil {
		return list, true
	}
	return []json.RawMessage{raw}, false
}

func TestSchemaFixtures(t *testing.T) {
	root := requireDir(t, "fixtures/schemas")
	set := schemaSet(t)
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		t.Run(name, func(t *testing.T) {
			for file, raw := range readJSONFiles(t, filepath.Join(root, name, "valid")) {
				docs, _ := documents(raw)
				for i, doc := range docs {
					if err := set.Validate(name, doc); err != nil {
						t.Errorf("valid/%s[%d] rejected: %v", file, i, err)
					}
				}
			}
			for file, raw := range readJSONFiles(t, filepath.Join(root, name, "invalid")) {
				if !isInvalid(set, name, raw) {
					t.Errorf("invalid/%s accepted", file)
				}
			}
		})
	}
}

// isInvalid reports whether a fixture from an invalid/ folder is rejected:
// one document fails its schema, or an audit chain fails to verify.
func isInvalid(set *contracttest.SchemaSet, name string, raw []byte) bool {
	docs, isList := documents(raw)
	for _, doc := range docs {
		if set.Validate(name, doc) != nil {
			return true
		}
	}
	return isList && name == auditSchema && auditchain.Verify(docs) != nil
}

func TestAuditFixtureChainsVerify(t *testing.T) {
	dir := requireDir(t, "fixtures/schemas/"+auditSchema+"/valid")
	chains := 0
	for file, raw := range readJSONFiles(t, dir) {
		docs, isList := documents(raw)
		if !isList {
			continue
		}
		chains++
		if err := auditchain.Verify(docs); err != nil {
			t.Errorf("%s: %v", file, err)
		}
	}
	if chains == 0 {
		t.Error("no audit chain fixture (a JSON array of records) in " + dir)
	}
}

func TestEventFixturesFitCoreEvent(t *testing.T) {
	dir := requireDir(t, "fixtures/schemas/"+eventSchema+"/valid")
	set := schemaSet(t)
	for file, raw := range readJSONFiles(t, dir) {
		var e core.Event
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&e); err != nil {
			t.Errorf("%s does not decode into core.Event: %v", file, err)
			continue
		}
		again, err := json.Marshal(e)
		if err != nil {
			t.Fatal(err)
		}
		if err := set.Validate(eventSchema, again); err != nil {
			t.Errorf("%s re-encoded from core.Event: %v", file, err)
		}
	}
}

func validator(t *testing.T) *contracttest.Validator {
	t.Helper()
	v, err := contracttest.NewValidator(api.Spec)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func streamOperations() map[string]bool {
	streams := map[string]bool{}
	for _, r := range api.Routes {
		if r.Stream {
			streams[r.OperationID] = true
		}
	}
	return streams
}

func TestAPIFixturesMatchTheContract(t *testing.T) {
	fixtures, err := contracttest.LoadAPIFixtures(requireDir(t, "fixtures/api"))
	if err != nil {
		t.Fatal(err)
	}
	v := validator(t)
	streams := streamOperations()
	for op, variants := range fixtures {
		for variant, f := range variants {
			if streams[op] && f.Status < 300 {
				checkStreamFixture(t, f)
				continue
			}
			if err := v.ValidateJSONResponse(op, f.Status, f.Body); err != nil {
				t.Errorf("%s (variant %q): %v", filepath.Base(f.File), variant, err)
			}
		}
	}
}

// checkStreamFixture checks a stream fixture: its body is the list of
// events the stream sends.
func checkStreamFixture(t *testing.T, f contracttest.APIFixture) {
	t.Helper()
	docs, isList := documents(f.Body)
	if !isList {
		t.Errorf("%s: a stream fixture body must be a JSON array of events", filepath.Base(f.File))
		return
	}
	set := schemaSet(t)
	for i, doc := range docs {
		if err := set.Validate(eventSchema, doc); err != nil {
			t.Errorf("%s[%d]: %v", filepath.Base(f.File), i, err)
		}
	}
}

func TestEveryReadOperationHasADefaultFixture(t *testing.T) {
	fixtures, err := contracttest.LoadAPIFixtures(requireDir(t, "fixtures/api"))
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range api.Routes {
		if r.Method != "GET" || r.Stream {
			continue
		}
		if _, ok := fixtures[r.OperationID][""]; !ok {
			t.Errorf("GET %s has no contract/fixtures/api/%s.json", r.Path, r.OperationID)
		}
	}
}
