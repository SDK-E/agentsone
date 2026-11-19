// SPDX-License-Identifier: MIT

// Package system holds the supervisor's Linux system tests (TESTING.md
// B6): real users, homes, sockets, cgroup v2 limits and systemd units. They
// build only with the linux and integration tags and need root:
//
//	sudo -E env PATH="$PATH" go test -tags integration -race ./internal/agents/supervisor/system/
package system
