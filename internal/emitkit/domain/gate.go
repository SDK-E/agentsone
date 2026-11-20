// SPDX-License-Identifier: MIT

package domain

// Transition is what a reading did to a Gate.
type Transition int

// The three outcomes of a reading.
const (
	Steady Transition = iota
	WentHigh
	WentNormal
)

// String names the transition for logs.
func (t Transition) String() string {
	switch t {
	case WentHigh:
		return "went-high"
	case WentNormal:
		return "went-normal"
	default:
		return "steady"
	}
}

// Gate turns readings into transitions. It goes high at or above the
// threshold and back to normal only below threshold minus margin, so a
// reading that wobbles around the line does not flap. A sustained gate also
// needs several high readings in a row before it goes high.
type Gate struct {
	threshold int
	margin    int
	sustain   int
	streak    int
	high      bool
}

// NewGate returns a gate that starts normal. A negative margin counts as zero.
func NewGate(threshold, margin int) *Gate { return NewSustainedGate(threshold, margin, 1) }

// NewSustainedGate is a gate that goes high only after sustain readings in a
// row at or above the threshold. A sustain below one counts as one.
func NewSustainedGate(threshold, margin, sustain int) *Gate {
	return &Gate{threshold: threshold, margin: max(margin, 0), sustain: max(sustain, 1)}
}

// Observe feeds one reading.
func (g *Gate) Observe(reading int) Transition {
	if reading >= g.threshold {
		g.streak++
	} else {
		g.streak = 0
	}
	switch {
	case !g.high && g.streak >= g.sustain:
		g.high = true
		return WentHigh
	case g.high && reading < g.threshold-g.margin:
		g.high = false
		return WentNormal
	default:
		return Steady
	}
}

// High reports whether the gate is currently high.
func (g *Gate) High() bool { return g.high }
