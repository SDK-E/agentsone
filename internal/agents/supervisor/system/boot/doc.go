// SPDX-License-Identifier: MIT

// Package boot holds the system test of the daemon's start probe for agent
// limits (TESTING.md B6). It runs in a fresh container of its own, as root,
// because the probe rearranges the container's cgroup tree once at start.
// The go-integration CI job runs it twice: on the machine recipe
// (writable-cgroups=true, hard limits) and on a read-only cgroup tree (soft
// limits). It builds only with the linux and integration tags.
package boot
