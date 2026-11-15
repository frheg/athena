package files

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"

	"github.com/frheg/athena/internal/mcpx"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// who returns tenant and subject of a request: the tenant is the authenticated caller, the subject comes from the
// X-Athena-Subject header (or ?subject= on the REST API).
func who(ctx context.Context, subject string) (string, string) {
	tenant := mcpx.Caller(ctx)
	if tenant == "" {
		tenant = "default"
	}
	if subject == "" {
		subject = mcpx.Subject(ctx)
	}
	if subject == "" {
		subject = "default"
	}
	return tenant, subject
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, err error) {
	code := http.StatusBadRequest
	if errors.Is(err, ErrNotFound) {
		code = http.StatusNotFound
	}
	writeJSON(w, code, map[string]string{"error": err.Error()})
}

func intQuery(r *http.Request, name string, def int) int {
	if n, err := strconv.Atoi(r.URL.Query().Get(name)); err == nil {
		return n
	}
	return def
}

// inline: types a browser may show in place. Everything else is sent as a download, so an uploaded HTML or SVG
// file can never run in the page's origin.
var inline = map[string]bool{"image/png": true, "image/jpeg": true, "image/gif": true, "image/webp": true}

// Routes is the REST API (for the assistant's core and its UI), mounted under /v1/.
//
//	POST   /v1/files?name=report.pdf    the body is the file; answers with what was read from it
//	GET    /v1/files                    newest first, with usage
//	GET    /v1/files/{id}               one file
//	GET    /v1/files/{id}/content       the bytes (?preview=1: the image preview)
//	GET    /v1/files/{id}/text          the readable text (?offset=&length= in characters)
//	DELETE /v1/files/{id}
func (s *Service) Routes(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/files", func(w http.ResponseWriter, r *http.Request) {
		t, sub := who(r.Context(), r.URL.Query().Get("subject"))
		data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, s.maxBytes()))
		if err != nil {
			writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": fmt.Sprintf("the file is larger than %d MB", s.maxBytes()>>20)})
			return
		}
		f, err := s.Put(r.Context(), t, sub, r.URL.Query().Get("name"), data)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, f)
	})
	mux.HandleFunc("GET /v1/files", func(w http.ResponseWriter, r *http.Request) {
		t, sub := who(r.Context(), r.URL.Query().Get("subject"))
		list, err := s.List(r.Context(), t, sub, intQuery(r, "limit", 100))
		if err != nil {
			writeErr(w, err)
			return
		}
		u, err := s.Usage(r.Context(), t, sub)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"files": list, "usage": u})
	})
	mux.HandleFunc("GET /v1/files/{id}", func(w http.ResponseWriter, r *http.Request) {
		t, sub := who(r.Context(), r.URL.Query().Get("subject"))
		f, err := s.Get(r.Context(), t, sub, r.PathValue("id"))
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 200, f)
	})
	mux.HandleFunc("GET /v1/files/{id}/content", func(w http.ResponseWriter, r *http.Request) {
		t, sub := who(r.Context(), r.URL.Query().Get("subject"))
		preview := r.URL.Query().Get("preview") == "1"
		f, b, err := s.Content(r.Context(), t, sub, r.PathValue("id"), preview)
		if err != nil {
			writeErr(w, err)
			return
		}
		mime, disp := f.Mime, "inline"
		if preview {
			mime = "image/jpeg"
		}
		if !inline[mime] {
			mime, disp = "application/octet-stream", "attachment"
		}
		w.Header().Set("Content-Type", mime)
		w.Header().Set("Content-Disposition", fmt.Sprintf("%s; filename*=UTF-8''%s", disp, pathEscape(f.Name)))
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
		w.Header().Set("Cache-Control", "private, max-age=86400, immutable") // a file never changes
		w.Header().Set("Content-Length", strconv.Itoa(len(b)))
		_, _ = w.Write(b)
	})
	mux.HandleFunc("GET /v1/files/{id}/text", func(w http.ResponseWriter, r *http.Request) {
		t, sub := who(r.Context(), r.URL.Query().Get("subject"))
		f, text, err := s.Text(r.Context(), t, sub, r.PathValue("id"))
		if err != nil {
			writeErr(w, err)
			return
		}
		off := intQuery(r, "offset", 0)
		part, total := Read(text, off, intQuery(r, "length", MaxText))
		writeJSON(w, 200, map[string]any{"file": f, "text": part, "offset": off, "total": total})
	})
	mux.HandleFunc("DELETE /v1/files/{id}", func(w http.ResponseWriter, r *http.Request) {
		t, sub := who(r.Context(), r.URL.Query().Get("subject"))
		if err := s.Delete(r.Context(), t, sub, r.PathValue("id")); err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 200, map[string]bool{"ok": true})
	})
}

