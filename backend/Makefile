# Core Backend — задачи разработки. `make help` — список команд.

SHELL := /bin/bash
CMD   := ./cmd/core

# Настройки — общий .env в корне репозитория. Без него: dev-режим, уведомления
# только в лог. Всю систему удобнее поднимать из корня: make up.
ENV_FILE := ../.env
RUN_ENV  := HTTP_ADDR=:8090 DB_PATH=data/core.db DEV_MODE=true NOTIFY_MODE=log

.DEFAULT_GOAL := help
.PHONY: help run run-fast build test test-race vet fmt e2e scenario reset-db clean

help: ## Показать список команд
	@echo "Core Backend — доступные команды:"
	@echo
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) \
		| awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}'
	@echo

run: ## Запустить (настройки из ../.env; без него — dev-режим без бота)
	@if [ -f $(ENV_FILE) ]; then \
		echo "→ конфигурация из $(ENV_FILE)"; \
		set -a && . $(ENV_FILE) && set +a && HTTP_ADDR=:$${CORE_PORT:-8090} go run $(CMD); \
	else \
		echo "→ $(ENV_FILE) не найден: DEV_MODE, уведомления только в лог. Общие настройки: make env в корне"; \
		$(RUN_ENV) go run $(CMD); \
	fi

run-fast: ## То же, но часы в минутах: вся цепочка напоминаний за 10 минут
	@test -f $(ENV_FILE) || { echo "Нет $(ENV_FILE). В корне репозитория: make env"; exit 1; }
	@set -a && . $(ENV_FILE) && set +a && HTTP_ADDR=:$${CORE_PORT:-8090} TIMING_PRESET=fast go run $(CMD)

build: ## Собрать бинарник в bin/
	@mkdir -p bin
	go build -trimpath -o bin/core $(CMD)

test: ## Тесты
	go test ./...

test-race: ## Тесты с детектором гонок
	go test -race -count=1 ./...

vet: ## go vet
	go vet ./...

fmt: ## gofmt
	gofmt -w .

e2e: ## Сквозная проверка с ботом из ../bot (mock MAX, без токена)
	bash scripts/e2e-with-bot.sh

scenario: ## Проверка в настоящем MAX: бот и core должны быть запущены
	bash scripts/scenario.sh

reset-db: ## Удалить локальную базу
	rm -f data/core.db data/core.db-wal data/core.db-shm

clean: ## Удалить bin/
	rm -rf bin
