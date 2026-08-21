package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	sessionCookie = "til_session"
	sessionTTL    = 30 * 24 * time.Hour
)

// Auth 는 단일 비밀번호 기반 세션을 다룬다.
// 읽기는 누구나, 쓰기/수정/삭제는 로그인한 사람만 가능하다.
type Auth struct {
	password []byte
	secret   []byte
	secure   bool
}

func NewAuth() (*Auth, error) {
	pw := os.Getenv("TIL_PASSWORD")
	if pw == "" {
		return nil, fmt.Errorf("TIL_PASSWORD 환경변수가 필요합니다")
	}

	// 세션 서명 키. 따로 주지 않으면 비밀번호에서 파생한다.
	// (파생하면 비밀번호를 바꿀 때 기존 세션이 자동으로 무효가 된다)
	secret := []byte(os.Getenv("SESSION_SECRET"))
	if len(secret) == 0 {
		sum := sha256.Sum256([]byte("til-session\x00" + pw))
		secret = sum[:]
	}

	return &Auth{
		password: []byte(pw),
		secret:   secret,
		secure:   os.Getenv("INSECURE_COOKIES") == "",
	}, nil
}

func (a *Auth) Check(password string) bool {
	return subtle.ConstantTimeCompare([]byte(password), a.password) == 1
}

func (a *Auth) sign(msg string) string {
	mac := hmac.New(sha256.New, a.secret)
	mac.Write([]byte(msg))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (a *Auth) Issue(w http.ResponseWriter) {
	exp := strconv.FormatInt(time.Now().Add(sessionTTL).Unix(), 10)
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    exp + "." + a.sign(exp),
		Path:     "/",
		Expires:  time.Now().Add(sessionTTL),
		HttpOnly: true,
		Secure:   a.secure,
		SameSite: http.SameSiteLaxMode,
	})
}

func (a *Auth) Revoke(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   a.secure,
		SameSite: http.SameSiteLaxMode,
	})
}

func (a *Auth) LoggedIn(r *http.Request) bool {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return false
	}
	exp, sig, ok := strings.Cut(c.Value, ".")
	if !ok {
		return false
	}
	if !hmac.Equal([]byte(sig), []byte(a.sign(exp))) {
		return false
	}
	ts, err := strconv.ParseInt(exp, 10, 64)
	if err != nil {
		return false
	}
	return time.Now().Unix() < ts
}

// Require 는 쓰기 계열 핸들러를 감싼다.
func (a *Auth) Require(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !a.LoggedIn(r) {
			if r.Header.Get("HX-Request") == "true" {
				// htmx 요청이면 전체 페이지를 갈아끼우도록 지시한다.
				w.Header().Set("HX-Redirect", "/login")
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		next(w, r)
	}
}
