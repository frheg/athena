package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/frheg/athena/internal/speech"
	"github.com/frheg/athena/internal/store"
)

// Speakers are programs on other machines that play Athena's speech, such as one next to a Bluetooth speaker. A
// speaker connects to Athena (GET /api/speakers/{name}/jobs, an event stream) and waits; nothing has to reach it.
// When the user picks it, core sends it a job, and the speaker fetches the audio piece by piece
// (GET /api/messages/{id}/speech?part=n) and plays it.
type speakers struct {
	mu  sync.Mutex
	all map[string]*speaker // by owner/name
}

type speaker struct {
	Name    string    `json:"name"`
	Since   time.Time `json:"since"`
	Playing int64     `json:"playing,omitempty"` // the message it reports playing
	jobs    chan map[string]any
}

var speakerName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)

func (s *speakers) get(owner, name string) *speaker {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.all[owner+"/"+name]
}

// speakerList lists the connected speakers.
func (a *API) speakerList(w http.ResponseWriter, r *http.Request) {
	owner := a.owner(r)
	a.speakers.mu.Lock()
	out := []speaker{}
	for key, sp := range a.speakers.all {
		if strings.HasPrefix(key, owner+"/") {
			out = append(out, *sp)
		}
	}
	a.speakers.mu.Unlock()
	writeJSON(w, 200, map[string]any{"speakers": out})
}

// speakerJobs is the speaker's connection: an event stream of jobs, {"type":"play","message_id":..,"parts":..} or
// {"type":"stop"}. A second connection with the same name replaces the first.
func (a *API) speakerJobs(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !speakerName.MatchString(name) {
		fail(w, bad("a speaker name is lowercase letters, digits and -"))
		return
	}
	owner := a.owner(r)
	key := owner + "/" + name
	sp := &speaker{Name: name, Since: time.Now(), jobs: make(chan map[string]any, 8)}
	a.speakers.mu.Lock()
	if a.speakers.all == nil {
		a.speakers.all = map[string]*speaker{}
	}
	if old := a.speakers.all[key]; old != nil {
		close(old.jobs)
	}
	a.speakers.all[key] = sp
	a.speakers.mu.Unlock()
	defer func() {
		a.speakers.mu.Lock()
		if a.speakers.all[key] == sp {
			delete(a.speakers.all, key)
		}
		a.speakers.mu.Unlock()
	}()
	_ = a.E.Store.Audit(r.Context(), owner, "speaker:"+name, nil, nil, "speaker.connect", "", nil)

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	flusher, _ := w.(http.Flusher)
	send := func(event string, v any) bool {
		b, _ := json.Marshal(v)
		if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b); err != nil {
			return false
		}
		if flusher != nil {
			flusher.Flush()
		}
		return true
	}
	if !send("hello", map[string]string{"name": name}) {
		return
	}
	ping := time.NewTicker(25 * time.Second) // keeps proxies from closing a quiet connection
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case job, ok := <-sp.jobs:
			if !ok || !send("job", job) {
				return
			}
		case <-ping.C:
			if !send("ping", map[string]any{}) {
				return
			}
		}
	}
}

func (a *API) sendJob(w http.ResponseWriter, r *http.Request, name string, job map[string]any) bool {
	sp := a.speakers.get(a.owner(r), name)
	if sp == nil {
		fail(w, bad("the speaker %q is not connected", name))
		return false
	}
	select {
	case sp.jobs <- job:
		return true
	default:
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "the speaker is not taking jobs"})
		return false
	}
}

// speakOn sends an answer to a speaker: body {"to": "<speaker>"}.
func (a *API) speakOn(w http.ResponseWriter, r *http.Request) {
	if a.speechOff(w) {
		return
	}
	var in struct {
		To string `json:"to"`
	}
	if err := decode(r, &in); err != nil {
		fail(w, err)
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
	parts := len(speech.Parts(speech.Speakable(m.Content), speechPart))
	if parts == 0 {
		fail(w, bad("nothing to read aloud in this message"))
		return
	}
	if !a.sendJob(w, r, in.To, map[string]any{"type": "play", "message_id": id, "parts": parts}) {
		return
	}
	_ = a.E.Store.Audit(r.Context(), a.owner(r), "user", nil, nil, "speaker.play", "", map[string]any{"speaker": in.To, "message": id})
	writeJSON(w, http.StatusAccepted, map[string]any{"speaker": in.To, "parts": parts})
}

// speakerStop stops what a speaker plays.
func (a *API) speakerStop(w http.ResponseWriter, r *http.Request) {
	if a.sendJob(w, r, r.PathValue("name"), map[string]any{"type": "stop"}) {
		writeJSON(w, 200, map[string]bool{"ok": true})
	}
}

// speakerState is the speaker telling what it plays now (body {"playing": <message id> or 0}), for the page.
func (a *API) speakerState(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Playing int64 `json:"playing"`
	}
	if err := decode(r, &in); err != nil {
		fail(w, err)
		return
	}
	a.speakers.mu.Lock()
	sp := a.speakers.all[a.owner(r)+"/"+r.PathValue("name")]
	if sp != nil {
		sp.Playing = in.Playing
	}
	a.speakers.mu.Unlock()
	if sp == nil {
		fail(w, bad("the speaker is not connected"))
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}
