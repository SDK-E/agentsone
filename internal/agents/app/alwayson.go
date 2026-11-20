// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/SDK-E/agentsone/internal/agents/domain"
)

// RunAlwaysOn starts the runner of every active always-on agent, idle: the
// daemon calls it at start and once the society is seeded. Runners that
// cannot start are reported together; the others still start.
func (s *Society) RunAlwaysOn(ctx context.Context) error {
	chart, err := s.chart(ctx)
	if err != nil {
		return err
	}
	var errs []error
	for _, a := range chart.Agents() {
		if !a.Active() || a.Kind != domain.KindAlwaysOn {
			continue
		}
		if err := s.rt.Run(ctx, a.Name); err != nil {
			errs = append(errs, fmt.Errorf("start %s: %w", a.Name, err))
		}
	}
	return errors.Join(errs...)
}
