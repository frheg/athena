// Package memory is a small, general memory service: an assistant stores short items about a user (facts,
// preferences, people, places) and finds them again by meaning. Items live in Postgres; their vectors live in
// Qdrant and are made by an external embedding service. Without Qdrant or an embedder it still works, with plain
// text search. Every item belongs to a tenant (the calling service, known from its token) and a subject (the user).
package memory

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var wordRe = regexp.MustCompile(`[\p{L}\p{N}]+`)

// Kinds of items.
var Kinds = []string{"fact", "preference", "person", "place", "event", "other"}

// Item is one remembered thing.
type Item struct {
	ID        string    `json:"id"`
	Kind      string    `json:"kind"`
	Text      string    `json:"text"`
	Source    string    `json:"source,omitempty"`
	Score     float64   `json:"score,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ErrNotFound: no such item for this tenant and subject.
var ErrNotFound = errors.New("not found")

// Service does the work. Embed and Vectors may be nil (text search only).
type Service struct {
	DB       *pgxpool.Pool
	Embed    *Embedder
	Vectors  *Qdrant
	Log      *slog.Logger
	MinScore float64 // vector search threshold, default 0.45
	Same     float64 // above this similarity a new item replaces the old one, default 0.92
	MaxText  int     // longest item, default 1000 characters
}

func newID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func (s *Service) vectors() bool { return s.Embed != nil && s.Vectors != nil }

func checkKind(k string) (string, error) {
	k = strings.ToLower(strings.TrimSpace(k))
	if k == "" {
		return "fact", nil
	}
	for _, x := range Kinds {
		if x == k {
			return k, nil
		}
	}
	return "", fmt.Errorf("kind must be one of %s", strings.Join(Kinds, ", "))
}

func (s *Service) checkText(t string) (string, error) {
	t = strings.TrimSpace(t)
	max := s.MaxText
	if max <= 0 {
		max = 1000
	}
	if t == "" {
		return "", errors.New("the text is empty")
	}
	if len([]rune(t)) > max {
		return "", fmt.Errorf("the text is longer than %d characters: store one short fact per item", max)
	}
	return t, nil
}

const cols = `id, kind, text, source, created_at, updated_at`

func scan(r pgx.Row) (Item, error) {
	var it Item
	err := r.Scan(&it.ID, &it.Kind, &it.Text, &it.Source, &it.CreatedAt, &it.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return it, ErrNotFound
	}
	return it, err
}

// Remember stores an item. When a very similar item exists it is updated instead, so repeating a fact does not
// pile up copies. It returns the item and whether it replaced an older one.
func (s *Service) Remember(ctx context.Context, tenant, subject, kind, text, source string) (Item, bool, error) {
	kind, err := checkKind(kind)
	if err != nil {
		return Item{}, false, err
	}
	if text, err = s.checkText(text); err != nil {
		return Item{}, false, err
	}
	var vec []float32
	if s.vectors() {
		if v, err := s.Embed.Embed(ctx, []string{text}); err == nil {
			vec = v[0]
			same := s.Same
			if same <= 0 {
				same = 0.92
			}
			if hits, err := s.Vectors.search(ctx, vec, tenant, subject, 1, same); err == nil && len(hits) > 0 {
				it, err := s.update(ctx, tenant, subject, hits[0].ID, kind, text, vec)
				if err == nil {
					return it, true, nil
				}
			}
		} else {
			s.Log.Warn("embed failed: stored without a vector for now", "err", err)
		}
	} else {
		// without vectors, an exact repeat is still caught
		var id string
		if err := s.DB.QueryRow(ctx, `SELECT id FROM memory_items WHERE tenant = $1 AND subject = $2 AND lower(text) = lower($3)`, tenant, subject, text).Scan(&id); err == nil {
			it, err := s.update(ctx, tenant, subject, id, kind, text, nil)
			return it, true, err
		}
	}
	id := newID()
	it, err := scan(s.DB.QueryRow(ctx, `INSERT INTO memory_items (id, tenant, subject, kind, text, source) VALUES ($1,$2,$3,$4,$5,$6) RETURNING `+cols,
		id, tenant, subject, kind, text, source))
	if err != nil {
		return it, false, err
	}
	if vec != nil {
		s.index(ctx, tenant, subject, id, vec)
	}
	return it, false, nil
}

func (s *Service) index(ctx context.Context, tenant, subject, id string, vec []float32) {
	if err := s.Vectors.upsert(ctx, id, vec, map[string]string{"tenant": tenant, "subject": subject}); err != nil {
		s.Log.Warn("qdrant upsert failed: will retry", "err", err)
		return
	}
	_, _ = s.DB.Exec(ctx, `UPDATE memory_items SET embedded = true WHERE id = $1`, id)
}

func (s *Service) update(ctx context.Context, tenant, subject, id, kind, text string, vec []float32) (Item, error) {
	it, err := scan(s.DB.QueryRow(ctx, `UPDATE memory_items SET kind = $4, text = $5, embedded = false, updated_at = now()
		WHERE id = $1 AND tenant = $2 AND subject = $3 RETURNING `+cols, id, tenant, subject, kind, text))
	if err != nil {
		return it, err
	}
	if s.vectors() {
		if vec == nil {
			if v, err := s.Embed.Embed(ctx, []string{text}); err == nil {
				vec = v[0]
			}
		}
		if vec != nil {
			s.index(ctx, tenant, subject, id, vec)
		}
	}
	return it, nil
}

// Update changes an item's text or kind.
func (s *Service) Update(ctx context.Context, tenant, subject, id, kind, text string) (Item, error) {
	kind, err := checkKind(kind)
	if err != nil {
		return Item{}, err
	}
	if text, err = s.checkText(text); err != nil {
		return Item{}, err
	}
	return s.update(ctx, tenant, subject, id, kind, text, nil)
}

// Forget deletes an item.
func (s *Service) Forget(ctx context.Context, tenant, subject, id string) error {
	tag, err := s.DB.Exec(ctx, `DELETE FROM memory_items WHERE id = $1 AND tenant = $2 AND subject = $3`, id, tenant, subject)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	if s.vectors() {
		if err := s.Vectors.remove(ctx, id); err != nil {
			s.Log.Warn("qdrant delete failed: the point stays but is never returned", "err", err)
		}
	}
	return nil
}

// List returns items, newest first, optionally filtered by kind and a text match.
func (s *Service) List(ctx context.Context, tenant, subject, kind, q string, limit int) ([]Item, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	rows, err := s.DB.Query(ctx, `SELECT `+cols+` FROM memory_items WHERE tenant = $1 AND subject = $2 AND ($3 = '' OR kind = $3)
		AND ($4 = '' OR text ILIKE '%' || $4 || '%') ORDER BY updated_at DESC LIMIT $5`, tenant, subject, kind, q, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Item, error) { return scan(r) })
}

// Search finds the items closest in meaning to query (or by words, without vectors).
func (s *Service) Search(ctx context.Context, tenant, subject, query string, k int, min float64) ([]Item, error) {
	if k <= 0 || k > 50 {
		k = 5
	}
	if min <= 0 {
		min = s.MinScore
		if min <= 0 {
			min = 0.45
		}
	}
	if s.vectors() {
		items, err := s.searchVectors(ctx, tenant, subject, query, k, min)
		if err == nil {
			return items, nil
		}
		s.Log.Warn("vector search failed: using text search", "err", err)
	}
	// any of the words may match (a question shares few words with the fact that answers it)
	words := wordRe.FindAllString(strings.ToLower(query), 30)
	terms := []string{}
	for _, w := range words {
		if len([]rune(w)) >= 3 {
			terms = append(terms, w)
		}
	}
	if len(terms) == 0 {
		return []Item{}, nil
	}
	rows, err := s.DB.Query(ctx, `SELECT `+cols+`, ts_rank(to_tsvector('simple', text), q) AS r
		FROM memory_items, to_tsquery('simple', $3) q
		WHERE tenant = $1 AND subject = $2 AND to_tsvector('simple', text) @@ q
		ORDER BY r DESC, updated_at DESC LIMIT $4`, tenant, subject, strings.Join(terms, " | "), k)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Item, error) {
		var it Item
		var rank float64
		err := r.Scan(&it.ID, &it.Kind, &it.Text, &it.Source, &it.CreatedAt, &it.UpdatedAt, &rank)
		it.Score = rank / (rank + 0.1)
		return it, err
	})
}

func (s *Service) searchVectors(ctx context.Context, tenant, subject, query string, k int, min float64) ([]Item, error) {
	v, err := s.Embed.Embed(ctx, []string{query})
	if err != nil {
		return nil, err
	}
	hits, err := s.Vectors.search(ctx, v[0], tenant, subject, k, min)
	if err != nil {
		return nil, err
	}
	if len(hits) == 0 {
		return []Item{}, nil
	}
	ids := make([]string, len(hits))
	score := map[string]float64{}
	for i, h := range hits {
		ids[i] = h.ID
		score[h.ID] = h.Score
	}
	rows, err := s.DB.Query(ctx, `SELECT `+cols+` FROM memory_items WHERE tenant = $1 AND subject = $2 AND id::text = ANY($3)`, tenant, subject, ids)
	if err != nil {
		return nil, err
	}
	found, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Item, error) { return scan(r) })
	if err != nil {
		return nil, err
	}
	byID := map[string]Item{}
	for _, it := range found {
		byID[it.ID] = it
	}
	out := []Item{}
	for _, id := range ids { // keep Qdrant's order
		if it, ok := byID[id]; ok {
			it.Score = score[id]
			out = append(out, it)
		}
	}
	return out, nil
}

// Backfill embeds items that have no vector yet (the embedder or Qdrant was down, or a reindex was asked for).
func (s *Service) Backfill(ctx context.Context) (int, error) {
	if !s.vectors() {
		return 0, nil
	}
	n := 0
	for {
		rows, err := s.DB.Query(ctx, `SELECT id, tenant, subject, text FROM memory_items WHERE NOT embedded ORDER BY created_at LIMIT 32`)
		if err != nil {
			return n, err
		}
		type row struct{ id, tenant, subject, text string }
		batch, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (row, error) {
			var x row
			err := r.Scan(&x.id, &x.tenant, &x.subject, &x.text)
			return x, err
		})
		if err != nil || len(batch) == 0 {
			return n, err
		}
		texts := make([]string, len(batch))
		for i, b := range batch {
			texts[i] = b.text
		}
		vecs, err := s.Embed.Embed(ctx, texts)
		if err != nil {
			return n, err
		}
		for i, b := range batch {
			if err := s.Vectors.upsert(ctx, b.id, vecs[i], map[string]string{"tenant": b.tenant, "subject": b.subject}); err != nil {
				return n, err
			}
			if _, err := s.DB.Exec(ctx, `UPDATE memory_items SET embedded = true WHERE id = $1`, b.id); err != nil {
				return n, err
			}
			n++
		}
	}
}

// Reindex marks every item as not embedded, so Backfill rebuilds Qdrant (after changing the embedding model).
func (s *Service) Reindex(ctx context.Context) error {
	_, err := s.DB.Exec(ctx, `UPDATE memory_items SET embedded = false`)
	if s.Vectors != nil {
		s.Vectors.mu.Lock()
		s.Vectors.ready = false
		s.Vectors.mu.Unlock()
	}
	return err
}

// Ready checks Postgres and, when configured, Qdrant.
func (s *Service) Ready(ctx context.Context) error {
	if err := s.DB.Ping(ctx); err != nil {
		return fmt.Errorf("postgres: %w", err)
	}
	if s.Vectors != nil {
		if err := s.Vectors.ping(ctx); err != nil {
			return fmt.Errorf("qdrant: %w", err)
		}
	}
	return nil
}
