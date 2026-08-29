package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"log"
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

type Auth struct {
	passwordHash [sha256.Size]byte
	secret       []byte
	epoch        string
	secure       bool
	limiter      *loginLimiter
}

func NewAuth() (*Auth, error) {
	pw := os.Getenv("TIL_PASSWORD")
	if pw == "" {
		return nil, fmt.Errorf("TIL_PASSWORD 환경변수가 필요합니다")
	}

	secret := []byte(os.Getenv("SESSION_SECRET"))
	if len(secret) == 0 {
		// 쿠키에는 만료시각이 평문으로 들어 있다. 서명 키를 비밀번호에서 파생하면
		// 쿠키 한 장만 새어나가도 후보 비밀번호마다 해시 두 번으로 정답 여부를
		// 확인할 수 있다(오프라인 대입). SESSION_SECRET 을 따로 주면 그 통로가 막힌다.
		sum := sha256.Sum256([]byte("til-session\x00" + pw))
		secret = sum[:]
		log.Print("경고: SESSION_SECRET 이 비어 있어 비밀번호에서 서명 키를 파생합니다. " +
			"쿠키가 유출되면 비밀번호를 오프라인으로 대입할 수 있으니 " +
			"`openssl rand -base64 32` 로 만든 값을 넣어주세요")
	}

	return &Auth{
		passwordHash: sha256.Sum256([]byte(pw)),
		secret:       secret,
		epoch:        firstNonEmpty(os.Getenv("SESSION_EPOCH"), "1"),
		secure:       os.Getenv("INSECURE_COOKIES") == "",
		limiter:      newLoginLimiter(),
	}, nil
}

// Check 는 비밀번호를 상수 시간에 비교한다. 길이가 다르면 곧바로 어긋나는 원문
// 비교와 달리, 해시로 길이를 맞춰두면 타이밍으로 비밀번호 길이도 새지 않는다.
func (a *Auth) Check(password string) bool {
	sum := sha256.Sum256([]byte(password))
	return subtle.ConstantTimeCompare(sum[:], a.passwordHash[:]) == 1
}

// AllowLogin 은 이 요청에 로그인 시도 예산이 남았는지 본다.
// 남지 않았으면 얼마나 기다려야 하는지 함께 돌려준다.
func (a *Auth) AllowLogin(r *http.Request) (time.Duration, bool) {
	return a.limiter.take(clientIP(r))
}

// LoginSucceeded 는 성공한 주소의 시도 예산을 원래대로 돌린다.
func (a *Auth) LoginSucceeded(r *http.Request) {
	a.limiter.reset(clientIP(r))
}

// sign 은 세션 에폭을 함께 서명한다. 쿠키가 유출됐을 때 SESSION_EPOCH 를 올리면
// 이미 나간 쿠키가 한 번에 무효가 된다. 서버에 세션을 따로 저장하지 않으므로
// 이것이 유일한 회수 수단이다.
func (a *Auth) sign(msg string) string {
	mac := hmac.New(sha256.New, a.secret)
	mac.Write([]byte(a.epoch))
	mac.Write([]byte{0})
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

func (a *Auth) Require(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !a.LoggedIn(r) {
			noStore(w)
			if r.Header.Get("HX-Request") == "true" {
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
