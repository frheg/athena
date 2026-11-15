<script>
  import { app, refreshStats, refreshModels } from '../lib/state.svelte.js'
  import { t } from '../lib/i18n.js'
  import { num, dur, relative, toolName } from '../lib/format.js'
  import Kpi from '../lib/Kpi.svelte'
  import Bars from '../lib/Bars.svelte'
  import Meter from '../lib/Meter.svelte'

  $effect(() => {
    refreshStats()
    refreshModels()
  })

  let s = $derived(app.stats)
  let st = $derived(app.status)
  let days = $derived(s ? s.days : [])
  let dayLabel = (d) => new Date(d + 'T12:00:00').toLocaleDateString([], { weekday: 'short', day: 'numeric' })
  let turns14 = $derived(days.reduce((n, d) => n + d.turns, 0))
  let calls14 = $derived(days.reduce((n, d) => n + d.tool_calls, 0))
  let tokens14 = $derived(days.reduce((n, d) => n + d.tokens, 0))
  let maxCalls = $derived(s && s.tools.length ? Math.max(...s.tools.map((x) => x.calls)) : 1)
  let toolsTotal = $derived(st ? st.servers.reduce((n, x) => n + x.tools, 0) : 0)
  let endpoints = $derived(new Set(app.models.models.map((m) => m.endpoint)).size)
</script>

