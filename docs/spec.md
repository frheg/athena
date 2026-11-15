# Athena: spec

Status: steps 1 to 6 are built (section 12). Choices made while building are in [decisions.md](decisions.md).

Athena is a personal assistant you talk to through a web page, and later by voice. It answers, looks things up,
helps build and test things, remembers what matters about you, does some work on its own (jobs, reminders), and can
act on the systems it is allowed to touch, but only within limits you define.

It is a new design. It takes the lessons of an earlier assistant (one large API with a five-agent network and
about 25 tools) but none of its code.

## 1. Principles

1. **Small parts, one job each** (Unix philosophy). They talk over HTTP and MCP. A new ability is a new tool server,
   not a change to the core.
2. **The model is not trusted.** It reads web pages, mail and documents that can carry hidden instructions. Every
   limit holds even when the model is fooled: limits live in the infrastructure and in code, never in the prompt.
3. **Least privilege.** Each part can do only what it needs. Acting on systems is opt-in, per action.
4. **Runs anywhere with Docker.** One `docker compose up` gives a working Athena with its own data services. In a
   cluster the same images use shared services instead. The code never knows which.
5. **General parts.** Every service is useful to other programs too: memory, notes, notify and the tool servers know
   nothing about Athena beyond a caller name.
6. **Visible.** Every tool call, decision and memory can be seen, and undone where possible.
7. **Cold starts are fine.** A model that takes a minute to wake is shown as "starting", not as an error.
8. **Iterative.** Each step adds one thing that works end to end.

## 2. What it is for

- **Scope:** everything: talking, questions, research, building and testing things. It must stay extensible.
- **Interfaces:** a web page first. Voice later. Other channels (Telegram or similar) are noted for later.
- **Proactive as well as answering:** jobs on a schedule and reminders, delivered in the web page and by mail.
- **Acts on systems** (for example the cluster it runs on) only through actions that are listed explicitly.
- **Remembers you** across conversations: facts, preferences, people, places. You can see, correct and delete it.
- **Languages:** English first, Norwegian too. Answers in the language of the question.

## 3. Components

| Part | Job | Image target |
|---|---|---|
| `ui` | the web page: chat, approvals, memory, jobs and notifications, activity, status, settings | `ui/Dockerfile` |
| `core` | conversations, the tool loop, the gate, approvals, the audit log, the scheduler, model access, the API | `core` |
| `mcp-web` | web search (SearXNG) and page reading | `mcp-web` |
| `mcp-sources` | Wikipedia, arXiv, GitHub (fixed API hosts) | `mcp-sources` |
| `mcp-everyday` | weather, places, travel, currency, time and holidays, calculator and units, parcels | `mcp-everyday` |
| `mcp-notes` | notes and reminders | `mcp-notes` |
| `memory` | personal memory: items in Postgres, vectors in Qdrant, embeddings from a model server | `memory` |
| `notify` | sends messages out (mail now) to configured recipients only | `notify` |
| `mcp-cluster-read` | read-only cluster view: pods, deployments, events, logs, nodes, GPU, metrics | `mcp-cluster-read` |
| `mcp-cluster-act` | the listed cluster actions: restart a target, GPU acquire and release | `mcp-cluster-act` |
| `gate` | the one door: sign-in, `/` to the UI, `/api` to core | nginx |
| later | `mcp-sandbox` (run code safely, to define), `mcp-mail-read`, documents ingest, voice | |

Parts are split along **privilege lines**: anything with its own credentials or rights gets its own server.

Clients talk only to `core`. Only `core` talks to the model and to tool servers. `memory`, `notify` and `mcp-notes`
also have a small REST API (recall and the memory view; sending; due reminders).

### A turn, step by step

1. You send a message. `core` stores it, starts the turn in the background and returns its id. The page follows
   the turn's events over SSE (and can reconnect any time without losing any).
2. `core` recalls relevant memories and sends the conversation and the allowed tools to the model.
3. The model answers, or asks for tool calls.
4. Each call goes through the **gate** (section 6): allow, ask or deny. "Ask" stores an approval and the turn waits,
   in the database, for as long as it takes.
5. Allowed calls go to the tool server over MCP; results go back to the model. Repeat from 3, at most 8 rounds.
6. The answer streams to the page. The audit log has every step.

## 4. Tools: MCP plus our own rules

