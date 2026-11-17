package web

import (
	"crypto/sha256"
	"net/http"
	"net/url"

	"github.com/back-to-code/sante/internal/check"
	"github.com/back-to-code/sante/internal/scheduler"
)

const pushPath = "/api/push/"

type endpoint struct {
	method  string
	checker *check.HealthEndpoint
}

// Keyed by the token's digest, so lookup time says nothing about the tokens.
func endpoints(jobs []scheduler.Job) map[[sha256.Size]byte]endpoint {
	out := map[[sha256.Size]byte]endpoint{}
	for _, job := range jobs {
		if c, ok := job.Checker.(*check.HealthEndpoint); ok {
			out[sha256.Sum256([]byte(job.Monitor.Token))] = endpoint{job.Monitor.Method, c}
		}
	}
	return out
}

func (s *Server) handlePush(w http.ResponseWriter, r *http.Request) {
	e, ok := s.endpoints[sha256.Sum256([]byte(r.PathValue("token")))]
	if !ok {
		s.json(w, r, http.StatusNotFound, map[string]string{"error": "unknown token"})
		return
	}
	if r.Method != e.method {
		w.Header().Set("Allow", e.method)
		s.json(w, r, http.StatusMethodNotAllowed, map[string]string{"error": "use " + e.method})
		return
	}
	e.checker.RecordCall()
	s.json(w, r, http.StatusOK, map[string]string{"status": "ok"})
}

type endpointView struct {
	Method   string
	URL      string
	Interval string
}

// endpointURL assumes the service reaches Sante at the host the viewer used.
func endpointURL(r *http.Request, token string) string {
	scheme := "http"
	if isHTTPS(r) {
		scheme = "https"
	}
	return scheme + "://" + r.Host + pushPath + url.PathEscape(token)
}
