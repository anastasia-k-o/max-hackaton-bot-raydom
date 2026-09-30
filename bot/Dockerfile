# syntax=docker/dockerfile:1

# ---------------------------------------------------------------------------
# Этап сборки
# ---------------------------------------------------------------------------
FROM golang:1.24-alpine AS build

WORKDIR /src

# Зависимости отдельным слоем: он переиспользуется, пока go.mod/go.sum не
# менялись, поэтому правка кода не тянет за собой повторную загрузку модулей.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# CGO выключен, чтобы получить статический бинарник: он работает в scratch и
# не зависит от библиотек базового образа.
# Версия штампуется в бинарник и видна в GET /health.
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags="-s -w -X main.version=${VERSION}" \
    -o /out/bot ./cmd/bot

# ---------------------------------------------------------------------------
# Этап выполнения
# ---------------------------------------------------------------------------
FROM alpine:3.20

# ca-certificates нужны для HTTPS-вызовов к MAX API.
# tzdata нужен, чтобы time.Parse корректно работал со смещениями в RFC 3339.
# wget входит в busybox и используется для HEALTHCHECK — отдельный curl не нужен.
RUN apk add --no-cache ca-certificates tzdata

# Непривилегированный пользователь. Бот не пишет на диск и не слушает
# привилегированные порты, поэтому root ему не нужен ни для чего.
RUN addgroup -g 10001 -S botuser && \
    adduser -u 10001 -S -G botuser botuser

COPY --from=build /out/bot /usr/local/bin/bot

USER botuser:botuser

# Значение по умолчанию; переопределяется переменной HTTP_PORT.
EXPOSE 8080

# Проба обращается к /health (liveness), а не к /ready: недоступность MAX или
# Core Backend не должна приводить к перезапуску исправного процесса.
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD wget -q -O- http://127.0.0.1:${HTTP_PORT:-8080}/health || exit 1

# Конфигурация приходит только через переменные окружения. Секретов внутри
# образа нет и быть не должно.
ENTRYPOINT ["/usr/local/bin/bot"]
