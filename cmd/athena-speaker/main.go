// athena-speaker: plays Athena's speech on this machine. It connects to Athena and waits; when you pick it in the
// chat (by its name) and press play, it fetches the answer a piece at a time and plays it. Nothing needs to reach this
// machine: the connection goes out from here.
//
//	athena-speaker [-name kratos] [-play pw-play] [-before "command"] [-mixer wpctl] [-sink @DEFAULT_AUDIO_SINK@]
//
//	-name    how it is listed in the chat (lowercase letters, digits, -), default the host name
//	-play    the command that plays a WAV file; the file is added as its last argument (default pw-play)
//	-before  a command run before playing, for example one that connects a Bluetooth speaker; a failure is logged
//	-mixer   how the volume is read and set from the chat: wpctl (PipeWire) or none
//	-sink    the output whose volume it controls (wpctl), default the default output
//
// Athena's address and sign-in come from the environment: see internal/client (ATHENA_URL, ATHENA_USER,
// ATHENA_PASSWORD or ATHENA_TOKEN).
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/frheg/athena/internal/client"
)

var version = "dev"

type job struct {
	Type      string   `json:"type"`
	MessageID int64    `json:"message_id"`
	Parts     int      `json:"parts"`
	Volume    *float64 `json:"volume"`
	Muted     *bool    `json:"muted"`
}

type player struct {
	c      *client.Client
	name   string
	play   []string
	before string
	mixer  string
	sink   string
	log    *slog.Logger

	mu       sync.Mutex
	cancel   context.CancelFunc
	done     chan struct{}
	playing  int64
	reported string // the last state sent, to send only changes
}

