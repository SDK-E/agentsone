// SPDX-License-Identifier: MIT

package main

import (
	"go/format"
	"strings"
	"testing"
)

const commonPath = "github.com/SDK-E/agentsone/internal/contract/common"

func TestUseCommonTimestampRewritesEveryTimeType(t *testing.T) {
	src := `package x

import "time"

type Alias = time.Time

type T struct {
	A *time.Time ` + "`json:\"a\"`" + `
	B time.Time  ` + "`json:\"b\"`" + `
}
`
	out, err := useCommonTimestamp([]byte(src), commonPath)
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	for _, want := range []string{"type Alias = common.Timestamp", "A *common.Timestamp", "B common.Timestamp", `"` + commonPath + `"`} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "time.Time") || strings.Contains(got, `"time"`) {
		t.Errorf("time left behind:\n%s", got)
	}
	if _, err := format.Source(out); err != nil {
		t.Errorf("not valid Go: %v", err)
	}
}

func TestUseCommonTimestampKeepsTimeImportWhenStillUsed(t *testing.T) {
	src := `package x

import "time"

type T struct {
	A time.Time
	D time.Duration
}
`
	out, err := useCommonTimestamp([]byte(src), commonPath)
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if !strings.Contains(got, `"time"`) || !strings.Contains(got, "time.Duration") {
		t.Errorf("time import dropped while still used:\n%s", got)
	}
}

func TestUseCommonTimestampReusesExistingCommonImport(t *testing.T) {
	src := `package x

import common "` + commonPath + `"
import "time"

type T struct {
	A common.Markdown
	B *time.Time
}
`
	out, err := useCommonTimestamp([]byte(src), commonPath)
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if strings.Count(got, commonPath) != 1 {
		t.Errorf("common imported twice:\n%s", got)
	}
	if !strings.Contains(got, "B *common.Timestamp") {
		t.Errorf("not rewritten:\n%s", got)
	}
}

func TestUseCommonTimestampLeavesFilesWithoutTimeAlone(t *testing.T) {
	src := "package x\n\ntype T struct{ A string }\n"
	out, err := useCommonTimestamp([]byte(src), commonPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != src {
		t.Errorf("changed:\n%s", out)
	}
}
