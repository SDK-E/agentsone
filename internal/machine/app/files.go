// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"fmt"
	"io/fs"
	"sort"

	"github.com/SDK-E/agentsone/internal/machine/domain"
)

const defaultFileMode fs.FileMode = 0o644

// FileContent is a file read, cut at domain.MaxFileBytes.
type FileContent struct {
	Path      string
	Data      []byte
	Size      int64
	Truncated bool
}

// WriteRequest is a whole-file write.
type WriteRequest struct {
	Path          string
	Data          []byte
	Mode          *fs.FileMode
	CreateParents bool
}

// ListFiles lists a folder the actor may see, folders first, then by name.
func (m *Machine) ListFiles(ctx context.Context, a domain.Actor, path, cursor string, limit int) ([]domain.FileEntry, string, error) {
	area, err := domain.AreaFor(a, path)
	if err != nil {
		return nil, "", err
	}
	entries, err := m.d.Files.List(ctx, area)
	if err != nil {
		return nil, "", fmt.Errorf("list %s: %w", area.Path, err)
	}
	sort.Slice(entries, func(i, j int) bool {
		di, dj := entries[i].Type == domain.FileDir, entries[j].Type == domain.FileDir
		if di != dj {
			return di
		}
		return entries[i].Name < entries[j].Name
	})
	return domain.Page(entries, cursor, limit)
}

// ReadFile reads a file the actor may see.
func (m *Machine) ReadFile(ctx context.Context, a domain.Actor, path string) (FileContent, error) {
	area, err := domain.AreaFor(a, path)
	if err != nil {
		return FileContent{}, err
	}
	data, size, err := m.d.Files.Read(ctx, area, domain.MaxFileBytes)
	if err != nil {
		return FileContent{}, fmt.Errorf("read %s: %w", area.Path, err)
	}
	return FileContent{Path: area.Path, Data: data, Size: size, Truncated: size > int64(len(data))}, nil
}

// WriteFile writes a whole file as the actor's Linux user. A file outside the
// volumes joins the machine recipe so it survives a rescue.
func (m *Machine) WriteFile(ctx context.Context, a domain.Actor, req WriteRequest) (domain.FileEntry, error) {
	area, err := domain.AreaFor(a, req.Path)
	if err != nil {
		return domain.FileEntry{}, err
	}
	if err := domain.CheckFileSize(int64(len(req.Data))); err != nil {
		return domain.FileEntry{}, err
	}
	mode := defaultFileMode
	if req.Mode != nil {
		mode = *req.Mode
	}
	uid := a.RunAsUID()
	w := FileWrite{Data: req.Data, Mode: mode, UID: uid, GID: uid, CreateParents: req.CreateParents}
	entry, err := m.d.Files.Write(ctx, area, w)
	if err != nil {
		return domain.FileEntry{}, fmt.Errorf("write %s: %w", area.Path, err)
	}
	if domain.RecipeWorthy(area.Path) {
		if err := m.recordFile(ctx, area.Path, w); err != nil {
			return domain.FileEntry{}, err
		}
	}
	return entry, m.audit(ctx, a, "machine.file.write", area.Path, nil)
}

func (m *Machine) recordFile(ctx context.Context, path string, w FileWrite) error {
	hash, err := m.d.Recipes.PutBlob(ctx, w.Data)
	if err != nil {
		return fmt.Errorf("keep %s in the recipe: %w", path, err)
	}
	rec := domain.FileRecord{Path: path, Mode: w.Mode, UID: w.UID, GID: w.GID, Hash: hash}
	return m.changeRecipe(ctx, func(r *domain.Recipe) { r.RecordFile(rec) })
}

// DeleteFile deletes a file, or a folder when recursive, and drops it from
// the recipe.
func (m *Machine) DeleteFile(ctx context.Context, a domain.Actor, path string, recursive bool) error {
	area, err := domain.AreaFor(a, path)
	if err != nil {
		return err
	}
	if err := m.d.Files.Delete(ctx, area, recursive); err != nil {
		return fmt.Errorf("delete %s: %w", area.Path, err)
	}
	if domain.RecipeWorthy(area.Path) {
		if err := m.changeRecipe(ctx, func(r *domain.Recipe) { r.ForgetPath(area.Path) }); err != nil {
			return err
		}
	}
	return m.audit(ctx, a, "machine.file.delete", area.Path, map[string]any{"recursive": recursive})
}
