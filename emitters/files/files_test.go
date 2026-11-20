// SPDX-License-Identifier: MIT

package files_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/emitters/files"
	"github.com/SDK-E/agentsone/internal/emitkit/domain"
	"github.com/SDK-E/agentsone/internal/emitkit/emitkittest"
)

// tree is a fake file system: each scan sees the snapshot the test set for it.
type tree struct {
	scans []map[string]files.Entry // scans[i] is what scan i sees, for the watch "/w"
	err   map[int]error            // scan index to error
	n     int
}

func (tr *tree) Walk(_ string, _ int) (map[string]files.Entry, error) {
	i := tr.n
	tr.n++
	if err := tr.err[i]; err != nil {
		return nil, err
	}
	if i >= len(tr.scans) {
		i = len(tr.scans) - 1
	}
	return tr.scans[i], nil
}

func e(size int64, minute int) files.Entry {
	return files.Entry{Size: size, ModTime: time.Date(2026, 11, 20, 0, minute, 0, 0, time.UTC)}
}

type snap = map[string]files.Entry

func cfg() files.Config {
	c := files.DefaultConfig()
	c.Watches = []string{"/w"}
	return c
}

// run scans once per entry of tr.scans and returns what was published.
func run(t *testing.T, tr *tree, c files.Config) []domain.Draft {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	clk := emitkittest.NewClock(time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC))
	sl := &emitkittest.Sleeper{Clock: clk, Cancel: cancel, Limit: len(tr.scans)}
	pub := &emitkittest.Publisher{}
	if err := files.New(c, tr).Run(ctx, emitkittest.Deps(clk, sl, pub)); err != nil {
		t.Fatalf("Run error = %v", err)
	}
	return pub.Drafts()
}

func summary(drafts []domain.Draft) string {
	var out []string
	for _, d := range drafts {
		out = append(out, strings.TrimPrefix(d.Type, "agentsone.files.")+" "+d.Subject)
	}
	return strings.Join(out, ", ")
}

func TestRun_FirstScanIsTheBaselineAndSaysNothing(t *testing.T) {
	t.Parallel()
	tr := &tree{scans: []snap{{"/w/a": e(1, 1), "/w/b": e(2, 1)}}}
	if got := summary(run(t, tr, cfg())); got != "" {
		t.Fatalf("events = %q, want none for files that were already there", got)
	}
}

func TestRun_ANewFileIsCreatedOnceItHasSettled(t *testing.T) {
	t.Parallel()
	tr := &tree{scans: []snap{
		{},
		{"/w/new.csv": e(10, 2)}, // seen, still being written
		{"/w/new.csv": e(10, 2)}, // unchanged since the last scan: settled
		{"/w/new.csv": e(10, 2)},
	}}

	drafts := run(t, tr, cfg())

	if got := summary(drafts); got != "created /w/new.csv" {
		t.Fatalf("events = %q, want one created", got)
	}
	if want := `{"path":"/w/new.csv","size_bytes":10,"watch":"/w"}`; string(drafts[0].Data) != want {
		t.Fatalf("data = %s, want %s", drafts[0].Data, want)
	}
	emitkittest.RequireInCatalog(t, "files", drafts)
}

func TestRun_AFileStillGrowingIsNotReportedUntilItStops(t *testing.T) {
	t.Parallel()
	tr := &tree{scans: []snap{
		{},
		{"/w/big": e(10, 1)},
		{"/w/big": e(20, 2)},
		{"/w/big": e(30, 3)},
		{"/w/big": e(30, 3)},
	}}

	if got := summary(run(t, tr, cfg())); got != "created /w/big" {
		t.Fatalf("events = %q, want a single created after the file stopped changing", got)
	}
}

func TestRun_ModifiedWhenSizeOrTimeChangesAndThenSettles(t *testing.T) {
	t.Parallel()
	tr := &tree{scans: []snap{
		{"/w/a": e(1, 1)},
		{"/w/a": e(5, 2)},
		{"/w/a": e(5, 2)},
		{"/w/a": e(5, 3)}, // same size, new time
		{"/w/a": e(5, 3)},
	}}

	drafts := run(t, tr, cfg())

	if got := summary(drafts); got != "modified /w/a, modified /w/a" {
		t.Fatalf("events = %q", got)
	}
	emitkittest.RequireInCatalog(t, "files", drafts)
}

