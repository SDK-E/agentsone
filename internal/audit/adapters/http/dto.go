// SPDX-License-Identifier: MIT

package httpadapter

import (
	"encoding/json"

	"github.com/SDK-E/agentsone/internal/audit/domain"
	"github.com/SDK-E/agentsone/internal/contract/api"
)

// toAuditList maps domain records to the generated DTO. Field names of the
// wire format live only in the generated types, so a contract rename stays
// a change to this file alone.
func toAuditList(records []domain.Record) api.AuditList {
	items := make([]api.AuditRecord, 0, len(records))
	for _, r := range records {
		items = append(items, toAuditRecord(r))
	}
	return api.AuditList{Items: items}
}

func toAuditRecord(r domain.Record) api.AuditRecord {
	return api.AuditRecord{
		Seq:      int(r.Seq),
		Time:     r.Time.Time(),
		Actor:    api.AuditActor{Kind: api.ActorKind(r.Actor.Kind), Name: r.Actor.Name, Uid: r.Actor.UID},
		Action:   r.Action.String(),
		Target:   api.Target{Kind: api.TargetKind(r.Target.Kind), Id: r.Target.ID},
		Outcome:  api.AuditRecordOutcome(r.Outcome),
		Details:  detailsMap(r.Details),
		Origin:   api.AuditRecordOrigin(r.Origin),
		PrevHash: r.PrevHash,
		Hash:     r.Hash,
	}
}

// detailsMap decodes details for the DTO. Stored details are always a JSON
// object (the app layer checks), so a decode failure yields an empty map.
func detailsMap(raw json.RawMessage) map[string]any {
	m := map[string]any{}
	_ = json.Unmarshal(raw, &m)
	return m
}
