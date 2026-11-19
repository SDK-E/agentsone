// SPDX-License-Identifier: MIT

package domain

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/SDK-E/agentsone/internal/core"
)

// Chart is the org chart: every agent, retired ones included, keyed by
// name. It holds the rule that agents act only below themselves.
type Chart struct {
	byName map[string]Agent
}

// NewChart builds a chart from every agent of the society.
func NewChart(agents []Agent) Chart {
	c := Chart{byName: make(map[string]Agent, len(agents))}
	for _, a := range agents {
		c.byName[a.Name] = a
	}
	return c
}

// Get returns the named agent.
func (c Chart) Get(name string) (Agent, error) {
	a, ok := c.byName[name]
	if !ok {
		return Agent{}, fmt.Errorf("%w: %s", ErrAgentNotFound, name)
	}
	return a, nil
}

// Agents returns every agent sorted by name.
func (c Chart) Agents() []Agent {
	all := make([]Agent, 0, len(c.byName))
	for _, a := range c.byName {
		all = append(all, a)
	}
	slices.SortFunc(all, func(a, b Agent) int { return strings.Compare(a.Name, b.Name) })
	return all
}

// Founder returns the one agent without a manager, if the society has one.
func (c Chart) Founder() (Agent, bool) {
	for _, a := range c.byName {
		if a.Manager == "" {
			return a, true
		}
	}
	return Agent{}, false
}

// Empty reports whether the society has no agent yet.
func (c Chart) Empty() bool { return len(c.byName) == 0 }

// IsBelow reports whether name is strictly below ancestor.
func (c Chart) IsBelow(name, ancestor string) bool {
	return slices.Contains(c.managers(name), ancestor)
}

// managers returns name's managers from nearest to the top.
func (c Chart) managers(name string) []string {
	var chain []string
	a, ok := c.byName[name]
	for ok && a.Manager != "" && len(chain) <= len(c.byName) {
		chain = append(chain, a.Manager)
		a, ok = c.byName[a.Manager]
	}
	return chain
}

// Team is the head of name's chart branch: the agent directly below the
// founder that name sits in, name itself when it reports to the founder,
// and the founder for the founder.
func (c Chart) Team(name string) string {
	chain := append([]string{name}, c.managers(name)...)
	if len(chain) < 2 {
		return name
	}
	return chain[len(chain)-2]
}

// Children returns the active direct reports of name, sorted by name.
func (c Chart) Children(name string) []Agent {
	var out []Agent
	for _, a := range c.Agents() {
		if a.Manager == name && a.Active() {
			out = append(out, a)
		}
	}
	return out
}

// active returns the named agent when it is still in the society.
func (c Chart) active(name string) (Agent, error) {
	a, err := c.Get(name)
	if err != nil {
		return Agent{}, err
	}
	if !a.Active() {
		return Agent{}, fmt.Errorf("%w: %s", ErrRetired, name)
	}
	return a, nil
}

// CanManage checks that actor may edit target: target must be strictly
// below actor, and both must be active.
func (c Chart) CanManage(actor, target string) error {
	if _, err := c.active(actor); err != nil {
		return err
	}
	if _, err := c.active(target); err != nil {
		return err
	}
	if !c.IsBelow(target, actor) {
		return fmt.Errorf("%w: %s is not below %s", ErrNotBelowActor, target, actor)
	}
	return nil
}

// atOrBelow checks that name is actor itself or below it.
func (c Chart) atOrBelow(actor, name string) error {
	if name == actor {
		return nil
	}
	return c.CanManage(actor, name)
}

// Hire returns the agent actor hires with spec at time at. The new agent
// sits below spec.Manager, which defaults to actor and must be actor or
// below it. Only root agents hire root agents. A root agent gets uid 0; a
// custom agent gets the next uid of the agent range.
func (c Chart) Hire(actor string, spec HireSpec, at time.Time) (Agent, error) {
	hirer, err := c.active(actor)
	if err != nil {
		return Agent{}, err
	}
	if err := spec.validate(); err != nil {
		return Agent{}, err
	}
	if spec.IsRoot && !hirer.IsRoot {
		return Agent{}, fmt.Errorf("%w: %s is a custom agent", ErrRootOnly, actor)
	}
	if spec.Manager == "" {
		spec.Manager = actor
	}
	if err := c.atOrBelow(actor, spec.Manager); err != nil {
		return Agent{}, err
	}
	if _, taken := c.byName[spec.Name]; taken {
		return Agent{}, fmt.Errorf("%w: %s", ErrNameTaken, spec.Name)
	}
	a := Agent{
		Name: spec.Name, DisplayName: spec.DisplayName, Kind: spec.Kind, IsRoot: spec.IsRoot,
		Runner: spec.Runner, Manager: spec.Manager, Role: spec.Role, Description: spec.Description,
		State: StateSleeping, UID: -1, Home: homeFor(spec.Name, spec.IsRoot), Template: spec.Template,
		CreatedAt: at, CreatedBy: "agent:" + actor,
	}
	if spec.IsRoot {
		a.UID = 0
		return a, nil
	}
	uid, err := c.nextUID()
	if err != nil {
		return Agent{}, err
	}
	limits := DefaultLimits
	if spec.Limits != nil {
		limits = *spec.Limits
	}
	a.UID, a.Limits = uid, &limits
	return a, nil
}

