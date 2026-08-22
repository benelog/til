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

## 저장소

Turso(libSQL)를 씁니다. `libsql://` 스킴은 웹소켓으로 붙지만, 앱이 이를 자동으로 `https://`
로 바꿔 **요청 단위 HTTP**로 접속합니다. 인스턴스가 자주 뜨고 지는 환경에서 커넥션 풀을
새로 맺을 필요가 없기 때문입니다. 드라이버(`libsql-client-go`)도 순수 Go라 CGO가 필요 없습니다.

## 배포

배포처는 **Back4app Containers** 무료 티어(256MB RAM, 0.25 CPU, 카드 불필요)이고,
저장소는 **Turso**(aws-us-east-2), 도메인은 **Netlify 프록시**로 붙입니다.

### 1. Turso 데이터베이스

```bash
turso auth login
./setup-db.sh          # DB 생성 후 접속 정보를 .env.deploy 에 기록
```

### 2. Back4app Containers

1. [containers.back4app.com](https://containers.back4app.com) 에서 GitHub 계정으로 가입
2. New App → `benelog/til` 저장소, `main` 브랜치, Build type은 **Dockerfile**
3. Settings → Environment Variables 에 아래 4개 등록 후 Save (저장 시 자동 재배포)

   | 이름 | 값 |
   | --- | --- |
   | `TURSO_DATABASE_URL` | `.env.deploy` 의 값 |
   | `TURSO_AUTH_TOKEN` | `.env.deploy` 의 값 |
   | `TIL_PASSWORD` | 쓰기 비밀번호 |
   | `TZ` | `Asia/Seoul` |

`main` 에 푸시하면 자동으로 재빌드·재배포됩니다.

> 토큰을 붙여넣을 때 앞뒤에 `=` 나 공백이 섞이지 않도록 주의하세요. 값이 잘못되면
> 컨테이너 로그에 `JWT error` 또는 `401 Unauthorized` 가 남습니다.
> `turso db destroy` 후 재생성하면 **이전 토큰은 모두 무효**가 되므로 다시 발급해야 합니다.

### 3. til.benelog.net 연결

이 저장소는 Netlify 사이트로도 연결됩니다. `netlify.toml` 이 빌드 없이 `proxy/` 만
배포하고, `proxy/_redirects` 가 모든 요청을 컨테이너로 넘깁니다.

```
/*  https://til-nsh62xv6.b4a.run/:splat  200
```

1. Netlify에서 `benelog/til` 저장소로 새 프로젝트 생성
2. Domain management → `til.benelog.net` 을 커스텀 도메인으로 추가
   (benelog.net DNS를 Netlify가 관리 중이라 레코드는 자동 생성됨)
3. 백엔드 주소가 바뀌면 `proxy/_redirects` 만 고쳐서 푸시

상태 코드 `200` 은 리다이렉트가 아니라 **rewrite(프록시)** 라서, 주소창은
`til.benelog.net` 그대로 유지됩니다. Back4app의 커스텀 도메인 기능은 유료지만,
이 방식은 무료 플랜으로도 커스텀 도메인을 쓸 수 있게 해줍니다.

### 다른 배포처를 검토했다면

- **Netlify Functions (Go)**: Go 함수는 Lambda 호환 모드로만 작성 가능한데, 이 모드는
  **2027년 7월 1일부로 배포가 거부**됩니다. 이주 경로인 `@netlify/aws-lambda-compat` 은
  JS 전용이라 Go 에는 대안이 없습니다.
- **Google Cloud Run**: 무료 한도만 쓰더라도 **열려 있는 결제 계정 연결이 필수**입니다.
- **Northflank**: 무료 티어라도 리소스 생성 시 **카드 등록을 요구**합니다. 무료 리전도
  us-central 과 europe-west 뿐입니다.

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
