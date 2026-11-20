// SPDX-License-Identifier: MIT

package contracttest

import (
	"fmt"
	"path/filepath"
	"runtime"
	"sync"
)

// wakePayloads names, for each wake reason, the schema and definition its
// payload must match (WakeParams in contract/schemas/runner/messages.schema.json).
// An empty definition means the whole schema.
var wakePayloads = map[string]struct{ schema, def string }{
	"chat":    {"messages", "ChatMessage"},
	"message": {"messages", "BusMessage"},
	"task":    {"messages", "Task"},
	"timer":   {"messages", "TimerFired"},
	"event":   {"event", ""},
}

var (
	repoSchemasOnce sync.Once
	repoSchemas     *SchemaSet
	repoSchemasErr  error
)

// ValidateWakePayload checks payload, the payload of a wake with reason
// (core.WakeReason), against the runner contract. The runner refuses a wake
// whose payload does not match, so every module that wakes agents checks
// its payloads with this in its tests.
func ValidateWakePayload(reason string, payload []byte) error {
	def, ok := wakePayloads[reason]
	if !ok {
		return fmt.Errorf("wake reason %q is not in the runner contract", reason)
	}
	set, err := contractSchemas()
	if err != nil {
		return err
	}
	if def.def == "" {
		err = set.Validate(def.schema, payload)
	} else {
		err = set.ValidateDef(def.schema, def.def, payload)
	}
	if err != nil {
		return fmt.Errorf("%s wake payload: %w", reason, err)
	}
	return nil
}

// contractSchemas loads the repository's contract/schemas once, found from
// this file's place in the repository.
func contractSchemas() (*SchemaSet, error) {
	repoSchemasOnce.Do(func() {
		_, file, _, ok := runtime.Caller(0)
		if !ok {
			repoSchemasErr = fmt.Errorf("cannot find the contract schemas: no caller information")
			return
		}
		repoSchemas, repoSchemasErr = NewSchemaSet(filepath.Join(filepath.Dir(file), "..", "..", "..", "contract", "schemas"))
	})
	return repoSchemas, repoSchemasErr
}
