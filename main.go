package main

import (
	"context"
	"embed"
	"errors"
	"html/template"
	"log"
	"mime"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static
var staticFS embed.FS

var (
	tmplIndex = template.Must(template.ParseFS(templateFS,
		"templates/layout.html", "templates/index.html", "templates/partials.html"))
	tmplLogin = template.Must(template.ParseFS(templateFS,
		"templates/layout.html", "templates/login.html"))
	tmplPartials = template.Must(template.ParseFS(templateFS, "templates/partials.html"))
)

type server struct {
	store *Store
	auth  *Auth
}

// entryView 는 템플릿에 넘기는 표현형이다. 마크다운은 미리 렌더해둔다.
type entryView struct {
	ID        int64
	Title     string
	Body      string
	HTML      template.HTML
	CreatedAt string
	UpdatedAt string
	Edited    bool
	CanEdit   bool
}

func newEntryView(e Entry, canEdit bool) entryView {
	loc := displayLocation()
	return entryView{
		ID:        e.ID,
		Title:     e.Title,
		Body:      e.Body,
		HTML:      renderMarkdown(e.Body),
		CreatedAt: e.CreatedAt.In(loc).Format("2006-01-02 15:04"),
		UpdatedAt: e.UpdatedAt.In(loc).Format("2006-01-02 15:04"),
		Edited:    e.UpdatedAt.Sub(e.CreatedAt) > time.Second,
		CanEdit:   canEdit,
	}
}

func displayLocation() *time.Location {
	if loc, err := time.LoadLocation(firstNonEmpty(os.Getenv("TZ"), "Asia/Seoul")); err == nil {
		return loc
	}
	return time.UTC
}

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Go 내장 MIME 표에 .webmanifest 가 없고, distroless 이미지에는 /etc/mime.types 도
	// 없어서 매니페스트가 text/plain 으로 나간다. 직접 등록해둔다.
	if err := mime.AddExtensionType(".webmanifest", "application/manifest+json"); err != nil {
		return err
	}

	auth, err := NewAuth()
	if err != nil {
		return err
	}
	store, err := OpenStore(ctx)
	if err != nil {
		return err
	}
	defer store.Close()

	s := &server{store: store, auth: auth}

	mux := http.NewServeMux()
	mux.Handle("GET /static/", http.FileServer(http.FS(staticFS)))
	mux.HandleFunc("GET /sw.js", serveServiceWorker)
	// Cloud Run 앞단의 Google Frontend 가 정확히 /healthz 경로를 가로채 자체 404 를
	// 돌려주므로(다른 경로는 그대로 통과) 헬스 경로를 /health 로도 열어둔다.
	health := func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	}
	mux.HandleFunc("GET /healthz", health)
	mux.HandleFunc("GET /health", health)

	mux.HandleFunc("GET /{$}", s.handleIndex)
	mux.HandleFunc("GET /login", s.handleLoginForm)
	mux.HandleFunc("POST /login", s.handleLogin)
	mux.HandleFunc("POST /logout", s.handleLogout)

	mux.HandleFunc("GET /entries/{id}", s.handleEntry)
	mux.HandleFunc("GET /entries/{id}/edit", auth.Require(s.handleEditForm))
	mux.HandleFunc("POST /entries", auth.Require(s.handleCreate))
	mux.HandleFunc("PUT /entries/{id}", auth.Require(s.handleUpdate))
	mux.HandleFunc("DELETE /entries/{id}", auth.Require(s.handleDelete))
	mux.HandleFunc("POST /preview", auth.Require(s.handlePreview))

	srv := &http.Server{
		Addr:              ":" + firstNonEmpty(os.Getenv("PORT"), "8080"),
		Handler:           logRequests(compressResponses(mux)),
		ReadHeaderTimeout: 10 * time.Second,
		WriteTimeout:      30 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	log.Printf("TIL 서버 시작: http://localhost%s", srv.Addr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// serveServiceWorker 는 static/sw.js 를 루트 경로에서 내려준다.
// 서비스 워커의 제어 범위(scope)는 스크립트가 놓인 경로 아래로 제한되므로,
// 사이트 전체를 다루려면 /static/sw.js 가 아니라 /sw.js 여야 한다.
func serveServiceWorker(w http.ResponseWriter, r *http.Request) {
	b, err := staticFS.ReadFile("static/sw.js")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	// 워커 자체가 캐시에 굳으면 새 버전을 못 받으므로 매번 재검증하게 한다.
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(b)
}

func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("%s %s %s", r.Method, r.URL.Path, time.Since(start).Round(time.Millisecond))
	})
}

// --- 페이지 ---

