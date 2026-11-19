// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/SDK-E/agentsone/internal/core"
)

// maxChartDepth stops a broken chart (a manager loop) from looping forever.
const maxChartDepth = 64

// gatewayToken mints the agent's model gateway token through the daemon's
// one minter (core.GatewayTokens). Without the model gateway the error
// matches core.ErrUnavailable and says the gateway is not reachable.
func (s *Supervisor) gatewayToken(ctx context.Context, agent string) (string, error) {
	chart, err := s.chartPath(ctx, agent)
	if err != nil {
		return "", fmt.Errorf("gateway token for %s: %w", agent, err)
	}
	// The defence share is a soft wall: without a budget answer the agent
	// gets the ordinary share, never the defence one.
	b, err := s.d.Ports.Budget.Allocation(ctx, agent)
	defence := err == nil && b.Defence
	tok, err := s.d.Ports.GatewayTokens.Mint(ctx, core.GatewayGrant{
		Subject: agent, ChartPath: chart, Defence: defence, TTL: s.d.Config.GatewayTokenTTL,
	})
	if err != nil {
		return "", fmt.Errorf("gateway token for %s: %w", agent, err)
	}
	return tok, nil
}

// chartPath is the agent's path from the top of the chart, ending with its
// own name ("founder/cto/mira").
func (s *Supervisor) chartPath(ctx context.Context, agent string) (string, error) {
	path := []string{agent}
	for name := agent; ; {
		m, err := s.d.Ports.Agents.Manager(ctx, name)
		if err != nil {
			return "", fmt.Errorf("chart path: %w", err)
		}
		if m == "" {
			break
		}
		if len(path) >= maxChartDepth || slices.Contains(path, m) {
			return "", fmt.Errorf("chart path of %s: %w: the chart loops or is too deep", agent, core.ErrInvalid)
		}
		path, name = append(path, m), m
	}
	slices.Reverse(path)
	return strings.Join(path, "/"), nil
}
