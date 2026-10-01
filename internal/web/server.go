package web

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"mime"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/back-to-code/sante/internal/config"
	"github.com/back-to-code/sante/internal/scheduler"
	"github.com/back-to-code/sante/internal/store"
)

//go:embed templates static
var files embed.FS

// themeScript runs before first paint so a saved theme never flashes the
// system one; the CSP allows it by hash.
const themeScript = `try{var t=localStorage.getItem("sante-theme");if(t)document.documentElement.dataset.theme=t}catch(e){}`

type Server struct {
	cfg       *config.Config
	store     *store.Store
	scheduler *scheduler.Scheduler
	version   string
	started   time.Time
	log       *slog.Logger

	pages   map[string]*template.Template
	assets  map[string]asset
	csp     string
	session string
	// assetVersion changes whenever a static file does, busting browser caches.
	assetVersion string
}

type asset struct {
	body, gzipped []byte
	contentType   string
}

func New(cfg *config.Config, st *store.Store, sched *scheduler.Scheduler, version string, log *slog.Logger) (*Server, error) {
	s := &Server{cfg: cfg, store: st, scheduler: sched, version: version, started: time.Now(), log: log,
		session: sessionValue(cfg.Server.AuthToken)}
	if err := s.loadAssets(); err != nil {
		return nil, err
	}
	if err := s.loadTemplates(); err != nil {
		return nil, err
	}
	sum := sha256.Sum256([]byte(themeScript))
	s.csp = strings.Join([]string{
		"default-src 'self'",
		"script-src 'self' 'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'",
		"style-src 'self' 'unsafe-inline' https://fonts.googleapis.com",
		"font-src 'self' https://fonts.gstatic.com",
		"img-src 'self' data:",
		"connect-src 'self'",
		"frame-ancestors 'none'",
		"base-uri 'none'",
		"form-action 'self'",
	}, "; ")
	return s, nil
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.handleStatus)
	mux.HandleFunc("GET /monitors/{id}", s.requireAuth(s.handleMonitor))
	mux.HandleFunc("GET /health", s.requireAuth(s.handleHealth))
	mux.HandleFunc("GET /login", s.handleLoginPage)
	mux.HandleFunc("POST /login", s.handleLogin)
	mux.HandleFunc("POST /logout", s.handleLogout)
	mux.HandleFunc("GET /api/status", s.handleAPIStatus)
	mux.HandleFunc("GET /static/{file}", s.handleAsset)
	mux.HandleFunc("GET /favicon.svg", s.handleFavicon)
	return s.headers(http.NewCrossOriginProtection().Handler(mux))
}

func (s *Server) headers(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("Content-Security-Policy", s.csp)
		next.ServeHTTP(w, r)
	})
}

func (s *Server) loadAssets() error {
	s.assets = map[string]asset{}
	versionHash := sha256.New()
	err := fs.WalkDir(files, "static", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		body, err := files.ReadFile(p)
		if err != nil {
			return err
		}
		versionHash.Write(body)
		var gz bytes.Buffer
		zw, _ := gzip.NewWriterLevel(&gz, gzip.BestCompression)
		zw.Write(body)
		zw.Close()
		s.assets[path.Base(p)] = asset{body: body, gzipped: gz.Bytes(), contentType: mime.TypeByExtension(path.Ext(p))}
		return nil
	})
	s.assetVersion = hex.EncodeToString(versionHash.Sum(nil))[:12]
	return err
}

func (s *Server) handleAsset(w http.ResponseWriter, r *http.Request) {
	a, ok := s.assets[r.PathValue("file")]
	if !ok {
		http.NotFound(w, r)
		return
	}
	if r.URL.Query().Get("v") == s.assetVersion {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "public, max-age=300")
	}
	s.write(w, r, http.StatusOK, a.contentType, a.body, a.gzipped)
}

func (s *Server) handleFavicon(w http.ResponseWriter, r *http.Request) {
	// The fill matches the light theme's --primary in app.css.
	svg := `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 100 100">` +
		`<path fill="#65558F" d="` + shapes["cookie9"] + `"/>` +
		`<svg x="22" y="22" width="56" height="56" viewBox="0 -960 960 960"><path fill="#fff" d="` + icons["monitor"] + `"/></svg></svg>`
	w.Header().Set("Cache-Control", "public, max-age=86400")
	s.write(w, r, http.StatusOK, "image/svg+xml", []byte(svg), nil)
}

func (s *Server) loadTemplates() error {
	s.pages = map[string]*template.Template{}
	for _, page := range []string{"status", "monitor", "health", "login"} {
		t, err := template.New(page).Funcs(templateFuncs(s.cfg.Location)).
			ParseFS(files, "templates/layout.html", "templates/partials.html", "templates/"+page+".html")
		if err != nil {
			return err
		}
		s.pages[page] = t
	}
	return nil
}

func (s *Server) render(w http.ResponseWriter, r *http.Request, status int, page string, data any) {
	var buf bytes.Buffer
	if err := s.pages[page].ExecuteTemplate(&buf, "layout", data); err != nil {
		s.fail(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	s.write(w, r, status, "text/html; charset=utf-8", buf.Bytes(), nil)
}

func (s *Server) json(w http.ResponseWriter, r *http.Request, status int, v any) {
	body, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	s.write(w, r, status, "application/json", append(body, '\n'), nil)
}

// write sends body, gzipped when the client accepts it. gzipped may be nil to
// compress on the fly.
func (s *Server) write(w http.ResponseWriter, r *http.Request, status int, contentType string, body, gzipped []byte) {
	h := w.Header()
	h.Set("Content-Type", contentType)
	h.Add("Vary", "Accept-Encoding")
	if len(body) > 1024 && strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
		if gzipped == nil {
			var buf bytes.Buffer
			zw := gzip.NewWriter(&buf)
			zw.Write(body)
			zw.Close()
			gzipped = buf.Bytes()
		}
		h.Set("Content-Encoding", "gzip")
		body = gzipped
	}
	h.Set("Content-Length", fmt.Sprint(len(body)))
	w.WriteHeader(status)
	if r.Method != http.MethodHead {
		w.Write(body)
	}
}

func (s *Server) fail(w http.ResponseWriter, r *http.Request, err error) {
	s.log.Error("request failed", "path", r.URL.Path, "err", err)
	http.Error(w, "Internal server error", http.StatusInternalServerError)
}

func wantsJSON(r *http.Request) bool {
	if f := r.URL.Query().Get("format"); f != "" {
		return f == "json"
	}
	accept := r.Header.Get("Accept")
	return strings.Contains(accept, "application/json") && !strings.Contains(accept, "text/html")
}
