# TIL

Markdown으로 쓰는 간단한 Today I Learned 기록장.

- **Go** (표준 `net/http`, 외부 웹 프레임워크 없음) + **HTMX** — 페이지 새로고침 없이 작성/수정/삭제
- **Turso (libSQL)** 저장소, 로컬은 SQLite 파일로 동일 코드 동작
- 읽기는 공개, 쓰기/수정/삭제는 단일 비밀번호 세션으로 보호
- **PWA** — 홈 화면에 설치 가능하고, 한 번 본 화면은 오프라인에서도 열림
- 의존성 전부 순수 Go → CGO 없는 정적 바이너리 (28MB distroless 이미지)

## 로컬 실행

```bash
cp .env.example .env          # TIL_PASSWORD 만 채우면 됨
export $(grep -v '^#' .env | xargs)
export INSECURE_COOKIES=1     # http://localhost 에서 쿠키를 쓰기 위해
go run .                      # http://localhost:8080
```

`TURSO_DATABASE_URL` 을 비워두면 로컬 `til.db` 파일을 씁니다. Turso 없이도 그대로 개발 가능합니다.

## 저장소

Turso(libSQL)를 씁니다. `libsql://` 스킴은 웹소켓으로 붙지만, 앱이 이를 자동으로 `https://`
로 바꿔 **요청 단위 HTTP**로 접속합니다. 인스턴스가 자주 뜨고 지는 환경에서 커넥션 풀을
새로 맺을 필요가 없기 때문입니다. 드라이버(`libsql-client-go`)도 순수 Go라 CGO가 필요 없습니다.

**DB는 앱과 같은 리전에 둡니다.** 그룹 `tokyo`(`aws-ap-northeast-1`)의 `til-tokyo` 를 씁니다.
페이지 한 장이 쿼리 한 번이라 DB 왕복이 응답 시간을 그대로 지배합니다. 한국에서 같은 조건으로
재보면:

| DB 위치 | `GET /` |
| --- | --- |
| `aws-us-east-2` (오하이오) | 197~220ms |
| `aws-ap-northeast-1` (도쿄) | **38~45ms** |

## 배포

배포처는 **Google Cloud Run**(`asia-northeast1`, 도쿄)이고, 저장소는 **Turso**
(`aws-ap-northeast-1`, 도쿄), 도메인은 **Cloud Run 커스텀 도메인 매핑**으로 직접 붙입니다.
개인 앱 트래픽은 Cloud Run 무료 한도(월 2M 요청 / 360,000 GiB-초) 안에서 끝나 실질 비용은 $0 입니다.

> 2026-08-22 이전에는 서울 서비스(`til`, `asia-northeast3`) + Netlify 프록시 구성이었습니다.
> 서울 리전은 Cloud Run 직접 도메인 매핑을 지원하지 않아 프록시가 필요했는데, 측정 결과
> 프록시가 응답 시간의 상당 부분을 차지해 도쿄 서비스 + 직접 매핑으로 옮겼습니다.
> 서울 서비스와 Netlify 프록시 설정은 롤백 경로로 남겨 뒀습니다.

> 앞서 Back4app Containers 무료 티어를 썼다가 접었습니다. 임시 URL이 30~60분 뒤
> 만료되면서 배포가 통째로 파괴되어 상시 운영이 불가능했습니다. 아래 "다른 배포처를
> 검토했다면" 참고.

### 1. Turso 데이터베이스

```bash
turso auth login
./setup-db.sh          # DB 생성 후 접속 정보를 .env.deploy 에 기록
```

### 2. Cloud Run

```bash
gcloud auth login
gcloud config set project <project>
gcloud billing projects link <project> --billing-account=<ACCOUNT_ID>   # 무료 한도만 써도 필수
gcloud services enable run.googleapis.com cloudbuild.googleapis.com artifactregistry.googleapis.com
```

