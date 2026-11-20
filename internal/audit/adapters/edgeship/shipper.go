// SPDX-License-Identifier: MIT

// Package edgeship ships the daemon's audit records, as written, to the edge audit sink, where
// the edge keeps its own copy out of reach of root inside the machine.
//
// The backlog is audit.db itself: the shipper asks the edge for the last record it holds, reads
// the records after it from the log and posts them in order. Nothing is queued in memory, so
// nothing is lost while the edge is down or the daemon restarts, and an agent's turn never waits
// on the edge: Append writes to audit.db as before and the shipper catches up in the background,
// backing off while the edge does not answer.
//
// The sink takes records only from the daemon: every call carries a short-lived audit token
// (aud "audit", kind system) minted from the gateway key, which only root can read inside the
// machine. Without the key nothing is shipped and the records wait in audit.db.
package edgeship

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/SDK-E/agentsone/internal/audit/domain"
)

// Source is the daemon's audit log.
type Source interface {
	List(ctx context.Context, afterSeq int64, limit int) ([]domain.Record, error)
}

// Options configure a Shipper.
type Options struct {
	// Sink is the edge audit sink's base URL, such as http://10.77.42.2:7710.
	Sink   string
	Source Source
	Encode func(domain.Record) ([]byte, error)
	// Token mints the daemon's audit token, which the sink requires on every call. The shipper
	// mints one for each round, so it never holds a stale one.
	Token func(ctx context.Context) (string, error)
	// Client defaults to one with a short timeout.
	Client *http.Client
	Log    *slog.Logger
	// Wait sleeps for d or until ctx ends; false when ctx ended. Tests pass a fake.
	Wait func(ctx context.Context, d time.Duration) bool
	// Batch is the most records per request; 0 means DefaultBatch.
	Batch int
}

// Timing.
const (
	DefaultBatch = 100
	MinBackoff   = time.Second
	MaxBackoff   = time.Minute
	IdlePoll     = 2 * time.Second
	// requestTimeout bounds one call to the edge.
	requestTimeout = 15 * time.Second
	// maxAnswerBytes caps what is read from the edge.
	maxAnswerBytes = 64 << 10
)

// Shipper ships records.
type Shipper struct{ o Options }

// New returns a shipper.
func New(o Options) *Shipper {
	if o.Client == nil {
		o.Client = &http.Client{Timeout: requestTimeout}
	}
	if o.Log == nil {
		o.Log = slog.New(slog.DiscardHandler)
	}
	if o.Wait == nil {
		o.Wait = sleep
	}
	if o.Token == nil {
		o.Token = func(context.Context) (string, error) { return "", errNoToken }
	}
	if o.Batch <= 0 {
		o.Batch = DefaultBatch
	}
	o.Sink = strings.TrimRight(o.Sink, "/")
	return &Shipper{o: o}
}

// errNoToken is what a shipper built without a Token gets: it ships nothing.
var errNoToken = errors.New("no audit token source")

func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// ErrRefused is returned when the sink refuses a record: it flagged a gap, a fork or a broken
// record as an alert for the human. Shipping keeps retrying at the slowest pace.
var ErrRefused = errors.New("edgeship: the edge refused a record")

type head struct {
	Seq int64 `json:"seq"`
}

// ShipOnce ships the next batch after the edge's head and returns how many records went.
func (s *Shipper) ShipOnce(ctx context.Context) (int, error) {
	token, err := s.o.Token(ctx)
	if err != nil {
		return 0, fmt.Errorf("edgeship: mint the audit token: %w", err)
	}
	var h head
	if err := s.call(ctx, token, http.MethodGet, "/v1/audit/head", nil, &h); err != nil {
		return 0, err
	}
	recs, err := s.o.Source.List(ctx, h.Seq, s.o.Batch)
	if err != nil {
		return 0, fmt.Errorf("edgeship: read the log: %w", err)
	}
	if len(recs) == 0 {
		return 0, nil
	}
	batch := make([]json.RawMessage, 0, len(recs))
	for _, r := range recs {
		raw, err := s.o.Encode(r)
		if err != nil {
			return 0, fmt.Errorf("edgeship: encode record %d: %w", r.Seq, err)
		}
		batch = append(batch, raw)
	}
	body, err := json.Marshal(map[string]any{"records": batch})
	if err != nil {
		return 0, fmt.Errorf("edgeship: encode batch: %w", err)
	}
	if err := s.call(ctx, token, http.MethodPost, "/v1/audit/records", body, nil); err != nil {
		return 0, err
	}
	return len(recs), nil
}

func (s *Shipper) call(ctx context.Context, token, method, path string, body []byte, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, s.o.Sink+path, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("edgeship: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := s.o.Client.Do(req)
	if err != nil {
		return fmt.Errorf("edgeship: %s %s: %w", method, path, err)
	}
	defer func() { _ = res.Body.Close() }() //nolint:errcheck // read-only body; ReadAll below reports read failures
	answer, err := io.ReadAll(io.LimitReader(res.Body, maxAnswerBytes))
	if err != nil {
		return fmt.Errorf("edgeship: %s %s: %w", method, path, err)
	}
	switch {
	case res.StatusCode == http.StatusConflict:
		return fmt.Errorf("%w: %s", ErrRefused, answer)
	case res.StatusCode != http.StatusOK:
		return fmt.Errorf("edgeship: %s %s: status %d", method, path, res.StatusCode)
	case out != nil:
		if err := json.Unmarshal(answer, out); err != nil {
			return fmt.Errorf("edgeship: %s %s: %w", method, path, err)
		}
	}
	return nil
}

// Run ships until ctx ends: as fast as records come while there is a backlog, every IdlePoll
// when caught up, and with doubling backoff up to MaxBackoff while the edge does not answer or
// refuses a record. An edge that does not answer (not up yet, restarting) is logged once at info
// level, and again when shipping resumes; a refusal is a warning each time, since the edge has
// raised an alert for the human.
func (s *Shipper) Run(ctx context.Context) {
	backoff := time.Duration(0)
	down := false
	for ctx.Err() == nil {
		n, err := s.ShipOnce(ctx)
		if ctx.Err() != nil {
			return
		}
		var wait time.Duration
		switch {
		case err != nil:
			backoff = min(max(2*backoff, MinBackoff), MaxBackoff)
			if errors.Is(err, ErrRefused) {
				backoff = MaxBackoff
				s.o.Log.Warn("audit: the edge refused a record and raised an alert; retrying", "in", backoff, "err", err)
			} else if !down {
				down = true
				s.o.Log.Info("audit: edge audit sink not reachable; records wait in audit.db", "err", err)
			}
			wait = backoff
		case n > 0:
			backoff = 0
			if down {
				down = false
				s.o.Log.Info("audit: shipping to the edge audit sink again")
			}
			continue
		default:
			backoff = 0
			wait = IdlePoll
		}
		if !s.o.Wait(ctx, wait) {
			return
		}
	}
}
