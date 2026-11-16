<script>
  // Volume and mute of one speaker (athena-speaker), for the chat and Settings. Nothing shows for a speaker that
  // cannot tell its volume.
  import { t } from '../../lib/i18n.js'
  import { setVolume, setMuted } from '../../lib/speech.svelte.js'
  import Icon from '../../lib/Icon.svelte'

  let { speaker, compact = false } = $props()
  let shown = $state(null) // the slider's value while it is dragged
  let value = $derived(shown ?? speaker.volume ?? 0)
</script>

{#if speaker.volume != null}
  <span class="volume" class:compact title={t('Volume on {s}', { s: speaker.name })}>
    <button type="button" class="ghost icon-btn" onclick={() => setMuted(speaker.name, !speaker.muted)}
      aria-pressed={!!speaker.muted} aria-label={speaker.muted ? t('Unmute {s}', { s: speaker.name }) : t('Mute {s}', { s: speaker.name })}>
      <Icon name={speaker.muted ? 'mute' : 'speaker'} size={15} />
    </button>
    <input type="range" min="0" max="1" step="0.05" value={value} aria-label={t('Volume on {s}', { s: speaker.name })}
      oninput={(e) => (shown = Number(e.currentTarget.value))}
      onchange={(e) => { setVolume(speaker.name, Number(e.currentTarget.value)); shown = null }} />
    <span class="mono-sm num">{speaker.muted ? t('muted') : Math.round(value * 100) + '%'}</span>
  </span>
{/if}