func main() {
	host, _ := os.Hostname()
	name := flag.String("name", strings.ToLower(host), "how this speaker is listed in the chat")
	play := flag.String("play", "pw-play", "command that plays a WAV file (the file is added as the last argument)")
	before := flag.String("before", "", "command run before playing (for example connect a Bluetooth speaker)")
	mixer := flag.String("mixer", "wpctl", "how the volume is read and set: wpctl (PipeWire) or none")
	sink := flag.String("sink", "@DEFAULT_AUDIO_SINK@", "the output whose volume it controls")
	flag.Parse()
	log := slog.New(slog.NewTextHandler(os.Stdout, nil)).With("speaker", *name)
	if !regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`).MatchString(*name) {
		fmt.Fprintln(os.Stderr, "athena-speaker: -name must be lowercase letters, digits and -")
		os.Exit(2)
	}
	c, err := client.FromEnv()
	if err != nil {
		fmt.Fprintln(os.Stderr, "athena-speaker:", err)
		os.Exit(2)
	}
	p := &player{c: c, name: *name, play: strings.Fields(*play), before: *before, mixer: *mixer, sink: *sink, log: log}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()
	log.Info("waiting for jobs", "athena", c.URL, "version", version)
	go func() { // the volume can change on the speaker itself: tell Athena now and then
		for {
			select {
			case <-ctx.Done():
				return
			case <-time.After(15 * time.Second):
				p.report(false)
			}
		}
	}()
	c.Forever(ctx, "/speakers/"+*name+"/jobs", func(ev client.Event) {
		if ev.Type == "hello" { // a new connection: Athena knows nothing about this speaker yet
			go p.report(true)
			return
		}
		if ev.Type != "job" {
			return
		}
		var j job
		if err := json.Unmarshal(ev.Data, &j); err != nil {
			log.Warn("bad job", "err", err)
			return
		}
		switch j.Type {
		case "play":
			p.start(ctx, j)
		case "stop":
			p.stop()
		case "volume":
			go func() {
				p.setVolume(j.Volume, j.Muted)
				// a Bluetooth speaker takes a moment to apply it; reading at once gives the old value
				time.Sleep(1200 * time.Millisecond)
				p.report(true)
			}()
		}
	}, func(err error) { log.Warn("connection to Athena lost, trying again", "err", err) })
	p.stop()
}

// stop ends what plays and waits until it has.
func (p *player) stop() {
	p.mu.Lock()
	cancel, done := p.cancel, p.done
	p.cancel, p.done = nil, nil
	p.mu.Unlock()
	if cancel != nil {
		cancel()
		<-done
	}
}

// start plays a message, after stopping what plays now.
func (p *player) start(parent context.Context, j job) {
	p.stop()
	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	p.mu.Lock()
	p.cancel, p.done = cancel, done
	p.mu.Unlock()
	go func() {
		defer close(done)
		p.state(j.MessageID)
		defer p.state(0)
		if p.before != "" {
			if out, err := exec.CommandContext(ctx, "sh", "-c", p.before).CombinedOutput(); err != nil {
				p.log.Warn("the before command failed", "err", err, "output", strings.TrimSpace(string(out)))
			}
		}
		p.log.Info("playing", "message", j.MessageID, "parts", j.Parts)
		// the next piece downloads while one plays
		next := make(chan []byte, 1)
		fetch := func(part int) {
			b, err := p.c.Get(ctx, fmt.Sprintf("/messages/%d/speech?part=%d", j.MessageID, part))
			if err != nil {
				if ctx.Err() == nil {
					p.log.Warn("fetching speech", "part", part, "err", err)
				}
				b = nil
			}
			next <- b
		}
		go fetch(0)
		for part := 0; part < j.Parts; part++ {
			var wav []byte
			select {
			case <-ctx.Done():
				return
			case wav = <-next:
			}
			if wav == nil {
				return
			}
			if part+1 < j.Parts {
				go fetch(part + 1)
			}
			if err := p.playWAV(ctx, wav); err != nil {
				if ctx.Err() == nil {
					p.log.Warn("playing", "err", err)
				}
				return
			}
		}
	}()
}

func (p *player) playWAV(ctx context.Context, wav []byte) error {
	f, err := os.CreateTemp("", "athena-speech-*.wav")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(wav); err != nil {
		f.Close()
		return err
	}
	f.Close()
	args := append(append([]string{}, p.play[1:]...), filepath.Clean(f.Name()))
	cmd := exec.CommandContext(ctx, p.play[0], args...)
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// state notes what plays now (0 = nothing) and tells Athena.
func (p *player) state(id int64) {
	p.mu.Lock()
	p.playing = id
	p.mu.Unlock()
	p.report(true)
}

// report tells Athena what plays and how loud, so the page can show it; unless always, only when that changed.
func (p *player) report(always bool) {
	p.mu.Lock()
	body := map[string]any{"playing": p.playing}
	p.mu.Unlock()
	if v, muted, ok := p.volume(); ok {
		body["volume"], body["muted"] = v, muted
	}
	b, _ := json.Marshal(body)
	p.mu.Lock()
	same := string(b) == p.reported
	p.reported = string(b)
	p.mu.Unlock()
	if same && !always {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := p.c.JSON(ctx, "POST", "/speakers/"+p.name+"/state", body, nil); err != nil {
		p.log.Warn("telling Athena the state", "err", err)
		p.mu.Lock()
		p.reported = "" // send it again next time
		p.mu.Unlock()
	}
}

var wpctlVolume = regexp.MustCompile(`Volume:\s*([0-9.]+)(\s*\[MUTED\])?`)

// volume reads the output's volume (0 to 1) and whether it is muted.
func (p *player) volume() (float64, bool, bool) {
	if p.mixer != "wpctl" {
		return 0, false, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "wpctl", "get-volume", p.sink).Output()
	if err != nil {
		return 0, false, false
	}
	return parseVolume(string(out))
}

func parseVolume(s string) (float64, bool, bool) {
	m := wpctlVolume.FindStringSubmatch(s)
	if m == nil {
		return 0, false, false
	}
	var v float64
	if _, err := fmt.Sscanf(m[1], "%g", &v); err != nil {
		return 0, false, false
	}
	return v, m[2] != "", true
}

// setVolume sets the output's volume (0 to 1) and/or mute.
func (p *player) setVolume(v *float64, muted *bool) {
	if p.mixer != "wpctl" {
		p.log.Warn("asked to change the volume, but -mixer is none")
		return
	}
	run := func(args ...string) {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if out, err := exec.CommandContext(ctx, "wpctl", args...).CombinedOutput(); err != nil {
			p.log.Warn("wpctl", "args", args, "err", err, "output", strings.TrimSpace(string(out)))
		}
	}
	if v != nil {
		run("set-volume", p.sink, fmt.Sprintf("%.2f", min(max(*v, 0), 1)))
	}
	if muted != nil {
		m := "0"
		if *muted {
			m = "1"
		}
		run("set-mute", p.sink, m)
	}
}
