// SPDX-License-Identifier: MIT

// Package gateway serves the one model name autoagents/one to agents on the
// machine-side inner network and picks the real provider and model. It runs
// in the edge container, which holds the provider keys (files in the edge
// volume, written only through the admin socket), the budget and the gateway.
// It provides the gateway command.
//
// Owned by work package 16 (edge: gateway, budget, audit sink, web door).
package gateway
