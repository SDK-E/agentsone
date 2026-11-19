// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"io/fs"
	"time"

	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/machine/domain"
)

// PackageManager is apt and dpkg.
type PackageManager interface {
	Installed(ctx context.Context) ([]domain.Package, error)
	// Manual lists packages marked manually installed (apt-mark showmanual).
	Manual(ctx context.Context) ([]string, error)
	// Install installs name, at version when not empty.
	Install(ctx context.Context, name, version string) error
	Remove(ctx context.Context, name string) error
}

// FileWrite is the body and ownership of a file to write.
type FileWrite struct {
	Data          []byte
	Mode          fs.FileMode
	UID, GID      int
	CreateParents bool
}

// FileSystem reads and writes files inside an area. Implementations open
// the area's base as a root, so a symlink cannot lead outside it.
type FileSystem interface {
	List(ctx context.Context, a domain.Area) ([]domain.FileEntry, error)
	// Read returns at most limit bytes and the file's full size.
	Read(ctx context.Context, a domain.Area, limit int64) ([]byte, int64, error)
	Write(ctx context.Context, a domain.Area, w FileWrite) (domain.FileEntry, error)
	// Delete removes a file, or a folder when recursive. A non-empty folder
	// without recursive is an ErrConflict.
	Delete(ctx context.Context, a domain.Area, recursive bool) error
}

// ProcessTable lists and signals processes.
type ProcessTable interface {
	List(ctx context.Context) ([]domain.Process, error)
	Get(ctx context.Context, pid int) (domain.Process, error)
	Signal(ctx context.Context, pid int, s domain.Signal) error
}

// UserDB lists Linux accounts.
type UserDB interface {
	List(ctx context.Context) ([]domain.MachineUser, error)
}

// MetricsReader takes a snapshot of the machine.
type MetricsReader interface {
	Read(ctx context.Context) (domain.Metrics, error)
}

// RecipeStore keeps the recipe and file contents on the state volume.
type RecipeStore interface {
	// Load returns the recipe, and false when none was ever saved.
	Load(ctx context.Context) (domain.Recipe, bool, error)
	Save(ctx context.Context, r domain.Recipe) error
	// PutBlob stores content and returns its hash.
	PutBlob(ctx context.Context, data []byte) (string, error)
	Blob(ctx context.Context, hash string) ([]byte, error)
}

// BaselineSource reads the image's list of manually installed packages.
type BaselineSource interface {
	// Baseline returns the list, and false when the image ships none.
	Baseline(ctx context.Context) ([]string, bool, error)
}

// Power ends the daemon with an exit code.
type Power interface {
	Exit(code int)
}

// Timer runs f once after d.
type Timer interface {
	AfterFunc(d time.Duration, f func())
}

// Deps are the machine use cases' dependencies.
type Deps struct {
	Packages  PackageManager
	Files     FileSystem
	Processes ProcessTable
	Users     UserDB
	Metrics   MetricsReader
	Recipes   RecipeStore
	Baseline  BaselineSource
	Power     Power
	Timer     Timer
	Audit     core.AuditLog
	Clock     core.Clock
	IDs       core.IDs
}
