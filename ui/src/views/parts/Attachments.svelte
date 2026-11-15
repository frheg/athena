<script>
  // Files attached to a message: images as thumbnails (the store's preview), everything else as a chip that downloads.
  import { t } from '../../lib/i18n.js'
  import { bytes, num } from '../../lib/format.js'
  import Icon from '../../lib/Icon.svelte'

  let { files = [] } = $props()

  const src = (f, preview) => 'api/files/' + f.id + '/content' + (preview ? '?preview=1' : '')
  function detail(f) {
    const d = [bytes(f.size)]
    if (f.pages) d.push(t('{n} pages', { n: f.pages }))
    else if (f.chars) d.push(t('{n} chars', { n: num(f.chars) }))
    if (f.width) d.push(f.width + 'x' + f.height)
    return d.join(' / ')
  }
</script>

<div class="attachments">
  {#each files.filter((f) => f.kind === 'image' && f.preview) as f (f.id)}
    <a class="thumb" href={src(f, false)} target="_blank" rel="noopener" title={f.name + ' / ' + detail(f)}>
      <img src={src(f, true)} alt={f.name} loading="lazy" />
    </a>
  {/each}
  {#each files.filter((f) => !(f.kind === 'image' && f.preview)) as f (f.id)}
    <a class="file-chip" href={src(f, false)} download={f.name} title={f.note || f.mime}>
      <Icon name={f.kind === 'image' ? 'image' : 'file'} size={16} />
      <span class="fc-name">{f.name}</span>
      <span class="mono-sm">{detail(f)}</span>
      {#if f.kind === 'binary' || (f.kind !== 'image' && !f.chars)}<span class="tag">{t('not read')}</span>{:else if f.note}<span class="tag warn">{t('partly read')}</span>{/if}
    </a>
  {/each}
</div>
