"""pose: finds a person's pose, hands and hand gestures in camera pictures, with MediaPipe on the CPU. Live mode in
the browser sends a small picture a few times a second (through core); the answer is points and names.

    POST /v1/pose   a JPEG (image/jpeg, up to 2 MB) ->
                    {"ms": time spent, "pose": [[x, y, visibility] x 33] or null,   x and y from 0 to 1, as in the picture
                     "poses": ["left arm up", ...],                                 simple poses worked out from the points
                     "hands": [{"side": "left", "gesture": "thumbs up", "score": 0.93, "points": [[x, y] x 21]}]}
                    Sides are the person's own, for a picture that is not mirrored. Nothing is stored.
    GET  /health, /ready

Environment:
    MODELS_DIR  where the models are, default /models
    POSE_MODEL  full or lite, default full
    WIDTH       pictures are scaled down to this width first, default 640
    TOKENS      caller=token pairs; a request must carry "Authorization: Bearer <token>". Empty = no check.
    LISTEN      host:port, default 0.0.0.0:8080

One process, a thread per request, one lock around the models (they are not safe to share between threads).
Pictures are taken one at a time (IMAGE mode), so pictures from different cameras do not mix.
"""

import hmac
import json
import logging
import os
import sys
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

log = logging.getLogger("pose")

MODELS = os.environ.get("MODELS_DIR", "/models")
WIDTH = int(os.environ.get("WIDTH", "640"))
MAX_IMAGE = 2 << 20

GESTURES = {
    "Thumb_Up": "thumbs up", "Thumb_Down": "thumbs down", "Open_Palm": "open palm", "Closed_Fist": "fist",
    "Pointing_Up": "pointing up", "Victory": "victory", "ILoveYou": "I love you",
}

# pose landmark numbers (MediaPipe's): nose, then left/right shoulder, elbow, wrist, hip
NOSE, L_SH, R_SH, L_EL, R_EL, L_WR, R_WR, L_HIP, R_HIP = 0, 11, 12, 13, 14, 15, 16, 23, 24


def pairs(s):
    out = {}
    for p in (s or "").split(","):
        if "=" in p:
            k, v = p.split("=", 1)
            if k.strip() and v.strip():
                out[k.strip()] = v.strip()
    return out


TOKENS = pairs(os.environ.get("TOKENS", ""))


def poses(p):
    """Simple poses from the pose points (each [x, y, visibility]); y grows downwards."""
    seen = lambda *i: all(p[k][2] > 0.5 for k in i)
    out = []
    up = {}
    for side, wr, sh in (("left", L_WR, L_SH), ("right", R_WR, R_SH)):
        # a hand above the head
        up[side] = seen(wr, sh, NOSE) and p[wr][1] < p[NOSE][1]
    if up["left"] and up["right"]:
        out.append("both arms up")
    elif up["left"]:
        out.append("left arm up")
    elif up["right"]:
        out.append("right arm up")
    if seen(L_WR, R_WR, L_EL, R_EL, L_SH, R_SH):
        width = abs(p[L_SH][0] - p[R_SH][0])
        level = all(abs(p[w][1] - p[s][1]) < 0.6 * width and abs(p[e][1] - p[s][1]) < 0.6 * width
                    for w, e, s in ((L_WR, L_EL, L_SH), (R_WR, R_EL, R_SH)))
        wide = abs(p[L_WR][0] - p[R_WR][0]) > 2.5 * width
        if level and wide:
            out.append("T-pose")
    if seen(L_WR, R_WR, L_SH, R_SH, L_HIP, R_HIP):
        # both hands on the hips: wrists near the hips
        near = all(abs(p[w][0] - p[h][0]) < 0.12 and abs(p[w][1] - p[h][1]) < 0.12 for w, h in ((L_WR, L_HIP), (R_WR, R_HIP)))
        if near:
            out.append("hands on hips")
    return out


