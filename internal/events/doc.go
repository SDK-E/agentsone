// SPDX-License-Identifier: MIT

// Package events is the event context: CloudEvents from emitters, their
// storage and fan-out, emitter registration, and triggers that turn events
// into wakes and tasks. It owns the Events and Subscribe ports.
//
// Owned by work package 3 (bus, events, emitters, triggers, schedules).
// Shape: domain/, app/, adapters/{http,sqlite}/, wired in module.go. Copy
// internal/audit, the reference context.
package events
