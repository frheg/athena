// Package files is a small, general file store for assistants: what a user attaches to a conversation. Files belong
// to a tenant (the calling service, from its token) and a subject (the user, from X-Athena-Subject). At upload the
// readable text is taken out (text, code, PDF, Word, PowerPoint, Excel, OpenDocument text) and an image gets a
// downscaled JPEG preview, so readers never parse the original again.
package files

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Migrations holds the schema.
//
//go:embed migrations/*.sql
var Migrations embed.FS

// ErrNotFound: no such file for this tenant and subject.
var ErrNotFound = errors.New("not found")

// File is what is known about one file (never its bytes).
type File struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Mime      string    `json:"mime"`
	Kind      string    `json:"kind"` // image, text, document, binary
	Size      int64     `json:"size"`
	SHA256    string    `json:"sha256"`
	Chars     int       `json:"chars,omitempty"`
	Pages     int       `json:"pages,omitempty"`
	Width     int       `json:"width,omitempty"`
	Height    int       `json:"height,omitempty"`
	Preview   bool      `json:"preview,omitempty"`
	Note      string    `json:"note,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// Service does the work.
type Service struct {
	DB       *pgxpool.Pool
	MaxBytes int64 // largest file, default 20 MB
	Quota    int64 // bytes per subject, default 2 GB; 0 = the default
}

func (s *Service) maxBytes() int64 {
	if s.MaxBytes > 0 {
		return s.MaxBytes
	}
	return 20 << 20
}

func (s *Service) quota() int64 {
	if s.Quota > 0 {
		return s.Quota
	}
	return 2 << 30
}

func newID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// cleanName keeps a file name readable and harmless: no path, no control characters, at most 200 characters.
func cleanName(name string) string {
	name = strings.ReplaceAll(name, "\\", "/")
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	name = strings.Map(func(r rune) rune {
		if r < 32 || r == 127 || r == '"' {
			return -1
		}
		return r
	}, strings.ToValidUTF8(name, ""))
	name = strings.TrimSpace(name)
	if r := []rune(name); len(r) > 200 {
		name = string(r[:200])
	}
	if name == "" || name == "." || name == ".." {
		name = "file"
	}
	return name
}

const columns = `id, name, mime, kind, size, sha256, chars, pages, width, height, preview IS NOT NULL, note, created_at`

func scan(r pgx.Row) (File, error) {
	var f File
	err := r.Scan(&f.ID, &f.Name, &f.Mime, &f.Kind, &f.Size, &f.SHA256, &f.Chars, &f.Pages, &f.Width, &f.Height, &f.Preview, &f.Note, &f.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return f, ErrNotFound
	}
	return f, err
}

// Put stores a file and what can be read from it.
func (s *Service) Put(ctx context.Context, tenant, subject, name string, data []byte) (File, error) {
	if len(data) == 0 {
		return File{}, errors.New("the file is empty")
	}
	if int64(len(data)) > s.maxBytes() {
		return File{}, fmt.Errorf("the file is larger than %d MB", s.maxBytes()>>20)
	}
	var used int64
	if err := s.DB.QueryRow(ctx, `SELECT COALESCE(sum(size), 0) FROM files WHERE tenant = $1 AND subject = $2`, tenant, subject).Scan(&used); err != nil {
		return File{}, err
	}
	if used+int64(len(data)) > s.quota() {
		return File{}, fmt.Errorf("the file store is full (%d of %d MB used): delete some files first", used>>20, s.quota()>>20)
	}
	name = cleanName(name)
	x := Extract(name, data)
	sum := sha256.Sum256(data)
	f := File{ID: newID(), Name: name, Mime: x.Mime, Kind: x.Kind, Size: int64(len(data)), SHA256: hex.EncodeToString(sum[:]),
		Chars: utf8.RuneCountInString(x.Text), Pages: x.Pages, Width: x.Width, Height: x.Height, Preview: x.Preview != nil, Note: x.Note}
	var text any
	if x.Text != "" {
		text = x.Text
	}
	err := s.DB.QueryRow(ctx, `INSERT INTO files (id, tenant, subject, name, mime, kind, size, sha256, data, preview, text, chars, pages, width, height, note)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16) RETURNING created_at`,
		f.ID, tenant, subject, f.Name, f.Mime, f.Kind, f.Size, f.SHA256, data, x.Preview, text, f.Chars, f.Pages, f.Width, f.Height, f.Note).Scan(&f.CreatedAt)
	return f, err
}

// Get returns what is known about one file.
func (s *Service) Get(ctx context.Context, tenant, subject, id string) (File, error) {
	if !validID(id) {
		return File{}, ErrNotFound
	}
	return scan(s.DB.QueryRow(ctx, `SELECT `+columns+` FROM files WHERE tenant = $1 AND subject = $2 AND id = $3`, tenant, subject, id))
}

// List returns the newest files first.
func (s *Service) List(ctx context.Context, tenant, subject string, limit int) ([]File, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.DB.Query(ctx, `SELECT `+columns+` FROM files WHERE tenant = $1 AND subject = $2 ORDER BY created_at DESC LIMIT $3`, tenant, subject, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (File, error) { return scan(r) })
}

// Usage is how much a subject stores.
type Usage struct {
	Files int   `json:"files"`
	Bytes int64 `json:"bytes"`
	Quota int64 `json:"quota"`
}

// Usage returns the number and size of a subject's files.
func (s *Service) Usage(ctx context.Context, tenant, subject string) (Usage, error) {
	u := Usage{Quota: s.quota()}
	err := s.DB.QueryRow(ctx, `SELECT count(*), COALESCE(sum(size), 0) FROM files WHERE tenant = $1 AND subject = $2`, tenant, subject).Scan(&u.Files, &u.Bytes)
	return u, err
}

// Content returns the stored bytes: the original, or the preview when preview is true.
func (s *Service) Content(ctx context.Context, tenant, subject, id string, preview bool) (File, []byte, error) {
	f, err := s.Get(ctx, tenant, subject, id)
	if err != nil {
		return f, nil, err
	}
	col := "data"
	if preview {
		if !f.Preview {
			return f, nil, ErrNotFound
		}
		col = "preview"
	}
	var b []byte
	err = s.DB.QueryRow(ctx, `SELECT `+col+` FROM files WHERE tenant = $1 AND subject = $2 AND id = $3`, tenant, subject, id).Scan(&b)
	if errors.Is(err, pgx.ErrNoRows) {
		return f, nil, ErrNotFound
	}
	return f, b, err
}

// Text returns a file's readable text ("" when it has none).
func (s *Service) Text(ctx context.Context, tenant, subject, id string) (File, string, error) {
	f, err := s.Get(ctx, tenant, subject, id)
	if err != nil {
		return f, "", err
	}
	var t *string
	if err := s.DB.QueryRow(ctx, `SELECT text FROM files WHERE id = $1`, id).Scan(&t); err != nil {
		return f, "", err
	}
	if t == nil {
		return f, "", nil
	}
	return f, *t, nil
}

// Delete removes a file.
func (s *Service) Delete(ctx context.Context, tenant, subject, id string) error {
	if !validID(id) {
		return ErrNotFound
	}
	tag, err := s.DB.Exec(ctx, `DELETE FROM files WHERE tenant = $1 AND subject = $2 AND id = $3`, tenant, subject, id)
	if err == nil && tag.RowsAffected() == 0 {
		err = ErrNotFound
	}
	return err
}

func validID(id string) bool {
	if len(id) != 36 {
		return false
	}
	for i, c := range id {
		switch {
		case i == 8 || i == 13 || i == 18 || i == 23:
			if c != '-' {
				return false
			}
		case c >= '0' && c <= '9', c >= 'a' && c <= 'f':
		default:
			return false
		}
	}
	return true
}

// Read returns length characters of a file's text from offset (in characters), and the total length.
func Read(text string, offset, length int) (string, int) {
	r := []rune(text)
	if offset < 0 {
		offset = 0
	}
	if length <= 0 {
		length = 20000
	}
	if offset >= len(r) {
		return "", len(r)
	}
	end := min(len(r), offset+length)
	return string(r[offset:end]), len(r)
}

// Hit is one place where a search matched.
type Hit struct {
	Offset  int    `json:"offset"`
	Snippet string `json:"snippet"`
}

// Find returns up to limit places in text where all the words of query occur close together (within about 300
// characters of the first word), case-insensitive, each with the character offset to read from.
func Find(text, query string, limit int) []Hit {
	words := strings.Fields(strings.ToLower(query))
	if len(words) == 0 {
		return nil
	}
	if limit <= 0 {
		limit = 10
	}
	ls := strings.ToLower(text)
	src := text
	if len(ls) != len(text) { // lower-casing changed byte lengths (rare scripts): show the lower-cased text
		src = ls
	}
	var hits []Hit
	for from := 0; len(hits) < limit; {
		i := strings.Index(ls[from:], words[0])
		if i < 0 {
			break
		}
		b := from + i
		next := b + len(words[0])
		win := ls[clamp(ls, b-300):clamp(ls, b+300)]
		all := true
		for _, w := range words[1:] {
			if !strings.Contains(win, w) {
				all = false
				break
			}
		}
		if all {
			lo, hi := clamp(src, b-150), clamp(src, b+250)
			hits = append(hits, Hit{Offset: utf8.RuneCountInString(src[:lo]), Snippet: strings.Join(strings.Fields(src[lo:hi]), " ")})
			next = max(next, hi) // snippets do not overlap
		}
		from = next
	}
	return hits
}

// clamp puts a byte offset inside s, on the start of a character.
func clamp(s string, b int) int {
	b = min(max(b, 0), len(s))
	for b > 0 && b < len(s) && !utf8.RuneStart(s[b]) {
		b--
	}
	return b
}
