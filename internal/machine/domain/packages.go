// SPDX-License-Identifier: MIT

package domain

import (
	"regexp"
	"time"
)

// Debian package names and versions, as apt accepts them. Checked before any
// value reaches a command line.
var (
	packageNameRE    = regexp.MustCompile(`^[a-z0-9][a-z0-9+.-]+$`)
	packageVersionRE = regexp.MustCompile(`^[A-Za-z0-9.+~:-]*$`)
)

// Package is one installed Debian package.
type Package struct {
	Name         string
	Version      string
	Architecture string
	Summary      string
}

// JobAction is what a package job does.
type JobAction string

// Job actions.
const (
	JobInstall JobAction = "install"
	JobRemove  JobAction = "remove"
)

// JobState is where a package job is.
type JobState string

// Job states.
const (
	JobQueued  JobState = "queued"
	JobRunning JobState = "running"
	JobDone    JobState = "done"
	JobFailed  JobState = "failed"
)

// Job is one queued apt operation.
type Job struct {
	ID      string
	Action  JobAction
	Package string
	Version string
	State   JobState
	Created time.Time
	By      string
}

// ParsePackageName checks a Debian package name.
func ParsePackageName(name string) error {
	if !packageNameRE.MatchString(name) {
		return invalid("package name %q", name)
	}
	return nil
}

// ParsePackageVersion checks an optional Debian version.
func ParsePackageVersion(v string) error {
	if !packageVersionRE.MatchString(v) {
		return invalid("package version %q", v)
	}
	return nil
}

// CanManagePackages lets only root agents and humans change packages.
func CanManagePackages(a Actor) error {
	if !a.Privileged() {
		return forbidden("%s may not change packages", a.Label())
	}
	return nil
}
