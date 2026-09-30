#!/usr/bin/env bash
#
# Дымовой тест полного локального цикла.
#
# Поднимает бота в mock-режиме на свободном порту и проходит ровно тот же
# путь, что и Postman:
#
#   1. POST /api/v1/notifications  — уведомление отправлено
#   2. GET  /dev/max/messages      — MockMAX записал сообщение с кнопками
#   3. POST /webhooks/max          — имитация нажатия кнопки «Приду»
#   4. GET  /dev/core/actions      — StubCore получил confirm_registration
#   5. GET  /dev/max/messages      — сообщение заменено, кнопок действий нет
#
# Ни токена MAX, ни Core Backend, ни сети не требуется.
#
# Запуск:  make smoke

set -euo pipefail

PORT="${SMOKE_PORT:-18099}"
BASE="http://127.0.0.1:${PORT}"
API_KEY="smoke-internal-key"
WEBHOOK_SECRET="smoke-webhook-secret"
REGISTRATION_ID="registration_15"
EVENT_ID="event_42"
USER_ID="123456789"

RED=$'\033[31m'; GREEN=$'\033[32m'; DIM=$'\033[2m'; RESET=$'\033[0m'

failures=0
step=0

pass() { printf '  %s✓%s %s\n' "$GREEN" "$RESET" "$1"; }
fail() { printf '  %s✗%s %s\n' "$RED" "$RESET" "$1"; failures=$((failures + 1)); }
head_step() { step=$((step + 1)); printf '\n%s. %s\n' "$step" "$1"; }

cleanup() {
  if [[ -n "${BOT_PID:-}" ]] && kill -0 "$BOT_PID" 2>/dev/null; then
    kill "$BOT_PID" 2>/dev/null || true
    wait "$BOT_PID" 2>/dev/null || true
  fi
  rm -f "${BOT_LOG:-}" "${BOT_BIN:-}"
}
trap cleanup EXIT

command -v curl >/dev/null || { echo "нужен curl"; exit 1; }

# jq упрощает проверки, но не обязателен: без него используется grep.
HAVE_JQ=0
command -v jq >/dev/null && HAVE_JQ=1

echo "Сборка..."
BOT_BIN="$(mktemp -t maxbot-smoke.XXXXXX)"
go build -o "$BOT_BIN" ./cmd/bot

BOT_LOG="$(mktemp -t maxbot-smoke-log.XXXXXX)"
APP_ENV=dev \
HTTP_PORT="$PORT" \
LOG_LEVEL=warn \
MAX_MODE=mock \
CORE_MODE=stub \
INTERNAL_API_KEY="$API_KEY" \
MAX_WEBHOOK_SECRET="$WEBHOOK_SECRET" \
MINI_APP_URL="https://afisha.example.ru/app" \
  "$BOT_BIN" >"$BOT_LOG" 2>&1 &
BOT_PID=$!

echo "Ожидание запуска на порту ${PORT}..."
for _ in $(seq 1 50); do
  if curl -fsS "${BASE}/health" >/dev/null 2>&1; then break; fi
  if ! kill -0 "$BOT_PID" 2>/dev/null; then
    echo "${RED}Бот не запустился:${RESET}"; cat "$BOT_LOG"; exit 1
  fi
  sleep 0.2
done

# --- 1. Пробы -------------------------------------------------------------

head_step "Пробы состояния"

code=$(curl -s -o /dev/null -w '%{http_code}' "${BASE}/health")
[[ "$code" == "200" ]] && pass "GET /health → 200" || fail "GET /health → $code"

code=$(curl -s -o /dev/null -w '%{http_code}' "${BASE}/ready")
[[ "$code" == "200" ]] && pass "GET /ready → 200 (mock/stub)" || fail "GET /ready → $code"

# --- 2. Защита API --------------------------------------------------------

head_step "Защита integration API"

code=$(curl -s -o /dev/null -w '%{http_code}' -X POST "${BASE}/api/v1/notifications" \
  -H 'Content-Type: application/json' -d '{}')
[[ "$code" == "401" ]] && pass "без X-Internal-Api-Key → 401" || fail "без ключа → $code (ожидалось 401)"

code=$(curl -s -o /dev/null -w '%{http_code}' -X POST "${BASE}/webhooks/max" \
  -H 'Content-Type: application/json' -H 'X-Max-Bot-Api-Secret: wrong' -d '{"update_type":"bot_started"}')
[[ "$code" == "401" ]] && pass "неверный webhook secret → 401" || fail "неверный секрет → $code (ожидалось 401)"

# --- 3. Уведомление -------------------------------------------------------

head_step "Отправка уведомления (сценарий A)"

response=$(curl -s -X POST "${BASE}/api/v1/notifications" \
  -H 'Content-Type: application/json' \
  -H "X-Internal-Api-Key: ${API_KEY}" \
  -d "{
    \"request_id\": \"smoke_req_1\",
    \"type\": \"confirmation_required\",
    \"recipient\": {\"max_user_id\": ${USER_ID}},
    \"event\": {
      \"id\": \"${EVENT_ID}\",
      \"title\": \"Йога в парке\",
      \"starts_at\": \"2026-09-22T19:00:00+03:00\",
      \"address\": \"Парк Горького\",
      \"mini_app_url\": \"https://afisha.example.ru/app/${EVENT_ID}\"
    },
    \"registration\": {\"id\": \"${REGISTRATION_ID}\"}
  }")

if grep -q '"status":"sent"' <<<"$response"; then
  pass "POST /api/v1/notifications → sent"
else
  fail "POST /api/v1/notifications → $response"
fi

# --- 4. MockMAX -----------------------------------------------------------

head_step "Проверка MockMAX"