첫 배포는 **`PERMISSION_DENIED` 로 죽습니다.** Google이 기본 컴퓨트 서비스 계정의 빌드
권한을 걷어낸 뒤 새 프로젝트에서 나는 증상입니다. 한 번만 부여하면 됩니다:

```bash
gcloud projects add-iam-policy-binding <project> \
  --member=serviceAccount:<projectNumber>-compute@developer.gserviceaccount.com \
  --role=roles/cloudbuild.builds.builder
```

배포합니다. 환경변수는 `.env.deploy` 의 3개에 `TIL_PASSWORD` 를 더해 YAML 한 장으로
넘깁니다 (커맨드라인에 토큰이 남지 않게):

```bash
cat > /tmp/run-env.yaml <<EOF
TURSO_DATABASE_URL: "..."
TURSO_AUTH_TOKEN: "..."
TIL_PASSWORD: "..."
SESSION_SECRET: "..."   # openssl rand -base64 32
TZ: "Asia/Seoul"
EOF

gcloud run deploy til-tokyo --source . --region asia-northeast1 \
  --allow-unauthenticated --env-vars-file /tmp/run-env.yaml
```

`--source .` 가 Cloud Build로 이 저장소의 Dockerfile을 빌드해 배포합니다. **환경변수는
서비스에 남으므로 다음 배포부터는 `--env-vars-file` 없이** `gcloud run deploy til-tokyo --source .
--region asia-northeast1` 만 하면 됩니다.

> 서울 서비스 `til`(`asia-northeast3`)도 롤백용으로 남아 있습니다. 롤백을 대비해 코드가
> 크게 바뀌면 서울 서비스에도 같은 이미지를 한 번 더 배포해 두는 편이 안전합니다.

### 자동 배포 (GitHub Actions)

`main` 에 푸시하면 `.github/workflows/deploy.yml` 이 위와 같은 `gcloud run deploy
til-tokyo --source .` 를 대신 실행합니다. `README.md`, `proxy/`, `netlify.toml` 만 바뀐
커밋은 배포를 건너뜁니다. 롤백용 서울 서비스(`til`)는 자동 배포에서 빼 두었고, Actions
화면에서 workflow_dispatch 로 실행할 때 `deploy_seoul` 을 켜면 함께 배포합니다.

인증은 **서비스 계정 키 없이 Workload Identity Federation** 으로 합니다. GitHub 이
발급한 OIDC 토큰을 GCP 가 직접 검증하므로 저장소에 둘 시크릿이 없습니다. 공급자에
`assertion.repository == 'benelog/til'` 조건이 걸려 있어 다른 저장소는 이 풀로 토큰을
받지 못합니다 (저장소가 공개라 이 조건이 특히 중요합니다). 설정은 한 번만 하면 됩니다:

```bash
./scripts/setup-gh-deploy.sh
```

풀·공급자·배포용 서비스 계정(`github-deployer@benelog.iam.gserviceaccount.com`)을 만들고
`run.admin` / `cloudbuild.builds.editor` / `storage.admin` / `artifactregistry.writer` 와
런타임 서비스 계정에 대한 `iam.serviceAccountUser` 를 부여합니다. 이미 있는 리소스는
건너뛰므로 다시 실행해도 안전합니다.

환경변수(`TURSO_*`, `TIL_PASSWORD`, `TZ`)는 서비스에 남아 리비전 간에 유지되므로
워크플로는 넘기지 않습니다. **GitHub 에 저장하는 비밀값이 하나도 없습니다.**

> 토큰을 붙여넣을 때 앞뒤에 `=` 나 공백이 섞이지 않도록 주의하세요. 값이 잘못되면
> 로그에 `JWT error` 또는 `401 Unauthorized` 가 남습니다.
> `turso db destroy` 후 재생성하면 **이전 토큰은 모두 무효**가 되므로 다시 발급해야 합니다.

### 3. til.benelog.net 연결

Cloud Run의 커스텀 도메인 매핑으로 직접 붙입니다. 먼저 Google Search Console에서
`benelog.net` 소유권을 확인해야 하고, 매핑은 도쿄(`asia-northeast1`)처럼 이 기능을
지원하는 리전에서만 만들 수 있습니다.

