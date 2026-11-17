<script>
  import { onMount } from 'svelte'
  import { app, layout, startOwnerStream } from './lib/state.svelte.js'
  import { t } from './lib/i18n.js'
  import { num } from './lib/format.js'
  import Icon from './lib/Icon.svelte'
  import Chat from './views/Chat.svelte'
  import Overview from './views/Overview.svelte'
  import Approvals from './views/Approvals.svelte'
  import Memory from './views/Memory.svelte'
  import Activity from './views/Activity.svelte'
  import Jobs from './views/Jobs.svelte'
  import Settings from './views/Settings.svelte'

  const views = [
    { id: 'chat', label: t('Chat'), icon: 'chat' },
    { id: 'overview', label: t('Overview'), icon: 'overview' },
    { id: 'approvals', label: t('Approvals'), icon: 'approvals' },
    { id: 'memory', label: t('Memory'), icon: 'memory' },
    { id: 'jobs', label: t('Jobs'), icon: 'jobs' },
    { id: 'activity', label: t('Activity'), icon: 'activity' },
    { id: 'settings', label: t('Settings'), icon: 'settings' },
  ]
  // the phone's bottom bar has room for five; the rest are under More
  const bottom = ['chat', 'overview', 'approvals', 'memory']
  let more = $state(false)

  onMount(() => startOwnerStream())

  function badge(id) {
    if (id === 'approvals') return app.approvals
    if (id === 'jobs') return app.unread
    return 0
  }

  let serversUp = $derived(app.status ? app.status.servers.filter((s) => s.up).length : 0)
  let serversAll = $derived(app.status ? app.status.servers.length : 0)
  let moreBadge = $derived(views.filter((v) => !bottom.includes(v.id)).reduce((n, v) => n + badge(v.id), 0))
</script>

<div class="shell" class:focus={layout.focus && app.route.view === 'chat'}>
  <nav class="rail" aria-label={t('Main')}>
    <a class="brand" href="#/chat">Athena</a>
    <div class="rail-links">
      {#each views as v}
        <a href={'#/' + v.id} class:active={app.route.view === v.id}>
          <Icon name={v.icon} />
          <span>{v.label}</span>
          {#if badge(v.id) > 0}<span class="count">{badge(v.id)}</span>{/if}
        </a>
      {/each}
    </div>
    <div class="rail-foot">
      <div class="label">{t('System')}</div>
      <div class="foot-row"><span class="dot" class:ok={app.online} class:bad={!app.online}></span>{app.online ? t('Online') : t('Offline')}</div>
      {#if app.status}
        <div class="foot-row mono-sm">{t('Tools')} <b class="num">{serversUp}/{serversAll}</b></div>
        <div class="foot-row mono-sm" title={t('The model Athena uses now')}>{app.models.active || app.models.default || app.status.info.model}</div>
      {/if}
      {#if app.stats}
        <div class="foot-row mono-sm">{t('Today')} <b class="num">{num(app.stats.turns_24h)}</b> {t('turns')}</div>
      {/if}
    </div>
  </nav>

  <main class="main">
    {#if app.route.view === 'chat'}
      <Chat id={app.route.id} />
    {:else if app.route.view === 'overview' || app.route.view === 'status'}
      <Overview />
    {:else if app.route.view === 'approvals'}
      <Approvals />
    {:else if app.route.view === 'memory'}
      <Memory />
    {:else if app.route.view === 'jobs'}
      <Jobs />
    {:else if app.route.view === 'activity'}
      <Activity />
    {:else if app.route.view === 'settings'}
      <Settings />
    {:else}
      <div class="page"><p class="muted">{t('Nothing here.')} <a href="#/chat">{t('Back to chat')}</a></p></div>
    {/if}
  </main>

  <nav class="tabbar" aria-label={t('Main')}>
    {#each views.filter((v) => bottom.includes(v.id)) as v}
      <a href={'#/' + v.id} class:active={app.route.view === v.id} onclick={() => (more = false)}>
        <Icon name={v.icon} size={20} />
        <span>{v.label}</span>
        {#if badge(v.id) > 0}<span class="count">{badge(v.id)}</span>{/if}
      </a>
    {/each}
    <button class:active={more || !bottom.includes(app.route.view)} onclick={() => (more = !more)} aria-expanded={more}>
      <Icon name="more" size={20} />
      <span>{t('More')}</span>
      {#if moreBadge > 0}<span class="count">{moreBadge}</span>{/if}
    </button>
  </nav>

  {#if more}
    <button class="sheet-backdrop" aria-label={t('Close')} onclick={() => (more = false)}></button>
    <div class="sheet" role="dialog" aria-label={t('More')}>
      {#each views.filter((v) => !bottom.includes(v.id)) as v}
        <a href={'#/' + v.id} onclick={() => (more = false)}>
          <Icon name={v.icon} />
          <span>{v.label}</span>
          {#if badge(v.id) > 0}<span class="count">{badge(v.id)}</span>{/if}
        </a>
      {/each}
    </div>
  {/if}

  {#if app.toast}<div class="toast" role="status">{app.toast}</div>{/if}
</div>
