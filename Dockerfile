# 순수 Go 의존성만 쓰므로 CGO 없이 정적 바이너리로 빌드된다.
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/til .

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/til /til
# 시간대 표시(Asia/Seoul)를 위해 tzdata 를 함께 넣는다.
COPY --from=build /usr/local/go/lib/time/zoneinfo.zip /zoneinfo.zip
ENV ZONEINFO=/zoneinfo.zip
ENV PORT=8080
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/til"]
