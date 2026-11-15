-- Athena core: conversations, turns, tool calls, approvals, permissions, the audit log, jobs and notifications.
-- Every row carries an owner, so a second user can be added later without redesign.

CREATE TABLE sessions (
    id          uuid PRIMARY KEY,
    owner       text NOT NULL,
    principal   text NOT NULL,            -- user, or job:<name>
    title       text NOT NULL DEFAULT '',
    tainted     boolean NOT NULL DEFAULT false, -- has read untrusted content (web, mail, documents)
    archived    boolean NOT NULL DEFAULT false,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX sessions_owner ON sessions (owner, updated_at DESC);

CREATE TABLE turns (
    id           uuid PRIMARY KEY,
    session_id   uuid NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    owner        text NOT NULL,
    principal    text NOT NULL,
    status       text NOT NULL,            -- running, waiting, done, failed, cancelled
    model        text NOT NULL DEFAULT '',
    error        text NOT NULL DEFAULT '',
    rounds       int NOT NULL DEFAULT 0,
    tokens_in    int NOT NULL DEFAULT 0,
    tokens_out   int NOT NULL DEFAULT 0,
    created_at   timestamptz NOT NULL DEFAULT now(),
    finished_at  timestamptz
);
CREATE INDEX turns_session ON turns (session_id, created_at);
CREATE INDEX turns_status ON turns (status);

CREATE TABLE messages (
    id            bigserial PRIMARY KEY,
    session_id    uuid NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    turn_id       uuid REFERENCES turns(id) ON DELETE CASCADE,
    role          text NOT NULL,           -- user, assistant, tool
    content       text NOT NULL DEFAULT '',
    tool_calls    jsonb,                   -- assistant: the calls it asked for
    tool_call_id  text,                    -- tool: which call this answers
    name          text,                    -- tool: the tool's name
    created_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX messages_session ON messages (session_id, id);

CREATE TABLE tool_calls (
    id           uuid PRIMARY KEY,
    turn_id      uuid NOT NULL REFERENCES turns(id) ON DELETE CASCADE,
    session_id   uuid NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    call_id      text NOT NULL,            -- the model's id for the call
    tool         text NOT NULL,
    args         jsonb NOT NULL,
    class        text NOT NULL DEFAULT '',
    decision     text NOT NULL,            -- allow, ask, deny
    reason       text NOT NULL,
    status       text NOT NULL,            -- pending, ok, error, denied
    result       text NOT NULL DEFAULT '',
    duration_ms  int NOT NULL DEFAULT 0,
    created_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX tool_calls_turn ON tool_calls (turn_id, created_at);

CREATE TABLE approvals (
    id            uuid PRIMARY KEY,
    owner         text NOT NULL,
    session_id    uuid NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    turn_id       uuid NOT NULL REFERENCES turns(id) ON DELETE CASCADE,
    tool_call_id  uuid NOT NULL REFERENCES tool_calls(id) ON DELETE CASCADE,
    principal     text NOT NULL,
    tool          text NOT NULL,
    args          jsonb NOT NULL,
    class         text NOT NULL,
    reason        text NOT NULL,           -- why the gate asks
    status        text NOT NULL,           -- waiting, allowed, denied
    scope         text NOT NULL DEFAULT '',-- once, session, always (when allowed)
    created_at    timestamptz NOT NULL DEFAULT now(),
    answered_at   timestamptz
);
CREATE INDEX approvals_waiting ON approvals (owner, status, created_at);

CREATE TABLE permissions (
    id          uuid PRIMARY KEY,
    owner       text NOT NULL,
    tool        text NOT NULL,
    args        jsonb NOT NULL,            -- the exact arguments allowed ("always allow this exact pattern")
    session_id  uuid REFERENCES sessions(id) ON DELETE CASCADE, -- set: only for this session
    expires_at  timestamptz,
    created_at  timestamptz NOT NULL DEFAULT now(),
    revoked_at  timestamptz
);
CREATE INDEX permissions_tool ON permissions (owner, tool);

CREATE TABLE audit (
    id          bigserial PRIMARY KEY,
    at          timestamptz NOT NULL DEFAULT now(),
    owner       text NOT NULL,
    principal   text NOT NULL,
    session_id  uuid,
    turn_id     uuid,
    event       text NOT NULL,             -- turn.start, model.call, tool.decision, tool.result, approval.answer, ...
    tool        text NOT NULL DEFAULT '',
    detail      jsonb NOT NULL DEFAULT '{}'
);
CREATE INDEX audit_at ON audit (owner, at DESC);
CREATE INDEX audit_session ON audit (session_id, id);

-- The audit log is append-only: no updates, no deletes.
CREATE FUNCTION audit_append_only() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'the audit log is append-only';
END $$;
CREATE TRIGGER audit_no_change BEFORE UPDATE OR DELETE ON audit FOR EACH ROW EXECUTE FUNCTION audit_append_only();

CREATE TABLE jobs (
    id           uuid PRIMARY KEY,
    owner        text NOT NULL,
    name         text NOT NULL,
    schedule     text NOT NULL,            -- cron: minute hour day-of-month month day-of-week
    prompt       text NOT NULL,
    enabled      boolean NOT NULL DEFAULT true,
    notify       boolean NOT NULL DEFAULT true, -- also send the result through the notifier
    last_run_at  timestamptz,
    last_status  text NOT NULL DEFAULT '',
    created_at   timestamptz NOT NULL DEFAULT now(),
    UNIQUE (owner, name)
);

CREATE TABLE notifications (
    id          bigserial PRIMARY KEY,
    owner       text NOT NULL,
    at          timestamptz NOT NULL DEFAULT now(),
    title       text NOT NULL,
    body        text NOT NULL,
    session_id  uuid REFERENCES sessions(id) ON DELETE SET NULL,
    read        boolean NOT NULL DEFAULT false
);
CREATE INDEX notifications_owner ON notifications (owner, at DESC);
