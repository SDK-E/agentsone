// SPDX-License-Identifier: MIT

package domain

// HomeRoot holds the homes of custom agents.
const HomeRoot = "/home/agents"

// NoLoginShell is the shell of custom agent users. Agents never log in: no
// password, no SSH, no console. Terminals for an agent are started by the
// daemon, which runs the shell as the agent's user directly, so they do not
// need a login shell and nologin closes every login path.
const NoLoginShell = "/usr/sbin/nologin"

// Account is the Linux identity a runner runs as.
type Account struct {
	User string
	// Group is the agent's own group, named after it, with gid == uid;
	// empty for root agents.
	Group  string
	Home   string
	Shell  string
	IsRoot bool
	// Create is true when the account must be made (custom agents only).
	Create bool
	// UID is the uid and gid of a custom agent's user and group. The society
	// context allocates it from its range and stores it; zero means unknown,
	// and such an account is not created. A custom agent never runs as uid 0.
	UID int
}

// WithUID returns the account with its stored uid, so the user is created,
// or recreated after the system's user database was reset, with exactly
// that uid and gid. Values <= 0 are ignored, and so are root accounts.
func (a Account) WithUID(uid int) Account {
	if a.IsRoot || uid <= 0 {
		return a
	}
	a.UID = uid
	return a
}

// AccountFor returns the account of agent name. Root agents run as root and
// need no user. A custom agent gets its own user, named after it, with a
// private home (0700) under HomeRoot.
func AccountFor(name string, isRoot bool) Account {
	if isRoot {
		return Account{User: "root", Home: "/root", IsRoot: true}
	}
	return Account{
		User: name, Group: name, Home: HomeRoot + "/" + name,
		Shell: NoLoginShell, Create: true,
	}
}
