// SPDX-License-Identifier: MIT

// Package machine is machine control: metrics, processes, services,
// packages, users, files, power, the machine recipe, terminals, per-agent
// network rules and the IDE server.
//
// The machine is a Debian container whose root file system resets to the
// image on every restart. The machine recipe keeps what agents added
// (packages and settings files outside the volumes) and reapplies it at
// every start, before agents wake. A restart is the rescue.
//
// Owned by work package 7 (machine control, recipe; terminals next); the
// IDE part by work package 13. Shape: domain/, app/, adapters/{http,system}/,
// wired in module.go.
package machine
