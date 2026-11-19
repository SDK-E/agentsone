// SPDX-License-Identifier: MIT

package app_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/auditchain"
	"github.com/SDK-E/agentsone/internal/edge/audit/app"
	"github.com/SDK-E/agentsone/internal/edge/audit/domain"
)

var t0 = time.Date(2026, 11, 19, 22, 0, 0, 0, time.UTC)

type clock struct{ now time.Time }

func (c clock) Now() time.Time { return c.now }

// memStore is the in-memory Store used by these tests.
type memStore struct {
	mu      sync.Mutex
	chains  map[app.Chain][]app.Stored
	alerts  []domain.Alert
	failAdd error
}

func newMem() *memStore { return &memStore{chains: map[app.Chain][]app.Stored{}} }

func (m *memStore) Head(_ context.Context, c app.Chain) (domain.Head, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rs := m.chains[c]
	if len(rs) == 0 {
		return domain.Empty(), nil
	}
	return domain.Head{Seq: rs[len(rs)-1].Seq, Hash: rs[len(rs)-1].Hash}, nil
}

func (m *memStore) HashAt(_ context.Context, c app.Chain, seq int64) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, r := range m.chains[c] {
		if r.Seq == seq {
			return r.Hash, nil
		}
	}
	return "", nil
}

func (m *memStore) Append(_ context.Context, c app.Chain, r app.Stored) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failAdd != nil {
		return m.failAdd
	}
	m.chains[c] = append(m.chains[c], r)
	return nil
}

func (m *memStore) List(_ context.Context, c app.Chain, after int64, limit int) ([]app.Stored, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []app.Stored
	for _, r := range m.chains[c] {
		if r.Seq > after && len(out) < limit {
			out = append(out, r)
		}
	}
	return out, nil
}

func (m *memStore) AddAlert(_ context.Context, a domain.Alert) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, have := range m.alerts {
		if have.Kind == a.Kind && have.Seq == a.Seq {
			return nil
		}
	}
	m.alerts = append(m.alerts, a)
	return nil
}

