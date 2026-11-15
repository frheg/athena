// Package store is core's Postgres access. Migrations are embedded and run at start.
package store

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"io/fs"
	"strings"
	"time"

	"github.com/frheg/athena/internal/pg"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrations embed.FS

// ErrNotFound is returned when a row does not exist (or belongs to someone else).
var ErrNotFound = errors.New("not found")

// Store wraps the pool.
type Store struct{ DB *pgxpool.Pool }

// Open connects, retrying until ctx ends (the database may still be starting).
func Open(ctx context.Context, url string, log func(string, ...any)) (*Store, error) {
	pool, err := pg.Open(ctx, url, 10, log)
	if err != nil {
		return nil, err
	}
	return &Store{DB: pool}, nil
}

// Migrate applies the embedded migrations that have not run yet.
func (s *Store) Migrate(ctx context.Context) error {
	sub, _ := fs.Sub(migrations, "migrations")
	return pg.Migrate(ctx, s.DB, sub, "core_migrations")
}

// ---------- types ----------

// Session is a conversation.
type Session struct {
	ID        string    `json:"id"`
	Owner     string    `json:"-"`
	Principal string    `json:"principal"`
	Title     string    `json:"title"`
	Tainted   bool      `json:"tainted"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Message is one message in the model's format.
type Message struct {
	ID         int64           `json:"id"`
	TurnID     *string         `json:"turn_id,omitempty"`
	Role       string          `json:"role"`
	Content    string          `json:"content"`
	ToolCalls  json.RawMessage `json:"tool_calls,omitempty"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
	Name       string          `json:"name,omitempty"`
	CreatedAt  time.Time       `json:"created_at"`
}

