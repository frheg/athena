<script>
  // A stacked bar with a legend: parts = [{label, value, tone}], tone is ok, warn, bad or muted.
  let { parts = [] } = $props()
  let total = $derived(parts.reduce((s, p) => s + p.value, 0))
</script>

<div class="meter" role="img" aria-label={parts.map((p) => p.label + ' ' + p.value).join(', ')}>
  {#if total === 0}
    <div class="meter-part tone-muted" style:width="100%"></div>
  {:else}
    {#each parts as p}
      {#if p.value > 0}<div class="meter-part tone-{p.tone}" style:width={(p.value / total) * 100 + '%'}></div>{/if}
    {/each}
  {/if}
</div>
<div class="legend">
  {#each parts as p}
    <span><i class="dot tone-{p.tone}"></i>{p.label} <b class="num">{p.value}</b></span>
  {/each}
</div>
