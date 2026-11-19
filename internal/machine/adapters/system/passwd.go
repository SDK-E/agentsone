// SPDX-License-Identifier: MIT

package system

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/SDK-E/agentsone/internal/machine/app"
	"github.com/SDK-E/agentsone/internal/machine/domain"
)

// Passwd reads Linux accounts from a passwd file.
type Passwd struct{ path string }

var _ app.UserDB = Passwd{}

// NewPasswd reads accounts from path, normally /etc/passwd.
func NewPasswd(path string) Passwd { return Passwd{path: path} }

// List returns every well-formed account in file order.
func (p Passwd) List(context.Context) ([]domain.MachineUser, error) {
	f, err := os.Open(p.path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", p.path, err)
	}
	defer func() { _ = f.Close() }()
	var out []domain.MachineUser
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if u, ok := parsePasswdLine(sc.Text()); ok {
			out = append(out, u)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read %s: %w", p.path, err)
	}
	return out, nil
}

// nameOf returns the account name for uid, or the number when unknown.
func (p Passwd) nameOf(ctx context.Context, uid int) string {
	users, err := p.List(ctx)
	if err == nil {
		for _, u := range users {
			if u.UID == uid {
				return u.Name
			}
		}
	}
	return strconv.Itoa(uid)
}

func parsePasswdLine(line string) (domain.MachineUser, bool) {
	f := strings.Split(line, ":")
	if len(f) != 7 || strings.HasPrefix(line, "#") {
		return domain.MachineUser{}, false
	}
	uid, err1 := strconv.Atoi(f[2])
	gid, err2 := strconv.Atoi(f[3])
	if err1 != nil || err2 != nil {
		return domain.MachineUser{}, false
	}
	return domain.MachineUser{Name: f[0], UID: uid, GID: gid, Home: f[5], Shell: f[6], Kind: domain.KindOfUID(uid)}, true
}
