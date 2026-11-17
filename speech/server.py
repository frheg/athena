"""speech: text to speech (Piper and Kokoro voices) and speech to text (whisper.cpp) for assistants, behind an
OpenAI-compatible API.

    POST /v1/audio/speech   {"input": "...", "voice": "auto" | "<voice id>", "speaker": 0, "speed": 1.0,
                             "voices": {"no": "<id>", "en": "<id>"}}  ->  audio/wav
                            "auto" picks Norwegian or English from the text; "voices" overrides the defaults for that.
                            A voice written "<id>:<n>" is speaker n of a voice with several.
    POST /v1/audio/transcriptions   multipart/form-data with "file" (WAV) and optionally "prompt" -> {"text": "..."};
                            done by a whisper.cpp server this process starts on 127.0.0.1
    GET  /v1/models         the voices: id, language, speakers
    GET  /health, /ready

Environment:
    VOICES_DIR      where the voices are, default /voices: Piper voices as <id>.onnx and <id>.onnx.json, and Kokoro as
                    kokoro/kokoro-v1.0.onnx and kokoro/voices-v1.0.bin (its voices are kokoro-<name>, English)
    DEFAULT_VOICES  language=voice pairs for "auto", default no=no_NO-talesyntese-medium,en=kokoro-af_heart
    TOKENS          caller=token pairs; a request must carry "Authorization: Bearer <token>". Empty = no check.
    LISTEN          host:port, default 0.0.0.0:8080
    WHISPER_MODEL   the speech recognition model, default VOICES_DIR/whisper/ggml-small-q5_1.bin ("" turns it off)
    WHISPER_THREADS CPU threads for speech recognition, default 4

Python and the standard library only, besides Piper (GPL-3.0) and Kokoro (Apache 2.0): one process, a thread per request,
a lock per voice. Kokoro sounds more natural and has English only; Piper speaks Norwegian. Text is limited to 5000 characters a request; callers split longer text.
whisper.cpp's server has no sign-in of its own, so it listens on 127.0.0.1 only and this process checks the token.
"""

import hmac
import io
import json
import logging
import os
import re
import subprocess
import sys
import threading
import time
import urllib.error
import urllib.request
import wave
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

import numpy
import onnxruntime
from kokoro_onnx import Kokoro
from piper import PiperVoice, SynthesisConfig

onnxruntime.disable_telemetry_events()  # onnxruntime has usage telemetry; nothing here should phone home

MAX_CHARS = 5000
log = logging.getLogger("speech")


def pairs(s):
    out = {}
    for p in (s or "").split(","):
        if "=" in p:
            k, v = p.split("=", 1)
            if k.strip() and v.strip():
                out[k.strip()] = v.strip()
    return out


VOICES_DIR = os.environ.get("VOICES_DIR", "/voices")
DEFAULTS = pairs(os.environ.get("DEFAULT_VOICES", "no=no_NO-talesyntese-medium,en=kokoro-af_heart"))
# the Kokoro voices offered: the best rated ones (af/am American, bf/bm British; f woman, m man)
KOKORO = ["af_heart", "af_bella", "af_nicole", "af_sarah", "af_nova", "am_michael", "am_fenrir", "am_puck", "am_eric",
          "bf_emma", "bf_isabella", "bf_alice", "bm_george", "bm_fable", "bm_daniel"]
KOKORO_DIR = os.path.join(VOICES_DIR, "kokoro")
TOKENS = pairs(os.environ.get("TOKENS", ""))
WHISPER_MODEL = os.environ.get("WHISPER_MODEL", os.path.join(VOICES_DIR, "whisper", "ggml-small-q5_1.bin"))
WHISPER_THREADS = os.environ.get("WHISPER_THREADS", "4")
WHISPER_ADDR = ("127.0.0.1", 8081)
MAX_AUDIO = 10 << 20  # bytes: about 5 minutes of 16 kHz speech


def available():
    """The voices on disk: id -> language (the part before the first _ of the id, like no or en)."""
    out = {}
    for f in sorted(os.listdir(VOICES_DIR)):
        if f.endswith(".onnx") and os.path.exists(os.path.join(VOICES_DIR, f + ".json")):
            vid = f[: -len(".onnx")]
            out[vid] = vid.split("_", 1)[0]
    if os.path.exists(os.path.join(KOKORO_DIR, "kokoro-v1.0.onnx")):
        for name in KOKORO:
            out["kokoro-" + name] = "en"
    return out


_voices, _locks, _load_lock = {}, {}, threading.Lock()


def voice(vid):
    with _load_lock:
        if vid not in _voices:
            _voices[vid] = PiperVoice.load(os.path.join(VOICES_DIR, vid + ".onnx"))
            _locks[vid] = threading.Lock()
        return _voices[vid], _locks[vid]