func TestRun_DeletedIsReportedAtOnceAndCarriesNoSize(t *testing.T) {
	t.Parallel()
	tr := &tree{scans: []snap{{"/w/a": e(1, 1)}, {}, {}}}

	drafts := run(t, tr, cfg())

	if got := summary(drafts); got != "deleted /w/a" {
		t.Fatalf("events = %q, want one deleted", got)
	}
	if want := `{"path":"/w/a","watch":"/w"}`; string(drafts[0].Data) != want {
		t.Fatalf("data = %s, want %s", drafts[0].Data, want)
	}
	emitkittest.RequireInCatalog(t, "files", drafts)
}

func TestRun_AFileCreatedAndRemovedBeforeItSettledSaysNothing(t *testing.T) {
	t.Parallel()
	tr := &tree{scans: []snap{{}, {"/w/tmp": e(1, 1)}, {}, {}}}
	if got := summary(run(t, tr, cfg())); got != "" {
		t.Fatalf("events = %q, want none for a file nobody was told about", got)
	}
}

func TestRun_AFailedScanKeepsWhatIsKnown(t *testing.T) {
	t.Parallel()
	tr := &tree{
		scans: []snap{{"/w/a": e(1, 1)}, {"/w/a": e(1, 1)}, {"/w/a": e(1, 1)}},
		err:   map[int]error{1: errors.New("walk /w: permission denied")},
	}
	if got := summary(run(t, tr, cfg())); got != "" {
		t.Fatalf("events = %q, want none: a failed scan is not a delete", got)
	}
}

func TestRun_EachWatchIsScannedOnItsOwn(t *testing.T) {
	t.Parallel()
	c := cfg()
	c.Watches = []string{"/w", "/x"}
	calls := map[string]int{}
	walker := walkFn(func(watch string, _ int) (map[string]files.Entry, error) {
		calls[watch]++
		if calls[watch] == 1 {
			return snap{}, nil
		}
		return snap{watch + "/f": e(1, 1)}, nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	clk := emitkittest.NewClock(time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC))
	sl := &emitkittest.Sleeper{Clock: clk, Cancel: cancel, Limit: 3}
	pub := &emitkittest.Publisher{}

	if err := files.New(c, walker).Run(ctx, emitkittest.Deps(clk, sl, pub)); err != nil {
		t.Fatalf("Run error = %v", err)
	}

	got := summary(pub.Drafts())
	if got != "created /w/f, created /x/f" {
		t.Fatalf("events = %q, want one created per watch", got)
	}
	emitkittest.RequireInCatalog(t, "files", pub.Drafts())
}

type walkFn func(string, int) (map[string]files.Entry, error)

func (f walkFn) Walk(watch string, limit int) (map[string]files.Entry, error) { return f(watch, limit) }

func TestRun_AFailedPublishIsNotFatalAndIsRetried(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	clk := emitkittest.NewClock(time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC))
	tr := &tree{scans: []snap{{}, {"/w/a": e(1, 1)}, {"/w/a": e(1, 1)}, {"/w/a": e(1, 1)}, {"/w/a": e(1, 1)}}}
	sl := &emitkittest.Sleeper{Clock: clk, Cancel: cancel, Limit: len(tr.scans)}
	pub := &emitkittest.Publisher{Fail: func(n int) error {
		if n == 1 {
			return errors.New("bus down")
		}
		return nil
	}}

	if err := files.New(cfg(), tr).Run(ctx, emitkittest.Deps(clk, sl, pub)); err != nil {
		t.Fatalf("Run error = %v", err)
	}

	if pub.Calls() < 2 {
		t.Fatalf("publish calls = %d, want the event retried after the failure", pub.Calls())
	}
	if got := summary(pub.Drafts()); got != "created /w/a" {
		t.Fatalf("published = %q, want the created event once it got through", got)
	}
}
