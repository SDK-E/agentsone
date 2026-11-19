// SPDX-License-Identifier: MIT

package sqlite

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"strings"
	"time"

	"github.com/SDK-E/agentsone/internal/bus/domain"
	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/store"
)

// migrations must be a package variable: go:embed only applies to them.
//
//go:embed migrations/*.sql
var migrations embed.FS

const moduleName = "bus"

// Migrate brings the bus tables in bus.db up to date.
func Migrate(ctx context.Context, db *store.DB) error {
	ms, err := store.MigrationsFromFS(migrations, "migrations")
	if err != nil {
		return fmt.Errorf("load bus migrations: %w", err)
	}
	if err := db.Migrate(ctx, moduleName, ms); err != nil {
		return fmt.Errorf("migrate bus: %w", err)
	}
	return nil
}

// timeLayout is fixed width, so stored times sort as text in time order.
const timeLayout = "2006-01-02T15:04:05.000000000Z07:00"

func formatTime(t time.Time) string { return t.UTC().Format(timeLayout) }

func parseTime(s string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("stored time %q: %w", s, err)
	}
	return t.UTC(), nil
}

func isUnique(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}

// nullIfEmpty stores "" as NULL.
func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// Channels stores channels in bus.db.
type Channels struct{ db *store.DB }

var _ domain.ChannelRepository = (*Channels)(nil)

// NewChannels returns the channel repository on a migrated database.
func NewChannels(db *store.DB) *Channels { return &Channels{db: db} }

const channelColumns = `name, kind, description, team, created_at, created_by, last_message_at`

// Create stores c.
func (r *Channels) Create(ctx context.Context, c domain.Channel) error {
	var last any
	if c.LastMessageAt != nil {
		last = formatTime(*c.LastMessageAt)
	}
	_, err := r.db.SQL().ExecContext(ctx, `INSERT INTO bus_channels (`+channelColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		c.Name, string(c.Kind), c.Description, c.Team, formatTime(c.CreatedAt), c.CreatedBy, last)
	if isUnique(err) {
		return fmt.Errorf("channel %q: %w", c.Name, domain.ErrNameTaken)
	}
	if err != nil {
		return fmt.Errorf("create channel: %w", err)
	}
	return nil
}

// Get returns the channel called name.
func (r *Channels) Get(ctx context.Context, name string) (domain.Channel, error) {
	rows, err := r.db.SQL().QueryContext(ctx, `SELECT `+channelColumns+` FROM bus_channels WHERE name = ?`, name)
	if err != nil {
		return domain.Channel{}, fmt.Errorf("get channel: %w", err)
	}
	list, err := scanChannels(rows)
	if err != nil {
		return domain.Channel{}, err
	}
	if len(list) == 0 {
		return domain.Channel{}, fmt.Errorf("channel %q: %w", name, core.ErrNotFound)
	}
	return list[0], nil
}

// List returns up to limit channels after afterName, in name order.
func (r *Channels) List(ctx context.Context, afterName string, limit int) ([]domain.Channel, error) {
	rows, err := r.db.SQL().QueryContext(ctx,
		`SELECT `+channelColumns+` FROM bus_channels WHERE name > ? ORDER BY name LIMIT ?`, afterName, limit)
	if err != nil {
		return nil, fmt.Errorf("list channels: %w", err)
	}
	return scanChannels(rows)
}

// Touch records the time of the newest message.
func (r *Channels) Touch(ctx context.Context, name string, at time.Time) error {
	res, err := r.db.SQL().ExecContext(ctx, `UPDATE bus_channels SET last_message_at = ? WHERE name = ?`, formatTime(at), name)
	if err != nil {
		return fmt.Errorf("touch channel: %w", err)
	}
	if n, err := res.RowsAffected(); err != nil || n == 0 {
		return fmt.Errorf("channel %q: %w", name, core.ErrNotFound)
	}
	return nil
}

func scanChannels(rows *sql.Rows) ([]domain.Channel, error) {
	defer func() { _ = rows.Close() }()
	var out []domain.Channel
	for rows.Next() {
		var (
			c             domain.Channel
			kind, created string
			last          sql.NullString
		)
		if err := rows.Scan(&c.Name, &kind, &c.Description, &c.Team, &created, &c.CreatedBy, &last); err != nil {
			return nil, fmt.Errorf("scan channel: %w", err)
		}
		var err error
		if c.CreatedAt, err = parseTime(created); err != nil {
			return nil, err
		}
		if last.Valid {
			at, err := parseTime(last.String)
			if err != nil {
				return nil, err
			}
			c.LastMessageAt = &at
		}
		c.Kind = domain.ChannelKind(kind)
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list channels: %w", err)
	}
	return out, nil
}

// Messages stores the message log in bus.db.
type Messages struct{ db *store.DB }

var _ domain.MessageRepository = (*Messages)(nil)