// Turn is one answer to a user message (or a job run).
type Turn struct {
	ID         string     `json:"id"`
	SessionID  string     `json:"session_id"`
	Owner      string     `json:"-"`
	Principal  string     `json:"principal"`
	Status     string     `json:"status"`
	Model      string     `json:"model"`
	Error      string     `json:"error,omitempty"`
	Rounds     int        `json:"rounds"`
	TokensIn   int        `json:"tokens_in"`
	TokensOut  int        `json:"tokens_out"`
	CreatedAt  time.Time  `json:"created_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
}

// ToolCall is a tool call and what happened to it.
type ToolCall struct {
	ID         string          `json:"id"`
	TurnID     string          `json:"turn_id"`
	SessionID  string          `json:"session_id"`
	CallID     string          `json:"call_id"`
	Tool       string          `json:"tool"`
	Args       json.RawMessage `json:"args"`
	Class      string          `json:"class"`
	Decision   string          `json:"decision"`
	Reason     string          `json:"reason"`
	Status     string          `json:"status"`
	Result     string          `json:"result,omitempty"`
	DurationMS int             `json:"duration_ms"`
	CreatedAt  time.Time       `json:"created_at"`
}

// Approval is a request waiting for (or answered by) the user.
type Approval struct {
	ID         string          `json:"id"`
	Owner      string          `json:"-"`
	SessionID  string          `json:"session_id"`
	TurnID     string          `json:"turn_id"`
	ToolCallID string          `json:"tool_call_id"`
	Principal  string          `json:"principal"`
	Tool       string          `json:"tool"`
	Args       json.RawMessage `json:"args"`
	Class      string          `json:"class"`
	Reason     string          `json:"reason"`
	Status     string          `json:"status"`
	Scope      string          `json:"scope,omitempty"`
	CreatedAt  time.Time       `json:"created_at"`
	AnsweredAt *time.Time      `json:"answered_at,omitempty"`
}

// Permission is a standing allowance for one tool with exact arguments.
type Permission struct {
	ID        string          `json:"id"`
	Tool      string          `json:"tool"`
	Args      json.RawMessage `json:"args"`
	SessionID *string         `json:"session_id,omitempty"`
	ExpiresAt *time.Time      `json:"expires_at,omitempty"`
	CreatedAt time.Time       `json:"created_at"`
}

// AuditEntry is one audit row.
type AuditEntry struct {
	ID        int64           `json:"id"`
	At        time.Time       `json:"at"`
	Principal string          `json:"principal"`
	SessionID *string         `json:"session_id,omitempty"`
	TurnID    *string         `json:"turn_id,omitempty"`
	Event     string          `json:"event"`
	Tool      string          `json:"tool,omitempty"`
	Detail    json.RawMessage `json:"detail"`
}

// Job is a scheduled prompt.
type Job struct {
	ID         string     `json:"id"`
	Owner      string     `json:"-"`
	Name       string     `json:"name"`
	Schedule   string     `json:"schedule"`
	Prompt     string     `json:"prompt"`
	Enabled    bool       `json:"enabled"`
	Notify     bool       `json:"notify"`
	LastRunAt  *time.Time `json:"last_run_at,omitempty"`
	LastStatus string     `json:"last_status"`
	CreatedAt  time.Time  `json:"created_at"`
}

// Notification is a message to the user from a job.
type Notification struct {
	ID        int64     `json:"id"`
	At        time.Time `json:"at"`
	Title     string    `json:"title"`
	Body      string    `json:"body"`
	SessionID *string   `json:"session_id,omitempty"`
	Read      bool      `json:"read"`
}

func notFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

// ---------- sessions and messages ----------

// CreateSession starts a conversation.
func (s *Store) CreateSession(ctx context.Context, id, owner, principal, title string) (Session, error) {
	var x Session
	err := s.DB.QueryRow(ctx, `INSERT INTO sessions (id, owner, principal, title) VALUES ($1,$2,$3,$4)
		RETURNING id, owner, principal, title, tainted, created_at, updated_at`, id, owner, principal, title).
		Scan(&x.ID, &x.Owner, &x.Principal, &x.Title, &x.Tainted, &x.CreatedAt, &x.UpdatedAt)
	return x, err
}

// Session returns one conversation of owner.
func (s *Store) Session(ctx context.Context, owner, id string) (Session, error) {
	var x Session
	err := s.DB.QueryRow(ctx, `SELECT id, owner, principal, title, tainted, created_at, updated_at FROM sessions
		WHERE id = $1 AND owner = $2 AND NOT archived`, id, owner).Scan(&x.ID, &x.Owner, &x.Principal, &x.Title, &x.Tainted, &x.CreatedAt, &x.UpdatedAt)
	return x, notFound(err)
}

// Sessions lists conversations, newest first, optionally matching a search in the title or messages.
func (s *Store) Sessions(ctx context.Context, owner, q string, limit int) ([]Session, error) {
	rows, err := s.DB.Query(ctx, `SELECT id, owner, principal, title, tainted, created_at, updated_at FROM sessions s
		WHERE owner = $1 AND NOT archived AND ($2 = '' OR title ILIKE '%'||$2||'%'
		  OR EXISTS (SELECT 1 FROM messages m WHERE m.session_id = s.id AND m.role IN ('user','assistant') AND m.content ILIKE '%'||$2||'%'))
		ORDER BY updated_at DESC LIMIT $3`, owner, q, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Session, error) {
		var x Session
		err := r.Scan(&x.ID, &x.Owner, &x.Principal, &x.Title, &x.Tainted, &x.CreatedAt, &x.UpdatedAt)
		return x, err
	})
}

// UpdateSession renames or archives a conversation.
func (s *Store) UpdateSession(ctx context.Context, owner, id string, title *string, archive bool) error {
	tag, err := s.DB.Exec(ctx, `UPDATE sessions SET title = COALESCE($3, title), archived = archived OR $4, updated_at = now()
		WHERE id = $1 AND owner = $2`, id, owner, title, archive)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// Taint marks a session as having read untrusted content.
func (s *Store) Taint(ctx context.Context, id string) error {
	_, err := s.DB.Exec(ctx, `UPDATE sessions SET tainted = true WHERE id = $1`, id)
	return err
}

// AddMessage appends a message and touches the session.
func (s *Store) AddMessage(ctx context.Context, sessionID string, turnID *string, m Message) (int64, error) {
	var id int64
	var calls any
	if len(m.ToolCalls) > 0 {
		calls = []byte(m.ToolCalls)
	}
	err := pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `INSERT INTO messages (session_id, turn_id, role, content, tool_calls, tool_call_id, name)
			VALUES ($1,$2,$3,$4,$5,NULLIF($6,''),NULLIF($7,'')) RETURNING id`, sessionID, turnID, m.Role, m.Content, calls, m.ToolCallID, m.Name).Scan(&id); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE sessions SET updated_at = now() WHERE id = $1`, sessionID)
		return err
	})
	return id, err
}

