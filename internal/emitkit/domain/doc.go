// SPDX-License-Identifier: MIT

// Package domain holds the pure rules shared by the built-in event sources:
// the draft of an event a source wants to publish, and the gate that turns a
// stream of readings into "went high" and "went normal" transitions without
// flapping.
package domain
