package main

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	t.Setenv("TURSO_DATABASE_URL", "")
	t.Setenv("DATABASE_URL", "")
	t.Setenv("SQLITE_PATH", filepath.Join(t.TempDir(), "til.db"))
	s, err := OpenStore(context.Background())
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// 만들고, 고치고, 지우는 한 바퀴가 그대로 반영돼야 한다.
func TestStoreCRUD(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	created, err := s.Create(ctx, "제목", "본문")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.ID == 0 || created.Title != "제목" || created.Body != "본문" {
		t.Fatalf("Create 결과가 이상합니다: %+v", created)
	}
	if created.CreatedAt.IsZero() || !created.CreatedAt.Equal(created.UpdatedAt) {
		t.Fatalf("생성 시각이 이상합니다: %+v", created)
	}

	got, err := s.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != created {
		t.Fatalf("Get = %+v, want %+v", got, created)
	}

	time.Sleep(2 * time.Millisecond)
	updated, err := s.Update(ctx, created.ID, "새 제목", "새 본문")
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.Title != "새 제목" || updated.Body != "새 본문" {
		t.Fatalf("Update 결과가 이상합니다: %+v", updated)
	}
	if !updated.CreatedAt.Equal(created.CreatedAt) {
		t.Fatalf("수정 후 created_at 이 바뀌었습니다: %v → %v", created.CreatedAt, updated.CreatedAt)
	}
	if !updated.UpdatedAt.After(created.UpdatedAt) {
		t.Fatalf("updated_at 이 갱신되지 않았습니다: %v", updated.UpdatedAt)
	}

	if err := s.Delete(ctx, created.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := s.Get(ctx, created.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("삭제 후 Get err = %v, want ErrNotFound", err)
	}
}

// 없는 항목은 어떤 연산이든 ErrNotFound 로 알려야 한다.
func TestStoreNotFound(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	if _, err := s.Get(ctx, 42); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get err = %v, want ErrNotFound", err)
	}
	if _, err := s.Update(ctx, 42, "t", "b"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Update err = %v, want ErrNotFound", err)
	}
	if err := s.Delete(ctx, 42); !errors.Is(err, ErrNotFound) {
		t.Errorf("Delete err = %v, want ErrNotFound", err)
	}
}

// 목록은 최신 글이 먼저 나와야 하고, 시각은 기존 데이터와 같은 RFC3339Nano 로 저장돼야 한다.
func TestStoreListOrderAndTimeFormat(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	if list, err := s.List(ctx); err != nil || len(list) != 0 {
		t.Fatalf("빈 List = %v, %v", list, err)
	}

	first, _ := s.Create(ctx, "첫째", "a")
	time.Sleep(2 * time.Millisecond)
	second, _ := s.Create(ctx, "둘째", "b")

	list, err := s.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 2 || list[0].ID != second.ID || list[1].ID != first.ID {
		t.Fatalf("List 순서가 이상합니다: %+v", list)
	}

	var raw string
	if err := s.db.NewRaw("SELECT created_at FROM entries WHERE id = ?", first.ID).Scan(ctx, &raw); err != nil {
		t.Fatalf("created_at 조회: %v", err)
	}
	if _, err := time.Parse(time.RFC3339Nano, raw); err != nil {
		t.Fatalf("created_at 이 RFC3339Nano 가 아닙니다: %q", raw)
	}
}