messages=$(curl -s "${BASE}/dev/max/messages")

if grep -q 'Подтвердите участие' <<<"$messages"; then
  pass "MockMAX записал сообщение «Подтвердите участие»"
else
  fail "сообщение не найдено: $messages"
fi

if grep -q "v1|confirm|${REGISTRATION_ID}|${EVENT_ID}" <<<"$messages"; then
  pass "кнопка несёт payload v1|confirm|${REGISTRATION_ID}|${EVENT_ID}"
else
  fail "payload кнопки не найден"
fi

if grep -q '"text":"Приду"' <<<"$messages"; then
  pass "подпись кнопки «Приду» отделена от команды"
else
  fail "кнопка «Приду» не найдена"
fi

# --- 5. Callback ----------------------------------------------------------

head_step "Нажатие кнопки (сценарий B)"

response=$(curl -s -X POST "${BASE}/webhooks/max" \
  -H 'Content-Type: application/json' \
  -H "X-Max-Bot-Api-Secret: ${WEBHOOK_SECRET}" \
  -d "{
    \"update_type\": \"message_callback\",
    \"timestamp\": $(date +%s)000,
    \"chat_id\": 555,
    \"callback\": {
      \"callback_id\": \"smoke-cb-1\",
      \"payload\": \"v1|confirm|${REGISTRATION_ID}|${EVENT_ID}\",
      \"user\": {\"user_id\": ${USER_ID}, \"first_name\": \"Алексей\", \"name\": \"Алексей\"}
    },
    \"message\": {
      \"sender\": {\"user_id\": 1, \"is_bot\": true},
      \"recipient\": {\"chat_id\": 555, \"user_id\": ${USER_ID}, \"chat_type\": \"dialog\"},
      \"body\": {\"mid\": \"mock-mid-1\", \"seq\": 1, \"text\": \"Подтвердите участие\"}
    }
  }")

if grep -q '"result":"ok"' <<<"$response"; then
  pass "webhook обработан"
else
  fail "webhook → $response"
fi

# --- 6. StubCore ----------------------------------------------------------

head_step "Проверка StubCoreGateway"

actions=$(curl -s "${BASE}/dev/core/actions")

if grep -q '"kind":"confirm_registration"' <<<"$actions"; then
  pass "StubCore получил confirm_registration"
else
  fail "действие не зафиксировано: $actions"
fi

if grep -q "\"registration_id\":\"${REGISTRATION_ID}\"" <<<"$actions"; then
  pass "передан registration_id=${REGISTRATION_ID}"
else
  fail "registration_id не передан"
fi

if grep -q "\"max_user_id\":${USER_ID}" <<<"$actions"; then
  pass "передан max_user_id=${USER_ID} (из webhook, не из payload)"
else
  fail "max_user_id не передан"
fi

# --- 7. Обновление сообщения ---------------------------------------------

head_step "Обновление сообщения в MAX"

messages=$(curl -s "${BASE}/dev/max/messages")

if grep -q 'Участие подтверждено' <<<"$messages"; then
  pass "сообщение заменено на «✅ Участие подтверждено»"
else
  fail "обновлённое сообщение не найдено"
fi

if [[ "$HAVE_JQ" == "1" ]]; then
  stale=$(jq '[.messages[] | select(.kind == "callback_answer")
               | .keyboard // [] | flatten | .[]
               | select(.kind == "callback")] | length' <<<"$messages")
  if [[ "$stale" == "0" ]]; then
    pass "в обновлённом сообщении не осталось кнопок действий"
  else
    fail "в обновлённом сообщении осталось кнопок действий: ${stale}"
  fi
else
  printf '  %s· jq не установлен — проверка отсутствия старых кнопок пропущена%s\n' "$DIM" "$RESET"
fi

# --- 8. Устойчивость ------------------------------------------------------

head_step "Устойчивость к некорректным данным"

code=$(curl -s -o /dev/null -w '%{http_code}' -X POST "${BASE}/webhooks/max" \
  -H 'Content-Type: application/json' -H "X-Max-Bot-Api-Secret: ${WEBHOOK_SECRET}" \
  -d '{"update_type":"message_callback","callback":{"callback_id":"smoke-cb-2","payload":"Приду"}}')
[[ "$code" == "200" ]] && pass "текст кнопки как payload → 200, без паники" || fail "→ $code"

code=$(curl -s -o /dev/null -w '%{http_code}' -X POST "${BASE}/webhooks/max" \
  -H 'Content-Type: application/json' -H "X-Max-Bot-Api-Secret: ${WEBHOOK_SECRET}" \
  -d '{"update_type":"invented_in_2027"}')
[[ "$code" == "200" ]] && pass "неизвестный тип события → 200, подтверждён и отброшен" || fail "→ $code"

code=$(curl -s -o /dev/null -w '%{http_code}' -X POST "${BASE}/api/v1/notifications" \
  -H 'Content-Type: application/json' -H "X-Internal-Api-Key: ${API_KEY}" \
  -d '{"request_id":"x","type":"send_fireworks","recipient":{"max_user_id":1},"event":{"id":"e","title":"t","starts_at":"2026-09-22T19:00:00+03:00"}}')
[[ "$code" == "400" ]] && pass "неизвестный тип уведомления → 400" || fail "→ $code"

# --- итог ----------------------------------------------------------------

echo
if [[ "$failures" -eq 0 ]]; then
  printf '%sВсе проверки пройдены.%s\n\n' "$GREEN" "$RESET"
  exit 0
fi
printf '%sПровалено проверок: %d%s\n' "$RED" "$failures" "$RESET"
echo "Логи бота:"
cat "$BOT_LOG"
exit 1