NO_WORDS = set("og ikke jeg det er på til en et som med har for av deg du vi hva hvor kan skal vil blir var hun han "
               "meg min mitt mine dette den dere de sin seg også men når fra her der nå bare veldig hei takk ja nei "
               "god morgen kveld natt ha bra".split())
EN_WORDS = set("the and is you to of in it that what for with this are be was have not your can will on at from "
               "there here now just very which would about".split())


def language(text):
    """no or en, from letters and common words. Norwegian wins ties when æ, ø or å appear."""
    words = re.findall(r"[a-zæøå]+", text.lower())
    no = sum(w in NO_WORDS for w in words) + 3 * len(re.findall(r"[æøå]", text.lower()))
    en = sum(w in EN_WORDS for w in words)
    return "no" if no > en else "en"


_kokoro, _kokoro_lock = None, threading.Lock()


def kokoro():
    global _kokoro
    with _load_lock:
        if _kokoro is None:
            _kokoro = Kokoro(os.path.join(KOKORO_DIR, "kokoro-v1.0.onnx"), os.path.join(KOKORO_DIR, "voices-v1.0.bin"))
        return _kokoro


def wav16(samples, rate):
    """float samples (-1 to 1) as a 16-bit mono WAV"""
    pcm = (numpy.clip(samples, -1, 1) * 32767).astype("<i2").tobytes()
    buf = io.BytesIO()
    with wave.open(buf, "wb") as w:
        w.setnchannels(1)
        w.setsampwidth(2)
        w.setframerate(rate)
        w.writeframes(pcm)
    return buf.getvalue()


def synthesize(text, vid, speaker=None, speed=1.0):
    if vid.startswith("kokoro-"):
        name = vid[len("kokoro-"):]
        with _kokoro_lock:
            samples, rate = kokoro().create(text, voice=name, speed=speed, lang="en-gb" if name.startswith("b") else "en-us")
        return wav16(samples, rate)
    v, lock = voice(vid)
    cfg = SynthesisConfig(speaker_id=speaker if v.config.num_speakers > 1 else None, length_scale=1.0 / speed)
    buf = io.BytesIO()
    with lock, wave.open(buf, "wb") as w:
        v.synthesize_wav(text, w, syn_config=cfg)
    return buf.getvalue()


def whisper_cmd():
    # -ac 768: a shorter audio window (speech up to about 15 s), about twice as fast as the full 30 s;
    # -bs 1 -bo 1: greedy, no beam search; -nf: no retries at higher temperature (they make text up from noise)
    return ["whisper-server", "--host", WHISPER_ADDR[0], "--port", str(WHISPER_ADDR[1]), "-m", WHISPER_MODEL,
            "-t", WHISPER_THREADS, "-ac", "768", "-bs", "1", "-bo", "1", "-nf", "-fa", "-l", "auto"]