```bash
gcloud beta run domain-mappings create \
  --domain=til.benelog.net --service=til-tokyo \
  --region=asia-northeast1 --project=benelog
```

그 다음 DNS(Netlify가 `benelog.net` zone을 관리)에 매핑이 안내한 레코드 하나를 둡니다.

```
til  CNAME  ghs.googlehosted.com.
```

같은 이름의 `NETLIFY` / `NETLIFYv6` 레코드가 있으면 먼저 지워야 합니다. CNAME은 같은
이름의 다른 레코드와 공존할 수 없습니다. 관리형 인증서는 DNS가 `ghs.googlehosted.com.`
을 가리킨 뒤에야 발급되고, 그동안 해당 호스트는 TLS 오류를 냅니다. 실제 전환에서는
1시간 이내에 발급됐지만 Google 안내상 최대 24시간이 걸릴 수 있습니다.

> ⚠️ Cloud Run 직접 도메인 매핑은 **Preview 기능**이고 Google도 프로덕션 사용을
> 권장하지 않습니다. 개인 앱이라 응답 시간을 우선해 감수한 선택입니다.

#### 롤백 경로로 남긴 Netlify 프록시

이 저장소는 Netlify 사이트로도 연결돼 있습니다. `netlify.toml` 이 빌드 없이 `proxy/` 만
배포하고, `proxy/_redirects` 가 모든 요청을 서울 서비스로 넘깁니다.

```
/*  https://til-788537242173.asia-northeast3.run.app/:splat  200
```

상태 코드 `200` 은 리다이렉트가 아니라 **rewrite(프록시)** 라서 주소창은
`til.benelog.net` 그대로 유지됩니다. 지금은 운영 경로가 아니지만, DNS를 원래
`NETLIFY` / `NETLIFYv6` 레코드(값 `til-benelog.netlify.app`, TTL 3600)로 되돌리면
이 한 장으로 즉시 복구됩니다.

> 2026-08-22 한국의 같은 머신에서 워밍업 1회 후 20회씩 교차 측정한 전체 응답 시간
> 중앙값입니다.
>
> | 경로 | 전체 중앙값 | TCP 중앙값 |
> | --- | --- | --- |
> | 서울 `run.app` 직접 호출 | 99.7ms | 6.1ms |
> | 도쿄 `run.app` 직접 호출 | 98.8ms | 6.0ms |
> | 도쿄 서비스 + Cloud Run 도메인 매핑 (CNAME → `ghs.googlehosted.com.`) | 284.1ms | 62.9ms |
> | 서울 서비스 + Netlify 프록시 (당시 운영 경로) | 347.1ms | 71.7ms |
>
> 도메인 매핑 경로가 Netlify 경로보다 63.0ms 빨랐지만 `run.app` 수준으로 짧아지지는 않습니다.
> 커스텀 도메인 진입 경로 자체가 같은 서비스의 `run.app`보다 185.3ms를 더합니다.
>
> 전환(2026-08-22 20:22 KST) 직후 같은 방식으로 다시 재면 이렇습니다.
>
> | 경로 | 전체 중앙값 | TCP 중앙값 |
> | --- | --- | --- |
> | 도쿄 `run.app` 직접 호출 | 98.5ms | 5.7ms |
> | 도쿄 서비스 + Cloud Run 도메인 매핑 (전환 후 운영 경로) | 224.2ms | 54.2ms |

#### 응답 압축

Cloud Run 앞단의 Google Frontend 는 컨테이너 응답을 대신 압축해주지 않습니다.
Netlify 프록시를 쓸 때는 Netlify 가 Brotli 를 자동으로 걸어줬는데, 직접 매핑으로
바꾸면서 그게 사라졌습니다. 한국으로 나가는 전송에는 무료 구간이 없어서
`compress.go` 의 미들웨어로 앱이 직접 gzip 을 겁니다.

