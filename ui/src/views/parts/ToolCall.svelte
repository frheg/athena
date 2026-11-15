<script>
  import { args, toolName, classHelp } from '../../lib/format.js'
  import { t } from '../../lib/i18n.js'

  let { call } = $props()
  let open = $state(false)

  const label = {
    running: t('running'),
    pending: t('waits for you'),
    ok: t('done'),
    error: t('failed'),
    denied: t('not run'),
  }
</script>

<div class="tool" class:open>
  <button class="tool-head" onclick={() => (open = !open)} aria-expanded={open}>
    <span class="tool-name">{toolName(call.tool)}</span>
    {#if call.class}<span class="cls cls-{call.class}" title={classHelp[call.class]}>{call.class}</span>{/if}
    <span class="st st-{call.status}">{label[call.status] || call.status}</span>
    {#if call.duration_ms}<span class="muted small">{(call.duration_ms / 1000).toFixed(1)} s</span>{/if}
    <span class="chev" aria-hidden="true">{open ? '▾' : '▸'}</span>
  </button>
  {#if open}
    <div class="tool-body">
      {#if args(call.args)}
        <div class="small muted">{t('Arguments')}</div>
        <pre>{args(call.args)}</pre>
      {/if}
      <div class="small muted">{t('Gate')}: {call.decision}, {call.reason}</div>
      {#if call.result}
        <div class="small muted">{t('Result')}</div>
        <pre class="result">{call.result}</pre>
      {/if}
    </div>
  {/if}
</div>
