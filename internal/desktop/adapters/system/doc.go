// SPDX-License-Identifier: MIT

// Package system starts a desktop on the container: labwc on a headless
// output and wayvnc on a Unix socket, as the human's own Linux user, with
// a clean environment and a private runtime folder, and ends every program
// of it at stop.
package system
