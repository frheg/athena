<script>
  // A small bar chart: one bar per item, the last one highlighted. Values on hover through the title.
  let { values = [], labels = [], height = 72, unit = '' } = $props()
  let max = $derived(Math.max(1, ...values))
</script>

<div class="bars" style:height={height + 'px'} role="img" aria-label={values.join(', ')}>
  {#each values as v, i}
    <div class="bar-slot" title={(labels[i] ? labels[i] + ': ' : '') + v + unit}>
      <div class="bar" class:last={i === values.length - 1} class:zero={v === 0} style:height={Math.max(2, (v / max) * 100) + '%'}></div>
    </div>
  {/each}
</div>
