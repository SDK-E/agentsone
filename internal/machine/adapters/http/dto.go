// SPDX-License-Identifier: MIT

package httpadapter

import (
	"github.com/SDK-E/agentsone/internal/contract/api"
	"github.com/SDK-E/agentsone/internal/machine/domain"
)

// The mappers below are the only place that knows the wire format, so a
// contract rename stays a change to this file.

func toMetrics(m domain.Metrics) api.MachineMetrics {
	disks := make([]api.DiskMetrics, 0, len(m.Disks))
	for _, d := range m.Disks {
		disks = append(disks, api.DiskMetrics{Mount: d.Mount, TotalBytes: d.Total, UsedBytes: d.Used})
	}
	return api.MachineMetrics{
		Time:          api.Timestamp(m.Time),
		Hostname:      m.Hostname,
		UptimeSeconds: int64(m.Uptime.Seconds()),
		Load:          []float32{float32(m.Load[0]), float32(m.Load[1]), float32(m.Load[2])},
		Cpu:           api.CpuMetrics{Cores: max(m.Cores, 1), UsedPercent: float32(m.CPUPercent)},
		Memory:        api.MemoryMetrics{TotalBytes: m.MemTotal, UsedBytes: m.MemUsed, AvailableBytes: m.MemAvail},
		Disks:         disks,
		Net:           api.NetMetrics{RxBytes: m.RxBytes, TxBytes: m.TxBytes},
	}
}

func toProcess(p domain.Process) api.Process {
	return api.Process{
		Pid: p.PID, Ppid: p.PPID, User: p.User, Command: p.Command,
		CpuPercent: float32(p.CPUPercent), RssBytes: p.RSSBytes, StartedAt: api.Timestamp(p.Started),
		Unit: nullable(p.Unit), Agent: nullable(p.Agent),
	}
}

func toService(s domain.Service) api.Service {
	return api.Service{
		Unit: s.Unit, Description: s.Description, LoadState: s.LoadState,
		ActiveState: api.ServiceActiveState(s.ActiveState), SubState: s.SubState,
		Enabled: s.Enabled, Agent: nullable(s.Agent),
	}
}

func toPackage(p domain.Package) api.Package {
	return api.Package{Name: p.Name, Version: p.Version, Architecture: p.Architecture, Summary: p.Summary}
}

func toJob(j domain.Job) api.PackageJob {
	return api.PackageJob{
		Id: j.ID, Action: api.PackageJobAction(j.Action), Package: j.Package,
		State: api.JobState(j.State), CreatedAt: api.Timestamp(j.Created),
	}
}

func toUser(u domain.MachineUser) api.MachineUser {
	return api.MachineUser{Name: u.Name, Uid: u.UID, Gid: u.GID, Home: u.Home, Shell: u.Shell, Kind: api.MachineUserKind(u.Kind)}
}

func toFileEntry(e domain.FileEntry) api.FileEntry {
	return api.FileEntry{
		Path: e.Path, Name: e.Name, Type: api.FileType(e.Type), SizeBytes: e.Size,
		Mode: domain.FormatMode(e.Mode), Owner: e.Owner, Group: e.Group, ModifiedAt: api.Timestamp(e.Modified),
	}
}

func mapAll[D, W any](items []D, f func(D) W) []W {
	out := make([]W, 0, len(items))
	for _, item := range items {
		out = append(out, f(item))
	}
	return out
}

func nullable(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func cursor(next string) *api.Cursor { return nullable(next) }

func valueOr[T any](p *T, fallback T) T {
	if p == nil {
		return fallback
	}
	return *p
}
