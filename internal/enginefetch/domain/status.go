// SPDX-License-Identifier: MIT

package domain

import "time"

// State is where the engine fetch stands, with the contract's
// EngineFetchState values.
type State string

// The fetch states.
const (
	StateWaitingLicence State = "waiting-licence"
	StateQueued         State = "queued"
	StateDownloading    State = "downloading"
	StateVerifying      State = "verifying"
	StateReady          State = "ready"
	StateFailed         State = "failed"
)

// Status is what the daemon shows about the engine fetch.
type Status struct {
	State      State
	BytesDone  int64
	BytesTotal int64 // 0 when unknown
	Version    string
	// Error is the last failure in plain words, empty when none.
	Error     string
	UpdatedAt time.Time
}

// Progress is the share of the download done, from 0 to 1.
func (s Status) Progress() float64 {
	switch {
	case s.State == StateReady:
		return 1
	case s.BytesTotal <= 0:
		return 0
	case s.BytesDone >= s.BytesTotal:
		return 1
	}
	return float64(s.BytesDone) / float64(s.BytesTotal)
}
