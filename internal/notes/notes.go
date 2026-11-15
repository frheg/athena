// Package notes is a small, general service for notes and reminders. Items belong to a tenant (the calling
// service, from its token) and a subject (the user, from X-Athena-Subject). A reminder is delivered by whoever
// asks for due reminders (Athena's core does, every minute), so this service needs no way out of its own.
package notes

import (
	"context"
	"crypto/rand"
	"embed"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Migrations holds the schema.
//
//go:embed migrations/*.sql
var Migrations embed.FS

// ErrNotFound: no such note or reminder for this tenant and subject.
var ErrNotFound = errors.New("not found")

// Note is one note.
type Note struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Body      string    `json:"body,omitempty"`
	Tags      []string  `json:"tags"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Reminder is one reminder.
type Reminder struct {
	ID        string     `json:"id"`
	Subject   string     `json:"subject,omitempty"`
	Text      string     `json:"text"`
	DueAt     time.Time  `json:"due_at"`
	Delivered *time.Time `json:"delivered_at,omitempty"`
}

// Service does the work.
type Service struct {
	DB   *pgxpool.Pool
	Zone *time.Location // for reading times like "2026-11-16 08:00"
	Now  func() time.Time
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func newID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func notFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

func cleanTags(tags []string) []string {
	out := []string{}
	for _, t := range tags {
		t = strings.ToLower(strings.TrimSpace(t))
		if t != "" && len(out) < 10 {
			out = append(out, t)
		}
	}
	return out
}

const noteCols = `id, title, body, tags, created_at, updated_at`

func scanNote(r pgx.Row) (Note, error) {
	var n Note
	err := r.Scan(&n.ID, &n.Title, &n.Body, &n.Tags, &n.CreatedAt, &n.UpdatedAt)
	return n, notFound(err)
}

func check(title, body string) error {
	if strings.TrimSpace(title) == "" {
		return errors.New("a note needs a title")
	}
	if len(title) > 200 || len(body) > 20000 {
		return errors.New("the title is at most 200 characters and the body at most 20000")
	}
	return nil
}

// Add stores a note.
func (s *Service) Add(ctx context.Context, tenant, subject, title, body string, tags []string) (Note, error) {
	if err := check(title, body); err != nil {
		return Note{}, err
	}
	return scanNote(s.DB.QueryRow(ctx, `INSERT INTO notes (id, tenant, subject, title, body, tags) VALUES ($1,$2,$3,$4,$5,$6) RETURNING `+noteCols,
		newID(), tenant, subject, strings.TrimSpace(title), body, cleanTags(tags)))
}

// Update changes a note; empty fields keep their value.
func (s *Service) Update(ctx context.Context, tenant, subject, id, title, body string, tags []string) (Note, error) {
	old, err := s.Get(ctx, tenant, subject, id)
	if err != nil {
		return old, err
	}
	if title == "" {
		title = old.Title
	}
	if body == "" {
		body = old.Body
	}
	if tags == nil {
		tags = old.Tags
	}
	if err := check(title, body); err != nil {
		return Note{}, err
	}
	return scanNote(s.DB.QueryRow(ctx, `UPDATE notes SET title = $4, body = $5, tags = $6, updated_at = now()
		WHERE id = $1 AND tenant = $2 AND subject = $3 RETURNING `+noteCols, id, tenant, subject, title, body, cleanTags(tags)))
}

// Get returns one note.
func (s *Service) Get(ctx context.Context, tenant, subject, id string) (Note, error) {
	return scanNote(s.DB.QueryRow(ctx, `SELECT `+noteCols+` FROM notes WHERE id::text = $1 AND tenant = $2 AND subject = $3`, id, tenant, subject))
}

// Delete removes a note.
func (s *Service) Delete(ctx context.Context, tenant, subject, id string) error {
	tag, err := s.DB.Exec(ctx, `DELETE FROM notes WHERE id::text = $1 AND tenant = $2 AND subject = $3`, id, tenant, subject)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

var wordRe = regexp.MustCompile(`[\p{L}\p{N}]+`)

// Search finds notes by words in the title or body (any word), or lists the newest when q is empty.
func (s *Service) Search(ctx context.Context, tenant, subject, q, tag string, limit int) ([]Note, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	terms := []string{}
	for _, w := range wordRe.FindAllString(strings.ToLower(q), 20) {
		terms = append(terms, w+":*")
	}
	rows, err := s.DB.Query(ctx, `SELECT `+noteCols+` FROM notes WHERE tenant = $1 AND subject = $2
		AND ($3 = '' OR to_tsvector('simple', title || ' ' || body) @@ to_tsquery('simple', $3))
		AND ($4 = '' OR $4 = ANY(tags)) ORDER BY updated_at DESC LIMIT $5`, tenant, subject, strings.Join(terms, " | "), strings.ToLower(tag), limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Note, error) { return scanNote(r) })
}

// ParseWhen reads a time: RFC 3339, "2026-11-16 08:00" or "2026-11-16" (08:00) in the service's zone, or a delay
// like "in 2h", "45m", "1h30m".
func (s *Service) ParseWhen(v string) (time.Time, error) {
	v = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(v), "in "))
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		return t, nil
	}
	zone := s.Zone
	if zone == nil {
		zone = time.UTC
	}
	for _, f := range []string{"2006-01-02 15:04", "2006-01-02T15:04", "2006-01-02 15:04:05"} {
		if t, err := time.ParseInLocation(f, v, zone); err == nil {
			return t, nil
		}
	}
	if t, err := time.ParseInLocation("2006-01-02", v, zone); err == nil {
		return t.Add(8 * time.Hour), nil
	}
	if d, err := time.ParseDuration(v); err == nil && d > 0 {
		return s.now().Add(d), nil
	}
	return time.Time{}, fmt.Errorf("cannot read the time %q: use 2026-11-16 08:00 (%s), RFC 3339, or a delay like 2h", v, zone)
}

// Remind stores a reminder.
func (s *Service) Remind(ctx context.Context, tenant, subject, text, when string) (Reminder, error) {
	text = strings.TrimSpace(text)
	if text == "" || len(text) > 1000 {
		return Reminder{}, errors.New("a reminder needs a text of at most 1000 characters")
	}
	due, err := s.ParseWhen(when)
	if err != nil {
		return Reminder{}, err
	}
	if due.Before(s.now().Add(-time.Minute)) {
		return Reminder{}, fmt.Errorf("%s is in the past", due.In(s.zone()).Format("2006-01-02 15:04"))
	}
	if due.After(s.now().AddDate(2, 0, 0)) {
		return Reminder{}, errors.New("a reminder can be at most two years ahead")
	}
	r := Reminder{ID: newID(), Text: text, DueAt: due}
	_, err = s.DB.Exec(ctx, `INSERT INTO reminders (id, tenant, subject, text, due_at) VALUES ($1,$2,$3,$4,$5)`, r.ID, tenant, subject, text, due)
	return r, err
}

func (s *Service) zone() *time.Location {
	if s.Zone == nil {
		return time.UTC
	}
	return s.Zone
}

// Reminders lists coming reminders (and, with past, delivered ones too).
func (s *Service) Reminders(ctx context.Context, tenant, subject string, past bool) ([]Reminder, error) {
	rows, err := s.DB.Query(ctx, `SELECT id, text, due_at, delivered_at FROM reminders WHERE tenant = $1 AND subject = $2 AND NOT cancelled
		AND ($3 OR delivered_at IS NULL) ORDER BY due_at LIMIT 100`, tenant, subject, past)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Reminder, error) {
		var x Reminder
		err := r.Scan(&x.ID, &x.Text, &x.DueAt, &x.Delivered)
		return x, err
	})
}

// Cancel drops a coming reminder.
func (s *Service) Cancel(ctx context.Context, tenant, subject, id string) error {
	tag, err := s.DB.Exec(ctx, `UPDATE reminders SET cancelled = true WHERE id::text = $1 AND tenant = $2 AND subject = $3 AND delivered_at IS NULL`, id, tenant, subject)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// Due returns the tenant's reminders that are due and marks them delivered, so each is handed out once.
func (s *Service) Due(ctx context.Context, tenant string) ([]Reminder, error) {
	rows, err := s.DB.Query(ctx, `UPDATE reminders SET delivered_at = now() WHERE id IN (
		SELECT id FROM reminders WHERE tenant = $1 AND delivered_at IS NULL AND NOT cancelled AND due_at <= $2
		ORDER BY due_at LIMIT 100 FOR UPDATE SKIP LOCKED) RETURNING id, subject, text, due_at, delivered_at`, tenant, s.now())
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Reminder, error) {
		var x Reminder
		err := r.Scan(&x.ID, &x.Subject, &x.Text, &x.DueAt, &x.Delivered)
		return x, err
	})
}
