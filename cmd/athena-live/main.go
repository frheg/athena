// athena-live: talk with Athena out loud. An owl in the terminal listens for its name, answers through the speaker,
// and sees you through the camera (pose and gestures).
//
//	athena-live [-stt http://127.0.0.1:8420] [-camera http://127.0.0.1:8410] [-wake Athena] [-follow 15s]
//	            [-mic "pw-record --rate 16000 --channels 1 --format s16 --raw -"] [-play pw-play]
//
// Say "Athena, ..." to ask something; for 15 seconds after an answer you can go on without the name. While it speaks,
// "Athena" or "stopp" (or an open palm) stops it. When Athena asks to do something, answer "ja" or "nei" (or thumbs
// up or down). Keys: space talks without the name, s stops speaking, m mutes the microphone, q quits.
//
// What it needs: Athena (ATHENA_URL and sign-in, see internal/client), a whisper.cpp server for speech recognition
// (-stt), and optionally camview with pose analysis for the camera (-camera). What was heard without the wake word
// goes nowhere: it is thrown away on this machine. Everything said to Athena is a normal conversation, "Live" and
// the date, in the chat.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/frheg/athena/internal/client"
	"golang.org/x/term"
)

var version = "dev"

type app struct {
	c      *client.Client
	stt    string
	wake   string
	follow time.Duration
	player string
	cam    *camera
	ctx    context.Context

	mu          sync.Mutex
	v           view
	session     string
	turn        string // the running turn
	stopTurn    context.CancelFunc
	approval    string // waiting for yes or no
	awakeUntil  time.Time
	hearing     bool // speech is on now
	transcribes int
	norwegian   bool // the language last spoken, for what live mode says itself
	sent        sentences
	voice       *voice
}

func main() {
	stt := flag.String("stt", "http://127.0.0.1:8420", "whisper.cpp server for speech recognition")
	cam := flag.String("camera", "http://127.0.0.1:8410", "camview with pose analysis (\"\" for none)")
	wake := flag.String("wake", "Athena", "the wake word")
	follow := flag.Duration("follow", 15*time.Second, "how long after an answer it listens without the wake word")
	micCmd := flag.String("mic", "pw-record --rate 16000 --channels 1 --format s16 --raw -", "command that records raw 16-bit mono at 16 kHz to stdout")
	playCmd := flag.String("play", "pw-play", "command that plays a WAV file (added as its last argument)")
	flag.Parse()
	c, err := client.FromEnv()
	if err != nil {
		fmt.Fprintln(os.Stderr, "athena-live:", err)
		os.Exit(2)
	}
	if err := run(c, *stt, *cam, *wake, *follow, *micCmd, *playCmd); err != nil {
		fmt.Fprintln(os.Stderr, "athena-live:", err)
		os.Exit(1)
	}
}

func run(c *client.Client, stt, camURL, wake string, follow time.Duration, micCmd, playCmd string) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGHUP)
	defer stop()
	// check the parts before the screen takes over the terminal
	var status struct{ Version string }
	if err := c.JSON(ctx, "GET", "/status", nil, &status); err != nil {
		return fmt.Errorf("Athena does not answer at %s: %w", c.URL, err)
	}
	if res, err := http.Get(strings.TrimRight(stt, "/") + "/"); err != nil {
		return fmt.Errorf("no speech recognition at %s: %w", stt, err)
	} else {
		res.Body.Close()
	}
	var sess struct {
		Session struct{ ID string } `json:"session"`
	}
	if err := c.JSON(ctx, "POST", "/sessions", map[string]string{"title": "Live " + time.Now().Format("2006-01-02 15:04")}, &sess); err != nil {
		return fmt.Errorf("starting a conversation: %w", err)
	}

	a := &app{c: c, stt: stt, wake: wake, follow: follow, player: playCmd, ctx: ctx, session: sess.Session.ID, norwegian: true}
	a.v.started = time.Now()
	a.v.lines = []line{{"", fmt.Sprintf("Athena %s. Say \"%s, ...\" to talk; for %s after an answer, no name is needed.", status.Version, wake, follow)}}
	a.voice = newVoice(a)
	if camURL != "" {
		a.cam = newCamera(camURL)
		go a.cam.run(ctx)
		go a.gestures(ctx)
	}

	frames := make(chan []int16, 50)
	go func() {
		if err := mic(ctx, micCmd, frames); err != nil {
			a.note("the microphone stopped: " + err.Error())
		}
	}()
	go a.listen(ctx, frames)

	in := int(os.Stdin.Fd())
	old, err := term.MakeRaw(in)
	if err != nil {
		return err
	}
	fmt.Print("\x1b[?1049h\x1b[?25l\x1b[2J")
	defer func() {
		fmt.Print(reset + "\x1b[?25h\x1b[?1049l")
		_ = term.Restore(in, old)
	}()
	keys := make(chan byte, 8)
	go func() {
		b := make([]byte, 1)
		for {
			if _, err := os.Stdin.Read(b); err != nil {
				close(keys)
				return
			}
			keys <- b[0]
		}
	}()
	tick := time.NewTicker(time.Second / 15)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			a.stopAll()
			return nil
		case k, ok := <-keys:
			if !ok || k == 'q' || k == 3 {
				a.stopAll()
				return nil
			}
			a.key(k)
		case t := <-tick.C:
			a.draw(t)
		}
	}
}

