#!/usr/bin/env bash
# Turso DB 생성 → 시크릿 등록 → Cloud Run 배포 → Netlify 프록시 대상 갱신
set -euo pipefail

: "${GCP_PROJECT:?GCP_PROJECT 를 지정하세요 (예: GCP_PROJECT=benelog-til ./deploy.sh)}"
: "${TIL_PASSWORD:?TIL_PASSWORD 를 지정하세요 (.env 를 source 하세요)}"
REGION="${REGION:-asia-northeast3}"
SERVICE="${SERVICE:-til}"
DB_NAME="${DB_NAME:-til}"

echo "[1/4] Turso 데이터베이스 준비"
turso db create "$DB_NAME" 2>/dev/null || echo "  이미 존재: $DB_NAME"
DB_URL=$(turso db show "$DB_NAME" --url)
DB_TOKEN=$(turso db tokens create "$DB_NAME")
echo "  $DB_URL"

echo "[2/4] GCP 프로젝트/API 준비"
gcloud config set project "$GCP_PROJECT" >/dev/null
gcloud services enable run.googleapis.com cloudbuild.googleapis.com \
  secretmanager.googleapis.com artifactregistry.googleapis.com >/dev/null

put_secret() {
  local name=$1 value=$2
  if gcloud secrets describe "$name" >/dev/null 2>&1; then
    printf '%s' "$value" | gcloud secrets versions add "$name" --data-file=- >/dev/null
  else
    printf '%s' "$value" | gcloud secrets create "$name" --data-file=- >/dev/null
  fi
}
put_secret til-password "$TIL_PASSWORD"
put_secret turso-token  "$DB_TOKEN"

PROJECT_NUMBER=$(gcloud projects describe "$GCP_PROJECT" --format='value(projectNumber)')
RUNTIME_SA="${PROJECT_NUMBER}-compute@developer.gserviceaccount.com"
for name in til-password turso-token; do
  gcloud secrets add-iam-policy-binding "$name" \
    --member="serviceAccount:${RUNTIME_SA}" \
    --role=roles/secretmanager.secretAccessor >/dev/null
done

echo "[3/4] Cloud Run 배포"
gcloud run deploy "$SERVICE" \
  --source . \
  --region "$REGION" \
  --allow-unauthenticated \
  --min-instances 0 --max-instances 2 \
  --cpu 1 --memory 256Mi \
  --set-env-vars "TZ=Asia/Seoul,TURSO_DATABASE_URL=${DB_URL}" \
  --set-secrets "TIL_PASSWORD=til-password:latest,TURSO_AUTH_TOKEN=turso-token:latest" \
  --quiet

URL=$(gcloud run services describe "$SERVICE" --region "$REGION" --format='value(status.url)')

echo "[4/4] Netlify 프록시 대상 갱신"
sed -i -E "s#^/\*  https://[^ ]+#/*  ${URL}/:splat#" proxy/_redirects
grep '^/\*' proxy/_redirects

echo
echo "배포 완료: $URL"
echo "proxy/_redirects 를 커밋/푸시하면 til.benelog.net 이 이 주소를 가리킵니다."
