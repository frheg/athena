<script>
  import { untrack } from 'svelte'
  import { get } from '../lib/api.js'
  import { t } from '../lib/i18n.js'
  import { app, toast } from '../lib/state.svelte.js'
  import { when, full, num, toolName } from '../lib/format.js'
  import Icon from '../lib/Icon.svelte'
  import Kpi from '../lib/Kpi.svelte'
  import Bars from '../lib/Bars.svelte'

  let rows = $state([])
  let event = $state('')
  let tool = $state('')
  let q = $state('')
  let more = $state(false)

  async function load(append = false) {
    const before = append && rows.length ? rows[rows.length - 1].id : 0
    const p = new URLSearchParams({ event, tool, q, before, limit: 100 })
    try {
      const r = await get('/audit?' + p)
      rows = append ? [...rows, ...r.audit] : r.audit
      more = r.audit.length === 100
    } catch (e) {
      toast(e.message)
    }
  }

  $effect(() => {
    event
    untrack(() => load())
  })

  const events = [
    ['', t('all')],
    ['turn.', t('turns')],
    ['model.', t('model')],
    ['tool.', t('tools')],
    ['approval.', t('approvals')],
    ['permission.', t('permissions')],
    ['memory.', t('memory')],
    ['job.', t('jobs')],
    ['notify.', t('mail')],
  ]

  function tone(r) {
    const d = r.detail || {}
    if (r.event === 'turn.fail' || d.status === 'error' || d.decision === 'deny') return 'bad'
    if (r.event.startsWith('approval') || d.decision === 'ask') return 'warn'
    if (r.event === 'turn.done' || d.status === 'ok') return 'ok'
    return ''
  }

  function detail(r) {
    const d = r.detail || {}
    switch (r.event) {
      case 'tool.decision':
        return `${d.decision}: ${d.reason}`
      case 'tool.result':
        return `${d.status}, ${d.ms} ms, ${d.chars} chars`
      case 'model.call':
        return `${(d.model || '').replace(/^.*\//, '')} via ${d.endpoint}: ${d.tokens_in} in, ${d.tokens_out} out, ${d.tool_calls} tool calls`
      default:
        return Object.keys(d).length ? JSON.stringify(d) : ''
    }
  }

  let s = $derived(app.stats)
</script>

<div class="page">
  <div class="page-head">
    <div>
      <div class="label">{t('Activity')}</div>
      <h1>{t('Everything Athena did')}</h1>
      <p>{t('In order, newest first. This log cannot be changed or deleted, not even by Athena.')}</p>
    </div>
  </div>

  {#if s}
    <div class="kpis">
      <Kpi label={t('Entries')} value={num(s.audit_entries)} note={t('in the log')} />
      <Kpi label={t('Turns, 24 h')} value={num(s.turns_24h)} note={t('{n} failed this week', { n: s.failed_7d })} />
      <Kpi label={t('Tool calls, 7 d')} value={num(s.tool_calls_7d)} note={t('{n} denied', { n: s.decisions_7d.deny || 0 })} />
      <div class="kpi">
        <span class="label">{t('Turns, 14 d')}</span>
        <Bars values={s.days.map((d) => d.turns)} labels={s.days.map((d) => d.day)} height={42} />
      </div>
    </div>
  {/if}

  <div class="toolbar">
    <div class="pills">
      {#each events as [v, l]}<button class="pill" class:on={event === v} onclick={() => (event = v)}>{l}</button>{/each}
    </div>
  </div>
  <form class="toolbar" onsubmit={(e) => { e.preventDefault(); load() }}>
    <input bind:value={tool} placeholder={t('Tool, for example web__fetch')} class="tool-name" />
    <div class="search grow" style:min-width="160px">
      <Icon name="search" size={15} />
      <input type="search" bind:value={q} placeholder={t('Text in the details')} />
    </div>
    <button>{t('Filter')}</button>
  </form>

  <div class="card">
    <div class="table-wrap">
      <table class="table">
        <thead><tr><th>{t('When')}</th><th>{t('Event')}</th><th class="hide-phone">{t('Who')}</th><th>{t('Tool')}</th><th class="hide-phone">{t('Details')}</th></tr></thead>
        <tbody>
          {#each rows as r (r.id)}
            <tr>
              <td class="nowrap mono-sm" title={full(r.at)}>{when(r.at)}</td>
              <td><span class="row" style:flex-wrap="nowrap"><span class="dot tone-{tone(r) || 'muted'}"></span>
                {#if r.session_id}<a class="tool-name" href={'#/chat/' + r.session_id}>{r.event}</a>{:else}<span class="tool-name">{r.event}</span>{/if}</span></td>
              <td class="hide-phone mono-sm">{r.principal}</td>
              <td class="tool-name">{toolName(r.tool)}</td>
              <td class="detail hide-phone small">{detail(r)}</td>
            </tr>
          {:else}
            <tr><td colspan="5" class="empty-note">{t('Nothing matches.')}</td></tr>
          {/each}
        </tbody>
      </table>
    </div>
  </div>
  {#if more}<div style:margin-top="1rem"><button onclick={() => load(true)}>{t('Older')}</button></div>{/if}
</div>
