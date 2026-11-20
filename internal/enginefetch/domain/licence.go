// SPDX-License-Identifier: MIT

package domain

import (
	"encoding/json"
	"fmt"
)

// LicenceNotice is the text a human reads before accepting the engine. It is
// the only copy: the image build writes it into the licence file from here.
const LicenceNotice = "Claude Code is proprietary software by Anthropic PBC. Agents One does not include it. " +
	"If you accept, this machine downloads it for you, unchanged. Your use of it is subject to Anthropic's terms " +
	"for your account, listed at https://code.claude.com/docs/en/legal-and-compliance."

// LicenceJSON renders the licence file the image ships at
// /usr/lib/agentsone/engine/licence.json: the pinned engine and version with
// LicenceNotice. It never holds an acceptance, which only a human gives.
func (p Pin) LicenceJSON() ([]byte, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	b, err := json.MarshalIndent(struct {
		Engine  string `json:"engine"`
		Version string `json:"version"`
		Text    string `json:"text"`
	}{p.Engine, p.Version, LicenceNotice}, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("render engine licence: %w", err)
	}
	return append(b, '\n'), nil
}
