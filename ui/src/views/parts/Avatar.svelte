<script>
  // Athena's face in live mode: an orb with two eyes. It sleeps (eyes shut, dim), listens (awake, breathing), hears
  // (grows with your voice), thinks (a turning arc), speaks (pulses with its voice) and asks (warm, waiting).
  let { mood = 'asleep', level = -90, mouth = 0, happy = false, size = 96 } = $props()

  // the microphone's loudness from -60 dB (quiet room) to -15 dB (speech close by), as 0 to 1
  let voice = $derived(Math.max(0, Math.min(1, (level + 60) / 45)))
  let grow = $derived(mood === 'speaking' ? mouth : mood === 'hearing' ? voice : 0)
</script>

<div class="avatar {mood}" class:happy style:--size={size + 'px'} style:--grow={grow} aria-hidden="true">
  <svg viewBox="0 0 100 100" width={size} height={size}>
    <circle class="halo" cx="50" cy="50" r="40" />
    <circle class="orb" cx="50" cy="50" r="30" />
    {#if mood === 'thinking'}<path class="arc" d="M50 14a36 36 0 0 1 36 36" />{/if}
    {#if mood === 'asleep'}
      <path class="eye-shut" d="M37 50q4 3 8 0M55 50q4 3 8 0" />
    {:else if happy}
      <path class="eye-shut" d="M37 50q4 -4 8 0M55 50q4 -4 8 0" />
    {:else}
      <circle class="eye" cx="41" cy="48" r="3.2" />
      <circle class="eye" cx="59" cy="48" r="3.2" />
    {/if}
  </svg>
</div>
