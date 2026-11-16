package api

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/frheg/athena/internal/speech"
	"github.com/frheg/athena/internal/store"
)

// Settings for speech, the same for every device: one voice per language and a speed.
const (
	speechVoiceNO = "speech_voice_no"
	speechVoiceEN = "speech_voice_en"
	speechSpeed   = "speech_speed"
	speechPart    = 400 // characters per piece of audio
)

var samples = map[string]string{
	"no": "Hei, jeg er Athena. Slik høres jeg ut når jeg leser svarene mine høyt.",
	"en": "Hello, I am Athena. This is how I sound when I read my answers aloud.",
}

func (a *API) speechOptions(ctx context.Context) speech.Options {
	o := speech.Options{Voices: map[string]string{}}
	for lang, key := range map[string]string{"no": speechVoiceNO, "en": speechVoiceEN} {
		if v, _ := a.E.Store.Setting(ctx, key); v != "" {
			o.Voices[lang] = v
		}
	}
	if v, _ := a.E.Store.Setting(ctx, speechSpeed); v != "" {
		o.Speed, _ = strconv.ParseFloat(v, 64)
	}
	return o
}

func (a *API) speechOff(w http.ResponseWriter) bool {
	if a.Speech == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "speech is off: no speech server is set up"})
		return true
	}
	return false
}

func writeAudio(w http.ResponseWriter, b []byte, voice string, parts int) {
	w.Header().Set("Content-Type", "audio/wav")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Voice", voice)
	if parts > 0 {
		w.Header().Set("X-Speech-Parts", strconv.Itoa(parts))
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(b)))
	_, _ = w.Write(b)
}

// messageSpeech reads an answer aloud, one piece at a time: ?part=0, 1, ... until 404. Pieces are whole sentences,
// so the player can start on the first while the next is made.
func (a *API) messageSpeech(w http.ResponseWriter, r *http.Request) {
	if a.speechOff(w) {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		fail(w, store.ErrNotFound)
		return
	}
	m, err := a.E.Store.Message(r.Context(), a.owner(r), id)
	if err != nil {
		fail(w, err)
		return
	}
	if m.Role != "assistant" && m.Role != "user" {
		fail(w, bad("only answers and your own messages can be read aloud"))
		return
	}
	parts := speech.Parts(speech.Speakable(m.Content), speechPart)
	part, _ := strconv.Atoi(r.URL.Query().Get("part"))
	if part < 0 || part >= len(parts) {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "no such part", "parts": len(parts)})
		return
	}
	b, voice, err := a.Speech.Speak(r.Context(), parts[part], a.speechOptions(r.Context()))
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	writeAudio(w, b, voice, len(parts))
}

// speechSample plays a fixed sentence in a language with the chosen voice (?lang=no or en), for the settings page.
func (a *API) speechSample(w http.ResponseWriter, r *http.Request) {
	if a.speechOff(w) {
		return
	}
	text, ok := samples[r.URL.Query().Get("lang")]
	if !ok {
		fail(w, bad("lang must be no or en"))
		return
	}
	b, voice, err := a.Speech.Speak(r.Context(), text, a.speechOptions(r.Context()))
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	writeAudio(w, b, voice, 1)
}

// speechSettings lists the voices and the current choice.
func (a *API) speechSettings(w http.ResponseWriter, r *http.Request) {
	if a.Speech == nil {
		writeJSON(w, 200, map[string]any{"configured": false})
		return
	}
	voices, err := a.Speech.Voices(r.Context())
	o := a.speechOptions(r.Context())
	if o.Speed == 0 {
		o.Speed = 1
	}
	writeJSON(w, 200, map[string]any{"configured": true, "voices": voices, "error": errString(err),
		"chosen": map[string]string{"no": o.Voices["no"], "en": o.Voices["en"]}, "speed": o.Speed})
}

// setSpeech stores the voice per language ("" = the server's default; "id:3" = speaker 3) and the speed.
func (a *API) setSpeech(w http.ResponseWriter, r *http.Request) {
	var in struct {
		NO    *string  `json:"no"`
		EN    *string  `json:"en"`
		Speed *float64 `json:"speed"`
	}
	if err := decode(r, &in); err != nil {
		fail(w, err)
		return
	}
	ctx := r.Context()
	for key, v := range map[string]*string{speechVoiceNO: in.NO, speechVoiceEN: in.EN} {
		if v == nil {
			continue
		}
		if len(*v) > 120 || strings.ContainsAny(*v, " \n\"") {
			fail(w, bad("not a voice name: %q", *v))
			return
		}
		if err := a.E.Store.SetSetting(ctx, key, *v); err != nil {
			fail(w, err)
			return
		}
	}
	if in.Speed != nil {
		if *in.Speed < 0.5 || *in.Speed > 2 {
			fail(w, bad("speed must be between 0.5 and 2"))
			return
		}
		if err := a.E.Store.SetSetting(ctx, speechSpeed, strconv.FormatFloat(*in.Speed, 'f', 2, 64)); err != nil {
			fail(w, err)
			return
		}
	}
	a.speechSettings(w, r)
}

// speakText reads any text aloud with the chosen voices (live mode speaks answers sentence by sentence as they
// stream in, before they are stored). Body: {"text": "..."}, at most 2000 characters; the answer is WAV.
func (a *API) speakText(w http.ResponseWriter, r *http.Request) {
	if a.speechOff(w) {
		return
	}
	var in struct {
		Text string `json:"text"`
	}
	if err := decode(r, &in); err != nil {
		fail(w, err)
		return
	}
	text := speech.Speakable(in.Text)
	if text == "" {
		fail(w, bad("nothing to say"))
		return
	}
	if len(text) > 2000 {
		fail(w, bad("the text is longer than 2000 characters: send it a sentence or two at a time"))
		return
	}
	b, voice, err := a.Speech.Speak(r.Context(), text, a.speechOptions(r.Context()))
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	writeAudio(w, b, voice, 0)
}
