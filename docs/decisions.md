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
20. **default** Memory recall runs once when a turn starts or resumes and is kept for all its rounds, top 5 above the memory service's threshold, with an
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

### Models and the UI (later)

46. **One model at a time** (agreed). Only one LLM runs on the GPU; a second big model next to a partly offloaded
    one starves it of memory and CPU. So the model choice is global, not per conversation: the picker in the chat
    sets the one model all of Athena uses (`PUT /api/models/active`, stored as a setting), and every model call
    reads it, so a switch applies from the next step on. The model server swaps models; core never needs two.
47. **default** The picker offers only the models of the endpoints in `MODEL_SWITCH_ENDPOINTS` (default
    `primary`), so a fallback server is never chosen by hand. On kratos there is no fallback at all.
48. **default** `GET /api/stats` gives the numbers the UI shows everywhere: turns, tokens, tool calls per day for
    14 days, gate decisions, tool and model use for 7 days, waiting approvals, jobs, permissions, audit size.
49. **default** The UI follows a warm, quiet style: neutral surfaces, one sage accent, mono uppercase labels for
    data, a serif for headings (Geist, Geist Mono, Instrument Serif, served by Athena itself). No emoji; icons
    are inline line drawings. Side rail on laptops, an icon rail on tablets, a tab bar with a More sheet on phones.

### Attachments (later)

50. **Attachments count as outside text** (agreed). A message with files taints its conversation, like a web page:
    acts and messages ask again from then on. Their text reaches the model inside `<untrusted>` like tool results.
51. **default** Files are their own service, `files`, with its own database, like memory and notes. At upload it
    reads the text out once (plain text and code in UTF-8, UTF-16 or Latin-1; PDF text layers; Word, PowerPoint,
    Excel and OpenDocument text) and makes a JPEG preview of an image, at most 1536 px on the long side and turned
    upright by its EXIF orientation. The original is kept. Up to 20 MB a file (`MAX_FILE_MB`), 2 GB per user
    (`QUOTA_MB`). A scanned PDF keeps only the text it has, with a note; HEIC and archives are stored, not read.
52. **default** Only PNG, JPEG, GIF and WebP are ever shown in the page. Every other file, HTML and SVG included, is
    sent as a download with `nosniff` and a sandbox CSP, so an upload can never run in Athena's origin.
53. **default** What the model sees of attachments: the newest message's files up to 20 000 characters each and
    60 000 together, older messages' files 4 000 each (the whole history goes to the model every round). The rest is
    read with `files__read` and searched with `files__find`. The 4 newest images in the history are sent as images,
    to models listed in `VISION_MODELS` only; others get the file's name and a note that they cannot see it.
54. **default** At most 10 files per message. A file taken out of the message before sending is deleted. Files stay
    when a conversation is archived; there is no file list in the UI yet.
55. **default** The prompt is laid out for the model server's prompt cache: the system prompt holds only what stays
    the same (the date, not the time), and what changes every turn (the time, recalled memories, the "has read
    outside text" note) is a `<context>` block before the newest user message. The server then reads only the new
    end of a conversation instead of all of it every turn (reading a prompt is the slow part when the model's
    experts are in RAM).

### Speech (later)

56. **Speech plays in the browser first** (agreed), and later also from kratos itself to a Bluetooth speaker. Over
    Bluetooth, the browser path plays wherever the phone or laptop sends its sound.
57. **default** Text to speech is its own service, `speech`: Piper voices behind an OpenAI-compatible
    `/v1/audio/speech`, so core works with any such server (`TTS_URL`). It is the one part in Python (Piper is
    Python and GPL-3.0). The image holds three voices, checked by sha256 at build: `no_NO-talesyntese-medium`
    (default Norwegian), `no_NO-nvcc-medium` (ten speakers) and `en_GB-cori-high` (default English). It picks
    Norwegian or English from the text itself. It runs on the CPU (about 50 times faster than real time for the
    medium voices) and needs no network.
58. **default** Core turns the answer's markdown into speakable text (code blocks and link targets left out, list
    items and table rows as sentences) and makes the audio a piece of whole sentences at a time
    (`GET /api/messages/{id}/speech?part=n`, a short first piece), so playing starts at once and the next piece
    loads while one plays. Voices and speed are one setting for all devices (Settings); "read new answers aloud"
    is per device. A browser only plays sound after you have touched the page, so it can refuse to start reading
    by itself; the answer's play button always works.
