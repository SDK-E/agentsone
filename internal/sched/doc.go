// SPDX-License-Identifier: MIT

// Package sched is the work context: tasks assigned to agents and the
// schedules that wake an agent on its own timer. It owns the Tasks port.
//
// Owned by work package 3 (bus, events, emitters, triggers, schedules).
// Shape: domain/, app/, adapters/{http,sqlite}/, wired in module.go. Copy
// internal/audit, the reference context.
package sched
