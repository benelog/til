package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	_ "github.com/tursodatabase/libsql-client-go/libsql"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/sqlitedialect"
	_ "modernc.org/sqlite"
)

// Entry 는 TIL 한 건이다.
type Entry struct {
	ID        int64
	Title     string
	Body      string // 원본 마크다운
	CreatedAt time.Time
	UpdatedAt time.Time
}

type Store struct {
	db *bun.DB
}

const schema = `
CREATE TABLE IF NOT EXISTS entries (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	title      TEXT NOT NULL DEFAULT '',
	body       TEXT NOT NULL,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_entries_created_at ON entries (created_at DESC);
`

// resolveDSN 은 환경변수에서 접속 정보를 만든다.
//
//	TURSO_DATABASE_URL + TURSO_AUTH_TOKEN → Turso (원격, HTTP)
//	DATABASE_URL                          → 위와 동일하게 취급 (토큰이 이미 붙은 형태 허용)
//	둘 다 없으면                            → 로컬 SQLite 파일 (til.db)
//
// 반환값은 (드라이버명, DSN) 이다.
func resolveDSN() (string, string, error) {
	raw := firstNonEmpty(os.Getenv("TURSO_DATABASE_URL"), os.Getenv("DATABASE_URL"))
	if raw == "" {
		path := firstNonEmpty(os.Getenv("SQLITE_PATH"), "til.db")
		return "sqlite", "file:" + path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)", nil
	}

	u, err := url.Parse(raw)
	if err != nil {
		return "", "", fmt.Errorf("데이터베이스 URL 파싱 실패: %w", err)
	}

	switch u.Scheme {
	case "file":
		return "sqlite", raw, nil
	case "libsql", "wss", "ws":
		// libsql:// 은 웹소켓으로 붙는다. Cloud Run 처럼 인스턴스가 자주 뜨고 지는
		// 환경에서는 요청 단위 HTTP 가 유리하므로 https 로 바꿔서 쓴다.
		u.Scheme = "https"
	case "http", "https":
	default:
		return "", "", fmt.Errorf("지원하지 않는 스킴입니다: %s", u.Scheme)
	}

	if token := os.Getenv("TURSO_AUTH_TOKEN"); token != "" {
		q := u.Query()
		q.Set("authToken", token)
		u.RawQuery = q.Encode()
	}
	return "libsql", u.String(), nil
}

func OpenStore(ctx context.Context) (*Store, error) {
	driver, dsn, err := resolveDSN()
	if err != nil {
		return nil, err
	}

	db, err := sql.Open(driver, dsn)
	if err != nil {
		return nil, fmt.Errorf("DB 열기 실패(%s): %w", driver, err)
	}

	// 원격 libSQL 은 요청마다 HTTP 를 쓰므로 커넥션을 크게 잡을 이유가 없고,
	// 로컬 SQLite 는 쓰기 락 때문에 오히려 좁게 잡는 편이 안전하다.
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(2)
	db.SetConnMaxIdleTime(time.Minute)

	if err := db.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("DB 접속 실패(%s): %w", driver, err)
	}
	if _, err := db.ExecContext(ctx, schema); err != nil {
		return nil, fmt.Errorf("스키마 생성 실패: %w", err)
	}
	// libSQL 도 SQLite 방언이므로 두 드라이버 모두 sqlitedialect 로 쿼리를 만든다.
	return &Store{db: bun.NewDB(db, sqlitedialect.New())}, nil
}

func (s *Store) Close() error { return s.db.Close() }

// entryRow 는 entries 테이블의 한 행이다. 시각은 기존 데이터와 같은 RFC3339Nano 문자열로
// 저장한다. bun 기본 time 포맷("2006-01-02 15:04:05...")과 섞이면 created_at 문자열
// 정렬이 깨지므로 time.Time 대신 문자열로 둔다.
type entryRow struct {
	bun.BaseModel `bun:"table:entries"`

	ID        int64  `bun:"id,pk,autoincrement"`
	Title     string `bun:"title"`
	Body      string `bun:"body"`
	CreatedAt string `bun:"created_at"`
	UpdatedAt string `bun:"updated_at"`
}

func (r entryRow) entry() Entry {
	return Entry{
		ID:        r.ID,
		Title:     r.Title,
		Body:      r.Body,
		CreatedAt: parseTime(r.CreatedAt),
		UpdatedAt: parseTime(r.UpdatedAt),
	}
}

func (s *Store) List(ctx context.Context) ([]Entry, error) {
	var rows []entryRow
	if err := s.db.NewSelect().Model(&rows).
		OrderExpr("created_at DESC, id DESC").
		Scan(ctx); err != nil {
		return nil, err
	}

	out := make([]Entry, len(rows))
	for i, r := range rows {
		out[i] = r.entry()
	}
	return out, nil
}

var ErrNotFound = errors.New("항목을 찾을 수 없습니다")

func (s *Store) Get(ctx context.Context, id int64) (Entry, error) {
	var r entryRow
	err := s.db.NewSelect().Model(&r).Where("id = ?", id).Scan(ctx)
	return found(r, err)
}

func (s *Store) Create(ctx context.Context, title, body string) (Entry, error) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	r := entryRow{Title: title, Body: body, CreatedAt: now, UpdatedAt: now}
	// RETURNING 으로 저장된 행을 그대로 받아 다시 조회하는 왕복을 줄인다.
	err := s.db.NewInsert().Model(&r).Returning("*").Scan(ctx)
	return found(r, err)
}

func (s *Store) Update(ctx context.Context, id int64, title, body string) (Entry, error) {
	r := entryRow{
		ID:        id,
		Title:     title,
		Body:      body,
		UpdatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	err := s.db.NewUpdate().Model(&r).
		Column("title", "body", "updated_at").
		WherePK().
		Returning("*").
		Scan(ctx)
	return found(r, err)
}

func (s *Store) Delete(ctx context.Context, id int64) error {
	res, err := s.db.NewDelete().Model((*entryRow)(nil)).Where("id = ?", id).Exec(ctx)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return ErrNotFound
	}
	return nil
}

// found 는 한 행을 돌려받는 쿼리 결과를 Entry 로 바꾸고, 행이 없으면 ErrNotFound 로 바꾼다.
func found(r entryRow, err error) (Entry, error) {
	if errors.Is(err, sql.ErrNoRows) {
		return Entry{}, ErrNotFound
	}
	if err != nil {
		return Entry{}, err
	}
	return r.entry(), nil
}

func parseTime(s string) time.Time {
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
