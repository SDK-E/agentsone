// SPDX-License-Identifier: MIT

package domain

import (
	"errors"
	"strconv"
	"strings"
)

// ErrNoCommonVersion means the runner and the daemon share no protocol version.
var ErrNoCommonVersion = errors.New("no protocol version in common")

// DaemonVersions are the runner protocol versions this daemon speaks,
// highest first. The daemon supports the current and the previous major.
var DaemonVersions = []string{"1.1", "1.0"}

// CredentialsUpdates reports whether a connection that negotiated version
// gets credentials.update (rule K1: 1.1 or later).
func CredentialsUpdates(version string) bool {
	v, ok := parseVersion(version)
	return ok && !v.less(version11)
}

var version11 = version{major: 1, minor: 1, raw: "1.1"}

// Negotiate picks the highest version that both lists hold (PROTOCOL.md
// handshake step 2, rule H4). Versions compare as <major>.<minor> numbers, so
// "1.10" is above "1.9". Malformed entries are ignored.
func Negotiate(runner, daemon []string) (string, error) {
	best, found := version{}, false
	for _, r := range runner {
		rv, ok := parseVersion(r)
		if !ok {
			continue
		}
		for _, d := range daemon {
			if d != r {
				continue
			}
			if !found || best.less(rv) {
				best, found = rv, true
			}
		}
	}
	if !found {
		return "", ErrNoCommonVersion
	}
	return best.raw, nil
}

type version struct {
	major, minor int
	raw          string
}

func (v version) less(o version) bool {
	if v.major != o.major {
		return v.major < o.major
	}
	return v.minor < o.minor
}

func parseVersion(s string) (version, bool) {
	majorText, minorText, ok := strings.Cut(s, ".")
	if !ok {
		return version{}, false
	}
	a, err := strconv.Atoi(majorText)
	if err != nil || a < 0 {
		return version{}, false
	}
	b, err := strconv.Atoi(minorText)
	if err != nil || b < 0 {
		return version{}, false
	}
	return version{major: a, minor: b, raw: s}, true
}
