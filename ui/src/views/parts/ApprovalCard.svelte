<script>
  import { post } from '../../lib/api.js'
  import { args, toolName, classHelp, relative } from '../../lib/format.js'
  import { t } from '../../lib/i18n.js'
  import { toast, refreshCounts } from '../../lib/state.svelte.js'
  import Icon from '../../lib/Icon.svelte'

  let { approval, showSession = false, onanswered = () => {} } = $props()
  let busy = $state(false)

  async function answer(a) {
    busy = true
    try {
      await post('/approvals/' + approval.id, { answer: a })
      refreshCounts()
      onanswered(a)
    } catch (e) {
      toast(e.message)
    } finally {
      busy = false
    }
  }
</script>

<div class="approval">
  <div class="approval-head">
    <Icon name="approvals" size={16} />
    <span class="tool-name">{toolName(approval.tool)}</span>
    <span class="tag {approval.class}" title={classHelp[approval.class]}>{approval.class}</span>
    {#if approval.principal !== 'user'}<span class="tag warn" title={t('A scheduled job asked for this, not you.')}>{approval.principal}</span>{/if}
    <span class="mono-sm" style:margin-left="auto">{relative(approval.created_at)}</span>
  </div>
  <div class="small muted">{approval.reason}</div>
  {#if args(approval.args)}<pre>{args(approval.args)}</pre>{/if}
  {#if showSession}<a class="small" href={'#/chat/' + approval.session_id}>{t('Open the conversation')}</a>{/if}
  <div class="buttons">
    <button class="primary" disabled={busy} onclick={() => answer('once')} title={t('Run it this one time.')}><Icon name="check" size={15} />{t('Allow once')}</button>
    <button disabled={busy} onclick={() => answer('session')} title={t('Allow exactly this call again in this conversation without asking.')}>{t('This conversation')}</button>
    <button disabled={busy} onclick={() => answer('always')} title={t('Allow exactly this call, with these arguments, from now on. You can withdraw it under Settings.')}>{t('Always')}</button>
    <button class="danger" disabled={busy} onclick={() => answer('deny')}>{t('Deny')}</button>
  </div>
</div>
