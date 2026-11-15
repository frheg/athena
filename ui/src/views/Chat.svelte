<script>
  import { untrack } from 'svelte'
  import { get, post, patch, put, stream } from '../lib/api.js'
  import { t } from '../lib/i18n.js'
  import { when, num, dur } from '../lib/format.js'
  import { app, go, toast, onOwnerEvent, refreshStats, refreshModels } from '../lib/state.svelte.js'
  import Icon from '../lib/Icon.svelte'
  import Kpi from '../lib/Kpi.svelte'
  import Answer from './parts/Answer.svelte'
  import ToolCall from './parts/ToolCall.svelte'
  import ApprovalCard from './parts/ApprovalCard.svelte'

  let { id = '' } = $props()

  let sessions = $state([])
  let q = $state('')
  let showList = $state(false)
  let data = $state(null) // {session, messages, turns, tool_calls, approvals}
  let input = $state('')
  let sending = $state(false)
  let live = $state(null) // {turn, status, text, detail, recalled, model, round}
  let scroller
  let stop = null
  let reloadTimer

  async function loadSessions() {
    try {
      sessions = (await get('/sessions?q=' + encodeURIComponent(q))).sessions
    } catch (e) {
      toast(e.message)
    }
  }

  async function load(sid) {
    if (!sid) {
      data = null
      return
    }
    try {
      data = await get('/sessions/' + sid)
    } catch (e) {
      toast(e.message)
      data = null
      return
    }
    const active = data.turns.findLast((tr) => tr.status === 'running' || tr.status === 'waiting')
    if (active && (!live || live.turn !== active.id)) follow(active.id, active.status)
    scrollDown()
  }

  function reloadSoon() {
    clearTimeout(reloadTimer)
    reloadTimer = setTimeout(() => load(id), 250)
  }

  function follow(turn, status = 'running') {
    if (stop) stop()
    live = { turn, status, text: '', detail: '', recalled: [], model: '', round: 0 }
    stop = stream('/turns/' + turn + '/events', (type, d) => {
      if (!live || live.turn !== turn) return
      switch (type) {
        case 'status':
          live.status = d.status
          live.detail = d.detail || ''
          if (d.model) live.model = d.model
          if (d.round) live.round = d.round
          if (d.status === 'waiting') reloadSoon()
          break
        case 'text':
          live.text += d.delta
          scrollDown()
          break
        case 'text_reset':
          live.text = ''
          break
        case 'waiting_model':
          live.status = 'starting'
          live.detail = t('the model is starting or busy, next try in {s} s', { s: d.seconds })
          break
        case 'recalled':
          live.recalled = d
          break
        case 'tool_call':
        case 'tool_result':
        case 'approval':
        case 'approval_answer':
        case 'tainted':
          if (type === 'approval_answer') live.status = 'running'
          reloadSoon()
          break
        case 'done':
          live = null
          load(id)
          loadSessions()
          refreshStats()
          break
        case 'error':
          if (d && d.error && d.error !== 'cancelled' && !d.error.startsWith('superseded')) toast(t('The turn failed: {e}', { e: d.error }))
          live = null
          load(id)
          break
      }
    })
  }

  function scrollDown() {
    requestAnimationFrame(() => {
      if (scroller) scroller.scrollTop = scroller.scrollHeight
    })
  }

  async function send(text = input.trim()) {
    if (!text || sending) return
    sending = true
    try {
      if (!id) {
        const r = await post('/sessions', { text })
        input = ''
        await loadSessions()
        go('chat', r.session.id)
        follow(r.turn_id)
      } else {
        const r = await post('/sessions/' + id + '/messages', { text })
        input = ''
        await load(id)
        follow(r.turn_id)
      }
    } catch (e) {
      toast(e.message)
    } finally {
      sending = false
    }
  }

  async function cancel() {
    if (!live) return
    try {
      await post('/turns/' + live.turn + '/cancel')
    } catch (e) {
      toast(e.message)
    }
  }

  // one model for all of Athena: switching it here switches it everywhere, and the model server swaps on the next answer
  let switching = $state(false)
  async function pickModel(m) {
    switching = true
    try {
      await put('/models/active', { model: m })
      await refreshModels()
      toast(t('Athena now uses {m} everywhere. The first answer may take a while as the model loads.', { m: m || app.models.default }))
    } catch (e) {
      toast(e.message)
    } finally {
      switching = false
    }
  }

  async function rename() {
    const title = prompt(t('Name of the conversation'), data.session.title)
    if (title === null) return
    await patch('/sessions/' + id, { title })
    load(id)
    loadSessions()
  }

  async function archive() {
    if (!confirm(t('Archive this conversation? It leaves the list; the activity log keeps what happened.'))) return
    await patch('/sessions/' + id, { archived: true })
    go('chat')
    loadSessions()
  }

  function keydown(e) {
    if (e.key === 'Enter' && !e.shiftKey && !e.isComposing && !matchMedia('(max-width: 760px)').matches) {
      e.preventDefault()
      send()
    }
  }

  // grow the box with its text
  function autosize(el) {
    const fit = () => {
      el.style.height = 'auto'
      el.style.height = Math.min(el.scrollHeight, window.innerHeight * 0.4) + 'px'
    }
    el.addEventListener('input', fit)
    $effect(() => {
      input
      fit()
    })
  }

  // user and assistant messages; tool calls under the message that made them; numbers from the turn under the answer
  let rows = $derived.by(() => {
    if (!data) return []
    const byCall = new Map(data.tool_calls.map((c) => [c.turn_id + '/' + c.call_id, c]))
    const turns = new Map(data.turns.map((tr) => [tr.id, tr]))
    const callsPerTurn = new Map()
    for (const c of data.tool_calls) callsPerTurn.set(c.turn_id, (callsPerTurn.get(c.turn_id) || 0) + 1)
    const msgs = data.messages.filter((m) => m.role === 'user' || m.role === 'assistant')
    return msgs.map((m) => {
      const calls = (m.tool_calls || []).map((tc) => byCall.get(m.turn_id + '/' + tc.id)).filter(Boolean)
      const final = m.role === 'assistant' && !(m.tool_calls && m.tool_calls.length) && m.content
      let meta = null
      if (final && turns.has(m.turn_id)) {
        const tr = turns.get(m.turn_id)
        const secs = tr.finished_at ? (new Date(tr.finished_at) - new Date(tr.created_at)) / 1000 : null
        meta = { model: tr.model.replace(/^.*\//, '').replace(/\.gguf$/, ''), tokens: tr.tokens_in + tr.tokens_out,
          secs, tools: callsPerTurn.get(tr.id) || 0, tps: secs ? tr.tokens_out / secs : 0 }
      }
      return { ...m, calls, meta }
    })
  })

  let totals = $derived.by(() => {
    if (!data) return null
    let tokens = 0
    for (const tr of data.turns) tokens += tr.tokens_in + tr.tokens_out
    return { turns: data.turns.length, tokens, tools: data.tool_calls.length,
      denied: data.tool_calls.filter((c) => c.decision === 'deny').length }
  })

  let busy = $derived(live && live.status !== 'waiting')
  let current = $derived(app.models.active || '')
  let currentLoaded = $derived((app.models.models.find((m) => m.id === (current || app.models.default)) || {}).loaded)

  $effect(() => {
    load(id)
    return () => {
      if (stop) stop()
      stop = null
      live = null
    }
  })

  $effect(() => {
    untrack(loadSessions)
    return onOwnerEvent((type) => {
      if (type.startsWith('approval') && id) reloadSoon()
    })
  })

  const statusText = {
    running: t('Working'),
    thinking: t('Thinking'),
    starting: t('Waiting for the model'),
    waiting: t('Waiting for your approval'),
  }
  const suggestions = [t('What is the weather at home tomorrow?'), t('Which pods are not healthy?'), t('Remind me in an hour to stretch'), t('Find recent arXiv papers on retrieval')]
</script>

<div class="chat">
  <aside class="sessions" class:show={showList}>
    <div class="sessions-head">
      <div class="row">
        <span class="label grow">{t('Conversations')} <span class="num">{sessions.length}</span></span>
        <button class="primary" onclick={() => { go('chat'); showList = false }}><Icon name="plus" size={15} />{t('New')}</button>
      </div>
      <div class="search">
        <Icon name="search" size={15} />
        <input type="search" placeholder={t('Search conversations')} bind:value={q} oninput={loadSessions} aria-label={t('Search conversations')} />
      </div>
    </div>
    <ul>
      {#each sessions as s (s.id)}
        <li>
          <a href={'#/chat/' + s.id} class:active={s.id === id} onclick={() => (showList = false)}>
            <span class="s-title">{s.title || t('New conversation')}</span>
            <span class="s-meta">
              <span class="mono-sm">{when(s.updated_at)}</span>
              {#if s.principal !== 'user'}<span class="tag">{t('job')}</span>{/if}
              {#if s.tainted}<span class="tag warn" title={t('Has read outside text')}>{t('ext')}</span>{/if}
            </span>
          </a>
        </li>
      {:else}
        <li class="empty-note">{q ? t('No match.') : t('No conversations yet.')}</li>
      {/each}
    </ul>
  </aside>

  <section class="conversation">
    <header class="conv-head">
      <button class="ghost icon-btn list-toggle" onclick={() => (showList = !showList)} aria-label={t('Conversations')}><Icon name="menu" /></button>
      <div class="conv-title">
        <h3>{data ? data.session.title || t('New conversation') : t('New conversation')}</h3>
        {#if totals}
          <div class="conv-stats mono-sm">
            <span><b class="num">{totals.turns}</b> {t('turns')}</span>
            <span><b class="num">{num(totals.tokens)}</b> {t('tokens')}</span>
            <span><b class="num">{totals.tools}</b> {t('tool calls')}</span>
            {#if totals.denied}<span><b class="num">{totals.denied}</b> {t('denied')}</span>{/if}
            {#if data?.session.tainted}<span class="tag warn" title={t('This conversation has read text from outside (web pages, documents). Actions and messages now always ask you first, because that text could try to steer Athena.')}>{t('outside text')}</span>{/if}
          </div>
        {/if}
      </div>
      {#if data}
        <button class="ghost icon-btn" onclick={rename} title={t('Rename')} aria-label={t('Rename')}><Icon name="edit" size={16} /></button>
        <button class="ghost icon-btn" onclick={archive} title={t('Archive')} aria-label={t('Archive')}><Icon name="trash" size={16} /></button>
      {/if}
    </header>

    <div class="messages" bind:this={scroller}>
      {#if !data && !live}
        <div class="empty">
          <div class="label">{t('Athena')}</div>
          <h1>{t('What can I do for you?')}</h1>
          <p class="muted">{t('Ask, look things up, plan. Anything that changes something asks you first.')}</p>
          <div class="suggestions">
            {#each suggestions as s}<button class="pill" onclick={() => send(s)}>{s}</button>{/each}
          </div>
          {#if app.stats && app.status}
            <div class="kpis">
              <Kpi label={t('Today')} value={num(app.stats.turns_24h)} note={t('turns')} />
              <Kpi label={t('Tools')} value={app.status.servers.reduce((n, s) => n + s.tools, 0)} note={t('{n} servers up', { n: app.status.servers.filter((s) => s.up).length })} />
              <Kpi label={t('Waiting')} value={app.stats.approvals_waiting} note={t('approvals')} tone={app.stats.approvals_waiting ? 'warn' : ''} href="#/approvals" />
            </div>
          {/if}
        </div>
      {/if}
      {#each rows as m (m.id)}
        <div class="msg {m.role}">
          {#if m.role === 'user'}
            <div class="bubble">{m.content}</div>
          {:else}
            {#each m.calls as c (c.id)}<ToolCall call={c} />{/each}
            {#if m.content}<div class="bubble"><Answer text={m.content} /></div>{/if}
            {#if m.meta}
              <div class="msg-meta mono-sm">
                {#if m.meta.model}<span>{m.meta.model}</span>{/if}
                <span class="num">{num(m.meta.tokens)} tok</span>
                {#if m.meta.secs != null}<span class="num">{dur(m.meta.secs)}</span>{/if}
                {#if m.meta.tps > 0}<span class="num">{m.meta.tps.toFixed(1)} tok/s</span>{/if}
                {#if m.meta.tools}<span class="num">{m.meta.tools} {m.meta.tools === 1 ? t('tool') : t('tools')}</span>{/if}
              </div>
            {/if}
          {/if}
        </div>
      {/each}
      {#if data?.approvals.length}
        <div class="waiting">
          <div class="label">{t('Waiting for your answer')}</div>
          {#each data.approvals as a (a.id)}
            <ApprovalCard approval={a} onanswered={() => load(id)} />
          {/each}
        </div>
      {/if}
      {#if live}
        <div class="msg assistant">
          {#if live.recalled.length}
            <div class="mono-sm" title={live.recalled.map((r) => r.text).join('\n')}>{t('Recalled {n} memories', { n: live.recalled.length })}</div>
          {/if}
          {#if live.text}<div class="bubble"><Answer text={live.text} /></div>{/if}
          {#if live.status !== 'waiting' || !data?.approvals.length}
            <div class="status-line mono-sm">
              {#if live.status !== 'waiting'}<span class="pulse"></span>{/if}
              <span>{statusText[live.status] || live.status}</span>
              {#if live.round}<span class="num">{t('round')} {live.round}</span>{/if}
              {#if live.model}<span>{live.model}</span>{/if}
              {#if live.detail}<span>{live.detail}</span>{/if}
            </div>
          {/if}
        </div>
      {/if}
    </div>

    <div class="composer-wrap">
      <form class="composer" onsubmit={(e) => { e.preventDefault(); send() }}>
        <textarea bind:value={input} use:autosize onkeydown={keydown} rows="1" placeholder={t('Message Athena')} aria-label={t('Message')} disabled={sending}></textarea>
        <div class="composer-bar">
          {#if app.models.switchable && app.models.models.length}
            <label class="model-pick" title={t('The one model Athena uses, in every conversation and job. Only one model runs at a time; switching loads the new one on the next answer.')}>
              <span class="dot" class:ok={currentLoaded}></span>
              <select value={current} disabled={switching} onchange={(e) => pickModel(e.currentTarget.value)} aria-label={t('Model for all of Athena')}>
                <option value="">{app.models.default} ({t('default')})</option>
                {#each app.models.models.filter((m) => m.id !== app.models.default) as m (m.id)}
                  <option value={m.id}>{m.id}{m.loaded ? ' (' + t('loaded') + ')' : ''}{m.endpoint !== 'primary' ? ', ' + m.endpoint : ''}</option>
                {/each}
              </select>
            </label>
          {:else if app.models.default}
            <span class="mono-sm">{app.models.default}</span>
          {/if}
          <span class="mono-sm hide-narrow">{t('Enter to send, Shift+Enter for a new line')}</span>
          {#if live}
            <button type="button" class="icon-btn send" onclick={cancel} title={t('Stop')} aria-label={t('Stop')}><Icon name="stop" size={16} /></button>
          {:else}
            <button type="submit" class="primary icon-btn send" disabled={!input.trim() || sending || busy} aria-label={t('Send')}><Icon name="send" size={17} /></button>
          {/if}
        </div>
      </form>
    </div>
  </section>
</div>
