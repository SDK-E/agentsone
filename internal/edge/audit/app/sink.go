// SPDX-License-Identifier: MIT

// Package app is the edge audit sink: it takes the machine's records as written, checks each one
// continues the chain, keeps its own copy, raises alerts on gaps and forks, and writes the edge's
// own proof records in a second chain.
package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/SDK-E/agentsone/internal/auditchain"
	"github.com/SDK-E/agentsone/internal/edge/audit/domain"
)

// Chain names one of the two chains the edge keeps.
type Chain string

const (
	// Inside is the machine's chain: testimony, copied as written.
	Inside Chain = "inside"
	// Proof is the edge's own chain: what the edge itself saw.
	Proof Chain = "proof"
)

// Stored is one record as kept.
type Stored struct {
	Seq  int64
	Hash string
	Raw  json.RawMessage
}

// Store keeps both chains and the alerts, append only.
type Store interface {
	Head(ctx context.Context, c Chain) (domain.Head, error)
	// HashAt returns the hash held at seq, empty when none.
	HashAt(ctx context.Context, c Chain, seq int64) (string, error)
	Append(ctx context.Context, c Chain, r Stored) error
	// List returns up to limit records with Seq > after, oldest first.
	List(ctx context.Context, c Chain, after int64, limit int) ([]Stored, error)
	// AddAlert keeps an alert; the same kind at the same seq is kept once.
	AddAlert(ctx context.Context, a domain.Alert) error
	// Alerts returns the newest alerts first.
	Alerts(ctx context.Context, limit int) ([]domain.Alert, error)
}

// Clock tells the time.
type Clock interface {
	Now() time.Time
}

// ProofEntry is one thing the edge saw.
type ProofEntry struct {
	Action  string
	Target  string // kind:id
	Outcome string
	Details map[string]any
}

// ErrRejected is wrapped by every RejectError.
var ErrRejected = errors.New("audit: record rejected")

// RejectError says why an arriving record was refused and where the edge's copy stands, so the
// shipper knows where to resume.
type RejectError struct {
	Verdict domain.Verdict
	Seq     int64
	Detail  string
	Head    domain.Head
}

func (e *RejectError) Error() string {
	return fmt.Sprintf("audit: record %d rejected (%s): %s", e.Seq, e.Verdict, e.Detail)
}

// Unwrap makes errors.Is(err, ErrRejected) true.
func (e *RejectError) Unwrap() error { return ErrRejected }

// Page limits.
const (
	DefaultLimit = 100
	MaxLimit     = 500
)

// ActorName names the edge in its own proof records.
const ActorName = "agentsone-edge"

// Sink is the edge audit sink. It is safe for concurrent use.
type Sink struct {
	store Store
	clock Clock
	log   *slog.Logger
	mu    sync.Mutex
}

// New returns the sink over store.
func New(store Store, clock Clock, log *slog.Logger) *Sink {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Sink{store: store, clock: clock, log: log}
}

// Head returns the last machine record the edge holds.
func (s *Sink) Head(ctx context.Context) (domain.Head, error) {
	return s.store.Head(ctx, Inside)
}

type linkFields struct {
	Seq      int64  `json:"seq"`
	PrevHash string `json:"prev_hash"`
	Hash     string `json:"hash"`
	Origin   string `json:"origin"`
}

// Receive takes machine records in order, as written, and returns the head after them. Records
// the edge already holds are skipped, so a resend is harmless. The first record that does not
// continue the chain stops the batch: the edge raises an alert (once per kind and sequence) and
// returns a RejectError with its head. Records written by the machine must say origin inside: a
// record that claims to be edge proof is refused.
func (s *Sink) Receive(ctx context.Context, records []json.RawMessage) (domain.Head, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	head, err := s.store.Head(ctx, Inside)
	if err != nil {
		return domain.Head{}, fmt.Errorf("audit: read head: %w", err)
	}
	for _, raw := range records {
		var f linkFields
		verdict, detail := domain.Broken, ""
		computed, hashErr := auditchain.Hash(raw)
		switch {
		case hashErr != nil || json.Unmarshal(raw, &f) != nil:
			detail = "the record is not a JSON object"
		case f.Origin != "inside":
			detail = "a machine record must have origin inside"
		default:
			stored := ""
			if f.Seq >= 1 && f.Seq <= head.Seq {
				if stored, err = s.store.HashAt(ctx, Inside, f.Seq); err != nil {
					return head, fmt.Errorf("audit: read record %d: %w", f.Seq, err)
				}
			}
			verdict = domain.Judge(head, domain.Link{Seq: f.Seq, PrevHash: f.PrevHash, Hash: f.Hash}, computed, stored)
			detail = describe(verdict, head)
		}
		switch verdict {
		case domain.Duplicate:
			continue
		case domain.Append:
			if err := s.store.Append(ctx, Inside, Stored{Seq: f.Seq, Hash: f.Hash, Raw: raw}); err != nil {
				return head, fmt.Errorf("audit: keep record %d: %w", f.Seq, err)
			}
			head = domain.Head{Seq: f.Seq, Hash: f.Hash}
			continue
		}
		alert := domain.Alert{Kind: verdict.String(), Seq: f.Seq, Detail: detail, Time: s.clock.Now().UTC()}
		if err := s.store.AddAlert(ctx, alert); err != nil {
			s.log.Error("audit: keep alert", "kind", alert.Kind, "seq", alert.Seq, "err", err)
		}
		s.log.Warn("audit: machine chain break", "kind", alert.Kind, "seq", alert.Seq, "detail", detail)
		return head, &RejectError{Verdict: verdict, Seq: f.Seq, Detail: detail, Head: head}
	}
	return head, nil
}