// Messages returns a conversation's messages in order (the newest `limit`, 0 = all).
func (s *Store) Messages(ctx context.Context, sessionID string, limit int) ([]Message, error) {
	q := `SELECT id, turn_id, role, content, tool_calls, COALESCE(tool_call_id,''), COALESCE(name,''), created_at FROM messages WHERE session_id = $1 ORDER BY id`
	args := []any{sessionID}
	if limit > 0 {
		q = `SELECT * FROM (SELECT id, turn_id, role, content, tool_calls, COALESCE(tool_call_id,''), COALESCE(name,''), created_at FROM messages
			WHERE session_id = $1 ORDER BY id DESC LIMIT $2) x ORDER BY id`
		args = append(args, limit)
	}
	rows, err := s.DB.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Message, error) {
		var m Message
		var calls []byte
		err := r.Scan(&m.ID, &m.TurnID, &m.Role, &m.Content, &calls, &m.ToolCallID, &m.Name, &m.CreatedAt)
		m.ToolCalls = calls
		return m, err
	})
}

// ---------- turns ----------

// CreateTurn starts a turn in status running.
func (s *Store) CreateTurn(ctx context.Context, id, sessionID, owner, principal string) error {
	_, err := s.DB.Exec(ctx, `INSERT INTO turns (id, session_id, owner, principal, status) VALUES ($1,$2,$3,$4,'running')`, id, sessionID, owner, principal)
	return err
}

// Turn returns one turn of owner.
func (s *Store) Turn(ctx context.Context, owner, id string) (Turn, error) {
	var t Turn
	err := s.DB.QueryRow(ctx, `SELECT id, session_id, owner, principal, status, model, error, rounds, tokens_in, tokens_out, created_at, finished_at
		FROM turns WHERE id = $1 AND ($2 = '' OR owner = $2)`, id, owner).
		Scan(&t.ID, &t.SessionID, &t.Owner, &t.Principal, &t.Status, &t.Model, &t.Error, &t.Rounds, &t.TokensIn, &t.TokensOut, &t.CreatedAt, &t.FinishedAt)
	return t, notFound(err)
}

// Turns lists the turns of a session.
func (s *Store) Turns(ctx context.Context, sessionID string) ([]Turn, error) {
	rows, err := s.DB.Query(ctx, `SELECT id, session_id, owner, principal, status, model, error, rounds, tokens_in, tokens_out, created_at, finished_at
		FROM turns WHERE session_id = $1 ORDER BY created_at`, sessionID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Turn, error) {
		var t Turn
		err := r.Scan(&t.ID, &t.SessionID, &t.Owner, &t.Principal, &t.Status, &t.Model, &t.Error, &t.Rounds, &t.TokensIn, &t.TokensOut, &t.CreatedAt, &t.FinishedAt)
		return t, err
	})
}

// TurnsByStatus lists turns in a status (for resuming after a restart).
func (s *Store) TurnsByStatus(ctx context.Context, status string) ([]Turn, error) {
	rows, err := s.DB.Query(ctx, `SELECT id, session_id, owner, principal, status, model, error, rounds, tokens_in, tokens_out, created_at, finished_at
		FROM turns WHERE status = $1 ORDER BY created_at`, status)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Turn, error) {
		var t Turn
		err := r.Scan(&t.ID, &t.SessionID, &t.Owner, &t.Principal, &t.Status, &t.Model, &t.Error, &t.Rounds, &t.TokensIn, &t.TokensOut, &t.CreatedAt, &t.FinishedAt)
		return t, err
	})
}

