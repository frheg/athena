<script>
  import { get, del } from '../lib/api.js'
  import { t, languages, language, setLanguage } from '../lib/i18n.js'
  import { toast } from '../lib/state.svelte.js'
  import { toolName, classHelp, args, when } from '../lib/format.js'

  let tools = $state(null)
  let perms = $state([])

  async function load() {
    try {
      const [a, b] = await Promise.all([get('/tools'), get('/permissions')])
      tools = a
      perms = b.permissions
    } catch (e) {
      toast(e.message)
    }
  }

  async function revoke(p) {
    try {
      await del('/permissions/' + p.id)
      load()
    } catch (e) {
      toast(e.message)
    }
  }

  $effect(() => {
    load()
  })

  const defaults = { allow: t('runs'), ask: t('asks you'), ask_once: t('asks once per conversation'), deny: t('refused') }
</script>

<div class="page wide">
  <h1>{t('Settings')}</h1>

  <h2>{t('Language')}</h2>
  <select value={language()} onchange={(e) => setLanguage(e.target.value)} aria-label={t('Language')}>
    {#each languages as l}<option value={l.code}>{l.name}</option>{/each}
  </select>

  <h2>{t('Standing permissions')}</h2>
  <p class="muted small">{t('Calls you allowed with "Always" or "This conversation". Each covers exactly one tool with exactly these arguments.')}</p>
  {#each perms as p (p.id)}
    <div class="card row">
      <strong>{toolName(p.tool)}</strong>
      <pre class="grow">{args(p.args) || '{}'}</pre>
      <span class="small muted">
        {p.session_id ? t('this conversation') : t('always')}{#if p.expires_at}, {t('until')} {when(p.expires_at)}{/if}
      </span>
      <button class="danger" onclick={() => revoke(p)}>{t('Withdraw')}</button>
    </div>
  {:else}
    <p class="muted">{t('None.')}</p>
  {/each}

  {#if tools}
    <h2>{t('Tools and what they may do')}</h2>
    <p class="muted small">
      {t('Set in the policy file; Athena reloads it when it changes. A tool that is not listed there is never offered and always refused.')}
      {t('Scheduled jobs may do on their own:')} <strong>{tools.jobs_may.join(', ')}</strong>.
    </p>
    <div class="table-wrap">
      <table class="table">
        <thead><tr><th>{t('Tool')}</th><th>{t('Class')}</th><th>{t('By default')}</th><th>{t('What it does')}</th></tr></thead>
        <tbody>
          {#each tools.tools as x (x.name)}
            <tr>
              <td class="nowrap">{toolName(x.name)}{#if x.untrusted} <span class="chip warn" title={t('Its results are text from outside. After reading one, actions and messages ask again.')}>{t('outside text')}</span>{/if}</td>
              <td>{#if x.class}<span class="cls cls-{x.class}" title={classHelp[x.class]}>{x.class}</span>{/if}</td>
              <td class="small">{defaults[x.default] || x.default}</td>
              <td class="small">{x.description}</td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
    {#if tools.rules?.length}
      <h2>{t('Rules')}</h2>
      <table class="table">
        <thead><tr><th>{t('Tool')}</th><th>{t('When the arguments are')}</th><th>{t('Then')}</th></tr></thead>
        <tbody>
          {#each tools.rules as r}
            <tr><td>{toolName(r.tool)}</td><td><code>{JSON.stringify(r.args || {})}</code></td><td>{r.decision}{#if r.note}: {r.note}{/if}</td></tr>
          {/each}
        </tbody>
      </table>
    {/if}
    <p class="small muted">{t('Limits: {r} model rounds per turn, {c} tool calls per minute per conversation.', { r: tools.limits.rounds_per_turn, c: tools.limits.calls_per_minute })}</p>
  {/if}
</div>
