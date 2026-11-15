<script>
  import { untrack } from 'svelte'
  import { get, post, patch, stream } from '../lib/api.js'
  import { t } from '../lib/i18n.js'
  import { when } from '../lib/format.js'
  import { go, toast, onOwnerEvent } from '../lib/state.svelte.js'
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
  let live = $state(null) // {turn, status, text, detail, recalled}
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
    if (active && (!live || live.turn !== active.id)) follow(active.id)
    scrollDown()
  }

  function reloadSoon() {
    clearTimeout(reloadTimer)
    reloadTimer = setTimeout(() => load(id), 250)
  }

  function follow(turn) {
    if (stop) stop()
    live = { turn, status: 'running', text: '', detail: '', recalled: [] }
    stop = stream('/turns/' + turn + '/events', (type, d) => {
      if (!live || live.turn !== turn) return
      switch (type) {
        case 'status':
          live.status = d.status
          live.detail = d.detail || ''
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
          live.detail = t('The model is starting or busy; waiting {s} s.', { s: d.seconds })
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
          break
        case 'error':
          if (d.error && d.error !== 'cancelled' && !d.error.startsWith('superseded')) toast(t('The turn failed: {e}', { e: d.error }))
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

  async function send() {
    const text = input.trim()
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

  async function rename() {
    const title = prompt(t('Name of the conversation'), data.session.title)
    if (title === null) return
    await patch('/sessions/' + id, { title })
    load(id)
    loadSessions()
  }

  async function archive() {
    if (!confirm(t('Archive this conversation? It disappears from the list; the audit log keeps what happened.'))) return
    await patch('/sessions/' + id, { archived: true })
    go('chat')
    loadSessions()
  }

  function keydown(e) {
    if (e.key === 'Enter' && !e.shiftKey && !e.isComposing) {
      e.preventDefault()
      send()
    }
  }

  // what the chat shows: user and assistant messages; tool calls under the assistant message that made them
  let rows = $derived.by(() => {
    if (!data) return []
    const byCall = new Map(data.tool_calls.map((c) => [c.turn_id + '/' + c.call_id, c]))
    return data.messages
      .filter((m) => m.role === 'user' || m.role === 'assistant')
      .map((m) => {
        let calls = []
        if (m.tool_calls && m.tool_calls.length) {
          calls = m.tool_calls.map((tc) => byCall.get(m.turn_id + '/' + tc.id)).filter(Boolean)
        }
        return { ...m, calls }
      })
  })

  let busy = $derived(live && live.status !== 'waiting')

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
    running: t('Working…'),
    thinking: t('Thinking…'),
    starting: t('Waiting for the model…'),
    waiting: t('Waiting for your approval'),
  }
</script>

<div class="chat">
  <aside class="sessions" class:show={showList}>
    <div class="sessions-head">
      <button class="primary" onclick={() => { go('chat'); showList = false }}>{t('New chat')}</button>
      <input type="search" placeholder={t('Search')} bind:value={q} oninput={loadSessions} aria-label={t('Search conversations')} />
    </div>
    <ul>
      {#each sessions as s (s.id)}
        <li>
          <a href={'#/chat/' + s.id} class:active={s.id === id} onclick={() => (showList = false)}>
            <span class="title">{s.title || t('New conversation')}</span>
            <span class="muted small">
              {#if s.principal !== 'user'}⏰ {/if}{when(s.updated_at)}
            </span>
          </a>
        </li>
      {:else}
        <li class="muted pad small">{q ? t('No match.') : t('No conversations yet.')}</li>
      {/each}
    </ul>
  </aside>

  <section class="conversation">
    <header class="conv-head">
      <button class="list-toggle" onclick={() => (showList = !showList)} aria-label={t('Conversations')}>☰</button>
      <h1>{data ? data.session.title || t('New conversation') : t('New chat')}</h1>
      {#if data?.session.tainted}
        <span class="chip warn" title={t('This conversation has read text from outside (web pages, documents). From now on, actions and messages always ask you first, because that text could try to steer Athena.')}>
          {t('read outside text')}
        </span>
      {/if}
      {#if data}
        <button class="link small" onclick={rename}>{t('Rename')}</button>
        <button class="link small" onclick={archive}>{t('Archive')}</button>
      {/if}
    </header>

    <div class="messages" bind:this={scroller}>
      {#if !data && !live}
        <div class="empty">
          <h2>{t('What can I do for you?')}</h2>
          <p class="muted">{t('Ask a question, look something up, or plan something. Anything that changes things asks you first.')}</p>
        </div>
      {/if}
      {#each rows as m (m.id)}
        <div class="msg {m.role}">
          {#if m.role === 'user'}
            <div class="bubble">{m.content}</div>
          {:else}
            {#if m.content}<div class="bubble"><Answer text={m.content} /></div>{/if}
            {#each m.calls as c (c.id)}<ToolCall call={c} />{/each}
          {/if}
        </div>
      {/each}
      {#if data?.approvals.length}
        <div class="waiting">
          <div class="small muted">{t('Athena waits for your answer:')}</div>
          {#each data.approvals as a (a.id)}
            <ApprovalCard approval={a} onanswered={() => load(id)} />
          {/each}
        </div>
      {/if}
      {#if live}
        <div class="msg assistant live">
          {#if live.recalled.length}
            <div class="small muted" title={live.recalled.map((r) => r.text).join('\n')}>🧠 {t('Remembered {n} thing(s) about you', { n: live.recalled.length })}</div>
          {/if}
          {#if live.text}<div class="bubble"><Answer text={live.text} /></div>{/if}
          {#if live.status !== 'waiting' || !data?.approvals.length}
            <div class="status-line">
              {#if live.status !== 'waiting'}<span class="spinner" aria-hidden="true"></span>{/if}
              {statusText[live.status] || live.status}
              {#if live.detail}<span class="muted small">{live.detail}</span>{/if}
            </div>
          {/if}
        </div>
      {/if}
    </div>

    <form class="composer" onsubmit={(e) => { e.preventDefault(); send() }}>
      <textarea bind:value={input} onkeydown={keydown} rows="2" placeholder={t('Message Athena (Enter sends, Shift+Enter for a new line)')} aria-label={t('Message')} disabled={sending}></textarea>
      {#if live}
        <button type="button" class="danger" onclick={cancel}>{t('Stop')}</button>
      {/if}
      <button type="submit" class="primary" disabled={!input.trim() || sending || busy} title={busy ? t('Athena is still working on the last message') : ''}>{t('Send')}</button>
    </form>
  </section>
</div>
