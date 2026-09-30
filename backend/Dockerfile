# syntax=docker/dockerfile:1

# SQLite-драйвер (mattn/go-sqlite3) собирается через cgo, поэтому нужен
# компилятор C. Сборка и запуск — на Alpine, как у бота: так бинарник и его
# libc (musl) совпадают, а образ ведёт себя так же, как соседний.
FROM golang:1.24-alpine AS build
RUN apk add --no-cache build-base
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# _LARGEFILE64_SOURCE: новые musl убрали 64-битные псевдонимы (lseek64 и
# др.), которые ждёт встроенный в драйвер исходник SQLite.
# Статическая сборка: бинарнику не нужен загрузчик musl из образа, он
# запускается так же, как бинарник бота (тот собран без cgo).
RUN CGO_ENABLED=1 CGO_CFLAGS="-D_LARGEFILE64_SOURCE" \
    go build -trimpath -ldflags='-s -w -linkmode external -extldflags "-static"' -o /out/core ./cmd/core

FROM alpine:3.20
# tzdata не обязателен (зоны встроены в бинарник), ca-certificates — для
# HTTPS, если когда-нибудь понадобится.
RUN apk add --no-cache ca-certificates && \
    addgroup -g 10002 -S core && adduser -u 10002 -S -G core core && \
    mkdir -p /data && chown core:core /data
COPY --from=build /out/core /usr/local/bin/core
USER core:core
# База живёт в томе: он переживает пересоздание контейнера (make down / up).
ENV HTTP_ADDR=:8090 DB_PATH=/data/core.db
VOLUME /data
EXPOSE 8090
HEALTHCHECK --interval=10s --timeout=3s --start-period=5s --retries=3 \
    CMD ["/usr/local/bin/core", "healthcheck"]
ENTRYPOINT ["/usr/local/bin/core"]
