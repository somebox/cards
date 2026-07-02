package httpapi

import (
	"bytes"
	"log"
	"net/http"

	"github.com/somebox/cards/internal/core"
)

// --- actor middleware ---

func (s *Server) resolveActor(r *http.Request) string {
	if h := r.Header.Get("X-Work-Cards-Actor"); h != "" {
		return h
	}
	if s.envUser != "" {
		return s.envUser
	}
	return s.ws.Settings.DefaultUser
}

// withActor wraps write handlers that need an actor (API only; UI always has default).
func (s *Server) withActor(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		actor := s.resolveActor(r)
		if actor == "" {
			writeAPIError(w, core.ActorRequired())
			return
		}
		r = r.WithContext(core.WithActor(r.Context(), actor))
		h(w, r)
	}
}

func (s *Server) actorFromCtx(r *http.Request) string {
	if a := core.ActorFromCtx(r.Context()); a != "" {
		return a
	}
	return s.ws.Settings.DefaultUser
}

// --- idempotency ---

// idempotent wraps a write handler so that an Idempotency-Key header replays
// the original response. SPEC §11. Key is scoped per actor.
func (s *Server) idempotent(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("Idempotency-Key")
		if key == "" {
			h(w, r)
			return
		}
		actor := s.actorFromCtx(r)
		rec, err := s.store.GetIdempotency(r.Context(), key, actor)
		if err != nil {
			writeAPIError(w, core.NewValidationError("idempotency", err.Error()))
			return
		}
		if rec != nil {
			// Replay: SPEC §10 lists idempotency_replay as HTTP 200 carrying the
			// original response body.
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Idempotent-Replay", "true")
			w.WriteHeader(200)
			w.Write(rec.Body)
			return
		}
		// Record the response. The handler already ran (mutation durable), so a
		// failed write here cannot fail the request — but it means a client
		// retry with this key will re-execute instead of replaying. Log it.
		rw := &recordingWriter{header: http.Header{}, status: 200, buf: new(bytes.Buffer)}
		h(rw, r)
		if err := s.store.PutIdempotency(r.Context(), core.IdempotencyRecord{
			Key: key, Actor: actor, Status: rw.status, Body: rw.buf.Bytes(),
		}); err != nil {
			log.Printf("ERROR: idempotency record for key %q (actor %s) not persisted; a retry will re-execute: %v", key, actor, err)
		}
		// Forward to the real response writer.
		for k, vs := range rw.header {
			for _, v := range vs {
				w.Header().Add(k, v)
			}
		}
		w.WriteHeader(rw.status)
		w.Write(rw.buf.Bytes())
	}
}

// recordingWriter captures a handler's response for idempotency replay.
type recordingWriter struct {
	header http.Header
	status int
	buf    *bytes.Buffer
}

func (rw *recordingWriter) Header() http.Header {
	if rw.header == nil {
		rw.header = http.Header{}
	}
	return rw.header
}
func (rw *recordingWriter) WriteHeader(code int)        { rw.status = code }
func (rw *recordingWriter) Write(b []byte) (int, error) { return rw.buf.Write(b) }
