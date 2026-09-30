# Вся система «Рядом»: бот, Core Backend, мини-приложение.
# `make help` — список команд.

SHELL   := /bin/bash
COMPOSE := docker compose

.DEFAULT_GOAL := help
.PHONY: help env up down restart stop logs ps build rebuild clean \
        run-core run-bot run-front scenario e2e test check-env

help: ## Показать список команд
	@echo "max-hackaton — доступные команды:"
	@echo
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) \
		| awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-10s\033[0m %s\n", $$1, $$2}'
	@echo

env: ## Создать .env из .env.example со случайными ключами
	@if [ -f .env ]; then echo ".env уже есть — не трогаю"; exit 0; fi; \
	cp .env.example .env; \
	sed -i "s/^INTERNAL_API_KEY=.*/INTERNAL_API_KEY=$$(openssl rand -hex 24)/" .env; \
	sed -i "s/^CORE_API_KEY=.*/CORE_API_KEY=$$(openssl rand -hex 24)/" .env; \
	echo "→ .env создан, ключи сгенерированы. Для настоящего MAX впишите MAX_MODE=real и MAX_BOT_TOKEN."

check-env:
	@test -f .env || { echo "Нет .env. Выполните: make env"; exit 1; }
	@if grep -qE '^(INTERNAL_API_KEY|CORE_API_KEY)=change-me' .env; then \
		echo "⚠ В .env остались ключи change-me — замените их или удалите .env и выполните make env"; fi

up: check-env ## Собрать и поднять всю систему в Docker
	$(COMPOSE) up -d --build
	@$(MAKE) --no-print-directory ps
	@set -a && . ./.env && set +a; \
	echo; \
	echo "  мини-приложение  http://localhost:$${FRONTEND_PORT:-4173}"; \
	echo "  Core Backend     http://localhost:$${CORE_PORT:-8090}/health"; \
	echo "  бот              http://localhost:$${BOT_PORT:-8080}/health"; \
	echo; \
	echo "  Логи: make logs    Остановить: make down"

down: ## Остановить и удалить контейнеры (данные Core Backend сохраняются)
	$(COMPOSE) down

stop: ## Приостановить контейнеры, не удаляя их
	$(COMPOSE) stop

restart: check-env ## Перезапустить без пересборки
	$(COMPOSE) restart

rebuild: check-env ## Пересобрать образы с нуля и поднять
	$(COMPOSE) build --no-cache
	$(COMPOSE) up -d

build: check-env ## Только собрать образы
	$(COMPOSE) build

logs: ## Логи всех сервисов (make logs s=bot — одного)
	$(COMPOSE) logs -f --tail=100 $(s)

ps: ## Состояние контейнеров
	@$(COMPOSE) ps --format 'table {{.Service}}\t{{.State}}\t{{.Status}}\t{{.Ports}}'

clean: ## Остановить и УДАЛИТЬ данные Core Backend (том с базой)
	@read -r -p "Удалить базу Core Backend вместе с контейнерами? [да/нет]: " a; \
	[ "$$a" = "да" ] && $(COMPOSE) down -v || echo "Отменено."

# --- Без Docker: каждый сервис в своём терминале, настройки из того же .env ---

run-core: check-env ## Core Backend без Docker (нужен Go и gcc)
	@set -a && . ./.env && set +a && cd backend && HTTP_ADDR=:$${CORE_PORT:-8090} go run ./cmd/core

run-bot: check-env ## Бот без Docker
	@set -a && . ./.env && set +a && cd bot && HTTP_PORT=$${BOT_PORT:-8080} go run ./cmd/bot

run-front: ## Мини-приложение в режиме разработки (Vite)
	@set -a && [ -f .env ] && . ./.env; set +a; cd frontend && npm install --no-audit --no-fund && \
		VITE_MAX_BOT_URL=$${MAX_BOT_URL:-} npx vite --port $${FRONTEND_PORT:-4173} --host

# --- Проверки ---------------------------------------------------------------

scenario: check-env ## Проверка цепочки в настоящем MAX (система должна быть запущена)
	@set -a && . ./.env && set +a && CORE_URL=http://localhost:$${CORE_PORT:-8090} bash backend/scripts/scenario.sh

e2e: ## Автотест «Core Backend ↔ бот» без MAX и без Docker
	cd backend && bash scripts/e2e-with-bot.sh

test: ## Юнит-тесты бота и Core Backend
	cd bot && go test ./...
	cd backend && go test ./...
