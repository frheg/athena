<script>
  // One number with a label and an optional note underneath.
  let { label, value, note = '', tone = '', href = '' } = $props()
</script>

{#if href}
  <a class="kpi link-kpi tone-text-{tone}" {href}>
    <span class="label">{label}</span>
    <span class="kpi-value num">{value}</span>
    {#if note}<span class="kpi-note">{note}</span>{/if}
  </a>
{:else}
  <div class="kpi tone-text-{tone}">
    <span class="label">{label}</span>
    <span class="kpi-value num">{value}</span>
    {#if note}<span class="kpi-note">{note}</span>{/if}
  </div>
{/if}
