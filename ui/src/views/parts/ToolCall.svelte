<script>
  import { args, toolName, classHelp } from '../../lib/format.js'
  import { t } from '../../lib/i18n.js'
  import Icon from '../../lib/Icon.svelte'

  let { call } = $props()
  let open = $state(false)

  const label = {
    running: t('running'),
    pending: t('waiting for you'),
    ok: t('done'),
    error: t('failed'),
    denied: t('not run'),
  }
</script>

<div class="tool" class:open>
  <button class="tool-head" onclick={() => (open = !open)} aria-expanded={open}>
    <Icon name="tool" size={14} />
    <span class="tool-name">{toolName(call.tool)}</span>
    {#if call.class}<span class="tag {call.class}" title={classHelp[call.class]}>{call.class}</span>{/if}
    <span class="tag {call.status}">{label[call.status] || call.status}</span>
    {#if call.duration_ms}<span class="mono-sm num">{call.duration_ms < 1000 ? call.duration_ms + ' ms' : (call.duration_ms / 1000).toFixed(1) + ' s'}</span>{/if}
    {#if call.result}<span class="mono-sm num hide-phone">{call.result.length.toLocaleString()} chars</span>{/if}
    <span class="chev"><Icon name="chevron" size={14} /></span>
  </button>
  {#if open}
    <div class="tool-body">
      <dl class="kv">
        <dt>{t('Gate')}</dt>
        <dd>{call.decision}: {call.reason}</dd>
        {#if call.class}<dt>{t('Class')}</dt><dd>{classHelp[call.class]}</dd>{/if}
      </dl>
      {#if args(call.args)}
        <div class="label">{t('Arguments')}</div>
        <pre>{args(call.args)}</pre>
      {/if}
      {#if call.result}
        <div class="label">{t('Result')}</div>
        <pre class="result">{call.result}</pre>
      {/if}
    </div>
  {/if}
</div>