func describe(v domain.Verdict, head domain.Head) string {
	switch v {
	case domain.Gap:
		return fmt.Sprintf("records after %d never arrived", head.Seq)
	case domain.Fork:
		return "the record contradicts the copy the edge holds"
	case domain.Broken:
		return "the record's content does not match its hash, or its sequence is invalid"
	}
	return ""
}

type proofActor struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
}

type proofTarget struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

type proofRecord struct {
	Seq     int64          `json:"seq"`
	Time    string         `json:"time"`
	Actor   proofActor     `json:"actor"`
	Action  string         `json:"action"`
	Target  proofTarget    `json:"target"`
	Outcome string         `json:"outcome"`
	Details map[string]any `json:"details"`
	Origin  string         `json:"origin"`
}

// ErrInvalidProof is returned for a proof entry the audit record schema would refuse.
var ErrInvalidProof = errors.New("audit: invalid proof entry")

// RecordProof appends one edge proof record (origin host) to the edge's own chain.
func (s *Sink) RecordProof(ctx context.Context, p ProofEntry) error {
	if !validAction(p.Action) || p.Target == "" {
		return fmt.Errorf("%w: action %q target %q", ErrInvalidProof, p.Action, p.Target)
	}
	kind, id, ok := strings.Cut(p.Target, ":")
	if !ok {
		id = kind
	}
	details := p.Details
	if details == nil {
		details = map[string]any{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	head, err := s.store.Head(ctx, Proof)
	if err != nil {
		return fmt.Errorf("audit: read proof head: %w", err)
	}
	raw, err := json.Marshal(proofRecord{
		Seq: head.Seq + 1, Time: s.clock.Now().UTC().Format(time.RFC3339Nano),
		Actor: proofActor{Kind: "host", Name: ActorName}, Action: p.Action,
		Target: proofTarget{Kind: kind, ID: id}, Outcome: p.Outcome, Details: details, Origin: "host",
	})
	if err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidProof, err)
	}
	sealed, err := auditchain.Seal(raw, head.Hash)
	if err != nil {
		return fmt.Errorf("audit: seal proof: %w", err)
	}
	hash, err := auditchain.Hash(sealed)
	if err != nil {
		return fmt.Errorf("audit: seal proof: %w", err)
	}
	if err := s.store.Append(ctx, Proof, Stored{Seq: head.Seq + 1, Hash: hash, Raw: sealed}); err != nil {
		return fmt.Errorf("audit: keep proof: %w", err)
	}
	return nil
}

// validAction follows the schema: dotted lowercase words, at most 64 characters.
func validAction(a string) bool {
	if a == "" || len(a) > 64 || !strings.Contains(a, ".") {
		return false
	}
	for word := range strings.SplitSeq(a, ".") {
		if word == "" || word[0] < 'a' || word[0] > 'z' {
			return false
		}
		for _, r := range word {
			if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
				return false
			}
		}
	}
	return true
}

func clamp(limit int) int {
	if limit <= 0 {
		return DefaultLimit
	}
	return min(limit, MaxLimit)
}

// List returns up to limit records of a chain after the given sequence, oldest first.
func (s *Sink) List(ctx context.Context, c Chain, after int64, limit int) ([]Stored, error) {
	rs, err := s.store.List(ctx, c, after, clamp(limit))
	if err != nil {
		return nil, fmt.Errorf("audit: list %s: %w", c, err)
	}
	return rs, nil
}

// Alerts returns up to limit alerts, newest first.
func (s *Sink) Alerts(ctx context.Context, limit int) ([]domain.Alert, error) {
	as, err := s.store.Alerts(ctx, clamp(limit))
	if err != nil {
		return nil, fmt.Errorf("audit: alerts: %w", err)
	}
	return as, nil
}

// Verify checks a whole chain as the edge holds it, with internal/auditchain.
func (s *Sink) Verify(ctx context.Context, c Chain) error {
	var all []json.RawMessage
	for after := int64(0); ; {
		page, err := s.store.List(ctx, c, after, MaxLimit)
		if err != nil {
			return fmt.Errorf("audit: verify %s: %w", c, err)
		}
		for _, r := range page {
			all = append(all, r.Raw)
		}
		if len(page) < MaxLimit {
			break
		}
		after = page[len(page)-1].Seq
	}
	if err := auditchain.Verify(all); err != nil {
		return fmt.Errorf("audit: verify %s: %w", c, err)
	}
	return nil
}
