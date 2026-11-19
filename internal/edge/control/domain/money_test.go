// SPDX-License-Identifier: MIT

package domain_test

import (
	"errors"
	"testing"

	"github.com/SDK-E/agentsone/internal/edge/control/domain"
)

func TestParseDollars(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in      string
		want    int64
		wantErr error
	}{
		{"25", 25_000_000, nil},
		{"$25", 25_000_000, nil},
		{" 25.5 ", 25_500_000, nil},
		{"0", 0, nil},
		{"0.000001", 1, nil},
		{"1,250.75", 1_250_750_000, nil},
		{"1000000000", 1_000_000_000_000_000, nil},
		{"1000000000.01", 0, domain.ErrAmountTooLarge},
		{"0.0000001", 0, domain.ErrAmount},
		{"-1", 0, domain.ErrAmount},
		{"", 0, domain.ErrAmount},
		{"ten", 0, domain.ErrAmount},
		{"1.2.3", 0, domain.ErrAmount},
		{".5", 0, domain.ErrAmount},
		{"5.", 0, domain.ErrAmount},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			t.Parallel()
			got, err := domain.ParseDollars(tt.in)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("ParseDollars(%q) error = %v, want %v", tt.in, err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("ParseDollars(%q) = %d, want %d", tt.in, got, tt.want)
			}
		})
	}
}

func TestFormatDollars(t *testing.T) {
	t.Parallel()
	tests := []struct {
		micros int64
		want   string
	}{
		{0, "$0.00"},
		{25_500_000, "$25.50"},
		{1_250_750_000, "$1,250.75"},
		{1_234_567_890_000, "$1,234,567.89"},
		{1, "$0.000001"},
		{12_340, "$0.01234"},
		{-2_500_000, "-$2.50"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			t.Parallel()
			if got := domain.FormatDollars(tt.micros); got != tt.want {
				t.Fatalf("FormatDollars(%d) = %q, want %q", tt.micros, got, tt.want)
			}
		})
	}
}

func TestParseShare(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in   string
		want int
		ok   bool
	}{
		{"0", 0, true},
		{"10", 10, true},
		{" 100 ", 100, true},
		{"10%", 10, true},
		{"101", 0, false},
		{"-1", 0, false},
		{"5.5", 0, false},
		{"", 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			t.Parallel()
			got, err := domain.ParseShare(tt.in)
			if (err == nil) != tt.ok || got != tt.want {
				t.Fatalf("ParseShare(%q) = %d, %v; want %d, ok %v", tt.in, got, err, tt.want, tt.ok)
			}
			if err != nil && !errors.Is(err, domain.ErrShare) {
				t.Fatalf("ParseShare(%q) error = %v, want %v", tt.in, err, domain.ErrShare)
			}
		})
	}
}

func TestParsePeriod(t *testing.T) {
	t.Parallel()
	for _, ok := range []string{"day", "week", "month"} {
		if got, err := domain.ParsePeriod(ok); err != nil || string(got) != ok {
			t.Fatalf("ParsePeriod(%q) = %q, %v", ok, got, err)
		}
	}
	for _, bad := range []string{"", "year", "Month"} {
		if _, err := domain.ParsePeriod(bad); !errors.Is(err, domain.ErrPeriod) {
			t.Fatalf("ParsePeriod(%q) error = %v, want %v", bad, err, domain.ErrPeriod)
		}
	}
}
