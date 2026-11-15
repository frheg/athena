<script>
  import { get, del } from '../lib/api.js'
  import { t, languages, language, setLanguage } from '../lib/i18n.js'
  import { app, toast, refreshStats } from '../lib/state.svelte.js'
  import { toolName, classHelp, args, when } from '../lib/format.js'
  import Kpi from '../lib/Kpi.svelte'

  let tools = $state(null)
  let perms = $state([])
  let cls = $state('')

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
      refreshStats()
    } catch (e) {
      toast(e.message)
    }
  }

  $effect(() => {
    load()
  })

  const defaults = { allow: t('runs'), ask: t('asks you'), ask_once: t('asks once per conversation'), deny: t('refused') }
  const classes = ['read', 'write', 'act', 'communicate', 'remember']
  let byClass = $derived(tools ? Object.fromEntries(classes.map((c) => [c, tools.tools.filter((x) => x.class === c).length])) : {})
  let shown = $derived(tools ? tools.tools.filter((x) => !cls || x.class === cls) : [])
</script>

<div class="page">
  <div class="page-head">
    <div>
      <div class="label">{t('Settings')}</div>
      <h1>{t('What Athena may do')}</h1>
      <p>{t('Tools and their class come from the policy file, which Athena reloads when it changes. A tool that is not listed there is never offered and always refused.')}</p>
    </div>
    <label class="field" style:margin="0">
      <span class="label">{t('Language')}</span>
      <select value={language()} onchange={(e) => setLanguage(e.currentTarget.value)}>
        {#each languages as l}<option value={l.code}>{l.name}</option>{/each}
      </select>
    </label>
  </div>

  {#if tools}
    <div class="kpis">
      <Kpi label={t('Tools')} value={tools.tools.length} note={t('{n} servers', { n: tools.servers.length })} />
      <Kpi label={t('Read')} value={byClass.read} note={t('run freely')} />
      <Kpi label={t('Change')} value={(byClass.write || 0) + (byClass.act || 0)} note={t('ask first')} tone="warn" />
      <Kpi label={t('Send out')} value={byClass.communicate || 0} note={t('ask first')} tone="bad" />
      <Kpi label={t('Rules')} value={tools.rules ? tools.rules.length : 0} note={t('by argument')} />
      <Kpi label={t('Standing')} value={perms.length} note={t('permissions')} />
    </div>
  {/if}

  <h2>{t('Standing permissions')}</h2>
  <p class="muted small">{t('Calls you allowed with "Always" or "This conversation". Each covers one tool with exactly these arguments.')}</p>
  <div class="card">
    <ul class="list">
      {#each perms as p (p.id)}
        <li>
          <span class="tool-name">{toolName(p.tool)}</span>
          <code class="grow mono-sm">{JSON.stringify(p.args)}</code>
          <span class="tag">{p.session_id ? t('conversation') : t('always')}</span>
          {#if p.expires_at}<span class="mono-sm">{t('until')} {when(p.expires_at)}</span>{/if}
          <button class="danger" onclick={() => revoke(p)}>{t('Withdraw')}</button>
        </li>
      {:else}
        <li class="empty-note">{t('None.')}</li>
      {/each}
    </ul>
  </div>

  {#if tools}
    <h2>{t('Tools')}</h2>
    <div class="toolbar">
      <div class="pills">
        <button class="pill" class:on={cls === ''} onclick={() => (cls = '')}>{t('all')} <span class="num">{tools.tools.length}</span></button>
        {#each classes as c}
          <button class="pill" class:on={cls === c} onclick={() => (cls = c)} title={classHelp[c]}>{c} <span class="num">{byClass[c] || 0}</span></button>
        {/each}
      </div>
    </div>
    <div class="card">
      <div class="table-wrap">
        <table class="table">
          <thead><tr><th>{t('Tool')}</th><th>{t('Class')}</th><th>{t('By default')}</th><th class="hide-phone">{t('What it does')}</th></tr></thead>
          <tbody>
            {#each shown as x (x.name)}
              <tr>
                <td class="nowrap"><span class="tool-name">{toolName(x.name)}</span>
                  {#if x.untrusted}<span class="tag warn" title={t('Its results are text from outside. After reading one, actions and messages ask again.')}>{t('outside')}</span>{/if}</td>
                <td>{#if x.class}<span class="tag {x.class}" title={classHelp[x.class]}>{x.class}</span>{/if}</td>
                <td class="small nowrap">{defaults[x.default] || x.default}</td>
                <td class="small muted hide-phone">{x.description}</td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    </div>

    {#if tools.rules?.length}
      <h2>{t('Rules')}</h2>
      <div class="card">
        <div class="table-wrap">
          <table class="table">
            <thead><tr><th>{t('Tool')}</th><th>{t('When the arguments are')}</th><th>{t('Then')}</th></tr></thead>
            <tbody>
              {#each tools.rules as r}
                <tr><td class="tool-name">{toolName(r.tool)}</td><td><code>{JSON.stringify(r.args || {})}</code></td>
                  <td><span class="tag" class:ok={r.decision === 'allow'} class:bad={r.decision === 'deny'}>{r.decision}</span> <span class="small muted">{r.note || ''}</span></td></tr>
              {/each}
            </tbody>
          </table>
        </div>
      </div>
    {/if}
    <p class="mono-sm" style:margin-top="1rem">
      {t('Limits: {r} model rounds per turn, {c} tool calls per minute per conversation. Jobs may do on their own: {j}.', { r: tools.limits.rounds_per_turn, c: tools.limits.calls_per_minute, j: tools.jobs_may.join(', ') })}
    </p>
  {/if}
  {#if app.status}
    <p class="mono-sm">{t('Policy file')}: {app.status.info.policy_file}</p>
  {/if}
</div>
