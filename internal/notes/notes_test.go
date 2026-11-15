package notes

import (
	"context"
	"io/fs"
	"os"
	"testing"
	"time"

	"github.com/frheg/athena/internal/pg"
)

func TestNotesAndReminders(t *testing.T) {
	url := os.Getenv("ATHENA_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("ATHENA_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	db, err := pg.Open(ctx, url, 2, func(string, ...any) {})
	if err != nil {
		t.Fatal(err)
	}
	sub, _ := fs.Sub(Migrations, "migrations")
	if err := pg.Migrate(ctx, db, sub, "notes_migrations"); err != nil {
		t.Fatal(err)
	}
	oslo, _ := time.LoadLocation("Europe/Oslo")
	now := time.Date(2026, 11, 15, 12, 0, 0, 0, oslo)
	s := &Service{DB: db, Zone: oslo, Now: func() time.Time { return now }}
	ten := "n-" + newID()[:8]
	n, err := s.Add(ctx, ten, "me", "Bike service", "Change the chain before spring", []string{"Bike", " "})
	if err != nil || len(n.Tags) != 1 || n.Tags[0] != "bike" {
		t.Fatal(n, err)
	}
	if found, _ := s.Search(ctx, ten, "me", "when do I change the chain?", "", 0); len(found) != 1 {
		t.Fatal("search by a word should find it", found)
	}
	if found, _ := s.Search(ctx, ten, "other", "chain", "", 0); len(found) != 0 {
		t.Fatal("another subject must not see it")
	}
	if _, err := s.Update(ctx, ten, "me", n.ID, "", "New body", nil); err != nil {
		t.Fatal(err)
	}
	if g, _ := s.Get(ctx, ten, "me", n.ID); g.Title != "Bike service" || g.Body != "New body" {
		t.Fatal(g)
	}
	if err := s.Delete(ctx, ten, "other", n.ID); err != ErrNotFound {
		t.Fatal("another subject must not delete it")
	}

	r, err := s.Remind(ctx, ten, "me", "Call the dentist", "2026-11-15 12:30")
	if err != nil || !r.DueAt.Equal(now.Add(30*time.Minute)) {
		t.Fatal(r, err)
	}
	if _, err := s.Remind(ctx, ten, "me", "x", "2026-11-14 08:00"); err == nil {
		t.Fatal("a time in the past must be refused")
	}
	r2, _ := s.Remind(ctx, ten, "me", "Stretch", "in 2h")
	if due, _ := s.Due(ctx, ten); len(due) != 0 {
		t.Fatal("nothing is due yet", due)
	}
	now = now.Add(time.Hour)
	due, err := s.Due(ctx, ten)
	if err != nil || len(due) != 1 || due[0].Text != "Call the dentist" || due[0].Subject != "me" {
		t.Fatal(due, err)
	}
	if again, _ := s.Due(ctx, ten); len(again) != 0 {
		t.Fatal("a reminder is handed out once")
	}
	if err := s.Cancel(ctx, ten, "me", r2.ID); err != nil {
		t.Fatal(err)
	}
	now = now.Add(3 * time.Hour)
	if due, _ := s.Due(ctx, ten); len(due) != 0 {
		t.Fatal("a cancelled reminder must not fire")
	}
}
