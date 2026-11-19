// SPDX-License-Identifier: MIT

package core_test

import (
	"testing"

	"github.com/SDK-E/agentsone/internal/core"
)

type pluginsSection struct {
	Sources []string `toml:"sources"`
	Limit   int      `toml:"limit"`
}

func TestSectionDecodesAModuleTable(t *testing.T) {
	c, err := core.ParseConfig([]byte("[api]\naddr = \"127.0.0.1:1\"\n[plugins]\nsources = [\"claude\", \"skills\"]\n"))
	if err != nil {
		t.Fatal(err)
	}
	got := pluginsSection{Limit: 5}
	if err := c.Section("plugins", &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Sources) != 2 || got.Sources[1] != "skills" || got.Limit != 5 {
		t.Fatalf("section %+v", got)
	}
	if c.API.Addr != "127.0.0.1:1" {
		t.Fatalf("core field lost: %+v", c.API)
	}
}

func TestSectionKeepsDefaultsWhenAbsent(t *testing.T) {
	c := core.DefaultConfig()
	got := pluginsSection{Limit: 5}
	if err := c.Section("plugins", &got); err != nil || got.Limit != 5 {
		t.Fatalf("got %+v, %v", got, err)
	}
}

func TestSectionRejectsUnknownKeys(t *testing.T) {
	c, err := core.ParseConfig([]byte("[plugins]\nsorces = []\n"))
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Section("plugins", &pluginsSection{}); err == nil {
		t.Fatal("typo accepted")
	}
}

func TestParseConfigRejectsUnknownTopLevelKeys(t *testing.T) {
	if _, err := core.ParseConfig([]byte("typo = 1\n")); err == nil {
		t.Fatal("unknown top-level key accepted")
	}
	if _, err := core.ParseConfig([]byte("[api]\nadr = \"x\"\n")); err == nil {
		t.Fatal("unknown core key accepted")
	}
}
