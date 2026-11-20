// SPDX-License-Identifier: MIT

// Package app runs one desktop per human: started on first connection,
// shared by that human's browser tabs, stopped after a while with no
// client, started again after a crash, and capped in number.
package app