// SetTurn updates a turn's status and counters. A final status sets finished_at.
func (s *Store) SetTurn(ctx context.Context, id, status, model, errText string, rounds, in, out int) error {
	_, err := s.DB.Exec(ctx, `UPDATE turns SET status = $2, model = CASE WHEN $3 = '' THEN model ELSE $3 END, error = $4,
		rounds = GREATEST(rounds, $5), tokens_in = tokens_in + $6, tokens_out = tokens_out + $7,
		finished_at = CASE WHEN $2 IN ('done','failed','cancelled') THEN now() ELSE NULL END WHERE id = $1`, id, status, model, errText, rounds, in, out)
	return err
}

// ---------- tool calls ----------

// AddToolCall records a tool call and the gate's decision.
func (s *Store) AddToolCall(ctx context.Context, c ToolCall) error {
	_, err := s.DB.Exec(ctx, `INSERT INTO tool_calls (id, turn_id, session_id, call_id, tool, args, class, decision, reason, status)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, c.ID, c.TurnID, c.SessionID, c.CallID, c.Tool, []byte(c.Args), c.Class, c.Decision, c.Reason, c.Status)
	return err
}

// FinishToolCall stores a tool call's outcome.
func (s *Store) FinishToolCall(ctx context.Context, id, status, result string, ms int) error {
	_, err := s.DB.Exec(ctx, `UPDATE tool_calls SET status = $2, result = $3, duration_ms = $4 WHERE id = $1`, id, status, result, ms)
	return err
}

// ToolCalls returns a turn's tool calls (or a session's, with turnID "").
func (s *Store) ToolCalls(ctx context.Context, sessionID, turnID string) ([]ToolCall, error) {
	rows, err := s.DB.Query(ctx, `SELECT id, turn_id, session_id, call_id, tool, args, class, decision, reason, status, result, duration_ms, created_at
		FROM tool_calls WHERE session_id = $1 AND ($2 = '' OR turn_id::text = $2) ORDER BY created_at`, sessionID, turnID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (ToolCall, error) {
		var c ToolCall
		var args []byte
		err := r.Scan(&c.ID, &c.TurnID, &c.SessionID, &c.CallID, &c.Tool, &args, &c.Class, &c.Decision, &c.Reason, &c.Status, &c.Result, &c.DurationMS, &c.CreatedAt)
		c.Args = args
		return c, err
	})
}

// ToolCall returns one tool call.
func (s *Store) ToolCall(ctx context.Context, id string) (ToolCall, error) {
	var c ToolCall
	var args []byte
	err := s.DB.QueryRow(ctx, `SELECT id, turn_id, session_id, call_id, tool, args, class, decision, reason, status, result, duration_ms, created_at
		FROM tool_calls WHERE id = $1`, id).Scan(&c.ID, &c.TurnID, &c.SessionID, &c.CallID, &c.Tool, &args, &c.Class, &c.Decision, &c.Reason, &c.Status, &c.Result, &c.DurationMS, &c.CreatedAt)
	c.Args = args
	return c, notFound(err)
}

// ---------- approvals and permissions ----------

// AddApproval creates a waiting approval.
func (s *Store) AddApproval(ctx context.Context, a Approval) error {
	_, err := s.DB.Exec(ctx, `INSERT INTO approvals (id, owner, session_id, turn_id, tool_call_id, principal, tool, args, class, reason, status)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,'waiting')`, a.ID, a.Owner, a.SessionID, a.TurnID, a.ToolCallID, a.Principal, a.Tool, []byte(a.Args), a.Class, a.Reason)
	return err
}

func scanApproval(r pgx.Row) (Approval, error) {
	var a Approval
	var args []byte
	err := r.Scan(&a.ID, &a.Owner, &a.SessionID, &a.TurnID, &a.ToolCallID, &a.Principal, &a.Tool, &args, &a.Class, &a.Reason, &a.Status, &a.Scope, &a.CreatedAt, &a.AnsweredAt)
	a.Args = args
	return a, err
}

const approvalCols = `id, owner, session_id, turn_id, tool_call_id, principal, tool, args, class, reason, status, scope, created_at, answered_at`

// Approvals lists approvals of owner in a status ("" = all), newest first.
func (s *Store) Approvals(ctx context.Context, owner, status, turnID string, limit int) ([]Approval, error) {
	rows, err := s.DB.Query(ctx, `SELECT `+approvalCols+` FROM approvals WHERE owner = $1 AND ($2 = '' OR status = $2)
		AND ($3 = '' OR turn_id::text = $3) ORDER BY created_at DESC LIMIT $4`, owner, status, turnID, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Approval, error) { return scanApproval(r) })
}

// Answer sets an approval's answer once; it returns ErrNotFound when it is not waiting anymore.
func (s *Store) Answer(ctx context.Context, owner, id, status, scope string) (Approval, error) {
	a, err := scanApproval(s.DB.QueryRow(ctx, `UPDATE approvals SET status = $3, scope = $4, answered_at = now()
		WHERE id = $1 AND owner = $2 AND status = 'waiting' RETURNING `+approvalCols, id, owner, status, scope))
	return a, notFound(err)
}

// WaitingInTurn counts the approvals a turn still waits for.
func (s *Store) WaitingInTurn(ctx context.Context, turnID string) (int, error) {
	var n int
	err := s.DB.QueryRow(ctx, `SELECT count(*) FROM approvals WHERE turn_id = $1 AND status = 'waiting'`, turnID).Scan(&n)
	return n, err
}

// AddPermission stores a standing permission.
func (s *Store) AddPermission(ctx context.Context, id, owner, tool string, args json.RawMessage, sessionID *string, expires *time.Time) error {
	_, err := s.DB.Exec(ctx, `INSERT INTO permissions (id, owner, tool, args, session_id, expires_at) VALUES ($1,$2,$3,$4,$5,$6)`,
		id, owner, tool, []byte(canonical(args)), sessionID, expires)
	return err
}

// HasPermission reports whether a standing permission allows tool with exactly these arguments.
func (s *Store) HasPermission(ctx context.Context, owner, tool string, args json.RawMessage, sessionID string) (bool, error) {
	var ok bool
	err := s.DB.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM permissions WHERE owner = $1 AND tool = $2 AND args = $3::jsonb
		AND revoked_at IS NULL AND (expires_at IS NULL OR expires_at > now()) AND (session_id IS NULL OR session_id::text = $4))`,
		owner, tool, string(canonical(args)), sessionID).Scan(&ok)
	return ok, err
}

