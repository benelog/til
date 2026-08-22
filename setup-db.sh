#!/usr/bin/env bash
# Turso 데이터베이스를 만들고, 배포처에 넣을 환경변수를 출력한다.
# 사전 조건: turso auth login
set -euo pipefail

DB_NAME="${DB_NAME:-til-tokyo}"
GROUP="${GROUP:-tokyo}"
# 앱이 뜨는 리전에 맞춘다. 앱은 서울(asia-northeast3), DB는 가장 가까운 도쿄.
# 한국에서 재보면 오하이오 DB 는 GET / 197~220ms, 도쿄는 38~45ms 였다.
LOCATION="${LOCATION:-aws-ap-northeast-1}"

if ! turso group show "$GROUP" >/dev/null 2>&1; then
  echo "Turso 그룹 생성: $GROUP ($LOCATION)"
  turso group create "$GROUP" --location "$LOCATION"
fi

if ! turso db show "$DB_NAME" >/dev/null 2>&1; then
  echo "Turso 데이터베이스 생성: $DB_NAME (group: $GROUP)"
  turso db create "$DB_NAME" --group "$GROUP"
else
  echo "이미 존재하는 데이터베이스를 사용합니다: $DB_NAME"
fi

DB_URL=$(turso db show "$DB_NAME" --url)
DB_TOKEN=$(turso db tokens create "$DB_NAME")

# 토큰이 터미널/로그에 남지 않도록 파일로만 쓴다 (.gitignore 대상)
umask 077
cat > .env.deploy <<OUT
# 배포처에 등록할 환경변수. 커밋 금지.
TURSO_DATABASE_URL=${DB_URL}
TURSO_AUTH_TOKEN=${DB_TOKEN}
TZ=Asia/Seoul
OUT

echo
echo "데이터베이스 URL: ${DB_URL}"
echo "인증 토큰을 포함한 환경변수를 .env.deploy 에 기록했습니다."
echo "TIL_PASSWORD 는 .env 의 값을 함께 등록하세요."
