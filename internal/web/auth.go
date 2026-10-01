package web

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	sessionCookie = "sante_session"
	sessionTTL    = 30 * 24 * time.Hour
)

// Derived from the token so the token never sits in the cookie jar, and
// rotating it signs every browser out.
func sessionValue(token string) string {
	mac := hmac.New(sha256.New, []byte(token))
	mac.Write([]byte("sante session"))
	return hex.EncodeToString(mac.Sum(nil))
}

func (s *Server) validToken(token string) bool {
	// Comparing digests keeps the comparison constant-time when the lengths differ.
	got, want := sha256.Sum256([]byte(token)), sha256.Sum256([]byte(s.cfg.Server.AuthToken))
	return subtle.ConstantTimeCompare(got[:], want[:]) == 1
}

func (s *Server) authed(r *http.Request) bool {
	if c, err := r.Cookie(sessionCookie); err == nil && hmac.Equal([]byte(c.Value), []byte(s.session)) {
		return true
	}
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	return ok && s.validToken(token)
}

func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.authed(r) {
			next(w, r)
			return
		}
		if wantsJSON(r) {
			w.Header().Set("WWW-Authenticate", `Bearer realm="Sante"`)
			s.json(w, r, http.StatusUnauthorized, map[string]string{"error": "authentication required"})
			return
		}
		http.Redirect(w, r, "/login?next="+url.QueryEscape(r.URL.RequestURI()), http.StatusSeeOther)
	}
}

type loginForm struct {
	Next   string
	Failed bool
}

func (s *Server) handleLoginPage(w http.ResponseWriter, r *http.Request) {
	next := localPath(r.URL.Query().Get("next"))
	if s.authed(r) {
		http.Redirect(w, r, next, http.StatusSeeOther)
		return
	}
	l := s.layout(r, "Authenticate", "login", false)
	l.Login.Next = next
	s.render(w, r, http.StatusOK, "login", l)
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	next := localPath(r.PostFormValue("next"))
	if !s.validToken(r.PostFormValue("token")) {
		l := s.layout(r, "Authenticate", "login", false)
		l.Login = loginForm{Next: next, Failed: true}
		s.render(w, r, http.StatusUnauthorized, "login", l)
		return
	}
	s.setSession(w, r, s.session, int(sessionTTL.Seconds()))
	http.Redirect(w, r, next, http.StatusSeeOther)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	s.setSession(w, r, "", -1)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) setSession(w http.ResponseWriter, r *http.Request, value string, maxAge int) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    value,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https",
		// Lax, not Strict, so a shared link to a monitor page opens signed in.
		SameSite: http.SameSiteLaxMode,
	})
}

// Browsers treat "//host" and "/\host" as other origins.
func localPath(p string) string {
	if !strings.HasPrefix(p, "/") || strings.HasPrefix(p, "//") || strings.HasPrefix(p, "/\\") {
		return "/"
	}
	return p
}