// Permissions lists active standing permissions.
func (s *Store) Permissions(ctx context.Context, owner string) ([]Permission, error) {
	rows, err := s.DB.Query(ctx, `SELECT id, tool, args, session_id, expires_at, created_at FROM permissions
		WHERE owner = $1 AND revoked_at IS NULL AND (expires_at IS NULL OR expires_at > now()) ORDER BY created_at DESC`, owner)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Permission, error) {
		var p Permission
		var args []byte
		err := r.Scan(&p.ID, &p.Tool, &args, &p.SessionID, &p.ExpiresAt, &p.CreatedAt)
		p.Args = args
		return p, err
	})
}

// RevokePermission withdraws a standing permission.
func (s *Store) RevokePermission(ctx context.Context, owner, id string) error {
	tag, err := s.DB.Exec(ctx, `UPDATE permissions SET revoked_at = now() WHERE id = $1 AND owner = $2 AND revoked_at IS NULL`, id, owner)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// canonical re-encodes JSON so equal arguments compare equal (sorted keys, no spaces).
func canonical(b json.RawMessage) json.RawMessage {
	var v any
	if json.Unmarshal(b, &v) != nil {
		return b
	}
	out, _ := json.Marshal(v)
	return out
}

// ---------- audit ----------

// Audit appends an audit row. Errors are returned, but callers log rather than fail a turn on them.
func (s *Store) Audit(ctx context.Context, owner, principal string, sessionID, turnID *string, event, tool string, detail any) error {
	b, _ := json.Marshal(detail)
	if detail == nil {
		b = []byte("{}")
	}
	_, err := s.DB.Exec(ctx, `INSERT INTO audit (owner, principal, session_id, turn_id, event, tool, detail) VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		owner, principal, sessionID, turnID, event, tool, b)
	return err
}

// AuditQuery filters the audit log.
type AuditQuery struct {
	SessionID, Tool, Event, Text string
	Before                       int64
	Limit                        int
}

// AuditLog returns audit rows, newest first.
func (s *Store) AuditLog(ctx context.Context, owner string, q AuditQuery) ([]AuditEntry, error) {
	if q.Limit <= 0 || q.Limit > 500 {
		q.Limit = 100
	}
	rows, err := s.DB.Query(ctx, `SELECT id, at, principal, session_id, turn_id, event, tool, detail FROM audit WHERE owner = $1
		AND ($2 = '' OR session_id::text = $2) AND ($3 = '' OR tool = $3) AND ($4 = '' OR event LIKE $4 || '%')
		AND ($5 = '' OR detail::text ILIKE '%' || $5 || '%') AND ($6 = 0 OR id < $6) ORDER BY id DESC LIMIT $7`,
		owner, q.SessionID, q.Tool, q.Event, q.Text, q.Before, q.Limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (AuditEntry, error) {
		var a AuditEntry
		var d []byte
		err := r.Scan(&a.ID, &a.At, &a.Principal, &a.SessionID, &a.TurnID, &a.Event, &a.Tool, &d)
		a.Detail = d
		return a, err
	})
}

// ---------- jobs and notifications ----------

const jobCols = `id, owner, name, schedule, prompt, enabled, notify, last_run_at, last_status, created_at`

func scanJob(r pgx.Row) (Job, error) {
	var j Job
	err := r.Scan(&j.ID, &j.Owner, &j.Name, &j.Schedule, &j.Prompt, &j.Enabled, &j.Notify, &j.LastRunAt, &j.LastStatus, &j.CreatedAt)
	return j, err
}

// UpsertJob creates or replaces a job by name.
func (s *Store) UpsertJob(ctx context.Context, j Job) (Job, error) {
	return scanJob(s.DB.QueryRow(ctx, `INSERT INTO jobs (id, owner, name, schedule, prompt, enabled, notify) VALUES ($1,$2,$3,$4,$5,$6,$7)
		ON CONFLICT (owner, name) DO UPDATE SET schedule = EXCLUDED.schedule, prompt = EXCLUDED.prompt, enabled = EXCLUDED.enabled, notify = EXCLUDED.notify
		RETURNING `+jobCols, j.ID, j.Owner, j.Name, j.Schedule, j.Prompt, j.Enabled, j.Notify))
}

// Jobs lists jobs (owner "" = all owners, for the scheduler).
func (s *Store) Jobs(ctx context.Context, owner string) ([]Job, error) {
	rows, err := s.DB.Query(ctx, `SELECT `+jobCols+` FROM jobs WHERE $1 = '' OR owner = $1 ORDER BY name`, owner)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Job, error) { return scanJob(r) })
}

// Job returns one job.
func (s *Store) Job(ctx context.Context, owner, id string) (Job, error) {
	j, err := scanJob(s.DB.QueryRow(ctx, `SELECT `+jobCols+` FROM jobs WHERE id = $1 AND owner = $2`, id, owner))
	return j, notFound(err)
}

// DeleteJob removes a job.
func (s *Store) DeleteJob(ctx context.Context, owner, id string) error {
	tag, err := s.DB.Exec(ctx, `DELETE FROM jobs WHERE id = $1 AND owner = $2`, id, owner)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// JobRan records a run.
func (s *Store) JobRan(ctx context.Context, id string, at time.Time, status string) error {
	_, err := s.DB.Exec(ctx, `UPDATE jobs SET last_run_at = $2, last_status = $3 WHERE id = $1`, id, at, status)
	return err
}

// Notify stores a notification.
func (s *Store) Notify(ctx context.Context, owner, title, body string, sessionID *string) (int64, error) {
	var id int64
	err := s.DB.QueryRow(ctx, `INSERT INTO notifications (owner, title, body, session_id) VALUES ($1,$2,$3,$4) RETURNING id`, owner, title, body, sessionID).Scan(&id)
	return id, err
}

// Notifications lists notifications, newest first.
func (s *Store) Notifications(ctx context.Context, owner string, unreadOnly bool, limit int) ([]Notification, error) {
	rows, err := s.DB.Query(ctx, `SELECT id, at, title, body, session_id, read FROM notifications WHERE owner = $1 AND (NOT $2 OR NOT read)
		ORDER BY at DESC LIMIT $3`, owner, unreadOnly, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Notification, error) {
		var n Notification
		err := r.Scan(&n.ID, &n.At, &n.Title, &n.Body, &n.SessionID, &n.Read)
		return n, err
	})
}

// MarkRead marks notifications as read (ids empty = all).
func (s *Store) MarkRead(ctx context.Context, owner string, ids []int64) error {
	_, err := s.DB.Exec(ctx, `UPDATE notifications SET read = true WHERE owner = $1 AND (cardinality($2::bigint[]) = 0 OR id = ANY($2))`, owner, ids)
	return err
}

// Like escapes % and _ for ILIKE patterns.
func Like(s string) string { return strings.NewReplacer(`%`, `\%`, `_`, `\_`).Replace(s) }

// ---------- stats ----------

// Stats is an overview of what Athena has done, for the UI.
type Stats struct {
	Sessions     int            `json:"sessions"`
	Turns        int            `json:"turns"`
	Turns24h     int            `json:"turns_24h"`
	Failed7d     int            `json:"failed_7d"`
	TokensIn7d   int64          `json:"tokens_in_7d"`
	TokensOut7d  int64          `json:"tokens_out_7d"`
	ToolCalls7d  int            `json:"tool_calls_7d"`
	AvgTurnSec7d float64        `json:"avg_turn_seconds_7d"`
	Waiting      int            `json:"approvals_waiting"`
	Decisions7d  map[string]int `json:"decisions_7d"`
	Days         []DayStat      `json:"days"`
	Tools        []ToolStat     `json:"tools"`
	Models       []ModelStat    `json:"models"`
	Jobs         int            `json:"jobs"`
	Unread       int            `json:"notifications_unread"`
	Permissions  int            `json:"permissions"`
	Audit        int64          `json:"audit_entries"`
}

// DayStat is one day of activity.
type DayStat struct {
	Day       string `json:"day"`
	Turns     int    `json:"turns"`
	ToolCalls int    `json:"tool_calls"`
	Tokens    int64  `json:"tokens"`
}

// ToolStat is how a tool was used in the last 7 days.
type ToolStat struct {
	Tool   string  `json:"tool"`
	Calls  int     `json:"calls"`
	Errors int     `json:"errors"`
	AvgMS  float64 `json:"avg_ms"`
}

// ModelStat is how a model was used in the last 7 days.
type ModelStat struct {
	Model     string  `json:"model"`
	Turns     int     `json:"turns"`
	TokensOut int64   `json:"tokens_out"`
	AvgSec    float64 `json:"avg_seconds"`
}

// Stats collects the overview for owner, with days in zone.
func (s *Store) Stats(ctx context.Context, owner string, zone string) (Stats, error) {
	var st Stats
	err := s.DB.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM sessions WHERE owner = $1 AND NOT archived),
		(SELECT count(*) FROM turns WHERE owner = $1),
		(SELECT count(*) FROM turns WHERE owner = $1 AND created_at > now() - interval '24 hours'),
		(SELECT count(*) FROM turns WHERE owner = $1 AND status = 'failed' AND created_at > now() - interval '7 days'),
		(SELECT COALESCE(sum(tokens_in), 0) FROM turns WHERE owner = $1 AND created_at > now() - interval '7 days'),
		(SELECT COALESCE(sum(tokens_out), 0) FROM turns WHERE owner = $1 AND created_at > now() - interval '7 days'),
		(SELECT count(*) FROM tool_calls c JOIN turns t ON t.id = c.turn_id WHERE t.owner = $1 AND c.created_at > now() - interval '7 days'),
		(SELECT COALESCE(avg(extract(epoch FROM finished_at - created_at)), 0) FROM turns
			WHERE owner = $1 AND status = 'done' AND created_at > now() - interval '7 days'),
		(SELECT count(*) FROM approvals WHERE owner = $1 AND status = 'waiting'),
		(SELECT count(*) FROM jobs WHERE owner = $1),
		(SELECT count(*) FROM notifications WHERE owner = $1 AND NOT read),
		(SELECT count(*) FROM permissions WHERE owner = $1 AND revoked_at IS NULL AND (expires_at IS NULL OR expires_at > now())),
		(SELECT count(*) FROM audit WHERE owner = $1)`, owner).Scan(&st.Sessions, &st.Turns, &st.Turns24h, &st.Failed7d,
		&st.TokensIn7d, &st.TokensOut7d, &st.ToolCalls7d, &st.AvgTurnSec7d, &st.Waiting, &st.Jobs, &st.Unread, &st.Permissions, &st.Audit)
	if err != nil {
		return st, err
	}
	st.Decisions7d = map[string]int{"allow": 0, "ask": 0, "deny": 0}
	rows, err := s.DB.Query(ctx, `SELECT c.decision, count(*) FROM tool_calls c JOIN turns t ON t.id = c.turn_id
		WHERE t.owner = $1 AND c.created_at > now() - interval '7 days' GROUP BY 1`, owner)
	if err != nil {
		return st, err
	}
	for rows.Next() {
		var d string
		var n int
		if err := rows.Scan(&d, &n); err != nil {
			rows.Close()
			return st, err
		}
		st.Decisions7d[d] = n
	}
	rows.Close()
	rows, err = s.DB.Query(ctx, `WITH days AS (
			SELECT generate_series((now() AT TIME ZONE $2)::date - 13, (now() AT TIME ZONE $2)::date, interval '1 day')::date AS day)
		SELECT to_char(d.day, 'YYYY-MM-DD'),
			(SELECT count(*) FROM turns t WHERE t.owner = $1 AND (t.created_at AT TIME ZONE $2)::date = d.day),
			(SELECT count(*) FROM tool_calls c JOIN turns t ON t.id = c.turn_id WHERE t.owner = $1 AND (c.created_at AT TIME ZONE $2)::date = d.day),
			(SELECT COALESCE(sum(tokens_in + tokens_out), 0) FROM turns t WHERE t.owner = $1 AND (t.created_at AT TIME ZONE $2)::date = d.day)
		FROM days d ORDER BY d.day`, owner, zone)
	if err != nil {
		return st, err
	}
	if st.Days, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (DayStat, error) {
		var d DayStat
		err := r.Scan(&d.Day, &d.Turns, &d.ToolCalls, &d.Tokens)
		return d, err
	}); err != nil {
		return st, err
	}
	rows, err = s.DB.Query(ctx, `SELECT c.tool, count(*), count(*) FILTER (WHERE c.status = 'error'), COALESCE(avg(c.duration_ms) FILTER (WHERE c.status IN ('ok','error')), 0)
		FROM tool_calls c JOIN turns t ON t.id = c.turn_id WHERE t.owner = $1 AND c.created_at > now() - interval '7 days'
		GROUP BY 1 ORDER BY 2 DESC LIMIT 12`, owner)
	if err != nil {
		return st, err
	}
	if st.Tools, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (ToolStat, error) {
		var x ToolStat
		err := r.Scan(&x.Tool, &x.Calls, &x.Errors, &x.AvgMS)
		return x, err
	}); err != nil {
		return st, err
	}
	rows, err = s.DB.Query(ctx, `SELECT model, count(*), COALESCE(sum(tokens_out), 0),
			COALESCE(avg(extract(epoch FROM finished_at - created_at)) FILTER (WHERE status = 'done'), 0)
		FROM turns WHERE owner = $1 AND model <> '' AND created_at > now() - interval '7 days' GROUP BY 1 ORDER BY 2 DESC`, owner)
	if err != nil {
		return st, err
	}
	st.Models, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (ModelStat, error) {
		var x ModelStat
		err := r.Scan(&x.Model, &x.Turns, &x.TokensOut, &x.AvgSec)
		return x, err
	})
	return st, err
}

// Setting returns a setting ("" when it is not set).
func (s *Store) Setting(ctx context.Context, key string) (string, error) {
	var v string
	err := s.DB.QueryRow(ctx, `SELECT value FROM settings WHERE key = $1`, key).Scan(&v)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return v, err
}

// SetSetting stores a setting.
func (s *Store) SetSetting(ctx context.Context, key, value string) error {
	_, err := s.DB.Exec(ctx, `INSERT INTO settings (key, value) VALUES ($1, $2)
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now()`, key, value)
	return err
}
