#!/usr/bin/env bash
# GitHub Actions 가 서비스 계정 키 없이 Cloud Run 에 배포할 수 있도록
# Workload Identity Federation 을 한 번만 설정한다.
#
#   ./scripts/setup-gh-deploy.sh
#
# 이미 만들어진 리소스는 건너뛰므로 다시 실행해도 안전하다.
set -euo pipefail

# gcloud 가 PATH 에 없으면 기본 설치 경로를 잡아준다.
command -v gcloud >/dev/null 2>&1 || PATH="$HOME/google-cloud-sdk/bin:$PATH"

PROJECT=benelog
REPO=benelog/til
POOL=github
PROVIDER=github
SA=github-deployer

PROJECT_NUMBER=$(gcloud projects describe "$PROJECT" --format='value(projectNumber)')
SA_EMAIL="${SA}@${PROJECT}.iam.gserviceaccount.com"
RUNTIME_SA="${PROJECT_NUMBER}-compute@developer.gserviceaccount.com"

echo "프로젝트 $PROJECT (번호 $PROJECT_NUMBER)"

echo "== API 사용 설정 =="
gcloud services enable sts.googleapis.com iamcredentials.googleapis.com \
  run.googleapis.com cloudbuild.googleapis.com artifactregistry.googleapis.com \
  --project "$PROJECT"

echo "== Workload Identity 풀 =="
gcloud iam workload-identity-pools describe "$POOL" \
  --location=global --project "$PROJECT" >/dev/null 2>&1 || \
gcloud iam workload-identity-pools create "$POOL" \
  --location=global --project "$PROJECT" --display-name="GitHub Actions"

echo "== OIDC 공급자 =="
# attribute-condition 이 핵심이다. 저장소가 공개라 이 조건이 없으면 아무 저장소나
# 이 풀로 토큰을 받아 배포할 수 있다.
gcloud iam workload-identity-pools providers describe "$PROVIDER" \
  --location=global --workload-identity-pool="$POOL" --project "$PROJECT" >/dev/null 2>&1 || \
gcloud iam workload-identity-pools providers create-oidc "$PROVIDER" \
  --location=global --workload-identity-pool="$POOL" --project "$PROJECT" \
  --display-name="GitHub OIDC" \
  --issuer-uri="https://token.actions.githubusercontent.com" \
  --attribute-mapping="google.subject=assertion.sub,attribute.repository=assertion.repository" \
  --attribute-condition="assertion.repository == '${REPO}'"

echo "== 배포용 서비스 계정 =="
gcloud iam service-accounts describe "$SA_EMAIL" --project "$PROJECT" >/dev/null 2>&1 || \
gcloud iam service-accounts create "$SA" --project "$PROJECT" \
  --display-name="GitHub Actions deployer"

echo "== 역할 부여 =="
for ROLE in roles/run.admin roles/cloudbuild.builds.editor roles/storage.admin roles/artifactregistry.writer; do
  gcloud projects add-iam-policy-binding "$PROJECT" \
    --member="serviceAccount:${SA_EMAIL}" --role="$ROLE" --condition=None >/dev/null
  echo "  $ROLE"
done

# Cloud Run 서비스가 실행될 때 쓰는 런타임 서비스 계정을 대신 지정할 수 있어야 한다.
gcloud iam service-accounts add-iam-policy-binding "$RUNTIME_SA" \
  --project "$PROJECT" --member="serviceAccount:${SA_EMAIL}" \
  --role=roles/iam.serviceAccountUser >/dev/null
echo "  roles/iam.serviceAccountUser (런타임 SA $RUNTIME_SA)"

echo "== GitHub 저장소에 배포 권한 위임 =="
gcloud iam service-accounts add-iam-policy-binding "$SA_EMAIL" \
  --project "$PROJECT" --role=roles/iam.workloadIdentityUser \
  --member="principalSet://iam.googleapis.com/projects/${PROJECT_NUMBER}/locations/global/workloadIdentityPools/${POOL}/attribute.repository/${REPO}" >/dev/null

echo
echo "완료. .github/workflows/deploy.yml 의 값과 맞는지 확인하세요:"
echo "  WIF_PROVIDER: projects/${PROJECT_NUMBER}/locations/global/workloadIdentityPools/${POOL}/providers/${PROVIDER}"
echo "  DEPLOY_SA:    ${SA_EMAIL}"