Tool servers speak **MCP** over streamable HTTP. `core` is the MCP client: it asks each configured server for its
tools every minute and calls them. Any MCP server can be plugged in, ours or someone else's.

- **The class of every tool is set in Athena's policy file**, never taken from the server. A tool with no class in
  the policy is not offered to the model and is denied if asked for.
- **Names are `<server>__<tool>`**, so two servers cannot shadow each other.
- **Results are data, not instructions.** Text from outside is wrapped as `<untrusted source=...>`, and reading an
  untrusted tool taints the conversation (section 6.2).
- **Limits per call:** 90 s, 30 000 characters (longer results are cut and say so).
- **Per-caller tokens:** each server accepts only callers with a token (`MCP_TOKENS`); the caller's name is the
  tenant for servers that keep data.

## 5. Deployment

### 5.1 Docker only

`deploy/docker-compose.yml`. The `deps` profile adds Postgres, Qdrant and SearXNG; the `model` profile adds
llama.cpp for chat and embeddings. Leave a profile out and point `.env` at your own services instead. Only the
gate is published.

### 5.2 In a cluster

The same images. The data services are shared platform services, not part of Athena, so other projects use them
too. The manifests live in the operator's infrastructure repo, not here.

### 5.3 Configuration

Environment variables only; each `cmd/*/main.go` lists its own. The main ones:

| Variable | Used by | For |
|---|---|---|
| `DATABASE_URL` | core, memory, mcp-notes | Postgres (one database each) |
| `LLM_URL`, `LLM_MODEL`, `LLM_FALLBACK_*` | core | models (OpenAI-compatible) |
| `TOOL_SERVERS`, `TOOL_TOKENS` | core | the MCP servers and the token for each |
| `POLICY_FILE` | core | the permission policy |
| `MEMORY_URL`, `NOTIFY_URL`, `REMINDERS_URL` (+ `_TOKEN`) | core | recall, job results by mail, due reminders |
| `API_TOKEN`, `OWNER`, `TIME_ZONE` | core | the gate's token, the user's name, the zone for dates and jobs |
| `QDRANT_URL`, `EMBED_URL`, `EMBED_MODEL` | memory | vectors (optional: word search without) |
| `SEARXNG_URL` | mcp-web | web search |
| `RECIPIENTS`, `SMTP_*` | notify | who may get mail, and how |
| `NAMESPACES`, `ACTIONS_FILE`, `GPUCTL_URL`, `METRICS_URL` | cluster servers | what they may see and do |
| `LISTEN`, `MCP_TOKENS` | every service | address, accepted callers |

Every part retries its database at start, honours `503` with `Retry-After` from model servers, and has `/health`,
`/ready` and `/metrics`.

### 5.4 Sharing data services with other projects

| Service | Athena's share |
|---|---|
| Postgres | one database and role per service (`athena`, `memory`, `notes`); each keeps its own migrations table |
| Qdrant | the collection `athena_memory`; points carry `tenant` and `subject` |
| Neo4j | not used yet; when it is: labels prefixed `Athena` and a `tenant` property |

## 6. Permissions

Three layers. Each one alone must stop a mistake: if the model is fooled, layer 2 stops it; if the gate has a bug,
layer 1 stops it; if a rule is too broad, layer 3 shows it and lets you take it back.

### 6.1 Layer 1: what a server can do at all

- Each server has a narrow job and its own credentials: `mcp-cluster-read` a read-only service account,
  `mcp-cluster-act` its own account that can only delete pods in the namespaces of its listed targets, `notify` its
  own mail login and a fixed list of recipients.
- An action that is not built into a server, or not listed in its file, does not exist.
- mcp-web reaches only public addresses (checked on the connected IP and on every redirect).
- The model never sees a secret.

### 6.2 Layer 2: the gate in `core`

Every call goes through the gate, in this order:

1. **The class** of the tool, from the policy file. Unknown: deny.
2. **Rules** on the tool and its arguments, first match wins (`{to: owner}: allow`, `re:` patterns for values).
3. **A standing permission** for exactly these arguments.
4. **The class default:** read allow, write ask once per conversation, act ask, communicate ask, remember allow.
5. **Who started the turn:** a job (`job:<name>`) may do only the classes the policy lets jobs do; anything else is
   queued for you.
