// SPDX-License-Identifier: MIT

// Package domain holds the pure rules of the runner supervisor: which agent
// names are safe on the system, how a protocol version is chosen, the state
// machine of one runner session, when a heartbeat deadline has passed, how a
// wake is handled for each session state, and how an agent's runner runs
// (its Linux account, command and limits). Nothing here touches the clock,
// the network or the file system.
package domain
