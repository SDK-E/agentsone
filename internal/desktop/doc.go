// SPDX-License-Identifier: MIT

// Package desktop is the full desktop shown in the browser's Desktop
// panel: one desktop per signed-in human, run as that human's own Linux
// user, so files and terminals opened in it belong to them.
//
// A desktop is labwc on a headless output (pixman, no GPU) with wayvnc
// serving it on a Unix socket in the human's private runtime folder. It
// starts on the human's first connection and stops after a while with
// nobody looking. The daemon bridges the browser's WebSocket at
// /desktop/<human>/vnc to that socket, for that human only, after the
// origin check and before anything starts.
//
// Shape: app/ (desktops per human, start, idle stop), adapters/http/ (the
// bridge), adapters/system/ (labwc and wayvnc as the human), wired in
// module.go.
package desktop
