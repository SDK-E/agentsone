// SPDX-License-Identifier: MIT

package contract_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"github.com/SDK-E/agentsone/internal/contract/contracttest"
)

const runnerMessages = "messages"

// resultDefs names the $defs entry each request's result must match.
var resultDefs = map[string]string{
	"hello":     "HelloResult",
	"wake":      "Ack",
	"deliver":   "Ack",
	"cancel":    "Ack",
	"shutdown":  "Ack",
	"task.done": "Ack",
	"sleep":     "SleepResult",
	"bus.send":  "BusSendResult",
}

type rpcLine struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Result json.RawMessage `json:"result"`
}

type pendingRequest struct {
	id     string
	method string
	line   int
}

func transcriptLines(t *testing.T) [][]byte {
	t.Helper()
	return transcriptFileLines(t, "transcript.ndjson")
}

func transcriptFileLines(t *testing.T, name string) [][]byte {
	t.Helper()
	raw, err := os.ReadFile(contractPath(t, "fixtures/schemas/runner-message/"+name)) // #nosec G304 -- contract fixture
	if os.IsNotExist(err) {
		t.Skip("contract/fixtures/schemas/runner-message/" + name + " does not exist yet")
	}
	if err != nil {
		t.Fatal(err)
	}
	return bytes.Split(bytes.TrimSpace(raw), []byte("\n"))
}

// checkTranscript validates every line, pairs each response with the
// earliest open request of the same id (ids are per direction, so the same
// id can be open once each way) and checks the result against the method's
// result definition.
func checkTranscript(set *contracttest.SchemaSet, lines [][]byte) []error {
	var errs []error
	var pending []pendingRequest
	for i, line := range lines {
		n := i + 1
		if err := set.Validate(runnerMessages, line); err != nil {
			errs = append(errs, fmt.Errorf("line %d: %w", n, err))
			continue
		}
		var msg rpcLine
		if err := json.Unmarshal(line, &msg); err != nil {
			errs = append(errs, fmt.Errorf("line %d: %w", n, err))
			continue
		}
		switch {
		case msg.Method != "" && msg.ID != nil:
			if _, ok := resultDefs[msg.Method]; !ok {
				errs = append(errs, fmt.Errorf("line %d: request %s has no known result", n, msg.Method))
			}
			pending = append(pending, pendingRequest{id: string(msg.ID), method: msg.Method, line: n})
		case msg.ID != nil:
			var err error
			pending, err = answer(set, pending, msg, n)
			if err != nil {
				errs = append(errs, err)
			}
		}
	}
	for _, p := range pending {
		errs = append(errs, fmt.Errorf("line %d: %s request %s never answered", p.line, p.method, p.id))
	}
	return errs
}

func answer(set *contracttest.SchemaSet, pending []pendingRequest, msg rpcLine, line int) ([]pendingRequest, error) {
	for i, p := range pending {
		if p.id != string(msg.ID) {
			continue
		}
		rest := append(pending[:i:i], pending[i+1:]...)
		if msg.Result == nil {
			return rest, nil // an error response; its shape is checked by the line schema
		}
		if err := set.ValidateDef(runnerMessages, resultDefs[p.method], msg.Result); err != nil {
			return rest, fmt.Errorf("line %d: result of %s (line %d): %w", line, p.method, p.line, err)
		}
		return rest, nil
	}
	return pending, fmt.Errorf("line %d: response %s matches no open request", line, msg.ID)
}

func TestRunnerTranscript(t *testing.T) {
	lines := transcriptLines(t)
	for _, err := range checkTranscript(schemaSet(t), lines) {
		t.Error(err)
	}
}

func TestRunnerTranscript11(t *testing.T) {
	lines := transcriptFileLines(t, "transcript-1.1.ndjson")
	for _, err := range checkTranscript(schemaSet(t), lines) {
		t.Error(err)
	}
	found := false
	for _, l := range lines {
		var msg rpcLine
		if json.Unmarshal(l, &msg) == nil && msg.Method == "credentials.update" {
			found = true
		}
	}
	if !found {
		t.Error("transcript-1.1.ndjson has no credentials.update")
	}
}

func TestRunnerTranscriptCheckCatchesBrokenPairs(t *testing.T) {
	lines := transcriptLines(t)
	set := schemaSet(t)
	firstResponse := -1
	for i, l := range lines {
		var msg rpcLine
		if json.Unmarshal(l, &msg) == nil && msg.Method == "" && msg.ID != nil {
			firstResponse = i
			break
		}
	}
	if firstResponse < 0 {
		t.Skip("transcript has no response")
	}
	dropped := append(append([][]byte{}, lines[:firstResponse]...), lines[firstResponse+1:]...)
	if len(checkTranscript(set, dropped)) == 0 {
		t.Error("an unanswered request passed")
	}
	stray := append(append([][]byte{}, lines...), lines[firstResponse])
	if len(checkTranscript(set, stray)) == 0 {
		t.Error("a response with no request passed")
	}
}
