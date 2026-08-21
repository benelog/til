#!/usr/bin/env bash
# Turso 데이터베이스를 만들고, 배포처에 넣을 환경변수를 출력한다.
# 사전 조건: turso auth login
set -euo pipefail

DB_NAME="${DB_NAME:-til}"

if ! turso db show "$DB_NAME" >/dev/null 2>&1; then
  echo "Turso 데이터베이스 생성: $DB_NAME"
  turso db create "$DB_NAME"
else
  echo "이미 존재하는 데이터베이스를 사용합니다: $DB_NAME"
fi

DB_URL=$(turso db show "$DB_NAME" --url)
DB_TOKEN=$(turso db tokens create "$DB_NAME")

cat <<OUT

배포처(Northflank 등)에 등록할 환경변수
────────────────────────────────────────────────
TURSO_DATABASE_URL = ${DB_URL}
TURSO_AUTH_TOKEN   = ${DB_TOKEN}        ← 시크릿으로 등록
TIL_PASSWORD       = <쓰기 비밀번호>     ← 시크릿으로 등록
TZ                 = Asia/Seoul
────────────────────────────────────────────────
OUT
