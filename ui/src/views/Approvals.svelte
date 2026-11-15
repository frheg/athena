<script>
  import { get } from '../lib/api.js'
  import { t } from '../lib/i18n.js'
  import { toast, onOwnerEvent } from '../lib/state.svelte.js'
  import { toolName, when } from '../lib/format.js'
  import ApprovalCard from './parts/ApprovalCard.svelte'

  let waiting = $state([])
  let history = $state([])
  let loaded = $state(false)

  async function load() {
    try {
      const [w, all] = await Promise.all([get('/approvals?status=waiting'), get('/approvals?status=all&limit=50')])
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

  const answerText = {
    once: t('allowed once'),
    session: t('allowed for the conversation'),
    always: t('allowed always'),
  }
</script>

<div class="page">
  <h1>{t('Approvals')}</h1>
  <p class="muted">{t('Calls the gate stopped for you to decide. "Always" and "This conversation" only cover exactly the same call with the same arguments.')}</p>
  {#each waiting as a (a.id)}
    <ApprovalCard approval={a} showSession onanswered={load} />
  {:else}
    {#if loaded}<p class="muted">{t('Nothing waits for you.')}</p>{/if}
  {/each}

  {#if history.length}
    <h2>{t('Answered')}</h2>
    <table class="table">
      <thead><tr><th>{t('When')}</th><th>{t('Tool')}</th><th>{t('By')}</th><th>{t('Answer')}</th></tr></thead>
      <tbody>
        {#each history as a (a.id)}
          <tr>
            <td class="nowrap">{when(a.answered_at || a.created_at)}</td>
            <td><a href={'#/chat/' + a.session_id}>{toolName(a.tool)}</a></td>
            <td>{a.principal}</td>
            <td>{a.status === 'allowed' ? answerText[a.scope] || a.scope : a.status === 'expired' ? t('dropped (you moved on)') : t('refused')}</td>
          </tr>
        {/each}
      </tbody>
    </table>
  {/if}
</div>