6. **Taint:** once a conversation has read untrusted content, every act and communicate call asks, even when a rule
   or a standing permission would allow it. This blocks the classic leak: private data + untrusted content + a way
   out.
7. **Limits:** rounds per turn, calls per minute.

### 6.3 Layer 3: you decide, and everything is visible

- **Approval card** with the tool, the class, the reason and the exact arguments: allow once, this conversation,
  always (exactly these arguments), or no.
- **Standing permissions** are listed under Settings and can be withdrawn.
- **Audit log:** an append-only table (a trigger refuses updates and deletes), shown under Activity.

### 6.4 The five classes

| Class | Changes | Examples | Default |
|---|---|---|---|
| read | nothing | weather, web search, pods, your notes | allow (web, sources and logs taint) |
| write | your own things in Athena, easy to undo | a note, a reminder, forgetting a memory | ask once per conversation |
| act | something outside Athena | restart a target, take the GPU | ask |
| communicate | something leaves and cannot be taken back | send a mail | ask; mail to yourself allowed by a rule, unless tainted |
| remember | a fact about you for later | "lives in Bergen" | allow; shown and editable under Memory |

## 7. Memory

| Kind | Store |
|---|---|
| Conversations, turns, tool calls, approvals, audit, jobs, notifications | Postgres (`athena`) |
| Personal memory | Postgres (`memory`, the items) + Qdrant `athena_memory` (search) |
| Notes and reminders | Postgres (`notes`) |
| Documents, relations | later (Qdrant, Neo4j) |

## 8. Proactive work

- **Jobs:** a prompt on a cron schedule, run by the scheduler in `core` as principal `job:<name>` in a conversation
  of its own. The result becomes a notification and a mail. If the job wants to do more than jobs may, it waits in
  Approvals and you are told.
- **Reminders:** set through `mcp-notes`; core delivers them as notifications and mail.
- Later: event-driven jobs (an alert fires, a mail arrives), push notifications on the phone, other channels.

## 9. Models

- Any OpenAI-compatible endpoint with tool calling; a primary and a fallback (used on connection errors, 5xx and
  429, not on 400).
- `503` with `Retry-After` (a model waking up, a busy GPU) is waited out for up to 3 minutes and shown as
  "starting".
- `<think>` blocks are removed from answers.

## 10. Stack and repo layout

```
cmd/<service>/       one main per service
internal/            api, engine (turns, hub, scheduler, cron), gate, llm, toolbox, store, pg, mcpx,
                     web, sources, everyday, notes, memory, notify, cluster, netguard, env, probe
ui/                  Svelte and Vite; nginx serves the build
policy/              example policy and cluster actions
deploy/              docker-compose.yml, gate, SearXNG and Postgres setup, .env.example
docs/                this spec, decisions
Dockerfile           all Go images, one target each
```

## 11. Observability and evaluation

- JSON logs, one line per event. `/metrics` per part (core: running turns, waiting approvals, tool servers up).
- Every turn's steps are in the audit log, linked to its conversation.
- Tests: unit tests per package; engine, API, memory and notes tests against a real Postgres
  (`ATHENA_TEST_DATABASE_URL`) with a scripted model and an in-process MCP server.
- Later: an evaluation set (questions with expected tool use) to run when the model or a prompt changes.

## 12. Steps

1. Done. `core` + `ui` + one model: streaming chat, conversations in Postgres, Compose with `deps`.
2. Done. MCP and the gate: mcp-web, mcp-sources, mcp-everyday; policy file, audit log, tool trace in the UI.
3. Done. Approvals: write tools (mcp-notes), approval cards, standing permissions.
4. Done. Personal memory: the `memory` service, auto-recall, the memory view.
5. Done. Cluster: `mcp-cluster-read` and `mcp-cluster-act` with restart and GPU actions.
6. Done. Proactive: the scheduler, jobs, reminders, notifications, `notify` (mail to you).
7. Next: `mcp-sandbox`, push notifications, mail reading, documents and knowledge, relations, voice, channels.

## 13. Open questions

1. `mcp-sandbox`: what it may run (languages, network, time and memory limits) and where (a container per call?).
2. Mail reading: a dedicated mailbox for Athena (recommended) or access to your own.
3. Which cluster actions come next.
4. Authentication when the page is reachable outside a private network (basic auth at the gate is enough inside).
