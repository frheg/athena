<script>
  import { onMount } from 'svelte'
  import { app, startOwnerStream } from './lib/state.svelte.js'
  import { t } from './lib/i18n.js'
  import Chat from './views/Chat.svelte'
  import Approvals from './views/Approvals.svelte'
  import Memory from './views/Memory.svelte'
  import Activity from './views/Activity.svelte'
  import Jobs from './views/Jobs.svelte'
  import Status from './views/Status.svelte'
  import Settings from './views/Settings.svelte'

  const views = [
    { id: 'chat', label: t('Chat'), icon: '💬' },
    { id: 'approvals', label: t('Approvals'), icon: '✋' },
    { id: 'memory', label: t('Memory'), icon: '🧠' },
    { id: 'jobs', label: t('Jobs'), icon: '⏰' },
    { id: 'activity', label: t('Activity'), icon: '📜' },
    { id: 'status', label: t('Status'), icon: '🩺' },
    { id: 'settings', label: t('Settings'), icon: '⚙️' },
  ]

  onMount(() => startOwnerStream())

  function badge(id) {
    if (id === 'approvals') return app.approvals
    if (id === 'jobs') return app.unread
    return 0
  }
</script>

<div class="shell">
  <nav class="nav" aria-label={t('Main')}>
    <div class="brand">Athena</div>
    {#each views as v}
      <a href={'#/' + v.id} class:active={app.route.view === v.id} title={v.label}>
        <span class="icon" aria-hidden="true">{v.icon}</span>
        <span class="label">{v.label}</span>
        {#if badge(v.id) > 0}<span class="badge">{badge(v.id)}</span>{/if}
      </a>
    {/each}
    {#if !app.online}<div class="offline" title={t('Athena does not answer right now')}>{t('offline')}</div>{/if}
  </nav>
  <main class="main">
    {#if app.route.view === 'chat'}
      <Chat id={app.route.id} />
    {:else if app.route.view === 'approvals'}
      <Approvals />
    {:else if app.route.view === 'memory'}
      <Memory />
    {:else if app.route.view === 'jobs'}
      <Jobs />
    {:else if app.route.view === 'activity'}
      <Activity />
    {:else if app.route.view === 'status'}
      <Status />
    {:else if app.route.view === 'settings'}
      <Settings />
    {:else}
      <p class="pad">{t('Nothing here.')} <a href="#/chat">{t('Back to chat')}</a></p>
    {/if}
  </main>
  {#if app.toast}<div class="toast" role="status">{app.toast}</div>{/if}
</div>
