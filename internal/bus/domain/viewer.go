// SPDX-License-Identifier: MIT

package domain

// Viewer says which channels and messages a caller may read.
type Viewer struct {
	// All is set for humans, the daemon, the host and root agents: they see everything.
	All bool
	// Name is the agent's name. Empty for plugins.
	Name string
	// TeamRoot is the head of the agent's chart branch.
	TeamRoot string
	// IsBelow reports whether an agent is strictly below the viewer in the chart.
	IsBelow func(agent string) bool
}

// CanSeeChannel reports whether v may read and post in c: public channels are
// open, team channels only to the branch.
func (v Viewer) CanSeeChannel(c Channel) bool {
	return v.All || c.Kind == KindPublic || (c.Team != "" && c.Team == v.TeamRoot)
}

// CanSeeDirect reports whether v may read a direct message: its sender, its
// recipient, and the managers above either of them.
func (v Viewer) CanSeeDirect(m Message) bool {
	if v.All {
		return true
	}
	if v.Name == "" {
		return false
	}
	sender, isAgent := agentOf(m.From)
	if (isAgent && sender == v.Name) || m.To == v.Name {
		return true
	}
	if v.IsBelow == nil {
		return false
	}
	return (isAgent && v.IsBelow(sender)) || v.IsBelow(m.To)
}

// agentOf returns the agent name of an "agent:<name>" actor.
func agentOf(actor string) (string, bool) {
	const prefix = "agent:"
	if len(actor) > len(prefix) && actor[:len(prefix)] == prefix {
		return actor[len(prefix):], true
	}
	return "", false
}
