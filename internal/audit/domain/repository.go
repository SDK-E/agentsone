// SPDX-License-Identifier: MIT

package domain

import "context"

// Repository stores the audit chain. Append rejects a record that does not
// follow the last one with ErrOutOfOrder. Every implementation passes
// audittest.RunRepositoryContract.
type Repository interface {
	Append(ctx context.Context, r Record) error
	// Last returns the newest record, and false for an empty log.
	Last(ctx context.Context) (Record, bool, error)
	// List returns up to limit records with Seq > afterSeq, oldest first.
	List(ctx context.Context, afterSeq int64, limit int) ([]Record, error)
}
