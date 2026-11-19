// SPDX-License-Identifier: MIT

// Package store opens the daemon's SQLite databases and runs per-module migrations.
//
// It uses the pure-Go modernc.org/sqlite driver, so no C toolchain is needed. Every database is
// opened in WAL mode with a busy timeout and foreign keys on; FTS5 is available.
//
// Owned by work package 0 (frozen seam); maintained by WP1 daemon core.
package store