def keep_whisper():
    """Runs the whisper.cpp server, and again whenever it stops."""
    while True:
        try:
            p = subprocess.Popen(whisper_cmd(), stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        except OSError as e:
            log.error("speech recognition does not start: %s", e)
            return
        code = p.wait()
        log.warning("speech recognition stopped (exit %s); starting it again", code)
        time.sleep(2)


def transcribe(body, ctype):
    """Hands a multipart request to whisper.cpp; returns (status, result)."""
    req = urllib.request.Request(f"http://{WHISPER_ADDR[0]}:{WHISPER_ADDR[1]}/inference", data=body,
                                 headers={"Content-Type": ctype}, method="POST")
    try:
        with urllib.request.urlopen(req, timeout=60) as res:
            out = json.loads(res.read(1 << 20) or b"{}")
    except urllib.error.HTTPError as e:
        return 502, {"error": f"speech recognition: {e.code} {e.read(500).decode(errors='replace').strip()}"}
    except (urllib.error.URLError, OSError) as e:
        return 503, {"error": f"speech recognition is not ready: {e}"}
    except json.JSONDecodeError:
        return 502, {"error": "speech recognition answered something that is not JSON"}
    if out.get("error"):
        return 502, {"error": "speech recognition: " + str(out["error"])}
    return 200, {"text": " ".join(str(out.get("text", "")).split())}


class Handler(BaseHTTPRequestHandler):
    server_version = "speech"
    sys_version = ""

    def log_message(self, format, *args):  # the base class's parameter name
        log.info("%s %s", self.address_string(), format % args)

    def send(self, code, body, ctype="application/json", headers=None):
        if isinstance(body, (dict, list)):
            body = json.dumps(body).encode()
        self.send_response(code)
        self.send_header("Content-Type", ctype)
        self.send_header("Content-Length", str(len(body)))
        for k, v in (headers or {}).items():
            self.send_header(k, v)
        self.end_headers()
        self.wfile.write(body)

    def error(self, code, msg):
        self.send(code, {"error": msg})

    def allowed(self):
        if not TOKENS:
            return True
        got = self.headers.get("Authorization", "").removeprefix("Bearer ")
        return any(hmac.compare_digest(got, t) for t in TOKENS.values())

    def do_GET(self):
        if self.path == "/health":
            return self.send(200, b"ok\n", "text/plain")
        if self.path == "/ready":
            return self.send(200, b"ready\n", "text/plain") if available() else self.error(503, "no voices in " + VOICES_DIR)
        if not self.allowed():
            return self.error(401, "unknown caller")
        if self.path == "/v1/models":
            data = []
            for vid, lang in available().items():
                speakers = 1
                if not vid.startswith("kokoro-"):
                    with open(os.path.join(VOICES_DIR, vid + ".onnx.json"), encoding="utf-8") as f:
                        speakers = json.load(f).get("num_speakers", 1)
                data.append({"id": vid, "object": "model", "language": lang, "speakers": speakers,
                             "default": DEFAULTS.get(lang) == vid})
            return self.send(200, {"object": "list", "data": data, "defaults": DEFAULTS})
        return self.error(404, "no such endpoint")

    def do_POST(self):
        if not self.allowed():
            return self.error(401, "unknown caller")
        if self.path == "/v1/audio/transcriptions":
            return self.transcription()
        if self.path != "/v1/audio/speech":
            return self.error(404, "no such endpoint")
        try:
            n = int(self.headers.get("Content-Length", "0"))
            if n > 64 * 1024:
                return self.error(413, "the request is too large")
            req = json.loads(self.rfile.read(n) or b"{}")
        except (ValueError, json.JSONDecodeError):
            return self.error(400, "bad JSON body")
        text = str(req.get("input", "")).strip()
        if not text:
            return self.error(400, "input is empty")
        if len(text) > MAX_CHARS:
            return self.error(400, f"input is longer than {MAX_CHARS} characters: split it")
        fmt = req.get("response_format", "wav")
        if fmt not in ("wav", None):
            return self.error(400, "only wav is supported")
        voices = available()
        vid = req.get("voice") or "auto"
        lang = ""
        if vid in ("auto", "no", "en"):
            lang = language(text) if vid == "auto" else vid
            chosen = {**DEFAULTS, **{k: v for k, v in (req.get("voices") or {}).items() if v.split(":")[0] in voices}}
            vid = chosen.get(lang, "")
        speaker = req.get("speaker")
        if ":" in vid:  # "<voice>:<speaker>" picks one speaker of a voice with several
            vid, speaker = vid.split(":", 1)
        if vid not in voices:
            return self.error(400, f"no such voice: {vid or 'none for ' + lang}")
        try:
            speed = min(max(float(req.get("speed", 1.0)), 0.5), 2.0)
            speaker = int(speaker) if speaker is not None else None
        except (TypeError, ValueError):
            return self.error(400, "speed and speaker must be numbers")
        try:
            audio = synthesize(text, vid, speaker, speed)
        except Exception as e:  # a voice that fails to load or speak
            log.exception("synthesis failed")
            return self.error(500, f"synthesis failed: {e}")
        self.send(200, audio, "audio/wav", {"X-Voice": vid, "X-Language": lang or voices[vid]})

    def transcription(self):
        if not WHISPER_MODEL:
            return self.error(503, "speech recognition is off")
        ctype = self.headers.get("Content-Type", "")
        if not ctype.startswith("multipart/form-data"):
            return self.error(400, "send multipart/form-data with the audio as file")
        try:
            n = int(self.headers.get("Content-Length", "0"))
        except ValueError:
            n = 0
        if n <= 0 or n > MAX_AUDIO:
            return self.error(413, "the audio is empty or too long")
        code, out = transcribe(self.rfile.read(n), ctype)
        self.send(code, out)


def main():
    logging.basicConfig(level=logging.INFO, stream=sys.stdout, format='{"time":"%(asctime)s","level":"%(levelname)s","msg":"%(message)s"}')
    if len(sys.argv) > 1 and sys.argv[1] == "health":  # the container's health check
        import urllib.request
        port = os.environ.get("LISTEN", "0.0.0.0:8080").rsplit(":", 1)[1]
        try:
            urllib.request.urlopen(f"http://127.0.0.1:{port}/health", timeout=3)
        except Exception:
            sys.exit(1)
        return
    host, port = os.environ.get("LISTEN", "0.0.0.0:8080").rsplit(":", 1)
    log.info("voices: %s; defaults: %s; auth: %s", ", ".join(available()), DEFAULTS, bool(TOKENS))
    if WHISPER_MODEL and os.path.exists(WHISPER_MODEL):
        threading.Thread(target=keep_whisper, daemon=True).start()
        log.info("speech recognition: %s, %s threads", os.path.basename(WHISPER_MODEL), WHISPER_THREADS)
    elif WHISPER_MODEL:
        log.warning("no speech recognition: %s is missing", WHISPER_MODEL)
    ThreadingHTTPServer((host, int(port)), Handler).serve_forever()


if __name__ == "__main__":
    main()
