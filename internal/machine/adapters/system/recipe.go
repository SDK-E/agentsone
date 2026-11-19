// SPDX-License-Identifier: MIT

package system

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/machine/app"
	"github.com/SDK-E/agentsone/internal/machine/domain"
)

// blobName is a SHA-256 hex digest; nothing else may name a blob file.
var blobName = regexp.MustCompile(`^[0-9a-f]{64}$`)

// RecipeStore keeps recipe.json and content blobs in a folder on the state
// volume (normally /var/lib/agentsone/recipe), readable by root only.
type RecipeStore struct{ dir string }

var _ app.RecipeStore = RecipeStore{}

// NewRecipeStore stores the recipe in dir.
func NewRecipeStore(dir string) RecipeStore { return RecipeStore{dir: dir} }

type recipeFile struct {
	Baseline []string     `json:"baseline"`
	Packages []string     `json:"packages"`
	Files    []fileRecord `json:"files"`
}

type fileRecord struct {
	Path string `json:"path"`
	Mode uint32 `json:"mode"`
	UID  int    `json:"uid"`
	GID  int    `json:"gid"`
	Hash string `json:"sha256"`
}

// Load reads the recipe, and false when none was saved yet.
func (s RecipeStore) Load(context.Context) (domain.Recipe, bool, error) {
	b, err := os.ReadFile(filepath.Join(s.dir, "recipe.json"))
	if errors.Is(err, fs.ErrNotExist) {
		return domain.Recipe{}, false, nil
	}
	if err != nil {
		return domain.Recipe{}, false, fmt.Errorf("read recipe: %w", err)
	}
	var f recipeFile
	if err := json.Unmarshal(b, &f); err != nil {
		return domain.Recipe{}, false, fmt.Errorf("decode recipe: %w", err)
	}
	r := domain.Recipe{Baseline: f.Baseline, Packages: f.Packages}
	for _, rec := range f.Files {
		r.Files = append(r.Files, domain.FileRecord{Path: rec.Path, Mode: fs.FileMode(rec.Mode), UID: rec.UID, GID: rec.GID, Hash: rec.Hash})
	}
	return r, true, nil
}

// Save writes the recipe atomically.
func (s RecipeStore) Save(_ context.Context, r domain.Recipe) error {
	f := recipeFile{Baseline: r.Baseline, Packages: r.Packages}
	for _, rec := range r.Files {
		f.Files = append(f.Files, fileRecord{Path: rec.Path, Mode: uint32(rec.Mode), UID: rec.UID, GID: rec.GID, Hash: rec.Hash})
	}
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return fmt.Errorf("encode recipe: %w", err)
	}
	return s.atomicWrite("recipe.json", b)
}

// PutBlob stores data under its SHA-256 digest.
func (s RecipeStore) PutBlob(_ context.Context, data []byte) (string, error) {
	sum := sha256.Sum256(data)
	h := hex.EncodeToString(sum[:])
	return h, s.atomicWrite(filepath.Join("files", h), data)
}

// Blob reads stored content by digest.
func (s RecipeStore) Blob(_ context.Context, h string) ([]byte, error) {
	if !blobName.MatchString(h) {
		return nil, fmt.Errorf("blob name %q: %w", h, core.ErrInvalid)
	}
	b, err := os.ReadFile(filepath.Join(s.dir, "files", h)) //nolint:gosec // G304: h is a checked hex digest inside the store.
	if err != nil {
		return nil, fmt.Errorf("read blob %s: %w", h, err)
	}
	return b, nil
}

func (s RecipeStore) atomicWrite(rel string, data []byte) error {
	p := filepath.Join(s.dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(p), err)
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", rel, err)
	}
	if err := os.Rename(tmp, p); err != nil {
		return fmt.Errorf("replace %s: %w", rel, err)
	}
	return nil
}

// Baseline reads the image's package list, one name per line.
type Baseline struct{ path string }

var _ app.BaselineSource = Baseline{}

// NewBaseline reads the list at path.
func NewBaseline(path string) Baseline { return Baseline{path: path} }

// Baseline returns the list, and false when the image ships none.
func (b Baseline) Baseline(context.Context) ([]string, bool, error) {
	f, err := os.Open(b.path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("open %s: %w", b.path, err)
	}
	defer func() { _ = f.Close() }()
	var out []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line != "" && !strings.HasPrefix(line, "#") {
			out = append(out, line)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, false, fmt.Errorf("read %s: %w", b.path, err)
	}
	return out, true, nil
}
