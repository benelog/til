package main

import (
	"math"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// 로그인은 계정 구분 없는 비밀번호 하나로 통과하므로, 온라인 대입을 그냥 두면
// 후보를 무한히 넣어볼 수 있다. IP 마다 토큰 버킷으로 실패 예산을 주고,
// 실패 응답 자체도 조금 늦춰서 초당 시도 횟수를 떨어뜨린다.
const (
	loginBurst     = 5                // 연달아 허용하는 시도 횟수
	loginRefill    = 20 * time.Second // 토큰 하나가 다시 차는 데 걸리는 시간
	loginFailDelay = 500 * time.Millisecond
	loginBucketTTL = 30 * time.Minute // 이 시간 동안 안 쓰인 버킷은 지운다
)

type loginBucket struct {
	tokens float64
	seen   time.Time
}

type loginLimiter struct {
	mu      sync.Mutex
	buckets map[string]*loginBucket
	swept   time.Time
}

func newLoginLimiter() *loginLimiter {
	return &loginLimiter{buckets: make(map[string]*loginBucket), swept: time.Now()}
}

// take 는 토큰 하나를 쓴다. 남은 게 없으면 (다음 토큰까지 남은 시간, false).
func (l *loginLimiter) take(key string) (time.Duration, bool) {
	now := time.Now()

	l.mu.Lock()
	defer l.mu.Unlock()
	l.sweep(now)

	b, ok := l.buckets[key]
	if !ok {
		b = &loginBucket{tokens: loginBurst, seen: now}
		l.buckets[key] = b
	} else {
		b.tokens = math.Min(loginBurst, b.tokens+now.Sub(b.seen).Seconds()/loginRefill.Seconds())
		b.seen = now
	}

	if b.tokens < 1 {
		return time.Duration((1 - b.tokens) * float64(loginRefill)), false
	}
	b.tokens--
	return 0, true
}

// reset 은 로그인에 성공한 주소의 예산을 되돌린다.
func (l *loginLimiter) reset(key string) {
	l.mu.Lock()
	delete(l.buckets, key)
	l.mu.Unlock()
}

// sweep 은 오래 안 쓰인 버킷을 치운다. 청소 전용 고루틴을 두는 대신
// 요청 흐름에 얹어서, 아무도 안 들어오면 아무것도 돌지 않게 한다.
// 호출자가 이미 mu 를 쥐고 있어야 한다.
func (l *loginLimiter) sweep(now time.Time) {
	if now.Sub(l.swept) < loginBucketTTL {
		return
	}
	l.swept = now
	for k, b := range l.buckets {
		if now.Sub(b.seen) > loginBucketTTL {
			delete(l.buckets, k)
		}
	}
}

// clientIP 는 레이트리밋 버킷의 키로 쓸 주소를 고른다.
//
// Cloud Run 앞단의 Google Frontend 는 요청에 이미 들어 있던 X-Forwarded-For 뒤에
// 실제 커넥션 IP 를 덧붙인다. 그래서 클라이언트가 헤더를 위조해 넣더라도 마지막
// 항목만은 프런트엔드가 본 진짜 주소다. 앞쪽 항목은 믿지 않는다.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		last := xff
		if i := strings.LastIndexByte(xff, ','); i >= 0 {
			last = xff[i+1:]
		}
		if last = strings.TrimSpace(last); last != "" {
			return last
		}
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}
