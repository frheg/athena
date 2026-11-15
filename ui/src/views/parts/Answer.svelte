<script>
  import { render, hasImages } from '../../lib/markdown.js'
  import { t } from '../../lib/i18n.js'

  let { text = '' } = $props()
  let images = $state(false)
  let html = $derived(render(text, images))
</script>

<div class="md">{@html html}</div>
{#if hasImages(text) && !images}
  <button class="link small" onclick={() => (images = true)} title={t('Images load from other sites, which then learn that you opened them.')}>
    {t('Load images')}
  </button>
{/if}