// pathEscape escapes a file name for filename*= (RFC 5987).
func pathEscape(s string) string {
	const ok = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-._~"
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < 128 && indexByte(ok, c) {
			out = append(out, c)
		} else {
			out = append(out, fmt.Sprintf("%%%02X", c)...)
		}
	}
	return string(out)
}

func indexByte(s string, c byte) bool {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return true
		}
	}
	return false
}

// ListIn lists files.
type ListIn struct {
	Limit int `json:"limit,omitempty" jsonschema:"how many, newest first (default 20)"`
}

// ReadIn reads part of a file's text.
type ReadIn struct {
	ID     string `json:"id" jsonschema:"the file id"`
	Offset int    `json:"offset,omitempty" jsonschema:"where to start, in characters (default 0)"`
	Length int    `json:"length,omitempty" jsonschema:"how many characters (default and at most 20000)"`
}

// FindIn searches a file's text.
type FindIn struct {
	ID    string `json:"id" jsonschema:"the file id"`
	Query string `json:"query" jsonschema:"words that must all occur close together (case does not matter)"`
	Limit int    `json:"limit,omitempty" jsonschema:"most places to return (default 10)"`
}

// Register adds the MCP tools.
func (s *Service) Register(srv *mcp.Server) {
	type list struct {
		Files []File `json:"files"`
	}
	mcp.AddTool(srv, &mcp.Tool{Name: "list", Description: "List the files the user has attached, newest first: name, type, size, pages, and the id to read them with."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in ListIn) (*mcp.CallToolResult, list, error) {
			t, sub := who(ctx, "")
			if in.Limit <= 0 {
				in.Limit = 20
			}
			fs, err := s.List(ctx, t, sub, in.Limit)
			return nil, list{fs}, err
		})
	mcp.AddTool(srv, &mcp.Tool{Name: "read", Description: "Read part of an attached file's text (documents, code, PDFs). Use offset to read on where an attachment was cut."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in ReadIn) (*mcp.CallToolResult, any, error) {
			t, sub := who(ctx, "")
			f, text, err := s.Text(ctx, t, sub, in.ID)
			if err != nil {
				return nil, nil, err
			}
			if text == "" {
				msg := "This file has no readable text."
				if f.Note != "" {
					msg += " " + f.Note
				}
				return mcpx.Text(msg), nil, nil
			}
			if in.Length <= 0 || in.Length > 20000 {
				in.Length = 20000
			}
			part, total := Read(text, in.Offset, in.Length)
			end := min(total, max(in.Offset, 0)+len([]rune(part)))
			head := fmt.Sprintf("%s: characters %d to %d of %d.", f.Name, max(in.Offset, 0), end, total)
			if end < total {
				head += fmt.Sprintf(" Read on with offset %d.", end)
			}
			return mcpx.Text(head + "\n" + mcpx.Untrusted("file: "+f.Name, part)), nil, nil
		})
	mcp.AddTool(srv, &mcp.Tool{Name: "find", Description: "Find where words occur in an attached file's text; returns snippets with the offset to read from."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in FindIn) (*mcp.CallToolResult, any, error) {
			t, sub := who(ctx, "")
			f, text, err := s.Text(ctx, t, sub, in.ID)
			if err != nil {
				return nil, nil, err
			}
			hits := Find(text, in.Query, in.Limit)
			if len(hits) == 0 {
				return mcpx.Text(fmt.Sprintf("No place in %s has all of: %s", f.Name, in.Query)), nil, nil
			}
			return mcpx.Text(mcpx.Untrusted("file: "+f.Name, mcpx.JSON(map[string]any{"file": f.Name, "total": len([]rune(text)), "hits": hits}))), nil, nil
		})
}
