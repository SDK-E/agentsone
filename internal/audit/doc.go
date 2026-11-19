// SPDX-License-Identifier: MIT

// Package audit is the audit context: the daemon's hash-chained log of what
// agents, humans and the system did. Records written inside the VM are
// testimony and are shipped to the host as written. It owns the Audit port.
//
// This is the reference context; every other one copies its shape:
//
//	audit/
//	  doc.go, module.go, module_test.go   wiring only
//	  domain/        Record, Action, Timestamp, Repository (no IO)
//	  app/           Log: Append, List, Verify (domain + core ports)
//	  adapters/http  generated audit group, DTO mapping in dto.go
//	  adapters/sqlite Repository on audit.db, embedded migrations
//	  adapters/memory Repository in memory
//	  audittest/     repository contract run on every Repository
//
// Owned by work package 1 (daemon core); shape by work package 0.
package audit
