<script>
  import { untrack } from 'svelte'
  import { get } from '../lib/api.js'
  import { t } from '../lib/i18n.js'
  import { toast } from '../lib/state.svelte.js'
  import { full, toolName } from '../lib/format.js'

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
    ['', t('everything')],
    ['turn.', t('turns')],
    ['model.', t('model calls')],
    ['tool.', t('tool calls')],
    ['approval.', t('approvals')],
    ['permission.', t('permissions')],
    ['memory.', t('memory edits')],
    ['job.', t('jobs')],
    ['notify.', t('messages sent')],
  ]

  function detail(r) {
    const d = r.detail || {}
    switch (r.event) {
      case 'tool.decision':
        return `${d.decision}: ${d.reason}`
      case 'tool.result':
        return `${d.status}, ${d.ms} ms, ${d.chars} chars`
      case 'model.call':
        return `${d.model || ''} (${d.endpoint}), ${d.tokens_in}+${d.tokens_out} tokens, ${d.tool_calls} tool call(s)`
      default:
        return Object.keys(d).length ? JSON.stringify(d) : ''
    }
  }
</script>

<div class="page wide">
  <h1>{t('Activity')}</h1>
  <p class="muted">{t('Everything Athena did, in order. This log cannot be changed or deleted, not even by Athena.')}</p>
  <form class="row" onsubmit={(e) => { e.preventDefault(); load() }}>
    <select bind:value={event} aria-label={t('Kind of event')}>
      {#each events as [v, l]}<option value={v}>{l}</option>{/each}
    </select>
    <input bind:value={tool} placeholder={t('Tool, for example web__fetch')} />
    <input class="grow" type="search" bind:value={q} placeholder={t('Text in the details')} />
    <button>{t('Filter')}</button>
  </form>
  <div class="table-wrap">
    <table class="table">
      <thead><tr><th>{t('When')}</th><th>{t('Who')}</th><th>{t('Event')}</th><th>{t('Tool')}</th><th>{t('Details')}</th></tr></thead>
      <tbody>
        {#each rows as r (r.id)}
          <tr>
            <td class="nowrap small">{full(r.at)}</td>
            <td class="small">{r.principal}</td>
            <td class="small">{#if r.session_id}<a href={'#/chat/' + r.session_id}>{r.event}</a>{:else}{r.event}{/if}</td>
            <td class="small">{toolName(r.tool)}</td>
            <td class="small detail">{detail(r)}</td>
          </tr>
        {/each}
      </tbody>
    </table>
  </div>
  {#if more}<button onclick={() => load(true)}>{t('Older')}</button>{/if}
</div>
