// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"fmt"

	"github.com/SDK-E/agentsone/internal/auth/domain"
	"github.com/SDK-E/agentsone/internal/core"
)

// LicenceAccepted reports whether a human accepted the current engine
// licence version (core.EngineSetup). An older acceptance does not count.
func (s *Service) LicenceAccepted(ctx context.Context) (bool, error) {
	v, err := s.EngineLicence(ctx)
	if err != nil {
		return false, err
	}
	return v.Accepted != nil, nil
}

// SaveFetchStatus records the engine download status reported by the
// fetcher (core.EngineSetup). Progress is derived from the byte counts, and
// the error text is kept only for a failed download.
func (s *Service) SaveFetchStatus(ctx context.Context, st core.EngineFetchStatus) error {
	if !fetchStates[st.State] {
		return fmt.Errorf("engine fetch state %q: %w", st.State, core.ErrInvalid)
	}
	if st.BytesDone < 0 || st.BytesTotal < 0 {
		return fmt.Errorf("engine fetch byte counts must not be negative: %w", core.ErrInvalid)
	}
	f := domain.EngineFetch{
		State: st.State, BytesDone: st.BytesDone, BytesTotal: st.BytesTotal,
		Version: st.Version, UpdatedAt: st.UpdatedAt,
	}
	if f.UpdatedAt.IsZero() {
		f.UpdatedAt = s.d.Clock.Now()
	}
	if st.State == FetchFailed {
		f.Error = st.Error
	}
	switch {
	case st.State == FetchReady:
		f.Progress = 1
	case st.BytesTotal > 0:
		f.Progress = min(1, float64(st.BytesDone)/float64(st.BytesTotal))
	}
	if err := s.d.Setup.SaveEngineFetch(ctx, f); err != nil {
		return fmt.Errorf("save engine fetch: %w", err)
	}
	return nil
}
