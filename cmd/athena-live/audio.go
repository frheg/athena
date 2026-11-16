package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"math"
	"os"
	"os/exec"
	"strings"
	"time"
)

const (
	rate      = 16000 // what speech recognition wants
	frameLen  = rate / 50
	frameTime = 20 * time.Millisecond
)

// mic runs the record command (raw 16-bit mono at 16 kHz on stdout) and sends 20 ms frames until ctx ends.
func mic(ctx context.Context, command string, frames chan<- []int16) error {
	args := strings.Fields(command)
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	defer func() { _ = cmd.Wait() }()
	rd := bufio.NewReaderSize(out, 1<<16)
	buf := make([]byte, frameLen*2)
	for {
		if _, err := io.ReadFull(rd, buf); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		f := make([]int16, frameLen)
		for i := range f {
			f[i] = int16(binary.LittleEndian.Uint16(buf[2*i:]))
		}
		select {
		case frames <- f:
		default: // the reader is behind: drop a frame rather than fall further behind
		}
	}
}

// level is a frame's loudness in dBFS (0 is full scale, silence about -90).
func level(f []int16) float64 {
	var s float64
	for _, x := range f {
		s += float64(x) * float64(x)
	}
	rms := math.Sqrt(s / float64(len(f)))
	return 20 * math.Log10(math.Max(rms, 1)/32768)
}

// vad finds stretches of speech by loudness: louder than the background (which it learns while nobody speaks) by
// margin dB for a few frames starts one; quiet for hangover ends it. It keeps a little audio from before the start,
// so the first sound is not cut.
type vad struct {
	margin   float64       // dB above the background, default 12
	hangover time.Duration // quiet that ends speech, default 700 ms
	maxLen   time.Duration // longest stretch, default 15 s
	minLen   time.Duration // shorter is noise, default 300 ms

	floor   float64
	loud    int // frames in a row above the threshold
	quiet   int // frames in a row below it, while speaking
	voiced  int // loud frames in this stretch
	on      bool
	pre     [][]int16
	speech  []int16
	started bool
}

func newVAD() *vad {
	return &vad{margin: 12, hangover: 700 * time.Millisecond, maxLen: 15 * time.Second, minLen: 300 * time.Millisecond, floor: -60}
}

// add takes a frame; it returns a finished stretch of speech, if this frame ended one, and whether speech is on.
func (v *vad) add(f []int16) ([]int16, bool) {
	db := level(f)
	if !v.on {
		// the background follows quiet slowly and rises slowly, so a voice does not become the background
		if db < v.floor {
			v.floor = 0.9*v.floor + 0.1*db
		} else {
			v.floor = 0.995*v.floor + 0.005*db
		}
		v.floor = math.Max(v.floor, -75)
	}
	above := db > v.floor+v.margin
	if !v.on {
		v.pre = append(v.pre, f)
		if len(v.pre) > 15 { // 300 ms before the start
			v.pre = v.pre[1:]
		}
		if above {
			v.loud++
		} else {
			v.loud = 0
		}
		if v.loud >= 3 {
			v.on, v.quiet, v.voiced = true, 0, v.loud
			v.speech = v.speech[:0]
			for _, p := range v.pre {
				v.speech = append(v.speech, p...)
			}
			v.pre = nil
		}
		return nil, v.on
	}
	v.speech = append(v.speech, f...)
	if above {
		v.quiet = 0
		v.voiced++
	} else {
		v.quiet++
	}
	length := time.Duration(len(v.speech)) * time.Second / rate
	if time.Duration(v.quiet)*frameTime >= v.hangover || length >= v.maxLen {
		v.on, v.loud = false, 0
		out := append([]int16(nil), v.speech...)
		if time.Duration(v.voiced)*frameTime < v.minLen { // a knock or a cough, not speech
			return nil, false
		}
		return out, false
	}
	return nil, true
}

// wav wraps 16-bit mono samples in a WAV header.
func wav(samples []int16, sampleRate int) []byte {
	var b bytes.Buffer
	n := len(samples) * 2
	b.WriteString("RIFF")
	_ = binary.Write(&b, binary.LittleEndian, uint32(36+n))
	b.WriteString("WAVEfmt ")
	for _, v := range []any{uint32(16), uint16(1), uint16(1), uint32(sampleRate), uint32(sampleRate * 2), uint16(2), uint16(16)} {
		_ = binary.Write(&b, binary.LittleEndian, v) // format: PCM, mono, 16-bit
	}
	b.WriteString("data")
	_ = binary.Write(&b, binary.LittleEndian, uint32(n))
	_ = binary.Write(&b, binary.LittleEndian, samples)
	return b.Bytes()
}

// envelope is the loudness of a WAV (16-bit PCM) per 50 ms, from 0 to 1, to move the avatar's beak with the voice.
func envelope(w []byte) ([]float64, error) {
	if len(w) < 12 || string(w[:4]) != "RIFF" || string(w[8:12]) != "WAVE" {
		return nil, errors.New("not a WAV")
	}
	var sampleRate, channels int
	var data []byte
	for p := 12; p+8 <= len(w); {
		id, size := string(w[p:p+4]), int(binary.LittleEndian.Uint32(w[p+4:]))
		body := w[p+8 : min(len(w), p+8+size)]
		switch id {
		case "fmt ":
			if len(body) >= 16 {
				channels = int(binary.LittleEndian.Uint16(body[2:]))
				sampleRate = int(binary.LittleEndian.Uint32(body[4:]))
			}
		case "data":
			data = body
		}
		p += 8 + size + size%2
	}
	if sampleRate == 0 || channels == 0 || data == nil {
		return nil, errors.New("no audio in the WAV")
	}
	step := sampleRate / 20 * channels * 2
	var out []float64
	for i := 0; i+step <= len(data); i += step {
		var s float64
		for j := i; j < i+step; j += 2 {
			x := float64(int16(binary.LittleEndian.Uint16(data[j:])))
			s += x * x
		}
		rms := math.Sqrt(s/float64(step/2)) / 32768
		out = append(out, math.Min(1, rms*5))
	}
	return out, nil
}

// play plays a WAV with the play command (the file is its last argument) and returns when it has ended or ctx ends.
func play(ctx context.Context, command string, w []byte) error {
	f, err := os.CreateTemp("", "athena-live-*.wav")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(w); err != nil {
		f.Close()
		return err
	}
	f.Close()
	args := strings.Fields(command)
	return exec.CommandContext(ctx, args[0], append(args[1:], f.Name())...).Run()
}
