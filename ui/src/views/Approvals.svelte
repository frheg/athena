<script>
  import { get } from '../lib/api.js'
  import { t } from '../lib/i18n.js'
  import { app, toast, onOwnerEvent } from '../lib/state.svelte.js'
  import { toolName, when } from '../lib/format.js'
  import Kpi from '../lib/Kpi.svelte'
  import Meter from '../lib/Meter.svelte'
  import ApprovalCard from './parts/ApprovalCard.svelte'

  let waiting = $state([])
  let history = $state([])
  let loaded = $state(false)

  async function load() {
    try {
      const [w, all] = await Promise.all([get('/approvals?status=waiting'), get('/approvals?status=all&limit=200')])
      waiting = w.approvals
      history = all.approvals.filter((a) => a.status !== 'waiting')
      loaded = true
    } catch (e) {
      toast(e.message)
    }
  }

  $effect(() => {
    load()
    return onOwnerEvent((type) => type.startsWith('approval') && load())
  })

  let counts = $derived.by(() => {
    const c = { once: 0, session: 0, always: 0, denied: 0, expired: 0 }
    for (const a of history) {
      if (a.status === 'allowed') c[a.scope] = (c[a.scope] || 0) + 1
      else if (a.status === 'denied') c.denied++
      else c.expired++
    }
    return c
  })
  let fromJobs = $derived(waiting.filter((a) => a.principal !== 'user').length)

  const answerText = {
    once: t('allowed once'),
    session: t('allowed for the conversation'),
    always: t('allowed always'),
  }
</script>

<div class="page">
  <div class="page-head">
    <div>
      <div class="label">{t('Approvals')}</div>
      <h1>{t('Waiting for you')}</h1>
      <p>{t('Calls the gate stopped for you to decide. "Always" and "This conversation" cover only the same call with the same arguments.')}</p>
    </div>
  </div>

  <div class="kpis">
    <Kpi label={t('Waiting')} value={waiting.length} note={fromJobs ? t('{n} from jobs', { n: fromJobs }) : t('from you')} tone={waiting.length ? 'warn' : ''} />
    <Kpi label={t('Answered')} value={history.length} note={t('last 200')} />
    <Kpi label={t('Allowed')} value={counts.once + counts.session + counts.always} note={t('{n} always', { n: counts.always })} />
    <Kpi label={t('Refused')} value={counts.denied} note={t('{n} dropped', { n: counts.expired })} />
    <Kpi label={t('Standing')} value={app.stats ? app.stats.permissions : '-'} note={t('permissions')} href="#/settings" />
  </div>

  {#each waiting as a (a.id)}
    <div style:margin-bottom="0.7rem"><ApprovalCard approval={a} showSession onanswered={load} /></div>
  {:else}
    {#if loaded}<div class="card"><p class="empty-note">{t('Nothing waits for you.')}</p></div>{/if}
  {/each}

  {#if history.length}
    <h2>{t('Answered')}</h2>
    <div class="card">
      <Meter parts={[
        { label: t('once'), value: counts.once, tone: 'ok' },
        { label: t('conversation'), value: counts.session, tone: 'accent' },
        { label: t('always'), value: counts.always, tone: 'warn' },
        { label: t('refused'), value: counts.denied, tone: 'bad' },
        { label: t('dropped'), value: counts.expired, tone: 'muted' },
      ]} />
      <div class="table-wrap" style:margin-top="1rem">
        <table class="table">
          <thead><tr><th>{t('When')}</th><th>{t('Tool')}</th><th class="hide-phone">{t('Class')}</th><th class="hide-phone">{t('By')}</th><th>{t('Answer')}</th></tr></thead>
          <tbody>
            {#each history as a (a.id)}
              <tr>
                <td class="nowrap mono-sm">{when(a.answered_at || a.created_at)}</td>
                <td><a class="tool-name" href={'#/chat/' + a.session_id}>{toolName(a.tool)}</a></td>
                <td class="hide-phone"><span class="tag {a.class}">{a.class}</span></td>
                <td class="hide-phone mono-sm">{a.principal}</td>
                <td>{#if a.status === 'allowed'}<span class="tag ok">{answerText[a.scope] || a.scope}</span>
                  {:else if a.status === 'expired'}<span class="tag">{t('dropped')}</span>
                  {:else}<span class="tag bad">{t('refused')}</span>{/if}</td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    </div>
  {/if}
</div>
