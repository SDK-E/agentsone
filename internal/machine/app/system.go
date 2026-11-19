// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strconv"

	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/machine/domain"
)

// DaemonUnit is the daemon itself in the services list.
const DaemonUnit = "agentsoned.service"

// ProcessFilter narrows the process list. Empty fields match all.
type ProcessFilter struct {
	User  string
	Agent string
}

// ListProcesses lists processes, highest CPU first.
func (m *Machine) ListProcesses(ctx context.Context, f ProcessFilter, cursor string, limit int) ([]domain.Process, string, error) {
	all, err := m.d.Processes.List(ctx)
	if err != nil {
		return nil, "", fmt.Errorf("list processes: %w", err)
	}
	all = slices.DeleteFunc(all, func(p domain.Process) bool {
		return (f.User != "" && p.User != f.User) || (f.Agent != "" && p.Agent != f.Agent)
	})
	sort.SliceStable(all, func(i, j int) bool { return all[i].CPUPercent > all[j].CPUPercent })
	return domain.Page(all, cursor, limit)
}

// SignalProcess sends a signal. Custom agents signal only their own processes.
func (m *Machine) SignalProcess(ctx context.Context, a domain.Actor, pid int, signal string) error {
	sig, err := domain.ParseSignal(signal)
	if err != nil {
		return err
	}
	p, err := m.d.Processes.Get(ctx, pid)
	if err != nil {
		return fmt.Errorf("find pid %d: %w", pid, err)
	}
	if err := domain.CanSignal(a, p); err != nil {
		return err
	}
	if err := m.d.Processes.Signal(ctx, pid, sig); err != nil {
		return fmt.Errorf("signal %d: %w", pid, err)
	}
	return m.audit(ctx, a, "machine.process.signal", strconv.Itoa(pid), map[string]any{"signal": string(sig)})
}

// ListUsers lists Linux accounts.
func (m *Machine) ListUsers(ctx context.Context, cursor string, limit int) ([]domain.MachineUser, string, error) {
	all, err := m.d.Users.List(ctx)
	if err != nil {
		return nil, "", fmt.Errorf("list users: %w", err)
	}
	return domain.Page(all, cursor, limit)
}

// Metrics returns a snapshot stamped with the daemon's clock.
func (m *Machine) Metrics(ctx context.Context) (domain.Metrics, error) {
	snap, err := m.d.Metrics.Read(ctx)
	if err != nil {
		return domain.Metrics{}, fmt.Errorf("read metrics: %w", err)
	}
	snap.Time = m.d.Clock.Now()
	return snap, nil
}

// Reboot audits the request, then ends the daemon with the reboot exit code
// after the delay, so the restart policy starts the container again.
func (m *Machine) Reboot(ctx context.Context, a domain.Actor, reason string, delaySeconds *int) (domain.Reboot, error) {
	r, err := domain.NewReboot(a, reason, delaySeconds, m.d.Clock.Now())
	if err != nil {
		return domain.Reboot{}, err
	}
	detail := map[string]any{"reason": r.Reason, "delay_seconds": int(r.Delay.Seconds()), "effect": domain.RebootEffect}
	if err := m.audit(ctx, a, "machine.reboot", "machine", detail); err != nil {
		return domain.Reboot{}, err
	}
	m.d.Timer.AfterFunc(r.Delay, func() { m.d.Power.Exit(domain.RebootExitCode) })
	return r, nil
}

// Shutdown is not the daemon's to do: humans stop and start the machine.
func (m *Machine) Shutdown(context.Context, domain.Actor) error {
	return fmt.Errorf("shutdown and start belong to the human, through Docker or Coolify: %w", core.ErrForbidden)
}

// ListServices lists the daemon and the machine recipe.
func (m *Machine) ListServices(_ context.Context, state domain.ServiceState, cursor string, limit int) ([]domain.Service, string, error) {
	all := []domain.Service{m.daemonService(), m.RecipeReport().Service()}
	all = slices.DeleteFunc(all, func(s domain.Service) bool { return state != "" && s.ActiveState != state })
	return domain.Page(all, cursor, limit)
}

func (m *Machine) daemonService() domain.Service {
	return domain.Service{
		Unit: DaemonUnit, Description: "Agents One daemon. Reboot: " + domain.RebootEffect, LoadState: "loaded",
		ActiveState: domain.ServiceActive, SubState: "running", Enabled: true,
	}
}

// StartService reapplies the recipe when asked for the recipe unit.
func (m *Machine) StartService(ctx context.Context, a domain.Actor, unit string) (domain.Service, error) {
	return m.reapplyUnit(ctx, a, unit, "start")
}

// RestartService reapplies the recipe when asked for the recipe unit.
func (m *Machine) RestartService(ctx context.Context, a domain.Actor, unit string) (domain.Service, error) {
	return m.reapplyUnit(ctx, a, unit, "restart")
}

// StopService refuses: the daemon stops through reboot, the recipe is one-shot.
func (m *Machine) StopService(_ context.Context, _ domain.Actor, unit string) (domain.Service, error) {
	switch unit {
	case DaemonUnit:
		return domain.Service{}, daemonRefusal("stop")
	case domain.RecipeUnit:
		return domain.Service{}, fmt.Errorf("%s runs once and cannot be stopped: %w", unit, core.ErrConflict)
	}
	return domain.Service{}, unknownUnit(unit)
}

func (m *Machine) reapplyUnit(ctx context.Context, a domain.Actor, unit, verb string) (domain.Service, error) {
	switch unit {
	case DaemonUnit:
		return domain.Service{}, daemonRefusal(verb)
	case domain.RecipeUnit:
		if !a.Privileged() {
			return domain.Service{}, fmt.Errorf("%s may not %s %s: %w", a.Label(), verb, unit, core.ErrForbidden)
		}
		return m.Reapply(ctx).Service(), nil
	}
	return domain.Service{}, unknownUnit(unit)
}

func daemonRefusal(verb string) error {
	return fmt.Errorf("the daemon does not %s itself; use reboot (%s): %w", verb, domain.RebootEffect, core.ErrForbidden)
}

func unknownUnit(unit string) error { return fmt.Errorf("service %s: %w", unit, core.ErrNotFound) }
