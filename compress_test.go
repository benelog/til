package main

import (
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func body(size int) string {
	return strings.Repeat("가나다 abc ", size)
}

func do(t *testing.T, h http.Handler, acceptEncoding string) *http.Response {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if acceptEncoding != "" {
		req.Header.Set("Accept-Encoding", acceptEncoding)
	}
	rec := httptest.NewRecorder()
	compressResponses(h).ServeHTTP(rec, req)
	return rec.Result()
}

func TestCompressesLargeTextResponse(t *testing.T) {
	want := body(200)
	res := do(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, want)
	}), "gzip")

	if got := res.Header.Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("Content-Encoding = %q, gzip 이어야 한다", got)
	}
	if got := res.Header.Get("Vary"); !strings.Contains(got, "Accept-Encoding") {
		t.Errorf("Vary = %q, Accept-Encoding 을 포함해야 한다", got)
	}
	if res.Header.Get("Content-Length") != "" {
		t.Errorf("압축했으면 원본 Content-Length 는 없어야 한다")
	}

	zr, err := gzip.NewReader(res.Body)
	if err != nil {
		t.Fatalf("gzip 해제 실패: %v", err)
	}
	got, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("본문 읽기 실패: %v", err)
	}
	if string(got) != want {
		t.Errorf("압축 해제 결과가 원본과 다르다")
	}
}

func TestSkipsWhenClientDoesNotAcceptGzip(t *testing.T) {
	want := body(200)
	res := do(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, want)
	}), "")

	if got := res.Header.Get("Content-Encoding"); got != "" {
		t.Fatalf("Content-Encoding = %q, 비어 있어야 한다", got)
	}
	got, _ := io.ReadAll(res.Body)
	if string(got) != want {
		t.Errorf("본문이 원본과 다르다")
	}
}

// 앞단 프록시를 다시 끼우더라도 이중 압축이 되지 않아야 한다.
func TestDoesNotRecompressAlreadyEncodedResponse(t *testing.T) {
	want := body(200)
	res := do(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Encoding", "br")
		_, _ = io.WriteString(w, want)
	}), "gzip")

	if got := res.Header.Get("Content-Encoding"); got != "br" {
		t.Fatalf("Content-Encoding = %q, br 그대로여야 한다", got)
	}
	got, _ := io.ReadAll(res.Body)
	if string(got) != want {
		t.Errorf("본문을 다시 압축하면 안 된다")
	}
}

func TestSkipsSmallResponse(t *testing.T) {
	res := do(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, "ok")
	}), "gzip")

	if got := res.Header.Get("Content-Encoding"); got != "" {
		t.Fatalf("작은 응답은 압축하지 않아야 한다. Content-Encoding = %q", got)
	}
	got, _ := io.ReadAll(res.Body)
	if string(got) != "ok" {
		t.Errorf("본문 = %q", got)
	}
}

func TestSkipsAlreadyCompressedContentType(t *testing.T) {
	want := body(200)
	res := do(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = io.WriteString(w, want)
	}), "gzip")

	if got := res.Header.Get("Content-Encoding"); got != "" {
		t.Fatalf("PNG 는 압축하지 않아야 한다. Content-Encoding = %q", got)
	}
}

func TestKeepsStatusCode(t *testing.T) {
	res := do(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, body(200))
	}), "gzip")

	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, 404 여야 한다", res.StatusCode)
	}
	if got := res.Header.Get("Content-Encoding"); got != "gzip" {
		t.Errorf("Content-Encoding = %q, gzip 이어야 한다", got)
	}
}

func TestRejectsGzipWithQZero(t *testing.T) {
	res := do(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, body(200))
	}), "gzip;q=0")

	if got := res.Header.Get("Content-Encoding"); got != "" {
		t.Fatalf("q=0 이면 압축하지 않아야 한다. Content-Encoding = %q", got)
	}
}
