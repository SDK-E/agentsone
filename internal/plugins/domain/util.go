// SPDX-License-Identifier: MIT

package domain

import "fmt"

func sprintf(format string, a ...any) string { return fmt.Sprintf(format, a...) }

func errNotSemantic(v string) error {
	return fmt.Errorf("version %q: %w", v, ErrNotSemantic)
}