func (s *server) handleIndex(w http.ResponseWriter, r *http.Request) {
	entries, err := s.store.List(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	loggedIn := s.auth.LoggedIn(r)
	views := make([]entryView, 0, len(entries))
	for _, e := range entries {
		views = append(views, newEntryView(e, loggedIn))
	}
	s.render(w, tmplIndex, "layout", map[string]any{
		"LoggedIn": loggedIn,
		"Entries":  views,
	})
}

func (s *server) handleLoginForm(w http.ResponseWriter, r *http.Request) {
	if s.auth.LoggedIn(r) {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	s.render(w, tmplLogin, "layout", map[string]any{"LoggedIn": false})
}

func (s *server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "잘못된 요청입니다", http.StatusBadRequest)
		return
	}
	if !s.auth.Check(r.PostFormValue("password")) {
		w.WriteHeader(http.StatusUnauthorized)
		s.render(w, tmplLogin, "layout", map[string]any{
			"LoggedIn": false,
			"Error":    "비밀번호가 맞지 않습니다.",
		})
		return
	}
	s.auth.Issue(w)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *server) handleLogout(w http.ResponseWriter, r *http.Request) {
	s.auth.Revoke(w)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// --- htmx 조각 ---

func (s *server) handleEditForm(w http.ResponseWriter, r *http.Request) {
	e, ok := s.lookup(w, r)
	if !ok {
		return
	}
	s.render(w, tmplPartials, "entry-form", newEntryView(e, true))
}

func (s *server) handleEntry(w http.ResponseWriter, r *http.Request) {
	e, ok := s.lookup(w, r)
	if !ok {
		return
	}
	s.render(w, tmplPartials, "entry", newEntryView(e, s.auth.LoggedIn(r)))
}

func (s *server) handleCreate(w http.ResponseWriter, r *http.Request) {
	title, body, ok := s.readForm(w, r)
	if !ok {
		return
	}
	e, err := s.store.Create(r.Context(), title, body)
	if err != nil {
		s.fail(w, err)
		return
	}
	// 새 항목 + 작성 폼을 비우는 out-of-band 스왑을 함께 돌려준다.
	s.render(w, tmplPartials, "created", newEntryView(e, true))
}

func (s *server) handleUpdate(w http.ResponseWriter, r *http.Request) {
	id, ok := entryID(w, r)
	if !ok {
		return
	}
	title, body, ok := s.readForm(w, r)
	if !ok {
		return
	}
	e, err := s.store.Update(r.Context(), id, title, body)
	if errors.Is(err, ErrNotFound) {
		http.Error(w, "항목을 찾을 수 없습니다", http.StatusNotFound)
		return
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	s.render(w, tmplPartials, "entry", newEntryView(e, true))
}

func (s *server) handleDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := entryID(w, r)
	if !ok {
		return
	}
	err := s.store.Delete(r.Context(), id)
	if errors.Is(err, ErrNotFound) {
		http.Error(w, "항목을 찾을 수 없습니다", http.StatusNotFound)
		return
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusOK) // 빈 본문 → htmx 가 해당 항목을 제거한다
}

func (s *server) handlePreview(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "잘못된 요청입니다", http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if strings.TrimSpace(r.PostFormValue("body")) == "" {
		return
	}
	_, _ = w.Write([]byte(renderMarkdown(r.PostFormValue("body"))))
}

// --- 도우미 ---

func (s *server) readForm(w http.ResponseWriter, r *http.Request) (title, body string, ok bool) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "잘못된 요청입니다", http.StatusBadRequest)
		return "", "", false
	}
	title = strings.TrimSpace(r.PostFormValue("title"))
	body = strings.TrimRight(r.PostFormValue("body"), " \t\r\n")
	if title == "" && strings.TrimSpace(body) == "" {
		http.Error(w, "제목이나 내용 중 하나는 있어야 합니다", http.StatusBadRequest)
		return "", "", false
	}
	return title, body, true
}

func entryID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "잘못된 id 입니다", http.StatusBadRequest)
		return 0, false
	}
	return id, true
}

func (s *server) lookup(w http.ResponseWriter, r *http.Request) (Entry, bool) {
	id, ok := entryID(w, r)
	if !ok {
		return Entry{}, false
	}
	e, err := s.store.Get(r.Context(), id)
	if errors.Is(err, ErrNotFound) {
		http.Error(w, "항목을 찾을 수 없습니다", http.StatusNotFound)
		return Entry{}, false
	}
	if err != nil {
		s.fail(w, err)
		return Entry{}, false
	}
	return e, true
}

func (s *server) render(w http.ResponseWriter, t *template.Template, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := t.ExecuteTemplate(w, name, data); err != nil {
		log.Printf("템플릿 렌더 실패(%s): %v", name, err)
	}
}

func (s *server) fail(w http.ResponseWriter, err error) {
	log.Printf("오류: %v", err)
	http.Error(w, "서버 오류가 발생했습니다", http.StatusInternalServerError)
}