| 응답 | 원본 | gzip |
| --- | --- | --- |
| `GET /` | 1,499 B | 768 B |
| `/static/htmx.min.js` | 50,917 B | 16,435 B |

이미 `Content-Encoding` 이 붙은 응답과 PNG 같은 압축 완료 형식, 512 B 미만의 작은
응답은 건드리지 않습니다. 앞단에 프록시를 다시 끼우더라도 프록시는 이미 인코딩된
응답을 다시 압축하지 않으므로 이중 압축이 되지 않습니다.

### 다른 배포처를 검토했다면

- **Netlify Functions (Go)**: Go 함수는 Lambda 호환 모드로만 작성 가능한데, 이 모드는
  **2027년 7월 1일부로 배포가 거부**됩니다. 이주 경로인 `@netlify/aws-lambda-compat` 은
  JS 전용이라 Go 에는 대안이 없습니다.
- **Back4app Containers**: 카드 없이 Dockerfile 배포가 되고 콜드스타트도 없어서 잠깐
  띄워 보여주기엔 좋습니다. 다만 무료 플랜의 주소가 임시라 만료되면 배포가 통째로
  파괴됩니다. 실제 로그:

  ```
  2026-08-21T23:59:20Z  SYSTEM  DEPLOYMENT READY
  2026-08-22T00:27:12Z  SYSTEM  The Back4app custom domain has expired for free plan
  2026-08-22T00:27:12Z  SYSTEM  COOLING DOWN...
  2026-08-22T00:28:12Z  SYSTEM  DEPLOYMENT DESTROYED
  ```

  만료된 `til-xxxxxxxx.b4a.run` 은 존재한 적 없는 서브도메인과 **똑같은** `404 not found`
  를 돌려줘서 컨테이너 장애(502/503)와 구별되지 않습니다. 재배포하면 접미사가 바뀌어
  `proxy/_redirects` 도 매번 고쳐야 합니다. 영구 URL은 $5/월부터.
- **Google Cloud Run**: 무료 한도만 쓰더라도 **열려 있는 결제 계정 연결이 필수**입니다.
  (지금은 결제 계정을 연결해 이쪽을 씁니다. 사용량은 무료 한도 안입니다.)
- **Northflank**: 무료 티어라도 리소스 생성 시 **카드 등록을 요구**합니다. 무료 리전도
  us-central 과 europe-west 뿐입니다.

## 인증

계정 구분 없이 `TIL_PASSWORD` 하나로 쓰기 권한을 얻고, 세션은 서버에 아무것도
저장하지 않는 서명 쿠키(`til_session`) 한 장으로 유지합니다. 값은
`만료시각.HMAC-SHA256(키, 에폭+만료시각)` 이고 `HttpOnly` + `Secure` +
`SameSite=Lax` 로 나갑니다. 변경 작업이 전부 POST/PUT/DELETE 라서 `SameSite=Lax`
만으로 CSRF 가 막히므로 별도 토큰은 두지 않았습니다.

**`SESSION_SECRET` 을 반드시 채우세요.** 비워두면 서명 키를 비밀번호에서
파생하는데(`SHA256("til-session\0" + 비밀번호)`), 쿠키에는 만료시각이 평문으로
들어 있어서 쿠키 한 장만 새어나가도 후보 비밀번호마다 해시 두 번으로 정답 여부를
확인할 수 있습니다. 솔트도 스트레칭도 없어 GPU 대입에 그대로 노출되므로, "세션
탈취"로 끝날 사고가 "비밀번호 유출"로 번집니다. 비어 있으면 시작할 때 경고 로그를
남깁니다.

**세션 회수**는 `SESSION_EPOCH` 로 합니다. 에폭이 서명 대상에 들어가므로 값을
올려 재배포하면 이미 나간 쿠키가 한 번에 무효가 됩니다. `Revoke`(로그아웃)는
브라우저 쿠키만 지울 뿐이라, 쿠키가 유출됐다면 이쪽을 써야 합니다.

