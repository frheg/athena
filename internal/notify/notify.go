// Package notify is a small, general service that sends messages out: mail over SMTP now, other channels later.
// It sends only to the recipients it is configured with, by name ("owner"), never to an address a caller makes up:
// that limit lives here, in the service, so no prompt can widen it.
package notify

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/smtp"
	"sort"
	"strings"
	"sync"
	"time"
)

// SMTP settings. Port 465 uses TLS from the start; other ports use STARTTLS when the server offers it.
type SMTP struct {
	Host, User, Pass, From string
	Port                   int
}

// Service sends messages.
type Service struct {
	SMTP       SMTP              // Host "" = dry run: messages are logged and kept, not sent
	Recipients map[string]string // name -> mail address
	PerHour    int               // most messages per hour in total, default 20
	Footer     string
	Log        *slog.Logger

	mu   sync.Mutex
	sent []time.Time
	Last []Message // the last messages (dry run and real), newest last, for checking
}

// Message is one message.
type Message struct {
	To      string    `json:"to"`
	Subject string    `json:"subject"`
	Body    string    `json:"body"`
	At      time.Time `json:"at"`
	DryRun  bool      `json:"dry_run"`
}

// Names lists the recipients by name.
func (s *Service) Names() []string {
	out := make([]string, 0, len(s.Recipients))
	for n := range s.Recipients {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

var errLimit = errors.New("the hourly limit for messages is reached")

// Send sends a message to a named recipient.
func (s *Service) Send(ctx context.Context, to, subject, body string) (Message, error) {
	to = strings.TrimSpace(to)
	addr, ok := s.Recipients[to]
	if !ok {
		return Message{}, fmt.Errorf("unknown recipient %q: this service sends only to %s", to, strings.Join(s.Names(), ", "))
	}
	subject = strings.Join(strings.Fields(subject), " ") // no line breaks: they would inject headers
	if subject == "" || strings.TrimSpace(body) == "" {
		return Message{}, errors.New("a message needs a subject and a body")
	}
	if r := []rune(subject); len(r) > 200 {
		subject = string(r[:200])
	}
	if len(body) > 100_000 {
		return Message{}, errors.New("the body is longer than 100000 bytes")
	}
	per := s.PerHour
	if per <= 0 {
		per = 20
	}
	s.mu.Lock()
	cut := time.Now().Add(-time.Hour)
	kept := s.sent[:0]
	for _, t := range s.sent {
		if t.After(cut) {
			kept = append(kept, t)
		}
	}
	s.sent = kept
	if len(s.sent) >= per {
		s.mu.Unlock()
		return Message{}, errLimit
	}
	s.sent = append(s.sent, time.Now())
	s.mu.Unlock()

	if s.Footer != "" {
		body = strings.TrimRight(body, "\n") + "\n\n-- \n" + s.Footer + "\n"
	}
	m := Message{To: to, Subject: subject, Body: body, At: time.Now(), DryRun: s.SMTP.Host == ""}
	if !m.DryRun {
		if err := s.mail(ctx, addr, subject, body); err != nil {
			return m, err
		}
	}
	s.mu.Lock()
	s.Last = append(s.Last, m)
	if len(s.Last) > 50 {
		s.Last = s.Last[len(s.Last)-50:]
	}
	s.mu.Unlock()
	s.Log.Info("sent", "to", to, "subject", subject, "dry_run", m.DryRun)
	return m, nil
}

func (s *Service) mail(ctx context.Context, to, subject, body string) error {
	port := s.SMTP.Port
	if port == 0 {
		port = 587
	}
	hostport := net.JoinHostPort(s.SMTP.Host, fmt.Sprint(port))
	d := net.Dialer{Timeout: 20 * time.Second}
	var conn net.Conn
	var err error
	if port == 465 {
		conn, err = tls.DialWithDialer(&d, "tcp", hostport, &tls.Config{ServerName: s.SMTP.Host, MinVersion: tls.VersionTLS12})
	} else {
		conn, err = d.DialContext(ctx, "tcp", hostport)
	}
	if err != nil {
		return fmt.Errorf("smtp: %w", err)
	}
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	} else {
		_ = conn.SetDeadline(time.Now().Add(time.Minute))
	}
	c, err := smtp.NewClient(conn, s.SMTP.Host)
	if err != nil {
		conn.Close()
		return fmt.Errorf("smtp: %w", err)
	}
	defer c.Close()
	if port != 465 {
		if ok, _ := c.Extension("STARTTLS"); ok {
			if err := c.StartTLS(&tls.Config{ServerName: s.SMTP.Host, MinVersion: tls.VersionTLS12}); err != nil {
				return fmt.Errorf("smtp starttls: %w", err)
			}
		} else if s.SMTP.User != "" {
			return errors.New("smtp: the server offers no STARTTLS; refusing to send the password in clear text")
		}
	}
	if s.SMTP.User != "" {
		if err := c.Auth(smtp.PlainAuth("", s.SMTP.User, s.SMTP.Pass, s.SMTP.Host)); err != nil {
			return fmt.Errorf("smtp auth: %w", err)
		}
	}
	if err := c.Mail(s.SMTP.From); err != nil {
		return fmt.Errorf("smtp: %w", err)
	}
	if err := c.Rcpt(to); err != nil {
		return fmt.Errorf("smtp: %w", err)
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("smtp: %w", err)
	}
	msg := "From: " + s.SMTP.From + "\r\nTo: " + to + "\r\nSubject: " + mimeWord(subject) +
		"\r\nDate: " + time.Now().Format(time.RFC1123Z) + "\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: 8bit\r\n\r\n" +
		strings.ReplaceAll(strings.ReplaceAll(body, "\r\n", "\n"), "\n", "\r\n")
	if _, err := w.Write([]byte(msg)); err != nil {
		return fmt.Errorf("smtp: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("smtp: %w", err)
	}
	return c.Quit()
}

// mimeWord encodes a header value with non-ASCII letters (æøå) as RFC 2047.
func mimeWord(s string) string {
	for _, r := range s {
		if r > 127 {
			return mimeQ(s)
		}
	}
	return s
}

func mimeQ(s string) string {
	var b strings.Builder
	b.WriteString("=?utf-8?q?")
	for _, c := range []byte(s) {
		switch {
		case c == ' ':
			b.WriteByte('_')
		case c >= 33 && c <= 126 && c != '=' && c != '?' && c != '_':
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, "=%02X", c)
		}
	}
	b.WriteString("?=")
	return b.String()
}
