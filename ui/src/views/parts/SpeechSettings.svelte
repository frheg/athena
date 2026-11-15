<script>
  // Voices for reading aloud: one per language, the same on every device, plus speed. A voice with several speakers
  // offers each of them.
  import { get, put } from '../../lib/api.js'
  import { t } from '../../lib/i18n.js'
  import { toast } from '../../lib/state.svelte.js'
  import Icon from '../../lib/Icon.svelte'

  let s = $state(null)
  let sample = null

  async function load() {
    try {
      s = await get('/speech')
    } catch (e) {
      toast(e.message)
    }
  }

  async function save(body) {
    try {
      s = await put('/speech', body)
    } catch (e) {
      toast(e.message)
    }
  }

  function choices(lang) {
    const out = []
    for (const v of (s?.voices || []).filter((v) => v.language === lang)) {
      if (v.speakers > 1) for (let i = 0; i < v.speakers; i++) out.push({ id: v.id + ':' + i, name: v.id + ', ' + t('speaker {n}', { n: i + 1 }) })
      else out.push({ id: v.id, name: v.id + (v.default ? ' (' + t('default') + ')' : '') })
    }
    return out
  }

  function listen(lang) {
    if (sample) sample.pause()
    sample = new Audio('api/speech/sample?lang=' + lang + '&t=' + Date.now())
    sample.play().catch(() => toast(t('Could not play the sample.')))
  }

  $effect(() => {
    load()
  })

  const langs = [
    { code: 'no', name: t('Norwegian') },
    { code: 'en', name: t('English') },
  ]
</script>

<h2>{t('Speech')}</h2>
<p class="muted small">{t('Answers can be read aloud in the browser: press the speaker under an answer, or turn on reading aloud in the chat. A Bluetooth speaker paired with your phone or laptop plays it.')}</p>
<div class="card">
  {#if !s}
    <p class="muted">{t('Loading')}</p>
  {:else if !s.configured}
    <p class="muted">{t('No speech server is set up (TTS_URL).')}</p>
  {:else}
    {#if s.error}<p class="error">{s.error}</p>{/if}
    <div class="speech-grid">
      {#each langs as l}
        <label class="field">
          <span class="label">{l.name}</span>
          <div class="row">
            <select class="grow" value={s.chosen[l.code] || ''} onchange={(e) => save({ [l.code]: e.currentTarget.value })}>
              <option value="">{t('Default voice')}</option>
              {#each choices(l.code) as c (c.id)}<option value={c.id}>{c.name}</option>{/each}
            </select>
            <button class="ghost icon-btn" onclick={() => listen(l.code)} title={t('Listen')} aria-label={t('Listen to {l}', { l: l.name })}><Icon name="speaker" size={16} /></button>
          </div>
        </label>
      {/each}
      <label class="field">
        <span class="label">{t('Speed')} <span class="num">{s.speed.toFixed(2)}x</span></span>
        <input type="range" min="0.75" max="1.5" step="0.05" value={s.speed} onchange={(e) => save({ speed: Number(e.currentTarget.value) })} />
      </label>
    </div>
  {/if}
</div>
