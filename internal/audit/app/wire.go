// SPDX-License-Identifier: MIT

package app

import (
	"encoding/json"
	"fmt"

	"github.com/SDK-E/agentsone/internal/audit/domain"
)

// wireRecord is the audit record as contract/schemas/audit-record.schema.json
// writes it. The chain hash is computed over this form, so it must follow
// the schema's field names exactly; this is the only place they appear.
type wireRecord struct {
	Seq      int64           `json:"seq"`
	Time     string          `json:"time"`
	Actor    wireActor       `json:"actor"`
	Action   string          `json:"action"`
	Target   wireTarget      `json:"target"`
	Outcome  string          `json:"outcome"`
	Details  json.RawMessage `json:"details"`
	Origin   string          `json:"origin"`
	PrevHash string          `json:"prevHash,omitempty"`
	Hash     string          `json:"hash,omitempty"`
}

type wireActor struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
	UID  *int   `json:"uid,omitempty"`
}

type wireTarget struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

// encodeRecord writes r in its schema form. An empty hash is left out, which
// is the form the chain hash is computed over.
func encodeRecord(r domain.Record) ([]byte, error) {
	b, err := json.Marshal(wireRecord{
		Seq:      r.Seq,
		Time:     r.Time.String(),
		Actor:    wireActor{Kind: string(r.Actor.Kind), Name: r.Actor.Name, UID: r.Actor.UID},
		Action:   r.Action.String(),
		Target:   wireTarget{Kind: r.Target.Kind, ID: r.Target.ID},
		Outcome:  string(r.Outcome),
		Details:  r.Details,
		Origin:   string(r.Origin),
		PrevHash: r.PrevHash,
		Hash:     r.Hash,
	})
	if err != nil {
		return nil, fmt.Errorf("encode audit record %d: %w", r.Seq, err)
	}
	return b, nil
}
