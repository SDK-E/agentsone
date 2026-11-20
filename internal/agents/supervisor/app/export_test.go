// SPDX-License-Identifier: MIT

package app

import "context"

// Launch calls the start path every wake and run shares, with no check made
// before it, so a test proves the wall is held below the callers.
func (s *Supervisor) Launch(ctx context.Context, name string) error {
	s.mu.Lock()
	r := s.runnerLocked(name)
	s.mu.Unlock()
	return s.launch(ctx, name, r)
}
