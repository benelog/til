package main

import (
	"compress/gzip"
	"net/http"
	"strings"
	"sync"
)

// Cloud Run 앞단의 Google Frontend 는 컨테이너 응답을 대신 압축해주지 않는다.
// (Netlify 프록시를 쓰던 때는 Netlify 가 Brotli 를 자동으로 걸어줬다.)
// 한국으로 나가는 전송에는 무료 구간이 없으므로 앱이 직접 압축한다.
//
// 이미 Content-Encoding 이 붙은 응답에는 손대지 않고, 앞단 프록시도 같은 규칙을
// 따르므로 나중에 프록시를 다시 끼워도 이중 압축이 되지 않는다.

// 이보다 작은 응답은 압축해도 이득이 없거나 오히려 커진다.
const minCompressSize = 512

var gzipPool = sync.Pool{
	New: func() any {
		w, _ := gzip.NewWriterLevel(nil, gzip.DefaultCompression)
		return w
	},
}

// compressible 은 압축해서 이득이 있는 Content-Type 인지 판단한다.
// 이미 압축된 형식(PNG, WebP, woff2 등)은 제외한다.
func compressible(contentType string) bool {
	ct := contentType
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = ct[:i]
	}
	ct = strings.ToLower(strings.TrimSpace(ct))
	if strings.HasPrefix(ct, "text/") {
		return true
	}
	switch ct {
	case "application/javascript", "application/json", "application/xml",
		"application/manifest+json", "application/xhtml+xml", "image/svg+xml":
		return true
	}
	return false
}

func acceptsGzip(r *http.Request) bool {
	for _, v := range strings.Split(r.Header.Get("Accept-Encoding"), ",") {
		// "gzip;q=0" 로 명시적으로 거부하는 경우는 제외한다.
		name, params, _ := strings.Cut(strings.TrimSpace(v), ";")
		if strings.EqualFold(strings.TrimSpace(name), "gzip") {
			return !strings.Contains(strings.ReplaceAll(params, " ", ""), "q=0")
		}
	}
	return false
}

// gzipResponseWriter 는 본문 앞부분을 잠시 모아두고, 압축할지 판단한 뒤
// 헤더와 본문을 내보낸다. Content-Type 이 핸들러에서 늦게 정해지거나
// 스니핑에 맡겨지는 경우가 있어서 첫 Write 시점까지 판단을 미룬다.
type gzipResponseWriter struct {
	http.ResponseWriter

	status  int
	buf     []byte
	decided bool
	gz      *gzip.Writer // nil 이면 압축하지 않기로 결정한 것
}

func (w *gzipResponseWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}

func (w *gzipResponseWriter) Write(p []byte) (int, error) {
	if w.decided {
		return w.write(p)
	}
	w.buf = append(w.buf, p...)
	if len(w.buf) >= minCompressSize {
		if err := w.decide(); err != nil {
			return 0, err
		}
	}
	return len(p), nil
}

// Flush 는 핸들러가 중간에 밀어낼 때 판단을 강제한다.
func (w *gzipResponseWriter) Flush() {
	if !w.decided {
		// 아직 임계값에 못 미쳤어도 여기서 확정한다.
		_ = w.decide()
	}
	if w.gz != nil {
		_ = w.gz.Flush()
	}
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// decide 는 압축 여부를 확정하고 헤더와 모아둔 본문을 내보낸다.
func (w *gzipResponseWriter) decide() error {
	w.decided = true
	h := w.ResponseWriter.Header()

	// 이미 압축된 응답이나 본문이 없는 응답은 그대로 통과시킨다.
	alreadyEncoded := h.Get("Content-Encoding") != ""
	noBody := w.status == http.StatusNoContent || w.status == http.StatusNotModified

	ct := h.Get("Content-Type")
	if ct == "" && len(w.buf) > 0 {
		ct = http.DetectContentType(w.buf)
		h.Set("Content-Type", ct)
	}

	if alreadyEncoded || noBody || len(w.buf) < minCompressSize || !compressible(ct) {
		w.writeHeaderNow()
		if len(w.buf) > 0 {
			_, err := w.ResponseWriter.Write(w.buf)
			w.buf = nil
			return err
		}
		w.buf = nil
		return nil
	}

	h.Set("Content-Encoding", "gzip")
	// 길이가 달라지므로 원본 Content-Length 는 버린다.
	h.Del("Content-Length")

	gz := gzipPool.Get().(*gzip.Writer)
	gz.Reset(w.ResponseWriter)
	w.gz = gz

	w.writeHeaderNow()
	_, err := gz.Write(w.buf)
	w.buf = nil
	return err
}

func (w *gzipResponseWriter) writeHeaderNow() {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	w.ResponseWriter.WriteHeader(w.status)
}

func (w *gzipResponseWriter) write(p []byte) (int, error) {
	if w.gz != nil {
		return w.gz.Write(p)
	}
	return w.ResponseWriter.Write(p)
}

// finish 는 요청 처리가 끝난 뒤 남은 본문을 흘려보내고 자원을 돌려준다.
func (w *gzipResponseWriter) finish() {
	if !w.decided {
		_ = w.decide()
	}
	if w.gz != nil {
		_ = w.gz.Close()
		gzipPool.Put(w.gz)
		w.gz = nil
	}
}

// compressResponses 는 gzip 을 받아들이는 클라이언트에게 텍스트 응답을 압축해 보낸다.
func compressResponses(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 같은 URL 이 압축본과 원본 두 가지로 나가므로 캐시가 구분하게 한다.
		w.Header().Add("Vary", "Accept-Encoding")

		if !acceptsGzip(r) {
			next.ServeHTTP(w, r)
			return
		}

		gw := &gzipResponseWriter{ResponseWriter: w}
		defer gw.finish()
		next.ServeHTTP(gw, r)
	})
}
