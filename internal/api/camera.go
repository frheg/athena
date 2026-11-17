package api

import (
	"io"
	"net/http"
)

// camera finds the pose, hands and gestures in one camera picture, for live mode in the browser: the body is a JPEG
// (a small one, a few times a second). The models run on the server; the picture is not kept.
func (a *API) camera(w http.ResponseWriter, r *http.Request) {
	if a.Pose == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "the camera is off: no pose service is set up"})
		return
	}
	b, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 2<<20))
	if err != nil {
		fail(w, bad("the picture is larger than 2 MB"))
		return
	}
	if len(b) < 3 || b[0] != 0xff || b[1] != 0xd8 || b[2] != 0xff {
		fail(w, bad("send the picture as a JPEG"))
		return
	}
	out, err := a.Pose.Analyse(r.Context(), b)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(out)
}
