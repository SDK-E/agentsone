// SPDX-License-Identifier: MIT

package domain

import "errors"

// Errors the plugin domain reports. The app layer wraps them with the core
// sentinels (invalid, conflict, not found) so the API maps them to statuses.
var (
	// ErrInvalidManifest means a manifest breaks the contract's rules.
	ErrInvalidManifest = errors.New("invalid plugin manifest")
	// ErrEmptyPlugin means every contents list of a manifest is empty.
	ErrEmptyPlugin = errors.New("plugin provides nothing")
	// ErrUnsafePath means a path leaves the plugin root or uses a dot segment.
	ErrUnsafePath = errors.New("unsafe path")
	// ErrNotSemantic means a version is not a semantic version.
	ErrNotSemantic = errors.New("version is not semantic")
	// ErrUnsupportedSource means a source type cannot be installed.
	ErrUnsupportedSource = errors.New("unsupported source")
	// ErrNothingToInstall means a source holds no plugin the adapter can read.
	ErrNothingToInstall = errors.New("nothing to install")
	// ErrTooLarge means a plugin or one of its files is over a size limit.
	ErrTooLarge = errors.New("plugin is too large")
	// ErrIntegrity means a fetched archive does not match its recorded digest.
	ErrIntegrity = errors.New("integrity check failed")
	// ErrAlreadyInstalled means a plugin of this name and version exists.
	ErrAlreadyInstalled = errors.New("plugin already installed")
	// ErrNotFound means no such plugin, theme or runner.
	ErrNotFound = errors.New("not found")
	// ErrInUse means a theme or plugin cannot be removed because it is used.
	ErrInUse = errors.New("in use")
	// ErrBuiltin means the item is built in and cannot be removed.
	ErrBuiltin = errors.New("built in")
	// ErrInvalidTheme means a theme breaks the schema rules or contrast pairs.
	ErrInvalidTheme = errors.New("invalid theme")
	// ErrIncompatible means a plugin does not work with this Agents One version.
	ErrIncompatible = errors.New("plugin is not compatible")
)
