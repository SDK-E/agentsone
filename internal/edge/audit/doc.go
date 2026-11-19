// SPDX-License-Identifier: MIT

// Package audit is the edge audit sink. It keeps two hash chains in audit.db in the edge data
// folder, both append only:
//
//   - inside: the machine's records, shipped by the daemon as written to the inner address (port
//     7710). Only the daemon may send them: each call carries a short-lived token for the audit
//     sink (aud "audit", kind system) signed with the gateway key, which only root can read inside
//     the machine. A custom agent's own token is for model calls and is refused, so it cannot slip
//     in a forged record and pause shipping. Each record is checked with internal/auditchain: its
//     hash must match its content and it must continue the chain. A gap or a fork is kept as an
//     alert for the control page and the record is refused, so the copy the edge holds never
//     contains a break. This is testimony: root inside the machine (root agents included) can read
//     the key and could have written anything, but it cannot change what the edge already holds.
//   - proof: what the edge itself saw (origin host): freeze changes, refused egress, model calls.
//     Every part records it through env.Proof, which this component shares.
//
// The control page reads both chains, the alerts and a full verification through the admin
// routes under /v1/audit/. It also provides the audit command.
//
// Shape: domain (judging an arriving record), app (the sink), adapters/sqlite (audit.db),
// adapters/http (inner and admin routes). component.go wires them as an edge component.
//
// Owned by work package 16 (edge: gateway, budget, audit sink, web door).
package audit
