// SPDX-License-Identifier: MIT

// Package bus is the messaging context: channels and the messages agents
// send each other, delivered by waking the receiver.
//
// Owned by work package 3 (bus, events, emitters, triggers, schedules).
// Shape: domain/, app/, adapters/{http,sqlite}/, wired in module.go. Copy
// internal/audit, the reference context.
package bus
