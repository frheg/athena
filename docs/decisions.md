# Decisions

Choices made while building, with the reason. The ones marked **default** were not discussed beforehand: they are
what seemed right, and any of them can be changed. Newest last.

## Agreed in discussion

1. **MCP plus our own rules.** Tool servers speak MCP; the class of each tool comes from Athena's policy file, never
   from the server. An unknown tool is denied.
2. **Three permission layers** (server capability, the gate in `core`, you and the audit log) and **five classes**
   (read, write, act, communicate, remember).
3. **Go** for `core` and the services; **Svelte** (plain, with Vite) for the UI.
4. **Runs on Docker alone**, bringing its own data services in the `deps` profile; in a cluster it uses shared
   platform services instead. The code never knows which.
5. **One user**, but every row has an owner.
6. **Turns run in the background** and survive a closed browser; waiting turns survive a restart.
7. **SSE** for turn events, plain POSTs for the rest (WebSocket only for voice later).
8. **Memory auto-recall** before each turn plus a search tool.
9. **mcp-web:** search and fetch with an SSRF guard, no headless browser, PDFs only noted.
10. **mcp-sources** is its own server (Wikipedia, arXiv, GitHub), split out of mcp-web: fixed API hosts only.
11. **mcp-everyday:** MET Norway for weather everywhere (it is global, so Open-Meteo was dropped), Kartverket and
    Nominatim for places, Entur, Norges Bank, time and holidays, a calculator, Bring only with a key.
12. **memory in Go.** Embeddings come from an external OpenAI-compatible `/v1/embeddings` service, so there is no
    Python need. Python waits for voice.
13. **mcp-sandbox** is on the map, to be defined later.

## Defaults chosen while building

### core

14. **default** The user is named by `OWNER` (default `owner`). There is no login inside Athena: the gate in front
    does sign-in and adds `API_TOKEN` as a bearer token; core trusts requests that carry it.
15. **default** No policy file means every tool is denied (core still starts). The file is re-read when it
    changes (checked every 30 s); a broken edit keeps the last good policy and is logged.
16. **default** A new message while a turn is **running** is refused (409). A new message while a turn **waits**
    for approval supersedes it: its waiting calls are dropped and get a "did not run" result, so the history stays
    whole.
17. **default** Answering an approval after its turn was cancelled records the answer and runs nothing.
18. **default** A tool call left without a result by a crash gets a placeholder result when the turn resumes; the
    model API refuses a history with an unanswered call otherwise.
19. **default** Limits: 8 model rounds per turn (the last round gets no tools and must answer), 30 tool calls per
    minute per conversation, 30 000 characters per tool result, 90 s per tool call.
20. **default** Memory recall runs once per turn (first round), top 5 above the memory service's threshold, with an
    8 s timeout. If memory is down, the turn goes on without it and says so in the status line.
21. **default** Standing permissions ("always") do not expire unless the API call sets `expires_in_days`. They are
    listed and can be withdrawn under Settings. "This conversation" ends with the conversation.
22. **default** Turn events stay in memory for 30 minutes after a turn ends (at most 2000 per turn). After a
    restart, a finished turn's stream closes at once from the database.
23. **default** The conversation title is the first message, cut at 60 characters; it can be renamed.
24. **default** History sent to the model: the last 40 messages (`HISTORY_MESSAGES`).
25. **default** Tool names are `<server>__<tool>`, with the server name from `TOOL_SERVERS`.
26. **default** The user a call is made for travels to tool servers in the header `X-Athena-Subject`. Servers with
    per-user data (memory, notes) use it, and trust it only from a caller that has a token.

### Jobs and reminders

27. **default** A job runs in a new conversation each time, as principal `job:<name>`. The example policy lets
    jobs do `read` and `remember` on their own; anything else is queued in Approvals and you are notified.
28. **default** A job's result is stored as a notification and mailed (when `NOTIFY_URL` is set) by core itself,
    not through a tool call, so it needs no approval. The model can only send mail through `notify__send`, which
    goes through the gate.
29. **default** Schedules are five-field cron (`@hourly`, `@daily`, `@weekly`, `@monthly` too), in `TIME_ZONE`.
    Runs missed while core was down are not caught up.
30. **default** Notes and reminders are their own server, `mcp-notes` (class write, except reading). A reminder is
    delivered by core: every minute it claims the due reminders (each is handed out once), stores a notification
    and mails it.

### Services

31. **default** **memory**: the tenant is the caller (from its token), the subject is the user. An item that is
    more than 0.92 similar to an existing one replaces it instead of adding a copy. Recall threshold 0.45. Without
    Qdrant or an embedder it falls back to word search in Postgres. Items that could not be embedded (the embedder
    asleep) are embedded later by a background loop. A different embedding size than the collection's is refused:
    a new model needs a new collection and `POST /v1/reindex`.
32. **default** **notify** sends only to recipients named in its own configuration (`RECIPIENTS=owner=...`), at most
    20 messages an hour. Without `SMTP_HOST` it is a dry run that only logs, so a fresh install cannot mail anyone.
33. **default** **mcp-cluster-act** restarts by deleting the pods of a listed target (its controller makes new
    ones). Nothing in the manifests changes, so GitOps tools have nothing to revert. At most one restart per target
    every 2 minutes. GPU acquire and release go through the arbiter, for listed consumers only, up to `max_ttl`.
34. **default** The cluster servers use plain REST against the Kubernetes API instead of client-go: a handful of
    calls, a much smaller binary.
35. **default** Untrusted (tainting) tools in the example policy: `web__*`, `sources__*` (text written by others),
    `cluster__logs`. `everyday__*` does not taint: it returns structured data from fixed public APIs; its free-text
    fields are named `quoted_*`.
36. **default** mcp-web reports search engines that did not answer, so an empty result is not read as "nothing
    exists".

### UI

37. **default** Hash routing (`#/chat/<id>`), so the static server needs no rewrites.
38. **default** Remote images in answers are links until you press "Load images" for that answer. Raw HTML from
    the model is shown as text. CSP allows only Athena's own scripts and styles.
39. **default** Interface text goes through `t()` with English as the key; Norwegian is added by filling one
    dictionary. Only English is offered for now.
40. **default** A web app manifest is there now; push notifications come with the proactive step on phones. While
    the tab is open but hidden, notifications show as browser notifications if you allow it.

### Running it

41. **default** Every Go image is distroless static, non-root, read-only root file system, all capabilities
    dropped. Health checks run the binary itself (`/app health`), since there is no shell.
42. **default** Compose: one Postgres with three databases (core `athena`, `memory`, `notes`); the gate is the only
    published port, on `127.0.0.1:8080` by default. The standalone gate uses nginx basic auth with the password
    from `.env`.
43. **default** The small Go services stay on (a few MB of memory each); scale-to-zero is for models, where it
    matters.
44. **default** Images are pinned by tag and digest. The optional llama.cpp image is pinned by digest only (its
    tags are build numbers).
45. **default** Neo4j is not used yet: it comes when a real use needs relations.
