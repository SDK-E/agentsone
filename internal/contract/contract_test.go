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
	"sort"
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

// fixtureSchemas names the schema of a fixture folder whose name differs
// from the schema file name.
var fixtureSchemas = map[string]string{
	"runner-message": "messages",
}

func schemaForFixtures(folder string) string {
	if name, ok := fixtureSchemas[folder]; ok {
		return name
	}
	return folder
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
		folder := e.Name()
		t.Run(folder, func(t *testing.T) {
			checkFixtureFolder(t, set, schemaForFixtures(folder), filepath.Join(root, folder))
		})
	}
}

func checkFixtureFolder(t *testing.T, set *contracttest.SchemaSet, schema, dir string) {
	t.Helper()
	for file, raw := range readJSONFiles(t, filepath.Join(dir, "valid")) {
		if err := set.Validate(schema, raw); err != nil {
			t.Errorf("valid/%s rejected: %v", file, err)
		}
	}
	for file, raw := range readJSONFiles(t, filepath.Join(dir, "invalid")) {
		if set.Validate(schema, raw) == nil {
			t.Errorf("invalid/%s accepted by schema %s", file, schema)
		}
	}
}

// auditChain reads valid/chain-*.json, one record per file, in name order.
func auditChain(t *testing.T) []json.RawMessage {
	t.Helper()
	dir := requireDir(t, "fixtures/schemas/"+auditSchema+"/valid")
	files, err := filepath.Glob(filepath.Join(dir, "chain-*.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatalf("no chain-*.json audit fixtures in %s", dir)
	}
	sort.Strings(files)
	chain := make([]json.RawMessage, 0, len(files))
	for _, f := range files {
		raw, err := os.ReadFile(f) // #nosec G304 -- contract fixtures
		if err != nil {
			t.Fatal(err)
		}
		chain = append(chain, raw)
	}
	return chain
}

func TestAuditFixtureChainVerifies(t *testing.T) {
	if err := auditchain.Verify(auditChain(t)); err != nil {
		t.Errorf("audit fixture chain: %v", err)
	}
}

func TestAuditFixtureChainCatchesReordering(t *testing.T) {
	chain := auditChain(t)
	if len(chain) < 2 {
		t.Skip("needs at least two chain fixtures")
	}
	chain[0], chain[1] = chain[1], chain[0]
	if auditchain.Verify(chain) == nil {
		t.Error("a reordered chain verified")
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
	var docs []json.RawMessage
	if err := json.Unmarshal(f.Body, &docs); err != nil {
		t.Errorf("%s: a stream fixture body must be a JSON array of events: %v", filepath.Base(f.File), err)
		return
	}
	set := schemaSet(t)
	for i, doc := range docs {
		if err := set.Validate(eventSchema, doc); err != nil {
			t.Errorf("%s[%d]: %v", filepath.Base(f.File), i, err)
		}
	}
}

// websocketOperations lists operations marked x-websocket; they upgrade
// the connection, so a JSON fixture cannot describe them.
func websocketOperations(t *testing.T) map[string]bool {
	t.Helper()
	var doc struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(api.Spec, &doc); err != nil {
		t.Fatal(err)
	}
	ws := map[string]bool{}
	for _, item := range doc.Paths {
		for _, raw := range item {
			var op struct {
				OperationID string          `json:"operationId"`
				WebSocket   json.RawMessage `json:"x-websocket"`
			}
			if json.Unmarshal(raw, &op) == nil && op.WebSocket != nil && string(op.WebSocket) != "false" {
				ws[op.OperationID] = true
			}
		}
	}
	return ws
}

func TestEveryReadOperationHasADefaultFixture(t *testing.T) {
	fixtures, err := contracttest.LoadAPIFixtures(requireDir(t, "fixtures/api"))
	if err != nil {
		t.Fatal(err)
	}
	websockets := websocketOperations(t)
	for _, r := range api.Routes {
		if r.Method != "GET" || r.Stream || websockets[r.OperationID] {
			continue
		}
		if _, ok := fixtures[r.OperationID][""]; !ok {
			t.Errorf("GET %s has no contract/fixtures/api/%s.json", r.Path, r.OperationID)
		}
	}
}