func (a *app) key(k byte) {
	a.mu.Lock()
	defer a.mu.Unlock()
	switch k {
	case ' ':
		a.awakeUntil = time.Now().Add(a.follow)
	case 's':
		go a.stopAll()
	case 'm':
		a.v.muted = !a.v.muted
	}
}

// draw works out the avatar's mood from what happens now and draws the screen.
func (a *app) draw(t time.Time) {
	w, h, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil || w < 40 || h < 20 {
		fmt.Print("\x1b[H\x1b[2Jathena-live needs a terminal of at least 40 x 20")
		return
	}
	talking, mouth := a.voice.state()
	a.mu.Lock()
	switch {
	case a.approval != "":
		a.v.mood = asking
	case talking:
		a.v.mood = speaking
	case a.turn != "" || a.transcribes > 0:
		a.v.mood = thinking
	case a.hearing:
		a.v.mood = hearing
	case t.Before(a.awakeUntil):
		a.v.mood = listening
	default:
		a.v.mood = asleep
	}
	a.v.mouth = mouth
	if a.cam != nil {
		camW := min(56, w-40)
		a.v.camera = a.cam.ascii(camW, camW*9/32)
		a.v.seen = a.cam.summary()
	}
	out := render(&a.v, t, w, h)
	a.mu.Unlock()
	fmt.Print(out)
}

func (a *app) note(s string) {
	a.mu.Lock()
	a.v.lines = append(a.v.lines, line{"", s})
	a.trim()
	a.mu.Unlock()
}

// trim keeps the screen's conversation short.
func (a *app) trim() {
	if len(a.v.lines) > 200 {
		a.v.lines = a.v.lines[len(a.v.lines)-200:]
	}
}

// listen finds speech in the microphone's frames and has each stretch written down, one at a time.
func (a *app) listen(ctx context.Context, frames <-chan []int16) {
	v := newVAD()
	heard := make(chan []int16, 2)
	go func() {
		for s := range heard {
			a.mu.Lock()
			a.transcribes++
			a.mu.Unlock()
			text, err := transcribe(ctx, a.stt, a.wake+".", s)
			a.mu.Lock()
			a.transcribes--
			a.mu.Unlock()
			if err != nil {
				a.note(err.Error())
				continue
			}
			a.heard(text)
		}
	}()
	for {
		select {
		case <-ctx.Done():
			close(heard)
			return
		case f := <-frames:
			a.mu.Lock()
			a.v.level = level(f)
			muted := a.v.muted
			a.mu.Unlock()
			if muted {
				continue
			}
			s, on := v.add(f)
			a.mu.Lock()
			a.hearing = on
			a.mu.Unlock()
			if s != nil {
				select {
				case heard <- s:
				default: // still writing down the last two: this one is lost
				}
			}
		}
	}
}

// heard decides what to do with what was said: a wake word, a follow-up, an answer to an approval, a request to stop,
// or nothing. Without the wake word and outside a conversation, the text is dropped right here.
func (a *app) heard(text string) {
	if heardNothing(text) {
		return
	}
	rest, woke := findWake(text, a.wake)
	talking, _ := a.voice.state()
	a.mu.Lock()
	busy := talking || a.turn != ""
	approval := a.approval
	awake := time.Now().Before(a.awakeUntil)
	a.mu.Unlock()

	switch {
	case busy && approval == "":
		// while it speaks it hears itself too: only its name or "stopp" count
		if !woke && !isStop(text) {
			return
		}
		a.stopAll()
		if woke && rest != "" && !isStop(rest) {
			a.ask(rest)
		} else {
			a.wakeUp()
		}
	case approval != "":
		if yes, ok := yesNo(rest + " " + text); ok {
			a.answer(yes)
		} else if woke && rest != "" {
			a.ask(rest) // something else: a new message makes Athena drop what waits
		}
	case woke && strings.TrimSpace(rest) == "":
		a.wakeUp()
		a.voice.say(a.pick("Ja?", "Yes?"))
	case woke:
		a.ask(rest)
	case awake:
		a.ask(text)
	}
}

func (a *app) wakeUp() {
	a.mu.Lock()
	a.awakeUntil = time.Now().Add(a.follow)
	a.mu.Unlock()
}

// pick returns the Norwegian or the English text, by the language last spoken.
func (a *app) pick(no, en string) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.norwegian {
		return no
	}
	return en
}

