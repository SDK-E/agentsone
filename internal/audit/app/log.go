// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/SDK-E/agentsone/internal/audit/domain"
	"github.com/SDK-E/agentsone/internal/auditchain"
	"github.com/SDK-E/agentsone/internal/core"
)

const verifyPage = 500

// Log is the audit log: it seals entries into the hash chain and stores them.
type Log struct {
	repo  domain.Repository
	clock core.Clock
	// mu serialises appends so sequence numbers and links never race.
	mu sync.Mutex
}

var _ core.AuditLog = (*Log)(nil)

// NewLog returns a log over repo.
func NewLog(repo domain.Repository, clock core.Clock) *Log {
	return &Log{repo: repo, clock: clock}
}

// Append records e as the next link of the chain. An empty e.Actor is taken
// from the caller on ctx.
func (l *Log) Append(ctx context.Context, e core.AuditEntry) error {
	r, err := recordFrom(ctx, e)
	if err != nil {
		return fmt.Errorf("audit append: %w: %w", core.ErrInvalid, err)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.link(ctx, &r); err != nil {
		return err
	}
	if err := l.repo.Append(ctx, r); err != nil {
		return fmt.Errorf("audit append: %w", err)
	}
	return nil
}

// List returns up to limit records after afterSeq, oldest first.
func (l *Log) List(ctx context.Context, afterSeq int64, limit int) ([]domain.Record, error) {
	records, err := l.repo.List(ctx, afterSeq, limit)
	if err != nil {
		return nil, fmt.Errorf("audit list: %w", err)
	}
	return records, nil
}

// Verify checks the whole stored chain with internal/auditchain.
func (l *Log) Verify(ctx context.Context) error {
	var chain []json.RawMessage
	for after := int64(0); ; {
		page, err := l.repo.List(ctx, after, verifyPage)
		if err != nil {
			return fmt.Errorf("audit verify: %w", err)
		}
		for _, r := range page {
			raw, err := encodeRecord(r)
			if err != nil {
				return fmt.Errorf("audit verify: %w", err)
			}
			chain = append(chain, raw)
		}
		if len(page) < verifyPage {
			break
		}
		after = page[len(page)-1].Seq
	}
	if err := auditchain.Verify(chain); err != nil {
		return fmt.Errorf("audit verify: %w", err)
	}
	return nil
}

// link gives r its sequence, time, previous hash and own hash.
func (l *Log) link(ctx context.Context, r *domain.Record) error {
	last, hasLast, err := l.repo.Last(ctx)
	if err != nil {
		return fmt.Errorf("audit append: %w", err)
	}
	r.Seq, r.PrevHash = 1, domain.GenesisHash
	if hasLast {
		r.Seq, r.PrevHash = last.Seq+1, last.Hash
	}
	r.Time = domain.NewTimestamp(l.clock.Now())
	r.Hash, err = sealedHash(*r)
	return err
}

func sealedHash(r domain.Record) (string, error) {
	raw, err := encodeRecord(r)
	if err != nil {
		return "", fmt.Errorf("audit seal: %w", err)
	}
	sealed, err := auditchain.Seal(raw, r.PrevHash)
	if err != nil {
		return "", fmt.Errorf("audit seal: %w", err)
	}
	var withHash struct {
		Hash string `json:"hash"`
	}
	if err := json.Unmarshal(sealed, &withHash); err != nil {
		return "", fmt.Errorf("audit seal: %w", err)
	}
	return withHash.Hash, nil
}

func recordFrom(ctx context.Context, e core.AuditEntry) (domain.Record, error) {
	action, err := domain.NewAction(e.Action)
	if err != nil {
		return domain.Record{}, err
	}
	actor, err := actorFor(ctx, e.Actor)
	if err != nil {
		return domain.Record{}, err
	}
	target, err := domain.ParseTargetRef(e.Target)
	if err != nil {
		return domain.Record{}, err
	}
	details, err := detailsObject(e.Detail)
	if err != nil {
		return domain.Record{}, err
	}
	outcome, err := outcomeOf(e.Outcome)
	if err != nil {
		return domain.Record{}, err
	}
	return domain.Record{
		Actor: actor, Action: action, Target: target, Details: details,
		Outcome: outcome, Origin: domain.OriginInside,
	}, nil
}

// outcomeOf maps the port's outcome to the record's; empty is success.
func outcomeOf(s string) (domain.Outcome, error) {
	switch s {
	case "":
		return domain.OutcomeSuccess, nil
	case core.AuditFailure:
		return domain.OutcomeFailure, nil
	case core.AuditDenied:
		return domain.OutcomeDenied, nil
	}
	return "", fmt.Errorf("audit outcome %q is not success, failure or denied", s)
}

func actorFor(ctx context.Context, ref string) (domain.Actor, error) {
	if ref != "" {
		return domain.ParseActorRef(ref)
	}
	c, ok := core.CallerFrom(ctx)
	if !ok {
		return domain.Actor{}, fmt.Errorf("%w: no actor and no caller", domain.ErrInvalidActor)
	}
	actor, err := domain.ParseActorRef(string(c.Kind) + ":" + c.Name)
	if err != nil {
		return domain.Actor{}, err
	}
	if c.UID >= 0 {
		uid := c.UID
		actor.UID = &uid
	}
	return actor, nil
}

var errDetailsNotObject = errors.New("audit details must be a JSON object")

func detailsObject(raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 {
		return json.RawMessage(`{}`), nil
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, fmt.Errorf("%w: %w", errDetailsNotObject, err)
	}
	return raw, nil
}