<div class="page">
  <div class="page-head">
    <div>
      <div class="label">{t('Overview')}</div>
      <h1>{t('How Athena is doing')}</h1>
      <p>{t('Activity, the permission gate, tools and models. Updated every 30 seconds.')}</p>
    </div>
    {#if st}<span class="mono-sm">{t('version')} {st.version} / {st.zone}</span>{/if}
  </div>

  {#if !s || !st}
    <p class="muted">{t('Loading…')}</p>
  {:else}
    <div class="kpis">
      <Kpi label={t('Turns, 24 h')} value={num(s.turns_24h)} note={t('{n} in all', { n: num(s.turns) })} />
      <Kpi label={t('Tokens, 7 d')} value={num(s.tokens_in_7d + s.tokens_out_7d)} note={t('{o} generated', { o: num(s.tokens_out_7d) })} />
      <Kpi label={t('Tool calls, 7 d')} value={num(s.tool_calls_7d)} note={t('{n} tools available', { n: toolsTotal })} />
      <Kpi label={t('Avg answer')} value={s.avg_turn_seconds_7d ? dur(s.avg_turn_seconds_7d) : '-'} note={t('per turn, 7 d')} />
      <Kpi label={t('Waiting')} value={s.approvals_waiting} note={t('approvals')} tone={s.approvals_waiting ? 'warn' : ''} href="#/approvals" />
      <Kpi label={t('Failed, 7 d')} value={s.failed_7d} note={t('turns')} tone={s.failed_7d ? 'bad' : ''} />
    </div>

    <div class="grid-2">
      <div class="card">
        <div class="card-head">
          <span class="label">{t('Turns per day, 14 d')}</span>
          <span class="mono-sm num">{num(turns14)} {t('turns')} / {num(calls14)} {t('calls')} / {num(tokens14)} tok</span>
        </div>
        <Bars values={days.map((d) => d.turns)} labels={days.map((d) => dayLabel(d.day))} height={96} unit={' ' + t('turns')} />
        <div class="axis mono-sm">
          <span>{days.length ? dayLabel(days[0].day) : ''}</span><span>{t('today')}</span>
        </div>
        <div class="split">
          <div>
            <div class="label">{t('Tool calls per day')}</div>
            <Bars values={days.map((d) => d.tool_calls)} labels={days.map((d) => dayLabel(d.day))} height={44} unit={' ' + t('calls')} />
          </div>
          <div>
            <div class="label">{t('Tokens per day')}</div>
            <Bars values={days.map((d) => d.tokens)} labels={days.map((d) => dayLabel(d.day))} height={44} unit=" tok" />
          </div>
        </div>
      </div>
      <div class="card">
        <div class="card-head">
          <span class="label">{t('Permission gate, 7 d')}</span>
          <a class="mono-sm" href="#/activity">{t('activity')}</a>
        </div>
        <Meter parts={[
          { label: t('allowed'), value: s.decisions_7d.allow || 0, tone: 'ok' },
          { label: t('asked you'), value: s.decisions_7d.ask || 0, tone: 'warn' },
          { label: t('denied'), value: s.decisions_7d.deny || 0, tone: 'bad' },
        ]} />
        <div class="kpis kpis-3" style:margin="1.1rem 0 0">
          <Kpi label={t('Standing')} value={s.permissions} note={t('permissions')} href="#/settings" />
          <Kpi label={t('Jobs')} value={s.jobs} note={t('{n} unread', { n: s.notifications_unread })} href="#/jobs" />
          <Kpi label={t('Audit log')} value={num(s.audit_entries)} note={t('entries')} href="#/activity" />
        </div>
      </div>
    </div>

    <div class="grid-2" style:margin-top="0.75rem">
      <div class="card">
        <div class="card-head"><span class="label">{t('Tools used, 7 d')}</span><span class="mono-sm">{t('calls / errors / avg')}</span></div>
        {#if s.tools.length}
          <ul class="list">
            {#each s.tools as x}
              <li>
                <div class="grow">
                  <div class="row"><span class="tool-name grow">{toolName(x.tool)}</span>
                    <span class="mono-sm num">{x.calls}{#if x.errors} / <span class="error">{x.errors}</span>{/if} / {dur(x.avg_ms / 1000)}</span></div>
                  <div class="rowbar"><div style:width={(x.calls / maxCalls) * 100 + '%'}></div></div>
                </div>
              </li>
            {/each}
          </ul>
        {:else}
          <p class="empty-note">{t('No tool calls in the last 7 days.')}</p>
        {/if}
      </div>
      <div class="card">
        <div class="card-head"><span class="label">{t('Models, 7 d')}</span><span class="mono-sm">{t('turns / out tok / avg')}</span></div>
        {#if s.models.length}
          <ul class="list">
            {#each s.models as m}
              <li>
                <span class="tool-name grow">{m.model.replace(/^.*\//, '').replace(/\.gguf$/, '')}</span>
                <span class="mono-sm num">{m.turns} / {num(m.tokens_out)} / {m.avg_seconds ? dur(m.avg_seconds) : '-'}</span>
              </li>
            {/each}
          </ul>
        {:else}
          <p class="empty-note">{t('No answers in the last 7 days.')}</p>
        {/if}
        <div class="label" style:margin-top="1rem">{t('Available')}</div>
        <ul class="list">
          {#each app.models.models as m (m.id)}
            <li>
              <span class="dot" class:ok={m.loaded}></span>
              <span class="tool-name grow">{m.id}</span>
              {#if m.id === (app.models.active || app.models.default)}<span class="tag solid">{t('active')}</span>{/if}
              {#if m.default}<span class="tag">{t('default')}</span>{/if}
              {#if endpoints > 1}<span class="tag">{m.endpoint}</span>{/if}
            </li>
          {:else}
            <li class="muted small">{t('The model servers do not list their models.')}</li>
          {/each}
        </ul>
      </div>
    </div>

    <div class="card" style:margin-top="0.75rem">
      <div class="card-head"><span class="label">{t('Services')}</span><span class="mono-sm">{t('checked every minute')}</span></div>
      <div class="table-wrap">
        <table class="table">
          <thead><tr><th>{t('Service')}</th><th>{t('State')}</th><th class="r">{t('Tools')}</th><th class="r">{t('Checked')}</th></tr></thead>
          <tbody>
            <tr><td class="tool-name">{t('database')}</td><td><span class="tag" class:ok={st.database?.up} class:bad={!st.database?.up}>{st.database?.up ? t('up') : t('down')}</span></td><td></td><td></td></tr>
            <tr><td class="tool-name">{t('memory')}</td><td><span class="tag" class:ok={st.memory.up} class:bad={!st.memory.up}>{st.memory.up ? t('up') : t('down')}</span>
              {#if !st.memory.up}<span class="mono-sm"> {st.memory.error}</span>{/if}</td><td></td><td></td></tr>
            <tr><td class="tool-name">{t('mail out')}</td><td><span class="tag">{st.info.notify ? t('configured') : t('off')}</span></td><td></td><td></td></tr>
            {#each st.servers as x}
              <tr>
                <td class="tool-name">{x.name}</td>
                <td><span class="tag" class:ok={x.up} class:bad={!x.up}>{x.up ? t('up') : t('down')}</span>
                  {#if x.error}<div class="mono-sm detail">{x.error}</div>{/if}</td>
                <td class="r num">{x.tools}</td>
                <td class="r mono-sm">{relative(x.checked_at)}</td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    </div>
  {/if}
</div>
