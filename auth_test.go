package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func newTestAuth(t *testing.T, env map[string]string) *Auth {
	t.Helper()
	t.Setenv("TIL_PASSWORD", "correct-horse")
	t.Setenv("SESSION_SECRET", "test-secret")
	t.Setenv("SESSION_EPOCH", "")
	t.Setenv("INSECURE_COOKIES", "1")
	for k, v := range env {
		t.Setenv(k, v)
	}
	a, err := NewAuth()
	if err != nil {
		t.Fatalf("NewAuth: %v", err)
	}
	return a
}

// 발급한 쿠키를 그대로 돌려주면 로그인 상태로 인정돼야 한다.
func TestIssuedCookieIsAccepted(t *testing.T) {
	a := newTestAuth(t, nil)

	rec := httptest.NewRecorder()
	a.Issue(rec)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	for _, c := range rec.Result().Cookies() {
		req.AddCookie(c)
	}
	if !a.LoggedIn(req) {
		t.Fatal("방금 발급한 쿠키가 거부됐다")
	}
}

// SESSION_EPOCH 를 올리면 이미 나간 쿠키가 전부 무효가 돼야 한다.
// 쿠키가 유출됐을 때의 유일한 회수 수단이므로 반드시 지켜져야 한다.
func TestEpochBumpInvalidatesIssuedCookies(t *testing.T) {
	old := newTestAuth(t, map[string]string{"SESSION_EPOCH": "1"})

	rec := httptest.NewRecorder()
	old.Issue(rec)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	for _, c := range rec.Result().Cookies() {
		req.AddCookie(c)
	}

	bumped := newTestAuth(t, map[string]string{"SESSION_EPOCH": "2"})
	if bumped.LoggedIn(req) {
		t.Fatal("에폭을 올렸는데도 예전 쿠키가 통과했다")
	}
	if !old.LoggedIn(req) {
		t.Fatal("같은 에폭에서는 통과해야 한다")
	}
}

// 만료시각만 미래로 늘려도 서명이 어긋나 거부돼야 한다.
func TestTamperedExpiryIsRejected(t *testing.T) {
	a := newTestAuth(t, nil)

	rec := httptest.NewRecorder()
	a.Issue(rec)
	c := rec.Result().Cookies()[0]

	_, sig, _ := strings.Cut(c.Value, ".")
	forged := "99999999999." + sig

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: forged})
	if a.LoggedIn(req) {
		t.Fatal("만료시각을 바꾼 쿠키가 통과했다")
	}
}

func TestCheckPassword(t *testing.T) {
	a := newTestAuth(t, nil)

	if !a.Check("correct-horse") {
		t.Error("맞는 비밀번호를 거부했다")
	}
	// 길이가 같은 오답과 다른 오답 모두 거부돼야 한다.
	for _, pw := range []string{"correct-horsf", "", "correct-horse-longer", "correct"} {
		if a.Check(pw) {
			t.Errorf("틀린 비밀번호 %q 를 통과시켰다", pw)
		}
	}
}

// 예산을 다 쓰면 막히고, 시간이 지나면 다시 열려야 한다.
func TestLoginLimiterBurstAndRefill(t *testing.T) {
	l := newLoginLimiter()

	for i := 0; i < loginBurst; i++ {
		if _, ok := l.take("1.2.3.4"); !ok {
			t.Fatalf("%d 번째 시도가 막혔다. %d 번까지는 열려야 한다", i+1, loginBurst)
		}
	}
	wait, ok := l.take("1.2.3.4")
	if ok {
		t.Fatal("예산을 다 썼는데도 통과했다")
	}
	if wait <= 0 || wait > loginRefill {
		t.Errorf("대기시간 = %v, (0, %v] 범위여야 한다", wait, loginRefill)
	}

	// 다른 IP 는 영향을 받지 않는다.
	if _, ok := l.take("5.6.7.8"); !ok {
		t.Error("다른 IP 까지 함께 막혔다")
	}

	// 토큰 하나가 찰 만큼 시간이 지난 것처럼 꾸민다.
	l.buckets["1.2.3.4"].seen = time.Now().Add(-loginRefill)
	if _, ok := l.take("1.2.3.4"); !ok {
		t.Error("토큰이 회복될 시간이 지났는데도 막혔다")
	}
}

// 로그인에 성공하면 예산이 원래대로 돌아와야 한다.
func TestLoginLimiterResetOnSuccess(t *testing.T) {
	l := newLoginLimiter()
	for i := 0; i < loginBurst; i++ {
		l.take("1.2.3.4")
	}
	l.reset("1.2.3.4")
	if _, ok := l.take("1.2.3.4"); !ok {
		t.Fatal("성공 후에도 예산이 비어 있다")
	}
}

// 클라이언트가 X-Forwarded-For 를 위조해도 프런트엔드가 덧붙인 마지막 IP 를
// 써야 한다. 그렇지 않으면 헤더만 바꿔가며 레이트리밋을 무한히 우회할 수 있다.
func TestClientIPIgnoresSpoofedForwardedFor(t *testing.T) {
	cases := []struct {
		xff, remote, want string
	}{
		{"", "203.0.113.9:54321", "203.0.113.9"},
		{"203.0.113.9", "10.0.0.1:1", "203.0.113.9"},
		{"1.1.1.1, 203.0.113.9", "10.0.0.1:1", "203.0.113.9"},
		{"위조된값, 또다른위조, 203.0.113.9", "10.0.0.1:1", "203.0.113.9"},
	}
	for _, c := range cases {
		req := httptest.NewRequest(http.MethodPost, "/login", nil)
		req.RemoteAddr = c.remote
		if c.xff != "" {
			req.Header.Set("X-Forwarded-For", c.xff)
		}
		if got := clientIP(req); got != c.want {
			t.Errorf("clientIP(XFF=%q) = %q, %q 여야 한다", c.xff, got, c.want)
		}
	}
}
