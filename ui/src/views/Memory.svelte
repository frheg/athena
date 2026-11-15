<script>
  import { untrack } from 'svelte'
  import { get, post, patch, del } from '../lib/api.js'
  import { t } from '../lib/i18n.js'
  import { toast } from '../lib/state.svelte.js'
  import { when, relative } from '../lib/format.js'
  import Icon from '../lib/Icon.svelte'
  import Kpi from '../lib/Kpi.svelte'

  let all = $state([]) // every item, for the numbers
  let kinds = $state(['fact', 'preference', 'person', 'place', 'event', 'other'])
  let q = $state('')
  let kind = $state('')
  let error = $state('')
  let draft = $state({ text: '', kind: 'fact' })
  let editing = $state(null)

  async function load() {
    try {
      const r = await get('/memory/items?limit=500')
      all = r.items
      if (r.kinds) kinds = r.kinds
      error = ''
    } catch (e) {
      error = e.message
    }
  }

  let items = $derived(all.filter((it) => (!kind || it.kind === kind) && (!q || it.text.toLowerCase().includes(q.toLowerCase()))))
  let byKind = $derived(Object.fromEntries(kinds.map((k) => [k, all.filter((it) => it.kind === k).length])))
  let byAssistant = $derived(all.filter((it) => it.source === 'assistant').length)
  let week = $derived(all.filter((it) => new Date(it.updated_at) > new Date(Date.now() - 7 * 86400000)).length)
  let newest = $derived(all.length ? all[0].updated_at : null)

  async function add() {
    if (!draft.text.trim()) return
    try {
      const r = await post('/memory/items', draft)
      toast(r.replaced ? t('Updated a very similar item instead of adding a copy.') : t('Stored.'))
      draft = { text: '', kind: draft.kind }
      load()
    } catch (e) {
      toast(e.message)
    }
  }

  async function save() {
    try {
      await patch('/memory/items/' + editing.id, { text: editing.text, kind: editing.kind })
      editing = null
      load()
    } catch (e) {
      toast(e.message)
    }
  }

  async function forget(it) {
    if (!confirm(t('Forget "{x}"?', { x: it.text }))) return
    try {
      await del('/memory/items/' + it.id)
      load()
    } catch (e) {
      toast(e.message)
    }
  }

  $effect(() => {
    untrack(load)
  })
</script>

<div class="page">
  <div class="page-head">
    <div>
      <div class="label">{t('Memory')}</div>
      <h1>{t('What Athena knows about you')}</h1>
      <p>{t('Athena looks here before answering and may add what it learns. Everything is yours to change or delete.')}</p>
    </div>
  </div>

  {#if error}
    <div class="card"><p class="error">{error}</p></div>
  {:else}
    <div class="kpis">
      <Kpi label={t('Items')} value={all.length} note={newest ? t('last change {r}', { r: relative(newest) }) : t('none yet')} />
      <Kpi label={t('This week')} value={week} note={t('added or changed')} />
      <Kpi label={t('By Athena')} value={byAssistant} note={t('{n} by you', { n: all.length - byAssistant })} />
      <Kpi label={t('People')} value={byKind.person || 0} note={t('{n} places', { n: byKind.place || 0 })} />
    </div>

    <form class="card stack-phone" onsubmit={(e) => { e.preventDefault(); add() }}>
      <div class="label" style:margin-bottom="0.6rem">{t('Add')}</div>
      <div class="row stack-phone">
        <select bind:value={draft.kind} aria-label={t('Kind')}>
          {#each kinds as k}<option value={k}>{k}</option>{/each}
        </select>
        <input class="grow" bind:value={draft.text} placeholder={t('For example: I live in Bergen')} />
        <button class="primary" disabled={!draft.text.trim()}><Icon name="plus" size={15} />{t('Add')}</button>
      </div>
    </form>

    <div class="toolbar" style:margin-top="1.2rem">
      <div class="pills">
        <button class="pill" class:on={kind === ''} onclick={() => (kind = '')}>{t('all')} <span class="num">{all.length}</span></button>
        {#each kinds as k}
          <button class="pill" class:on={kind === k} onclick={() => (kind = k)}>{k} <span class="num">{byKind[k] || 0}</span></button>
        {/each}
      </div>
      <div class="search grow" style:min-width="180px">
        <Icon name="search" size={15} />
        <input type="search" bind:value={q} placeholder={t('Search')} aria-label={t('Search memory')} />
      </div>
    </div>

    <div class="card">
      <ul class="list stack-phone">
        {#each items as it (it.id)}
          <li>
            {#if editing?.id === it.id}
              <form class="row grow" onsubmit={(e) => { e.preventDefault(); save() }}>
                <select bind:value={editing.kind}>{#each kinds as k}<option value={k}>{k}</option>{/each}</select>
                <input class="grow" bind:value={editing.text} />
                <button class="primary">{t('Save')}</button>
                <button type="button" onclick={() => (editing = null)}>{t('Cancel')}</button>
              </form>
            {:else}
              <span class="tag">{it.kind}</span>
              <span class="grow">{it.text}</span>
              <span class="mono-sm" title={t('Added by') + ' ' + (it.source || '?')}>{it.source === 'assistant' ? t('athena') : t('you')} / {when(it.updated_at)}</span>
              <button class="ghost icon-btn" onclick={() => (editing = { ...it })} aria-label={t('Edit')}><Icon name="edit" size={15} /></button>
              <button class="ghost icon-btn" onclick={() => forget(it)} aria-label={t('Forget')}><Icon name="trash" size={15} /></button>
            {/if}
          </li>
        {:else}
          <li class="empty-note">{q || kind ? t('No match.') : t('Nothing stored yet.')}</li>
        {/each}
      </ul>
    </div>
  {/if}
</div>
