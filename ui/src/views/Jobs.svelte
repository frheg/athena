<script>
  import { get, post, del } from '../lib/api.js'
  import { t } from '../lib/i18n.js'
  import { toast, refreshCounts, onOwnerEvent, go } from '../lib/state.svelte.js'
  import { when, full } from '../lib/format.js'
  import Answer from './parts/Answer.svelte'

  let jobs = $state([])
  let zone = $state('')
  let notes = $state([])
  let form = $state(null)

  const blank = { name: '', schedule: '0 7 * * 1-5', prompt: '', enabled: true, notify: true }

  async function load() {
    try {
      const [j, n] = await Promise.all([get('/jobs'), get('/notifications?limit=50')])
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
</script>

<div class="page">
  <h1>{t('Jobs')}</h1>
  <p class="muted">
    {t('A job is a message Athena sends itself on a schedule, in a conversation of its own. On its own it may only look things up and remember; anything else waits in Approvals, and you are told.')}
  </p>
  {#each jobs as j (j.id)}
    <div class="card">
      <div class="row">
        <strong class="grow">{j.name}</strong>
        {#if !j.enabled}<span class="chip">{t('paused')}</span>{/if}
        <code title={t('Schedule (minute hour day month weekday), in {z}', { z: zone })}>{j.schedule}</code>
      </div>
      <div class="small">{j.prompt}</div>
      <div class="small muted">
        {#if j.next_run}{t('Next')}: {full(j.next_run)}. {/if}
        {#if j.last_run_at}{t('Last')}: {when(j.last_run_at)}, {j.last_status}.{/if}
      </div>
      <div class="buttons">
        <button onclick={() => run(j)}>{t('Run now')}</button>
        <button onclick={() => (form = { ...j })}>{t('Edit')}</button>
        <button class="danger" onclick={() => remove(j)}>{t('Delete')}</button>
      </div>
    </div>
  {:else}
    <p class="muted">{t('No jobs yet. A morning brief is a good first one.')}</p>
  {/each}

  {#if form}
    <form class="card" onsubmit={(e) => { e.preventDefault(); save() }}>
      <label>{t('Name')} <input bind:value={form.name} required maxlength="60" /></label>
      <label>
        {t('Schedule')}
        <input bind:value={form.schedule} required />
        <span class="small muted">{t('Five fields: minute hour day month weekday, in {z}. "0 7 * * 1-5" is 07:00 on weekdays; "*/30 * * * *" every half hour; @daily is midnight.', { z: zone })}</span>
      </label>
      <label>{t('What Athena should do')} <textarea bind:value={form.prompt} rows="4" required placeholder={t('Give me the weather for home today and anything I should know about my travel.')}></textarea></label>
      <label class="check"><input type="checkbox" bind:checked={form.enabled} /> {t('Enabled')}</label>
      <label class="check"><input type="checkbox" bind:checked={form.notify} /> {t('Mail me the result')}</label>
      <div class="buttons">
        <button class="primary">{t('Save')}</button>
        <button type="button" onclick={() => (form = null)}>{t('Cancel')}</button>
      </div>
    </form>
  {:else}
    <button class="primary" onclick={() => (form = { ...blank })}>{t('New job')}</button>
  {/if}

  <h2>{t('Notifications')}</h2>
  <div class="row">
    <button class="link small" onclick={readAll}>{t('Mark all as read')}</button>
    {#if 'Notification' in window && Notification.permission === 'default'}
      <button class="link small" onclick={allowBrowser}>{t('Show them on this device too')}</button>
    {/if}
  </div>
  {#each notes as n (n.id)}
    <div class="card" class:unread={!n.read}>
      <div class="row">
        <strong class="grow">{n.title}</strong>
        <span class="muted small">{when(n.at)}</span>
      </div>
      <Answer text={n.body} />
      {#if n.session_id}<a class="small" href={'#/chat/' + n.session_id}>{t('Open the conversation')}</a>{/if}
    </div>
  {:else}
    <p class="muted">{t('No notifications.')}</p>
  {/each}
</div>
