# Athena

A personal assistant you talk to in a web page. It looks things up, remembers what matters about you, does work on
a schedule, and acts on your systems only through actions you list, with your approval.

Small parts, each with one job, that talk over HTTP and [MCP](https://modelcontextprotocol.io). Every tool call
passes a permission gate; everything is logged. Runs on plain Docker; the same images run in Kubernetes against
shared data services.

- [docs/spec.md](docs/spec.md): what it is and how it works
- [docs/decisions.md](docs/decisions.md): the choices made, and why

## Run it

You need Docker with Compose, and a model server with tool calling (or GGUF files for the bundled llama.cpp).

```sh
cd deploy
cp .env.example .env        # set the passwords and tokens: openssl rand -hex 24
docker compose --profile deps up -d --build
```

Open <http://localhost:8080> and sign in with `GATE_USER` and `GATE_PASSWORD`.

- `--profile deps` starts Postgres, Qdrant and SearXNG. Leave it out to use your own; set the URLs in `.env`.
- `--profile model` starts llama.cpp for chat and embeddings, with `MODEL_FILE` and `EMBED_FILE` from
  `deploy/models/`. Or set `LLM_URL` (and `EMBED_URL`) to any OpenAI-compatible server.
- Mail goes out only when `SMTP_HOST` is set, and only to the names in `RECIPIENTS`. Until then it is a dry run.

The policy is [policy/policy.example.yaml](policy/policy.example.yaml): which tools exist for Athena, their class,
and the rules. Edit it and core picks up the change within 30 seconds.

## The parts

| Service | What it does |
|---|---|
| `core` | turns, the gate, approvals, audit log, scheduler, API |
| `ui` | the web page |
| `mcp-web` | web search and reading pages |
| `mcp-sources` | Wikipedia, arXiv, GitHub |
| `mcp-everyday` | weather, places, travel, currency, time, calculator |
| `mcp-notes` | notes and reminders |
| `memory` | what Athena remembers about you |
| `notify` | mail out, to listed recipients only |
| `files` | what you attach to a chat: text read out of documents, previews of images |
| `speech` | answers read aloud (Piper voices, Norwegian and English; Python, and the image includes GPL-3.0 software) |
| `mcp-cluster-read`, `mcp-cluster-act` | a Kubernetes cluster: look, and the listed actions |

Each MCP server works with any MCP client, not only Athena.

## Develop

```sh
go test ./...                                   # unit tests
docker run -d --rm -p 127.0.0.1:55432:5432 -e POSTGRES_PASSWORD=test -e POSTGRES_DB=athena postgres:17.10-alpine
ATHENA_TEST_DATABASE_URL='postgres://postgres:test@127.0.0.1:55432/athena?sslmode=disable' go test ./...

cd ui && npm ci && npm run dev                  # the UI, with /api sent to a core on localhost:8080 (CORE_URL)
```

Images: `docker build --target core .` (one target per Go service) and `docker build ui`.
