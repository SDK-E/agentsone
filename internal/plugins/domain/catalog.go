// SPDX-License-Identifier: MIT

package domain

// SourceInfo is one configured catalog source.
type SourceInfo struct {
	ID      string // SourceID
	Name    string
	URL     string
	Enabled bool
}

// CatalogEntry is one installable plugin as a source lists it, normalised
// into one shape.
type CatalogEntry struct {
	Source    string // SourceID
	Ref       string // pass to install
	Name      string
	Version   string
	Summary   string // at most 500 characters
	Kinds     []string
	Publisher string
	Homepage  string // empty when unknown
	Installed bool
}

// CatalogQuery filters a catalog search.
type CatalogQuery struct {
	Text   string
	Source string // empty: all enabled sources
	Kind   string // empty: any
	After  string // paging cursor: the Ref of the last entry of the previous page
	Limit  int
}

// RunnerStatus is whether a runner can start.
type RunnerStatus string

// Runner statuses, as in the API.
const (
	RunnerReady       RunnerStatus = "ready"
	RunnerFetching    RunnerStatus = "fetching"
	RunnerUnavailable RunnerStatus = "unavailable"
)

// RunnerEntry describes one registered runner adapter.
type RunnerEntry struct {
	Name            string
	DisplayName     string
	Version         string
	ProtocolVersion string
	Builtin         bool
	PluginID        string // empty for built-ins
	Model           string // empty: none
	Status          RunnerStatus
	Description     string
}