// ask sends what was said to Athena as a spoken message, with what the camera sees, and follows the answer.
func (a *app) ask(text string) {
	seen := ""
	if a.cam != nil {
		seen = a.cam.summary()
	}
	a.mu.Lock()
	a.norwegian = norwegian(text)
	a.v.lines = append(a.v.lines, line{"you", text})
	a.trim()
	a.awakeUntil = time.Time{}
	a.v.detail = ""
	a.mu.Unlock()
	var out struct {
		TurnID string `json:"turn_id"`
	}
	body := map[string]any{"text": text, "voice": true, "seen": seen}
	err := a.c.JSON(a.ctx, "POST", "/sessions/"+a.session+"/messages", body, &out)
	var ce *client.Error
	if errors.As(err, &ce) && ce.Status == http.StatusConflict { // an answer is still running: stop it and ask again
		a.stopAll()
		time.Sleep(300 * time.Millisecond)
		err = a.c.JSON(a.ctx, "POST", "/sessions/"+a.session+"/messages", body, &out)
	}
	if err != nil {
		a.note("could not ask Athena: " + err.Error())
		return
	}
	ctx, cancel := context.WithCancel(a.ctx)
	a.mu.Lock()
	a.turn, a.stopTurn = out.TurnID, cancel
	a.sent.reset()
	a.v.lines = append(a.v.lines, line{"athena", ""})
	a.mu.Unlock()
	go a.followTurn(ctx, out.TurnID)
}

// followTurn reads the answer as it streams in and speaks it sentence by sentence.
func (a *app) followTurn(ctx context.Context, turn string) {
	defer func() {
		a.mu.Lock()
		if a.turn == turn {
			a.turn, a.stopTurn = "", nil
			if rest := a.sent.flush(); rest != "" {
				a.voice.say(rest)
			}
		}
		a.mu.Unlock()
		a.voice.whenDone(func() { a.wakeUp() })
	}()
	_ = a.c.Stream(ctx, "/turns/"+turn+"/events", func(ev client.Event) {
		var d map[string]any
		_ = json.Unmarshal(ev.Data, &d)
		a.mu.Lock()
		defer a.mu.Unlock()
		if a.turn != turn {
			return
		}
		last := &a.v.lines[len(a.v.lines)-1]
		switch ev.Type {
		case "text":
			delta, _ := d["delta"].(string)
			if last.who == "athena" {
				last.text += delta
			}
			for _, s := range a.sent.add(delta) {
				a.voice.say(s)
			}
		case "text_reset": // the text before a tool call is not the answer
			if last.who == "athena" {
				last.text = ""
			}
			a.sent.reset()
		case "tool_call":
			tool, _ := d["tool"].(string)
			a.v.detail = "using " + strings.ReplaceAll(tool, "__", " ")
		case "approval":
			id, _ := d["id"].(string)
			tool, _ := d["tool"].(string)
			a.approval = id
			name := strings.ReplaceAll(strings.ReplaceAll(tool, "__", " "), "_", " ")
			a.v.lines = append(a.v.lines, line{"", "Athena asks to use " + name + ": say ja or nei, or thumbs up or down"})
			if a.norwegian {
				a.voice.say("Jeg vil bruke " + name + ". Er det greit? Si ja eller nei.")
			} else {
				a.voice.say("I want to use " + name + ". Is that all right? Say yes or no.")
			}
		case "error":
			msg, _ := d["error"].(string)
			if msg != "cancelled" && !strings.HasPrefix(msg, "superseded") {
				a.v.lines = append(a.v.lines, line{"", "the answer failed: " + msg})
			}
		}
	})
}

// answer answers the approval that waits.
func (a *app) answer(yes bool) {
	a.mu.Lock()
	id := a.approval
	a.approval = ""
	a.mu.Unlock()
	if id == "" {
		return
	}
	ans, said := "deny", "no"
	if yes {
		ans, said = "once", "yes"
	}
	a.note("you answered " + said)
	if err := a.c.JSON(a.ctx, "POST", "/approvals/"+id, map[string]string{"answer": ans}, nil); err != nil {
		a.note("could not answer: " + err.Error())
	}
}

// stopAll stops speaking and the running answer.
func (a *app) stopAll() {
	a.voice.stop()
	a.mu.Lock()
	turn, stop := a.turn, a.stopTurn
	a.turn, a.stopTurn = "", nil
	a.sent.reset()
	a.mu.Unlock()
	if stop != nil {
		stop()
	}
	if turn != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = a.c.JSON(ctx, "POST", "/turns/"+turn+"/cancel", map[string]any{}, nil)
	}
}

// gestures acts on gestures held steady: an open palm stops speaking, thumbs up or down answers an approval, and a
// thumbs up otherwise makes the owl smile.
func (a *app) gestures(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case g := <-a.cam.gestures:
			talking, _ := a.voice.state()
			a.mu.Lock()
			approval := a.approval != ""
			busy := talking || a.turn != ""
			a.mu.Unlock()
			switch {
			case strings.HasSuffix(g, "open palm") && busy:
				a.note("open palm: stopped")
				a.stopAll()
				a.wakeUp()
			case strings.HasSuffix(g, "thumbs up") && approval:
				a.answer(true)
			case strings.HasSuffix(g, "thumbs down") && approval:
				a.answer(false)
			case strings.HasSuffix(g, "thumbs up"):
				a.mu.Lock()
				a.v.happy = time.Now().Add(3 * time.Second)
				a.mu.Unlock()
			}
		}
	}
}
