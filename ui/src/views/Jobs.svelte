<script>
  import { get, post, del } from '../lib/api.js'
  import { t } from '../lib/i18n.js'
  import { toast, refreshCounts, onOwnerEvent, go } from '../lib/state.svelte.js'
  import { when, full, relative } from '../lib/format.js'
  import Icon from '../lib/Icon.svelte'
  import Kpi from '../lib/Kpi.svelte'
  import Answer from './parts/Answer.svelte'

  let jobs = $state([])
  let zone = $state('')
  let notes = $state([])
  let form = $state(null)

  const blank = { name: '', schedule: '0 7 * * 1-5', prompt: '', enabled: true, notify: true }

  async function load() {
    try {
      const [j, n] = await Promise.all([get('/jobs'), get('/notifications?limit=100')])
      jobs = j.jobs
      zone = j.zone
      notes = n.notifications
    } catch (e) {
      toast(e.message)
    }
  }

  async function save() {
    try {
      await post('/jobs', form)
      form = null
      load()
    } catch (e) {
      toast(e.message)
    }
  }

  async function run(j) {
    try {
      const r = await post('/jobs/' + j.id + '/run')
      go('chat', r.session_id)
    } catch (e) {
      toast(e.message)
    }
  }

  async function remove(j) {
    if (!confirm(t('Delete the job "{x}"?', { x: j.name }))) return
    await del('/jobs/' + j.id)
    load()
  }

  async function readAll() {
    await post('/notifications/read', { ids: [] })
    refreshCounts()
    load()
  }

  async function allowBrowser() {
    if ('Notification' in window) await Notification.requestPermission()
  }

  $effect(() => {
    load()
    return onOwnerEvent((type) => type === 'notification' && load())
  })

  let enabled = $derived(jobs.filter((j) => j.enabled).length)
  let next = $derived(jobs.filter((j) => j.next_run).sort((a, b) => new Date(a.next_run) - new Date(b.next_run))[0])
  let unread = $derived(notes.filter((n) => !n.read).length)
  let failing = $derived(jobs.filter((j) => j.last_status && j.last_status.startsWith('failed')).length)
</script>

<div class="page">
  <div class="page-head">
    <div>
      <div class="label">{t('Jobs')}</div>
      <h1>{t('Work on a schedule')}</h1>
      <p>{t('A job is a message Athena sends itself on a schedule, in a conversation of its own. On its own it may only look things up and remember; anything else waits in Approvals and you are told.')}</p>
    </div>
    {#if !form}<button class="primary" onclick={() => (form = { ...blank })}><Icon name="plus" size={15} />{t('New job')}</button>{/if}
  </div>

  <div class="kpis">
    <Kpi label={t('Jobs')} value={jobs.length} note={t('{n} enabled', { n: enabled })} />
    <Kpi label={t('Next run')} value={next ? relative(next.next_run).replace('in ', '') : '-'} note={next ? next.name : t('nothing planned')} />
    <Kpi label={t('Failing')} value={failing} note={t('last run')} tone={failing ? 'bad' : ''} />
    <Kpi label={t('Unread')} value={unread} note={t('{n} notifications', { n: notes.length })} tone={unread ? 'warn' : ''} />
  </div>

  {#if form}
    <form class="card" onsubmit={(e) => { e.preventDefault(); save() }} style:margin-bottom="0.75rem">
      <label class="field"><span class="label">{t('Name')}</span><input bind:value={form.name} required maxlength="60" /></label>
      <label class="field">
        <span class="label">{t('Schedule')}</span>
        <input bind:value={form.schedule} required class="tool-name" />
        <span class="small muted">{t('Five fields: minute hour day month weekday, in {z}. "0 7 * * 1-5" is 07:00 on weekdays, "*/30 * * * *" every half hour, @daily midnight.', { z: zone })}</span>
      </label>
      <label class="field"><span class="label">{t('What Athena should do')}</span>
        <textarea bind:value={form.prompt} rows="4" required placeholder={t('Give me the weather for home today and anything I should know about my travel.')}></textarea></label>
      <label class="check"><input type="checkbox" bind:checked={form.enabled} /> {t('Enabled')}</label>
      <label class="check"><input type="checkbox" bind:checked={form.notify} /> {t('Mail me the result')}</label>
      <div class="buttons">
        <button class="primary">{t('Save')}</button>
        <button type="button" onclick={() => (form = null)}>{t('Cancel')}</button>
      </div>
    </form>
  {/if}

  {#each jobs as j (j.id)}
    <div class="card">
      <div class="card-head" style:margin-bottom="0.4rem">
        <div class="row grow">
          <strong>{j.name}</strong>
          <span class="tag" class:solid={j.enabled}>{j.enabled ? t('enabled') : t('paused')}</span>
          {#if j.notify}<span class="tag">{t('mail')}</span>{/if}
        </div>
        <code class="mono-sm" title={t('Schedule in {z}', { z: zone })}>{j.schedule}</code>
      </div>
      <div class="small">{j.prompt}</div>
      <dl class="kv">
        <dt>{t('Next')}</dt><dd>{j.next_run ? full(j.next_run) + ' (' + relative(j.next_run) + ')' : '-'}</dd>
        <dt>{t('Last')}</dt><dd>{j.last_run_at ? when(j.last_run_at) + ', ' + j.last_status : t('never ran')}</dd>
      </dl>
      <div class="buttons">
        <button onclick={() => run(j)}><Icon name="play" size={14} />{t('Run now')}</button>
        <button onclick={() => (form = { ...j })}><Icon name="edit" size={14} />{t('Edit')}</button>
        <button class="danger" onclick={() => remove(j)}><Icon name="trash" size={14} />{t('Delete')}</button>
      </div>
    </div>
  {:else}
    {#if !form}<div class="card"><p class="empty-note">{t('No jobs yet. A morning brief is a good first one.')}</p></div>{/if}
  {/each}

  <div class="card-head" style:margin-top="2rem">
    <h2 style:margin="0">{t('Notifications')}</h2>
    <div class="row">
      {#if 'Notification' in window && Notification.permission === 'default'}
        <button class="ghost" onclick={allowBrowser}><Icon name="bell" size={15} />{t('Show on this device')}</button>
      {/if}
      <button class="ghost" onclick={readAll} disabled={!unread}>{t('Mark all read')}</button>
    </div>
  </div>
  {#each notes as n (n.id)}
    <div class="card" class:unread={!n.read}>
      <div class="card-head" style:margin-bottom="0.4rem">
        <strong class="grow">{n.title}</strong>
        <span class="mono-sm">{when(n.at)}</span>
      </div>
      <Answer text={n.body} />
      {#if n.session_id}<a class="small" href={'#/chat/' + n.session_id}>{t('Open the conversation')}</a>{/if}
    </div>
  {:else}
    <div class="card"><p class="empty-note">{t('No notifications.')}</p></div>
  {/each}
</div>
