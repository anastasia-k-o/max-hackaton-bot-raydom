# MAX Bot — задачи разработки.
#
# `make help` выводит список команд.

SHELL := /bin/bash

BINARY      := bot
BUILD_DIR   := bin
CMD         := ./cmd/bot
VERSION     ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS     := -s -w -X main.version=$(VERSION)
DOCKER_IMAGE := max-bot
GOLANGCI    := $(shell command -v golangci-lint 2>/dev/null)

# Значения по умолчанию для `make run`: полностью локальный режим, токен не
# нужен. Файл .env, если он есть, имеет приоритет.
RUN_ENV := APP_ENV=dev \
           HTTP_PORT=8080 \
           LOG_LEVEL=debug \
           MAX_MODE=mock \
           MAX_UPDATES_MODE=webhook \
           CORE_MODE=stub \
           INTERNAL_API_KEY=change-me \
           MAX_WEBHOOK_SECRET=dev-webhook-secret \
           MINI_APP_URL=https://afisha.example.ru/app

.DEFAULT_GOAL := help
.PHONY: help run run-polling build test test-race test-cover lint fmt vet tidy check \
        docker-build docker-up docker-down docker-logs smoke smoke-polling demo demo-watch \
        max-info clean

help: ## Показать список команд
	@echo "MAX Bot — доступные команды:"
	@echo
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) \
		| awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}'
	@echo

run: ## Запустить бота локально (mock MAX + stub Core, токен не нужен)
	@if [ -f .env ]; then \
		echo "→ конфигурация из .env"; \
		set -a && . ./.env && set +a && go run $(CMD); \
	else \
		echo "→ .env не найден, используются значения по умолчанию (mock/stub)"; \
		$(RUN_ENV) go run $(CMD); \
	fi

run-polling: ## Запустить с настоящим MAX через long polling (нужен .env с токеном)
	@test -f .env || { echo "Нет .env. Выполните: cp .env.example .env"; exit 1; }
	@echo "→ конфигурация из .env, режим событий переопределён на polling"
	@echo "  Публичный адрес и туннель не нужны. Подробности: docs/MAX_SETUP.md, раздел 5a"
	@set -a && . ./.env && set +a && MAX_UPDATES_MODE=polling go run $(CMD)

build: ## Собрать бинарник в bin/
	@mkdir -p $(BUILD_DIR)
	go build -trimpath -ldflags="$(LDFLAGS)" -o $(BUILD_DIR)/$(BINARY) $(CMD)
	@echo "→ $(BUILD_DIR)/$(BINARY) ($(VERSION))"

test: ## Прогнать тесты
	go test ./...

test-race: ## Прогнать тесты с детектором гонок
	go test -race ./...

test-cover: ## Тесты с отчётом о покрытии (coverage.html)
	go test -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html
	@go tool cover -func=coverage.out | tail -1
	@echo "→ coverage.html"

fmt: ## Отформатировать код
	go fmt ./...

vet: ## Статический анализ go vet
	go vet ./...

lint: ## Линтер golangci-lint (если установлен)
ifdef GOLANGCI
	golangci-lint run ./...
else
	@echo "golangci-lint не установлен — выполняется go vet."
	@echo "Установка: https://golangci-lint.run/welcome/install/"
	@echo "  go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest"
	@echo
	go vet ./...
endif

tidy: ## Привести go.mod/go.sum в порядок
	go mod tidy

check: fmt vet test ## Быстрая проверка перед коммитом: fmt + vet + test

smoke: ## Проверить живой mock-режим: уведомление → callback → действие
	@bash scripts/smoke.sh

demo: ## Отправить себе все 8 уведомлений (нужен запущенный бот и max_user_id)
	@bash scripts/demo.sh $(ID)

demo-watch: ## Следить за тем, что бот передал бы в Core Backend при нажатии кнопок
	@bash scripts/demo.sh --watch

smoke-polling: ## Проверить режим long polling против подменного MAX (токен не нужен)
	@bash scripts/smoke-polling.sh

max-info: ## Кто наш бот в MAX и кому уходят его события (нужен MAX_BOT_TOKEN)
	@bash scripts/max-info.sh

docker-build: ## Собрать Docker-образ
	docker build --build-arg VERSION=$(VERSION) -t $(DOCKER_IMAGE):$(VERSION) -t $(DOCKER_IMAGE):latest .
	@echo "→ $(DOCKER_IMAGE):$(VERSION)"

docker-up: ## Поднять бота через docker compose (нужен .env)
	@test -f .env || { echo "Нет .env. Выполните: cp .env.example .env"; exit 1; }
	docker compose up -d --build
	@echo "→ http://localhost:$${HTTP_PORT:-8080}/health"

docker-down: ## Остановить docker compose
	docker compose down

docker-logs: ## Смотреть логи контейнера
	docker compose logs -f bot

clean: ## Удалить артефакты сборки
	rm -rf $(BUILD_DIR) coverage.out coverage.html
