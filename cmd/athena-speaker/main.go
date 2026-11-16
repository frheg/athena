// athena-speaker: plays Athena's speech on this machine. It connects to Athena and waits; when you pick it in the
// chat (by its name) and press play, it fetches the answer a piece at a time and plays it. Nothing needs to reach this
// machine: the connection goes out from here.
//
//	athena-speaker [-name kratos] [-play pw-play] [-before "command"]
//
//	-name    how it is listed in the chat (lowercase letters, digits, -), default the host name
//	-play    the command that plays a WAV file; the file is added as its last argument (default pw-play)
//	-before  a command run before playing, for example one that connects a Bluetooth speaker; a failure is logged
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
	Type      string `json:"type"`
	MessageID int64  `json:"message_id"`
	Parts     int    `json:"parts"`
}

type player struct {
	c      *client.Client
	name   string
	play   []string
	before string
	log    *slog.Logger

	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
}

func main() {
	host, _ := os.Hostname()
	name := flag.String("name", strings.ToLower(host), "how this speaker is listed in the chat")
	play := flag.String("play", "pw-play", "command that plays a WAV file (the file is added as the last argument)")
	before := flag.String("before", "", "command run before playing (for example connect a Bluetooth speaker)")
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
	p := &player{c: c, name: *name, play: strings.Fields(*play), before: *before, log: log}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()
	log.Info("waiting for jobs", "athena", c.URL, "version", version)
	c.Forever(ctx, "/speakers/"+*name+"/jobs", func(ev client.Event) {
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

// state tells Athena what plays now (0 = nothing), so the page can show it.
func (p *player) state(id int64) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := p.c.JSON(ctx, "POST", "/speakers/"+p.name+"/state", map[string]int64{"playing": id}, nil); err != nil {
		p.log.Warn("telling Athena what plays", "err", err)
	}
}
