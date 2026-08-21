# TIL

Markdown으로 쓰는 간단한 Today I Learned 기록장.

- **Go** (표준 `net/http`, 외부 웹 프레임워크 없음) + **HTMX** — 페이지 새로고침 없이 작성/수정/삭제
- **Turso (libSQL)** 저장소, 로컬은 SQLite 파일로 동일 코드 동작
- 읽기는 공개, 쓰기/수정/삭제는 단일 비밀번호 세션으로 보호
- 의존성 전부 순수 Go → CGO 없는 정적 바이너리 (약 12MB distroless 이미지)

## 로컬 실행

```bash
cp .env.example .env          # TIL_PASSWORD 만 채우면 됨
export $(grep -v '^#' .env | xargs)
export INSECURE_COOKIES=1     # http://localhost 에서 쿠키를 쓰기 위해
go run .                      # http://localhost:8080
```

`TURSO_DATABASE_URL` 을 비워두면 로컬 `til.db` 파일을 씁니다. Turso 없이도 그대로 개발 가능합니다.

## 저장소: Turso 준비

```bash
curl -sSfL https://get.tur.so/install.sh | bash
turso auth signup
turso db create til
turso db show til --url          # → libsql://til-<org>.turso.io
turso db tokens create til       # → 인증 토큰
```

앱은 `libsql://` 을 자동으로 `https://` 로 바꿔서 **요청 단위 HTTP**로 붙습니다. 인스턴스가
자주 뜨고 지는 서버리스 환경에서 커넥션 풀을 새로 맺을 필요가 없기 때문입니다.

## 배포

`turso`, `gcloud` CLI 로그인이 끝났다면 스크립트 한 번이면 됩니다.

```bash
set -a; source .env; set +a
GCP_PROJECT=<프로젝트ID> ./deploy.sh
```

Turso DB 생성 → Secret Manager 등록 → Cloud Run 배포 → `proxy/_redirects` 의
프록시 대상 갱신까지 처리합니다. 아래는 스크립트가 실제로 수행하는 내용입니다.

### Google Cloud Run

```bash
gcloud run deploy til \
  --source . \
  --region asia-northeast3 \
  --allow-unauthenticated \
  --min-instances 0 --max-instances 2 \
  --cpu 1 --memory 256Mi \
  --set-env-vars "TZ=Asia/Seoul,TURSO_DATABASE_URL=libsql://til-<org>.turso.io" \
  --set-secrets "TIL_PASSWORD=til-password:latest,TURSO_AUTH_TOKEN=turso-token:latest"
```

### 비밀값은 Secret Manager 로

```bash
printf '%s' '실제-비밀번호' | gcloud secrets create til-password --data-file=-
printf '%s' '실제-turso-토큰' | gcloud secrets create turso-token --data-file=-
```

`--min-instances 0` 이라 안 쓸 때는 과금이 없습니다. Cloud Run 상시 무료 한도는
월 200만 요청 / 36만 GB-초 / 18만 vCPU-초로, 개인 TIL 트래픽은 여기 한참 못 미칩니다.

## 도메인: Netlify 프록시로 til.benelog.net 연결

이 저장소는 Netlify 사이트로도 연결됩니다. `netlify.toml` 이 빌드 없이 `proxy/` 만
배포하고, `proxy/_redirects` 가 모든 요청을 Cloud Run 으로 넘깁니다.

```
# proxy/_redirects
/*  https://til-xxxxxxxx.asia-northeast3.run.app/:splat  200
```

Netlify 쪽 설정은 한 번만 하면 됩니다.

1. Netlify에서 `benelog/til` 저장소로 새 프로젝트 생성 (빌드 설정은 `netlify.toml` 이 담당)
2. Domain management → `til.benelog.net` 을 커스텀 도메인으로 추가
3. 이후 `deploy.sh` 가 `proxy/_redirects` 를 갱신하므로, 커밋/푸시만 하면 반영됨

`200` 상태 코드가 리다이렉트가 아니라 **프록시(rewrite)** 를 뜻합니다. 주소창은
`til.benelog.net` 그대로 유지되고, DNS와 인증서는 지금 쓰는 다른 사이트들처럼
Netlify가 계속 관리합니다. 무료 플랜에서 대역폭(월 100GB)만 소비합니다.

> Netlify Functions 로 직접 돌리지 않는 이유: Go 함수는 Lambda 호환 모드로만 작성 가능한데,
> 이 모드는 **2027년 7월 1일부로 배포가 거부**됩니다. 공식 이주 경로인
> `@netlify/aws-lambda-compat` 은 JS 전용이라 Go 에는 대안이 없습니다.

## 환경변수

| 이름 | 필수 | 설명 |
| --- | --- | --- |
| `TIL_PASSWORD` | ✅ | 쓰기 권한 비밀번호 |
| `SESSION_SECRET` | | 세션 서명 키. 비우면 비밀번호에서 파생 (비밀번호를 바꾸면 기존 세션 자동 무효화) |
| `TURSO_DATABASE_URL` | | 비우면 로컬 SQLite 파일 사용 |
| `TURSO_AUTH_TOKEN` | | Turso 인증 토큰 |
| `SQLITE_PATH` | | 로컬 파일 경로 (기본 `til.db`) |
| `PORT` | | 기본 `8080`. Cloud Run이 자동 주입 |
| `TZ` | | 표시용 시간대 (기본 `Asia/Seoul`) |
| `INSECURE_COOKIES` | | 값이 있으면 쿠키 `Secure` 해제. **로컬 전용** |

## 라우트

| 메서드 | 경로 | 인증 | 응답 |
| --- | --- | --- | --- |
| GET | `/` | | 전체 페이지 |
| GET | `/login`, POST `/login`, POST `/logout` | | 세션 |
| GET | `/entries/{id}` | | 항목 조각 (수정 취소용) |
| GET | `/entries/{id}/edit` | ✅ | 편집 폼 조각 |
| POST | `/entries` | ✅ | 새 항목 + 입력폼 초기화 (OOB 스왑) |
| PUT | `/entries/{id}` | ✅ | 갱신된 항목 조각 |
| DELETE | `/entries/{id}` | ✅ | 빈 본문 → htmx가 항목 제거 |
| POST | `/preview` | ✅ | 마크다운 실시간 미리보기 |
| GET | `/healthz` | | `ok` |

## 보안 메모

- 세션 쿠키는 HMAC-SHA256 서명 + 만료시각 포함, `HttpOnly` / `SameSite=Lax` / `Secure`.
  `SameSite=Lax` 라서 외부 사이트가 POST/PUT/DELETE 를 대신 쏘지 못합니다.
- 비밀번호 비교는 `subtle.ConstantTimeCompare` 로 타이밍 공격을 피합니다.
- 마크다운은 goldmark 렌더 후 bluemonday 로 정화합니다. 다른 곳에서 복사해 온
  마크다운에 `<script>` 가 섞여도 걸러집니다.
