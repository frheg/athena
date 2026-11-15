<script>
  import { get } from '../lib/api.js'
  import { t } from '../lib/i18n.js'
  import { when } from '../lib/format.js'

  let s = $state(null)
  let error = $state('')

  async function load() {
    try {
      s = await get('/status')
      error = ''
    } catch (e) {
      error = e.message
    }
  }

  $effect(() => {
    load()
    const iv = setInterval(load, 15000)
    return () => clearInterval(iv)
  })
</script>

<div class="page">
  <h1>{t('Status')}</h1>
  {#if error}<p class="error">{error}</p>{/if}
  {#if s}
    <table class="table">
      <tbody>
        <tr><th>{t('Version')}</th><td>{s.version}</td></tr>
        <tr><th>{t('Database')}</th><td>{s.database?.up ? '✅ ' + t('up') : '❌ ' + t('down')}</td></tr>
        <tr><th>{t('Model')}</th><td>{s.info.model || t('(server default)')}{#if s.info.fallback_model} · {t('fallback')} {s.info.fallback_model}{/if}</td></tr>
        <tr>
          <th>{t('Memory')}</th>
          <td>{s.memory.up ? '✅ ' + t('up') : '❌ ' + s.memory.error}</td>
        </tr>
        <tr><th>{t('Mail out')}</th><td>{s.info.notify ? '✅ ' + t('configured') : t('not configured')}</td></tr>
        <tr><th>{t('Running turns')}</th><td>{s.running_turns}</td></tr>
        <tr><th>{t('Time zone')}</th><td>{s.zone}</td></tr>
      </tbody>
    </table>
    <h2>{t('Tool servers')}</h2>
    <table class="table">
      <thead><tr><th>{t('Server')}</th><th>{t('State')}</th><th>{t('Tools')}</th><th>{t('Checked')}</th></tr></thead>
      <tbody>
        {#each s.servers as x}
          <tr>
            <td>{x.name}</td>
            <td>{x.up ? '✅ ' + t('up') : '❌ ' + (x.error || t('not checked yet'))}</td>
            <td>{x.tools}</td>
            <td class="small">{when(x.checked_at)}</td>
          </tr>
        {:else}
          <tr><td colspan="4" class="muted">{t('No tool servers configured (TOOL_SERVERS).')}</td></tr>
        {/each}
      </tbody>
    </table>
  {/if}
</div>
