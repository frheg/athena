<script>
  // Live mode's controls, in place of the message box: the avatar, what it heard and sees, and buttons for the
  // microphone, the camera and stopping. Space talks without the name; Escape stops it speaking.
  import { onMount } from 'svelte'
  import { t } from '../../lib/i18n.js'
  import Icon from '../../lib/Icon.svelte'
  import Avatar from './Avatar.svelte'
  import { live, getCamera, setCamera, setPreview, setMuted, talk, stopAll, stop } from '../../lib/live/live.svelte.js'

  let canvas = $state()

  const moods = {
    asleep: t('Say "Athena" to talk'),
    listening: t('Listening'),
    hearing: t('Hearing you'),
    thinking: t('Thinking'),
    speaking: t('Speaking'),
    asking: t('Waiting for yes or no'),
  }

  // the camera picture, mirrored, with the body points and hands it found
  onMount(() => {
    let raf
    const draw = () => {
      raf = requestAnimationFrame(draw)
      const cam = getCamera()
      if (!canvas || !cam?.video || cam.video.readyState < 2) return
      const g = canvas.getContext('2d')
      const w = canvas.width
      const h = canvas.height
      g.save()
      g.translate(w, 0)
      g.scale(-1, 1)
      g.drawImage(cam.video, 0, 0, w, h)
      const seen = cam.recent()
      if (seen) {
        g.fillStyle = '#c7d2ac'
        if (seen.pose) {
          for (const i of [0, 11, 12, 13, 14, 15, 16, 23, 24]) {
            const p = seen.pose[i]
            if (p && p[2] > 0.5) g.fillRect(p[0] * w - 3, p[1] * h - 3, 6, 6)
          }
        }
        g.fillStyle = '#f6e6cf'
        for (const hd of seen.hands) for (const p of hd.points) g.fillRect(p[0] * w - 1.5, p[1] * h - 1.5, 3, 3)
      }
      g.restore()
    }
    draw()
    return () => cancelAnimationFrame(raf)
  })

  function keydown(e) {
    const typing = e.target.closest?.('input, textarea, select, [contenteditable]')
    if (typing || e.ctrlKey || e.metaKey || e.altKey) return
    if (e.code === 'Space') {
      e.preventDefault()
      talk()
    } else if (e.key === 'Escape') {
      stopAll()
    }
  }
</script>

<svelte:window onkeydown={keydown} />

<div class="live-panel" role="region" aria-label={t('Live mode')}>
  <div class="live-main">
    <Avatar mood={live.mood} level={live.muted ? -90 : live.level} mouth={live.mouth} happy={live.happy} />
    <div class="live-text">
      <div class="label" aria-live="polite">{live.muted ? t('Microphone off') : moods[live.mood]}</div>
      {#if live.heard}<div class="live-heard">{live.heard}</div>{/if}
      {#if live.note}<div class="mono-sm">{live.note}</div>{/if}
      {#if live.seen}<div class="mono-sm live-seen" title={t('What Athena sees on the camera. These words go with what you say.')}>{t('Sees')}: {live.seen}</div>{/if}
    </div>
    {#if live.cameraAvailable && live.cameraOn && live.preview}
      <canvas class="live-cam" bind:this={canvas} width="192" height="144" aria-label={t('Camera')}></canvas>
    {/if}
  </div>
  <div class="live-bar">
    <button type="button" class="ghost icon-btn" class:off={live.muted} onclick={() => setMuted(!live.muted)} aria-pressed={live.muted}
      title={live.muted ? t('Turn the microphone on') : t('Turn the microphone off')} aria-label={t('Microphone')}><Icon name={live.muted ? 'mic-off' : 'mic'} size={17} /></button>
    {#if live.cameraAvailable}
      <button type="button" class="ghost icon-btn" class:off={!live.cameraOn} onclick={() => setCamera(!live.cameraOn)} aria-pressed={live.cameraOn}
        title={live.cameraOn ? t('Turn the camera off') : t('Turn the camera on: Athena sees your pose and gestures')} aria-label={t('Camera')}><Icon name={live.cameraOn ? 'camera' : 'camera-off'} size={17} /></button>
      {#if live.cameraOn}
        <button type="button" class="ghost icon-btn" class:off={!live.preview} onclick={() => setPreview(!live.preview)} aria-pressed={live.preview}
          title={live.preview ? t('Hide the camera picture (it keeps seeing)') : t('Show the camera picture')} aria-label={t('Camera picture')}><Icon name="eye" size={17} /></button>
      {/if}
    {/if}
    {#if live.approval}
      <span class="mono-sm">{live.approval.tool}</span>
    {/if}
    <span class="grow"></span>
    {#if live.mood === 'speaking' || live.mood === 'thinking'}
      <button type="button" class="ghost" onclick={stopAll} title={t('Stop (Escape, "stopp", or an open palm)')}><Icon name="stop" size={15} />{t('Stop')}</button>
    {:else}
      <button type="button" class="ghost" onclick={talk} title={t('Talk without saying the name (Space)')}><Icon name="mic" size={15} />{t('Talk')}</button>
    {/if}
    <button type="button" class="primary" onclick={stop}>{t('End live')}</button>
  </div>
</div>