func (m *memStore) Alerts(_ context.Context, limit int) ([]domain.Alert, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := append([]domain.Alert(nil), m.alerts...)
	sort.SliceStable(out, func(i, j int) bool { return i > j })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// machineChain seals n records the way the daemon does.
func machineChain(t *testing.T, n int, salt string) []json.RawMessage {
	t.Helper()
	prev := auditchain.Genesis
	out := make([]json.RawMessage, 0, n)
	for i := 1; i <= n; i++ {
		raw := fmt.Sprintf(`{"seq":%d,"time":"2026-11-19T21:00:00Z","actor":{"kind":"agent","name":"founder"},`+
			`"action":"agent.hire","target":{"kind":"agent","id":"a%s%d"},"outcome":"success","details":{},"origin":"inside"}`, i, salt, i)
		sealed, err := auditchain.Seal([]byte(raw), prev)
		if err != nil {
			t.Fatal(err)
		}
		h, err := auditchain.Hash(sealed)
		if err != nil {
			t.Fatal(err)
		}
		prev = h
		out = append(out, sealed)
	}
	return out
}

func newSink(m *memStore) *app.Sink { return app.New(m, clock{t0}, nil) }

func reject(t *testing.T, err error) *app.RejectError {
	t.Helper()
	var re *app.RejectError
	if !errors.As(err, &re) || !errors.Is(err, app.ErrRejected) {
		t.Fatalf("err = %v, want a RejectError", err)
	}
	return re
}

func TestReceive_KeepsAChainThatContinues(t *testing.T) {
	t.Parallel()
	m := newMem()
	s := newSink(m)
	chain := machineChain(t, 3, "")
	head, err := s.Receive(t.Context(), chain)
	if err != nil {
		t.Fatalf("Receive: %v", err)
	}
	if head.Seq != 3 || len(m.chains[app.Inside]) != 3 {
		t.Fatalf("head = %+v, stored %d; want 3 and 3", head, len(m.chains[app.Inside]))
	}
	if err := s.Verify(t.Context(), app.Inside); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if string(m.chains[app.Inside][0].Raw) != string(chain[0]) {
		t.Fatal("record not kept as written")
	}
}

func TestReceive_IgnoresRecordsSentAgain(t *testing.T) {
	t.Parallel()
	m := newMem()
	s := newSink(m)
	chain := machineChain(t, 3, "")
	if _, err := s.Receive(t.Context(), chain[:2]); err != nil {
		t.Fatal(err)
	}
	head, err := s.Receive(t.Context(), chain)
	if err != nil || head.Seq != 3 || len(m.chains[app.Inside]) != 3 || len(m.alerts) != 0 {
		t.Fatalf("resend: head %+v err %v stored %d alerts %v", head, err, len(m.chains[app.Inside]), m.alerts)
	}
}

func TestReceive_RaisesAGapAndKeepsTheHead(t *testing.T) {
	t.Parallel()
	m := newMem()
	s := newSink(m)
	chain := machineChain(t, 4, "")
	_, err := s.Receive(t.Context(), []json.RawMessage{chain[0], chain[2]})
	re := reject(t, err)
	if re.Verdict != domain.Gap || re.Seq != 3 || re.Head.Seq != 1 {
		t.Fatalf("reject = %+v, want gap at 3 with head 1", re)
	}
	if len(m.alerts) != 1 || m.alerts[0].Kind != "gap" || m.alerts[0].Seq != 3 || !m.alerts[0].Time.Equal(t0) {
		t.Fatalf("alerts = %+v, want one gap at 3", m.alerts)
	}
}

func TestReceive_RaisesAForkWhenTheLogIsRewritten(t *testing.T) {
	t.Parallel()
	m := newMem()
	s := newSink(m)
	if _, err := s.Receive(t.Context(), machineChain(t, 2, "")); err != nil {
		t.Fatal(err)
	}
	_, err := s.Receive(t.Context(), machineChain(t, 2, "rewritten"))
	if re := reject(t, err); re.Verdict != domain.Fork || re.Seq != 1 {
		t.Fatalf("reject = %+v, want fork at 1", re)
	}
	if _, err := s.Receive(t.Context(), machineChain(t, 2, "rewritten")); err == nil {
		t.Fatal("second fork accepted")
	}
	if len(m.alerts) != 1 {
		t.Fatalf("alerts = %+v, want the fork once", m.alerts)
	}
}

func TestReceive_RaisesBrokenOnAlteredContent(t *testing.T) {
	t.Parallel()
	m := newMem()
	s := newSink(m)
	chain := machineChain(t, 1, "")
	var obj map[string]any
	if err := json.Unmarshal(chain[0], &obj); err != nil {
		t.Fatal(err)
	}
	obj["action"] = "agent.retire"
	altered, _ := json.Marshal(obj)
	_, err := s.Receive(t.Context(), []json.RawMessage{altered})
	if re := reject(t, err); re.Verdict != domain.Broken {
		t.Fatalf("reject = %+v, want broken", re)
	}
}

func TestReceive_RefusesRecordsClaimingToBeEdgeProof(t *testing.T) {
	t.Parallel()
	m := newMem()
	s := newSink(m)
	raw := `{"seq":1,"time":"2026-11-19T21:00:00Z","actor":{"kind":"host","name":"agentsone-edge"},` +
		`"action":"freeze.off","target":{"kind":"machine","id":"all"},"outcome":"success","details":{},"origin":"host"}`
	sealed, err := auditchain.Seal([]byte(raw), auditchain.Genesis)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Receive(t.Context(), []json.RawMessage{sealed})
	if re := reject(t, err); re.Verdict != domain.Broken {
		t.Fatalf("reject = %+v, want broken", re)
	}
	if len(m.chains[app.Inside]) != 0 || len(m.alerts) != 1 {
		t.Fatal("a forged proof record was kept or not flagged")
	}
}

func TestReceive_RaisesBrokenOnRecordsThatAreNotJSON(t *testing.T) {
	t.Parallel()
	m := newMem()
	_, err := newSink(m).Receive(t.Context(), []json.RawMessage{json.RawMessage(`[1,2]`)})
	if re := reject(t, err); re.Verdict != domain.Broken {
		t.Fatalf("reject = %+v, want broken", re)
	}
}

func TestReceive_ReportsAStoreFailure(t *testing.T) {
	t.Parallel()
	m := newMem()
	m.failAdd = errors.New("disk full")
	_, err := newSink(m).Receive(t.Context(), machineChain(t, 1, ""))
	if err == nil || errors.Is(err, app.ErrRejected) {
		t.Fatalf("err = %v, want a store error, not a rejection", err)
	}
}

func TestRecordProof_WritesAHostChainThatVerifies(t *testing.T) {
	t.Parallel()
	m := newMem()
	s := newSink(m)
	for _, p := range []app.ProofEntry{
		{Action: "freeze.on", Target: "machine:all", Outcome: "success", Details: map[string]any{"frozen": true}},
		{Action: "net.deny", Target: "network:example.org", Outcome: "denied", Details: map[string]any{"port": 443}},
		{Action: "model.call", Target: "budget", Outcome: "success"},
	} {
		if err := s.RecordProof(t.Context(), p); err != nil {
			t.Fatalf("RecordProof: %v", err)
		}
	}
	if err := s.Verify(t.Context(), app.Proof); err != nil {
		t.Fatalf("Verify proof chain: %v", err)
	}
	var first struct {
		Origin string `json:"origin"`
		Actor  struct {
			Kind string `json:"kind"`
			Name string `json:"name"`
		} `json:"actor"`
		Target struct {
			Kind string `json:"kind"`
			ID   string `json:"id"`
		} `json:"target"`
		Time string `json:"time"`
	}
	if err := json.Unmarshal(m.chains[app.Proof][0].Raw, &first); err != nil {
		t.Fatal(err)
	}
	if first.Origin != "host" || first.Actor.Kind != "host" || first.Actor.Name != app.ActorName ||
		first.Target.Kind != "machine" || first.Target.ID != "all" || first.Time != "2026-11-19T22:00:00Z" {
		t.Fatalf("first proof = %+v", first)
	}
}

func TestRecordProof_RefusesAnInvalidAction(t *testing.T) {
	t.Parallel()
	if err := newSink(newMem()).RecordProof(t.Context(), app.ProofEntry{Action: "Freeze", Target: "machine:all", Outcome: "success"}); err == nil {
		t.Fatal("bad action accepted")
	}
}

func TestVerify_FindsATamperedCopy(t *testing.T) {
	t.Parallel()
	m := newMem()
	s := newSink(m)
	if _, err := s.Receive(t.Context(), machineChain(t, 2, "")); err != nil {
		t.Fatal(err)
	}
	m.chains[app.Inside][1].Raw = machineChain(t, 2, "x")[1]
	if err := s.Verify(t.Context(), app.Inside); err == nil {
		t.Fatal("Verify passed a tampered copy")
	}
}

func TestList_PagesAndClampsTheLimit(t *testing.T) {
	t.Parallel()
	m := newMem()
	s := newSink(m)
	if _, err := s.Receive(t.Context(), machineChain(t, 3, "")); err != nil {
		t.Fatal(err)
	}
	got, err := s.List(t.Context(), app.Inside, 1, 0)
	if err != nil || len(got) != 2 || got[0].Seq != 2 {
		t.Fatalf("List = %v, %v; want seq 2 and 3", got, err)
	}
	if got, _ := s.List(t.Context(), app.Inside, 0, 1); len(got) != 1 {
		t.Fatalf("List limit 1 = %d records", len(got))
	}
	if _, err := s.List(t.Context(), app.Inside, 0, app.MaxLimit+1); err != nil {
		t.Fatalf("List over the cap: %v", err)
	}
}

// downStore fails every call.
type downStore struct{}

var errDown = errors.New("store down")

func (downStore) Head(context.Context, app.Chain) (domain.Head, error) { return domain.Head{}, errDown }
func (downStore) HashAt(context.Context, app.Chain, int64) (string, error) {
	return "", errDown
}
func (downStore) Append(context.Context, app.Chain, app.Stored) error { return errDown }
func (downStore) List(context.Context, app.Chain, int64, int) ([]app.Stored, error) {
	return nil, errDown
}
func (downStore) AddAlert(context.Context, domain.Alert) error { return errDown }
func (downStore) Alerts(context.Context, int) ([]domain.Alert, error) {
	return nil, errDown
}

// hashDown holds a chain but cannot read old hashes or keep alerts.
type hashDown struct{ *memStore }

func (hashDown) HashAt(context.Context, app.Chain, int64) (string, error) { return "", errDown }
func (hashDown) AddAlert(context.Context, domain.Alert) error             { return errDown }

func TestSink_ReportsAStoreThatIsDown(t *testing.T) {
	t.Parallel()
	s := app.New(downStore{}, clock{t0}, nil)
	ctx := t.Context()
	if _, err := s.Head(ctx); !errors.Is(err, errDown) {
		t.Errorf("Head = %v", err)
	}
	if _, err := s.Receive(ctx, machineChain(t, 1, "")); !errors.Is(err, errDown) {
		t.Errorf("Receive = %v", err)
	}
	if err := s.RecordProof(ctx, app.ProofEntry{Action: "freeze.on", Target: "machine:all"}); !errors.Is(err, errDown) {
		t.Errorf("RecordProof = %v", err)
	}
	if _, err := s.List(ctx, app.Inside, 0, 10); !errors.Is(err, errDown) {
		t.Errorf("List = %v", err)
	}
	if _, err := s.Alerts(ctx, 10); !errors.Is(err, errDown) {
		t.Errorf("Alerts = %v", err)
	}
	if err := s.Verify(ctx, app.Inside); !errors.Is(err, errDown) {
		t.Errorf("Verify = %v", err)
	}
}

func TestReceive_ReportsAnUnreadableOldRecord(t *testing.T) {
	t.Parallel()
	m := newMem()
	if _, err := newSink(m).Receive(t.Context(), machineChain(t, 1, "")); err != nil {
		t.Fatal(err)
	}
	_, err := app.New(hashDown{m}, clock{t0}, nil).Receive(t.Context(), machineChain(t, 1, ""))
	if !errors.Is(err, errDown) {
		t.Fatalf("Receive = %v, want the store error", err)
	}
}

func TestReceive_StillRejectsWhenTheAlertCannotBeKept(t *testing.T) {
	t.Parallel()
	m := newMem()
	chain := machineChain(t, 3, "")
	_, err := app.New(hashDown{m}, clock{t0}, nil).Receive(t.Context(), []json.RawMessage{chain[2]})
	if re := reject(t, err); re.Verdict != domain.Gap {
		t.Fatalf("reject = %+v, want gap", re)
	}
	if re := reject(t, err); re.Error() == "" {
		t.Fatal("empty error text")
	}
}

func TestRecordProof_ReportsAFailedAppend(t *testing.T) {
	t.Parallel()
	m := newMem()
	m.failAdd = errDown
	if err := newSink(m).RecordProof(t.Context(), app.ProofEntry{Action: "freeze.on", Target: "machine:all"}); !errors.Is(err, errDown) {
		t.Fatalf("RecordProof = %v", err)
	}
}

func TestRecordProof_FollowsTheActionPattern(t *testing.T) {
	t.Parallel()
	for action, ok := range map[string]bool{
		"net.deny": true, "model.call-2": true, "nodot": false, "a..b": false, "1a.b": false,
		"a.B": false, "a.b c": false, "": false,
	} {
		err := newSink(newMem()).RecordProof(t.Context(), app.ProofEntry{Action: action, Target: "machine:all"})
		if (err == nil) != ok {
			t.Errorf("RecordProof(%q) = %v, want ok=%v", action, err, ok)
		}
	}
	if err := newSink(newMem()).RecordProof(t.Context(), app.ProofEntry{Action: "a.b"}); !errors.Is(err, app.ErrInvalidProof) {
		t.Errorf("empty target = %v, want ErrInvalidProof", err)
	}
}

func TestVerify_ReadsEveryPage(t *testing.T) {
	t.Parallel()
	m := newMem()
	s := newSink(m)
	if _, err := s.Receive(t.Context(), machineChain(t, app.MaxLimit+2, "")); err != nil {
		t.Fatal(err)
	}
	if err := s.Verify(t.Context(), app.Inside); err != nil {
		t.Fatalf("Verify over two pages: %v", err)
	}
}

func TestAlerts_ReturnsWhatWasRaised(t *testing.T) {
	t.Parallel()
	m := newMem()
	s := newSink(m)
	chain := machineChain(t, 3, "")
	_, _ = s.Receive(t.Context(), []json.RawMessage{chain[2]})
	got, err := s.Alerts(t.Context(), 0)
	if err != nil || len(got) != 1 || got[0].Kind != "gap" {
		t.Fatalf("Alerts = %+v, %v", got, err)
	}
}