**온라인 대입**은 IP 당 토큰 버킷으로 막습니다(`ratelimit.go`). 연속 5회까지
허용하고 20초에 한 번씩 회복하며, 실패 응답은 0.5초 늦춰 나갑니다. 예산이 떨어지면
`429` 와 `Retry-After` 를 돌려줍니다. 버킷 키로 쓰는 IP 는 `X-Forwarded-For` 의
**마지막** 항목입니다. Google Frontend 가 원래 헤더 뒤에 실제 커넥션 IP 를
덧붙이므로, 클라이언트가 헤더를 위조해도 마지막 항목만은 진짜 주소입니다.

로그인 상태에 따라 내용이 달라지는 응답에는 `Cache-Control: no-store` 와
`Vary: Cookie` 를 붙입니다. 지금은 Cloud Run 에 직접 붙어 있어 앞단 공유 캐시가
없지만, 롤백용 Netlify 프록시를 되살리면 프록시 캐시가 끼어들 수 있습니다.

## 환경변수

| 이름 | 필수 | 설명 |
| --- | --- | --- |
| `TIL_PASSWORD` | ✅ | 쓰기 권한 비밀번호 |
| `SESSION_SECRET` | 권장 | 세션 서명 키. `openssl rand -base64 32`. 비우면 비밀번호에서 파생하는데 안전하지 않다 (아래 참고) |
| `SESSION_EPOCH` | | 기본 `1`. 올리면 이미 나간 세션이 전부 무효가 된다 |
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
| GET | `/sw.js` | | 서비스 워커 (루트 스코프용) |
| GET | `/healthz`, `/health` | | `ok`. Cloud Run 앞단 Google Frontend 가 **정확히 `/healthz`** 만 가로채 자체 404 를 돌려주므로 `/health` 를 함께 연다 |

## PWA

`static/manifest.webmanifest` 와 서비스 워커로 홈 화면 설치를 지원합니다.

- **서비스 워커는 `/sw.js` 로 서빙합니다.** 워커의 제어 범위는 스크립트가 놓인 경로 아래로
  제한되므로, `/static/sw.js` 였다면 정적 파일만 다룰 수 있습니다. 파일 자체는 `static/`
  안에 두고 `serveServiceWorker` 가 루트 경로에서 내려줍니다.
- **정적 자원은 stale-while-revalidate.** 파일명에 해시를 붙이는 빌드 단계가 없어서,
  cache-first + 수동 버전 올리기는 언젠가 잊어버리면 낡은 CSS 가 계속 남습니다. 캐시 이름
  (`til-v1`)은 활성화 시 옛 캐시를 지우는 용도로만 씁니다.
- **페이지는 network-first**, 실패하면 마지막으로 받아둔 같은 페이지, 그것도 없으면
  `static/offline.html` 을 보여줍니다. htmx 조각(`/entries/...`)과 쓰기 요청은 캐시하지
  않고 그대로 네트워크로 보냅니다.
- 오프라인에서 보이는 페이지는 캐시된 사본이라 로그인 상태의 수정/삭제 버튼이 함께
  남아 있을 수 있습니다. 눌러도 요청이 나가지 않을 뿐이고, 혼자 쓰는 앱이라 그대로 뒀습니다.

아이콘은 `static/icon-*.png` 입니다. 다시 만들려면 `scripts/gen-icons.py` 를 실행하세요.

## 보안 메모

- 세션 쿠키는 HMAC-SHA256 서명 + 만료시각 포함, `HttpOnly` / `SameSite=Lax` / `Secure`.
  `SameSite=Lax` 라서 외부 사이트가 POST/PUT/DELETE 를 대신 쏘지 못합니다.
- 비밀번호 비교는 `subtle.ConstantTimeCompare` 로 타이밍 공격을 피합니다.
- 마크다운은 goldmark 렌더 후 bluemonday 로 정화합니다. 다른 곳에서 복사해 온
  마크다운에 `<script>` 가 섞여도 걸러집니다.