// Custom agents get uids (and equal gids) from a dedicated range, so they
// never share a uid, and its per-uid limits, with a human or system user.
const (
	FirstAgentUID = 200000
	LastAgentUID  = 299999
)

// ErrUIDsExhausted means every uid of the agent range has been handed out.
var ErrUIDsExhausted = fmt.Errorf("no agent uid left in %d..%d: %w", FirstAgentUID, LastAgentUID, core.ErrConflict)

// nextUID returns the uid after the highest one any agent, retired ones
// included, ever held in the range. Uids are never reused, so files and
// audit records of a retired agent stay its own.
func (c Chart) nextUID() (int, error) {
	next := FirstAgentUID
	for _, a := range c.byName {
		if a.UID >= FirstAgentUID && a.UID <= LastAgentUID && a.UID >= next {
			next = a.UID + 1
		}
	}
	if next > LastAgentUID {
		return 0, ErrUIDsExhausted
	}
	return next, nil
}

// Found returns the founder: the first agent, a root agent at the top of
// the chart. Only an empty society can be founded.
func (c Chart) Found(spec HireSpec, by string, at time.Time) (Agent, error) {
	if !c.Empty() {
		return Agent{}, fmt.Errorf("%w: the society already has a founder", ErrNameTaken)
	}
	spec.IsRoot, spec.Manager, spec.Limits = true, "", nil
	if err := spec.validate(); err != nil {
		return Agent{}, err
	}
	return Agent{
		Name: spec.Name, DisplayName: spec.DisplayName, Kind: spec.Kind, IsRoot: true,
		Runner: spec.Runner, Role: spec.Role, Description: spec.Description, State: StateSleeping,
		Home: homeFor(spec.Name, true), Template: spec.Template, CreatedAt: at, CreatedBy: by,
	}, nil
}

// RetirePlan says what retiring an agent changes.
type RetirePlan struct {
	Agent string
	// NewManager takes the retired agent's direct reports: the actor.
	NewManager string
	// Moved are the direct reports that move, sorted by name.
	Moved []string
}

// Retire plans actor retiring target. Target must be below actor; its
// direct reports move up to actor, so no branch is left without a manager
// and the work below goes on.
func (c Chart) Retire(actor, target string) (RetirePlan, error) {
	if err := c.CanManage(actor, target); err != nil {
		return RetirePlan{}, err
	}
	plan := RetirePlan{Agent: target, NewManager: actor}
	for _, child := range c.Children(target) {
		plan.Moved = append(plan.Moved, child.Name)
	}
	return plan, nil
}

// CanMove checks that actor may move agent and its branch below
// newManager. Agent must be below actor; newManager must be actor or below
// it, and must not be agent or one of its reports.
func (c Chart) CanMove(actor, agent, newManager string) error {
	if err := c.CanManage(actor, agent); err != nil {
		return err
	}
	if err := c.atOrBelow(actor, newManager); err != nil {
		return err
	}
	if newManager == agent || c.IsBelow(newManager, agent) {
		return fmt.Errorf("%w: %s below %s", ErrCycle, agent, newManager)
	}
	return nil
}

// Node is one agent in the chart tree.
type Node struct {
	Agent    Agent
	Children []Node
}

// Tree returns the active agents as a tree from the top of the chart.
func (c Chart) Tree() []Node {
	var roots []Node
	for _, a := range c.Agents() {
		if a.Manager == "" && a.Active() {
			roots = append(roots, c.node(a, 0))
		}
	}
	return roots
}

func (c Chart) node(a Agent, depth int) Node {
	n := Node{Agent: a, Children: []Node{}}
	if depth > len(c.byName) {
		return n
	}
	for _, child := range c.Children(a.Name) {
		n.Children = append(n.Children, c.node(child, depth+1))
	}
	return n
}

// ActiveCount is the number of agents still in the society.
func (c Chart) ActiveCount() int {
	n := 0
	for _, a := range c.byName {
		if a.Active() {
			n++
		}
	}
	return n
}
