package notify

import (
	"context"
	"log/slog"
	"strings"
	"testing"
)

func TestSend(t *testing.T) {
	s := &Service{Recipients: map[string]string{"owner": "me@example.org"}, PerHour: 2, Log: slog.Default()}
	ctx := context.Background()
	if _, err := s.Send(ctx, "attacker@example.com", "hi", "body"); err == nil || !strings.Contains(err.Error(), "unknown recipient") {
		t.Fatal("an address that is not a configured name must be refused:", err)
	}
	m, err := s.Send(ctx, "owner", "two\r\nBcc: x@y.z lines", "body")
	if err != nil || !m.DryRun || strings.ContainsAny(m.Subject, "\r\n") {
		t.Fatalf("%+v %v", m, err)
	}
	if _, err := s.Send(ctx, "owner", "", "body"); err == nil {
		t.Fatal("empty subject accepted")
	}
	_, _ = s.Send(ctx, "owner", "b", "body")
	if _, err := s.Send(ctx, "owner", "c", "body"); err != errLimit {
		t.Fatal("the hourly limit should stop the third message:", err)
	}
	if mimeWord("Været i Bergen") != "=?utf-8?q?V=C3=A6ret_i_Bergen?=" {
		t.Fatal(mimeWord("Været i Bergen"))
	}
}