class Models:
    """The pose and gesture models, loaded once."""

    def __init__(self):
        import cv2
        import mediapipe as mp
        import numpy as np
        from mediapipe.tasks.python import BaseOptions, vision

        self.cv2, self.mp, self.np = cv2, mp, np
        mode = vision.RunningMode.IMAGE
        pose_model = "pose_landmarker_%s.task" % os.environ.get("POSE_MODEL", "full")
        self.pose = vision.PoseLandmarker.create_from_options(vision.PoseLandmarkerOptions(
            base_options=BaseOptions(model_asset_path=os.path.join(MODELS, pose_model)), running_mode=mode, num_poses=1))
        self.hands = vision.GestureRecognizer.create_from_options(vision.GestureRecognizerOptions(
            base_options=BaseOptions(model_asset_path=os.path.join(MODELS, "gesture_recognizer.task")), running_mode=mode, num_hands=2))
        self.lock = threading.Lock()
        log.info("models: %s, gesture_recognizer", pose_model)

    def analyse(self, data):
        cv2, mp, np = self.cv2, self.mp, self.np
        start = time.monotonic()
        img = cv2.imdecode(np.frombuffer(data, np.uint8), cv2.IMREAD_COLOR)
        if img is None:
            return None
        h, w = img.shape[:2]
        if w > WIDTH:
            img = cv2.resize(img, (WIDTH, h * WIDTH // w), interpolation=cv2.INTER_AREA)
        frame = mp.Image(image_format=mp.ImageFormat.SRGB, data=cv2.cvtColor(img, cv2.COLOR_BGR2RGB))
        out = {"pose": None, "poses": [], "hands": []}
        with self.lock:
            pr = self.pose.detect(frame)
            gr = self.hands.recognize(frame)
        if pr.pose_landmarks:
            p = [[round(l.x, 4), round(l.y, 4), round(l.visibility or 0, 2)] for l in pr.pose_landmarks[0]]
            out["pose"], out["poses"] = p, poses(p)
        for i, pts in enumerate(gr.hand_landmarks):
            g = gr.gestures[i][0] if gr.gestures and gr.gestures[i] else None
            side = gr.handedness[i][0].category_name.lower() if gr.handedness and gr.handedness[i] else ""
            # MediaPipe names the side as seen in a mirrored selfie picture; this picture is not mirrored
            side = {"left": "right", "right": "left"}.get(side, side)
            out["hands"].append({
                "side": side,
                "gesture": GESTURES.get(g.category_name, "") if g else "",
                "score": round(g.score, 2) if g else 0,
                "points": [[round(l.x, 4), round(l.y, 4)] for l in pts],
            })
        out["ms"] = round((time.monotonic() - start) * 1000, 1)
        return out


models = None


class Handler(BaseHTTPRequestHandler):
    server_version = "pose"
    sys_version = ""

    def log_message(self, format, *args):  # the base class's parameter name
        pass  # a few requests a second while live mode runs: too many to log

    def send(self, code, body):
        b = json.dumps(body, separators=(",", ":")).encode()
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(b)))
        self.end_headers()
        self.wfile.write(b)

    def error(self, code, msg):
        self.send(code, {"error": msg})

    def allowed(self):
        if not TOKENS:
            return True
        got = self.headers.get("Authorization", "").removeprefix("Bearer ")
        return any(hmac.compare_digest(got, t) for t in TOKENS.values())

    def do_GET(self):
        if self.path == "/health":
            return self.send(200, {"ok": True})
        if self.path == "/ready":
            return self.send(200, {"ready": True}) if models else self.error(503, "loading the models")
        self.error(404, "no such endpoint")

    def do_POST(self):
        if not self.allowed():
            return self.error(401, "unknown caller")
        if self.path != "/v1/pose":
            return self.error(404, "no such endpoint")
        if not models:
            return self.error(503, "loading the models")
        try:
            n = int(self.headers.get("Content-Length", "0"))
        except ValueError:
            n = 0
        if n <= 0 or n > MAX_IMAGE:
            return self.error(413, "send one JPEG of at most 2 MB")
        data = self.rfile.read(n)
        if data[:3] != b"\xff\xd8\xff":
            return self.error(400, "not a JPEG")
        try:
            out = models.analyse(data)
        except Exception as e:  # a picture MediaPipe cannot take
            log.exception("analysis failed")
            return self.error(500, f"analysis failed: {e}")
        if out is None:
            return self.error(400, "not a JPEG")
        self.send(200, out)


def main():
    global models
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
    srv = ThreadingHTTPServer((host, int(port)), Handler)
    log.info("listening on %s:%s; auth: %s", host, port, bool(TOKENS))
    models = Models()
    srv.serve_forever()


if __name__ == "__main__":
    main()
