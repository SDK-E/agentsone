// SPDX-License-Identifier: MIT

package domain

import (
	"strconv"
	"time"
)

// Agent users hold uids in this range (set by the society context).
const (
	agentUIDMin = 200000
	agentUIDMax = 299999
)

// UserKind is what a Linux account is for. Human accounts live in the
// daemon's database, not in /etc/passwd, so every Linux account is an agent
// or a system account.
type UserKind string

// User kinds.
const (
	UserHuman  UserKind = "human"
	UserAgent  UserKind = "agent"
	UserSystem UserKind = "system"
)

// MachineUser is one Linux account.
type MachineUser struct {
	Name  string
	UID   int
	GID   int
	Home  string
	Shell string
	Kind  UserKind
}

// KindOfUID classifies a Linux uid.
func KindOfUID(uid int) UserKind {
	if uid >= agentUIDMin && uid <= agentUIDMax {
		return UserAgent
	}
	return UserSystem
}

// Metrics is a snapshot of the machine.
type Metrics struct {
	Time       time.Time
	Hostname   string
	Uptime     time.Duration
	Load       [3]float64
	Cores      int
	CPUPercent float64
	MemTotal   int64
	MemUsed    int64
	MemAvail   int64
	Disks      []Disk
	RxBytes    int64
	TxBytes    int64
}

// Disk is one mounted file system.
type Disk struct {
	Mount string
	Total int64
	Used  int64
}

// CPUSample is busy CPU time at a moment.
type CPUSample struct {
	BusyMicros int64
	At         time.Time
}

// CPUPercent is the share of all cores used between two samples, 0 to 100.
func CPUPercent(prev, cur CPUSample, cores int) float64 {
	wall := cur.At.Sub(prev.At).Microseconds() * int64(max(cores, 1))
	if wall <= 0 {
		return 0
	}
	pct := float64(cur.BusyMicros-prev.BusyMicros) * 100 / float64(wall)
	return min(max(pct, 0), 100)
}

const maxPage = 200

// Page returns one page of items after cursor, and the cursor of the next
// page ("" on the last). The cursor is the offset, opaque to callers.
func Page[T any](items []T, cursor string, limit int) ([]T, string, error) {
	if limit < 1 {
		return nil, "", invalid("limit must be at least 1")
	}
	limit = min(limit, maxPage)
	start := 0
	if cursor != "" {
		n, err := strconv.Atoi(cursor)
		if err != nil || n < 0 {
			return nil, "", invalid("cursor %q", cursor)
		}
		start = min(n, len(items))
	}
	end := min(start+limit, len(items))
	next := ""
	if end < len(items) {
		next = strconv.Itoa(end)
	}
	return items[start:end], next, nil
}