// NewMessages returns the message repository on a migrated database.
func NewMessages(db *store.DB) *Messages { return &Messages{db: db} }

const messageColumns = `seq, id, channel, recipient, sender, body, reply_to, correlation_id, created_at`

// Append stores m and returns it with its sequence.
func (r *Messages) Append(ctx context.Context, m domain.Message) (domain.Message, error) {
	res, err := r.db.SQL().ExecContext(ctx,
		`INSERT INTO bus_messages (id, channel, recipient, sender, body, reply_to, correlation_id, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		m.ID, nullIfEmpty(m.Channel), nullIfEmpty(m.To), m.From, m.Text, nullIfEmpty(m.ReplyTo), m.CorrelationID,
		formatTime(m.CreatedAt))
	if isUnique(err) {
		return domain.Message{}, fmt.Errorf("message %s: %w", m.ID, core.ErrConflict)
	}
	if err != nil {
		return domain.Message{}, fmt.Errorf("append message: %w", err)
	}
	seq, err := res.LastInsertId()
	if err != nil {
		return domain.Message{}, fmt.Errorf("append message: %w", err)
	}
	m.Sequence = uint64(seq) // #nosec G115 -- rowids are positive
	return m, nil
}

// Get returns the message with id.
func (r *Messages) Get(ctx context.Context, id string) (domain.Message, error) {
	rows, err := r.db.SQL().QueryContext(ctx, `SELECT `+messageColumns+` FROM bus_messages WHERE id = ?`, id)
	if err != nil {
		return domain.Message{}, fmt.Errorf("get message: %w", err)
	}
	list, err := scanMessages(rows)
	if err != nil {
		return domain.Message{}, err
	}
	if len(list) == 0 {
		return domain.Message{}, fmt.Errorf("message %s: %w", id, core.ErrNotFound)
	}
	return list[0], nil
}

// Before returns up to limit matching messages below sequence before, newest first.
func (r *Messages) Before(ctx context.Context, f domain.MessageFilter, before uint64, limit int) ([]domain.Message, error) {
	var (
		where []string
		args  []any
	)
	add := func(cond string, arg any) {
		where = append(where, cond)
		args = append(args, arg)
	}
	if before != 0 {
		add("seq < ?", int64(before)) // #nosec G115 -- sequences stay far below 2^63
	}
	if f.Channel != "" {
		add("channel = ?", f.Channel)
	}
	if f.To != "" {
		add("recipient = ?", f.To)
	}
	if f.From != "" {
		add("sender = ?", f.From)
	}
	if f.Since != nil {
		add("created_at >= ?", formatTime(*f.Since))
	}
	query := `SELECT ` + messageColumns + ` FROM bus_messages`
	if len(where) > 0 {
		query += ` WHERE ` + strings.Join(where, " AND ")
	}
	rows, err := r.db.SQL().QueryContext(ctx, query+` ORDER BY seq DESC LIMIT ?`, append(args, limit)...)
	if err != nil {
		return nil, fmt.Errorf("list messages: %w", err)
	}
	return scanMessages(rows)
}

// After returns up to limit messages after sequence after, oldest first.
func (r *Messages) After(ctx context.Context, after uint64, limit int) ([]domain.Message, error) {
	rows, err := r.db.SQL().QueryContext(ctx,
		`SELECT `+messageColumns+` FROM bus_messages WHERE seq > ? ORDER BY seq LIMIT ?`,
		int64(after), limit) // #nosec G115 -- sequences stay far below 2^63
	if err != nil {
		return nil, fmt.Errorf("list messages: %w", err)
	}
	return scanMessages(rows)
}

// Last returns the largest stored sequence.
func (r *Messages) Last(ctx context.Context) (uint64, error) {
	var seq sql.NullInt64
	if err := r.db.SQL().QueryRowContext(ctx, `SELECT MAX(seq) FROM bus_messages`).Scan(&seq); err != nil {
		return 0, fmt.Errorf("last message: %w", err)
	}
	return uint64(seq.Int64), nil // #nosec G115 -- rowids are positive
}

func scanMessages(rows *sql.Rows) ([]domain.Message, error) {
	defer func() { _ = rows.Close() }()
	var out []domain.Message
	for rows.Next() {
		var (
			m                           domain.Message
			seq                         int64
			channel, recipient, replyTo sql.NullString
			created                     string
		)
		if err := rows.Scan(&seq, &m.ID, &channel, &recipient, &m.From, &m.Text, &replyTo, &m.CorrelationID, &created); err != nil {
			return nil, fmt.Errorf("scan message: %w", err)
		}
		at, err := parseTime(created)
		if err != nil {
			return nil, err
		}
		m.Sequence = uint64(seq) // #nosec G115 -- rowids are positive
		m.Channel, m.To, m.ReplyTo, m.CreatedAt = channel.String, recipient.String, replyTo.String, at
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list messages: %w", err)
	}
	return out, nil
}
