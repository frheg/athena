<script>
  import { untrack } from 'svelte'
  import { get, post, patch, del } from '../lib/api.js'
  import { t } from '../lib/i18n.js'
  import { toast } from '../lib/state.svelte.js'
  import { when } from '../lib/format.js'

  let items = $state([])
  let kinds = $state(['fact', 'preference', 'person', 'place', 'event', 'other'])
  let q = $state('')
  let kind = $state('')
  let error = $state('')
  let draft = $state({ text: '', kind: 'fact' })
  let editing = $state(null)

  async function load() {
    try {
      const r = await get('/memory/items?q=' + encodeURIComponent(q) + '&kind=' + kind)
      items = r.items
      if (r.kinds) kinds = r.kinds
      error = ''
    } catch (e) {
      error = e.message
    }
  }

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
    kind
    untrack(load)
  })
</script>

<div class="page">
  <h1>{t('Memory')}</h1>
  <p class="muted">{t('What Athena remembers about you. Athena looks here before answering, and may add things it learns. Everything is yours to change or delete.')}</p>
  {#if error}
    <p class="error">{error}</p>
  {:else}
    <form class="row" onsubmit={(e) => { e.preventDefault(); add() }}>
      <select bind:value={draft.kind} aria-label={t('Kind')}>
        {#each kinds as k}<option value={k}>{k}</option>{/each}
      </select>
      <input class="grow" bind:value={draft.text} placeholder={t('Something Athena should know, for example: I live in Bergen')} />
      <button class="primary" disabled={!draft.text.trim()}>{t('Add')}</button>
    </form>
    <div class="row">
      <input type="search" class="grow" bind:value={q} oninput={load} placeholder={t('Search')} />
      <select bind:value={kind} aria-label={t('Filter by kind')}>
        <option value="">{t('all kinds')}</option>
        {#each kinds as k}<option value={k}>{k}</option>{/each}
      </select>
    </div>
    <ul class="items">
      {#each items as it (it.id)}
        <li>
          {#if editing?.id === it.id}
            <form class="row" onsubmit={(e) => { e.preventDefault(); save() }}>
              <select bind:value={editing.kind}>{#each kinds as k}<option value={k}>{k}</option>{/each}</select>
              <input class="grow" bind:value={editing.text} />
              <button class="primary">{t('Save')}</button>
              <button type="button" onclick={() => (editing = null)}>{t('Cancel')}</button>
            </form>
          {:else}
            <span class="chip">{it.kind}</span>
            <span class="grow">{it.text}</span>
            <span class="muted small" title={t('Added by') + ' ' + (it.source || '?')}>{when(it.updated_at)}</span>
            <button class="link small" onclick={() => (editing = { ...it })}>{t('Edit')}</button>
            <button class="link small danger-text" onclick={() => forget(it)}>{t('Forget')}</button>
          {/if}
        </li>
      {:else}
        <li class="muted">{q || kind ? t('No match.') : t('Nothing stored yet.')}</li>
      {/each}
    </ul>
  {/if}
</div>
