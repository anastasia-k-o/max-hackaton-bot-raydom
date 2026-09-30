#!/usr/bin/env bash
#
# Сквозная проверка «Core Backend ↔ бот» без MAX и без токена.
#
# Поднимает рядом два процесса из этого репозитория:
#   бот   — MAX_MODE=mock (сообщения пишутся в память), CORE_MODE=http;
#   core  — DEV_MODE=true, уведомления уходят в этого бота.
# Затем проходит сценарий и проверяет, что сообщения доходят до «MAX», а
# нажатия кнопок возвращаются в Core Backend.
#
# Запуск из backend/:   bash scripts/e2e-with-bot.sh
# Нужны: go, curl, jq.

set -uo pipefail

HERE="$(cd "$(dirname "$0")/.." && pwd)"
BOT_DIR="$HERE/../bot"
BOT_PORT="${BOT_PORT:-18080}"
CORE_PORT="${CORE_PORT:-18090}"
BOT="http://127.0.0.1:${BOT_PORT}"
CORE="http://127.0.0.1:${CORE_PORT}"
INTERNAL_KEY="e2e-internal-key"
BOT_KEY="e2e-bot-key"
WEBHOOK_SECRET="e2e-webhook-secret"
MAX_ID=424242

GREEN=$'\033[32m'; RED=$'\033[31m'; BOLD=$'\033[1m'; RESET=$'\033[0m'
PASSED=0; FAILED=0
pass() { echo "  ${GREEN}✓${RESET} $*"; PASSED=$((PASSED + 1)); }
fail() { echo "  ${RED}✗${RESET} $*"; FAILED=$((FAILED + 1)); }
step() { echo; echo "${BOLD}$*${RESET}"; }

for tool in go curl jq; do
  command -v "$tool" >/dev/null || { echo "нужен $tool"; exit 1; }
done
[[ -d "$BOT_DIR/cmd/bot" ]] || { echo "не найден бот в $BOT_DIR"; exit 1; }

WORK="$(mktemp -d)"
cleanup() {
  [[ -n "${BOT_PID:-}" ]] && kill "$BOT_PID" 2>/dev/null
  [[ -n "${CORE_PID:-}" ]] && kill "$CORE_PID" 2>/dev/null
  wait 2>/dev/null
  rm -rf "$WORK"
}
trap cleanup EXIT

step "Сборка"
(cd "$BOT_DIR" && go build -o "$WORK/bot" ./cmd/bot) || { echo "бот не собрался"; exit 1; }
(cd "$HERE" && go build -o "$WORK/core" ./cmd/core) || { echo "core не собрался"; exit 1; }
pass "бот и core собраны"

step "Запуск"
APP_ENV=dev HTTP_PORT="$BOT_PORT" LOG_LEVEL=warn MAX_MODE=mock MAX_UPDATES_MODE=webhook \
  MAX_WEBHOOK_SECRET="$WEBHOOK_SECRET" INTERNAL_API_KEY="$INTERNAL_KEY" \
  CORE_MODE=http CORE_BASE_URL="$CORE" CORE_API_KEY="$BOT_KEY" \
  MINI_APP_URL="https://afisha.test/app" \
  "$WORK/bot" >"$WORK/bot.log" 2>&1 &
BOT_PID=$!
HTTP_ADDR="127.0.0.1:${CORE_PORT}" DB_PATH="$WORK/core.db" DEV_MODE=true \
  BOT_BASE_URL="$BOT" BOT_INTERNAL_API_KEY="$INTERNAL_KEY" BOT_API_KEY="$BOT_KEY" \
  MINI_APP_URL="https://afisha.test/app" SCHEDULER_INTERVAL=1h \
  "$WORK/core" >"$WORK/core.log" 2>&1 &
CORE_PID=$!

wait_up() {
  for _ in $(seq 1 100); do
    curl -fsS "$1/health" >/dev/null 2>&1 && return 0
    sleep 0.1
  done
  return 1
}
wait_up "$BOT" && pass "бот отвечает на :$BOT_PORT" || { fail "бот не поднялся"; cat "$WORK/bot.log"; exit 1; }
wait_up "$CORE" && pass "core отвечает на :$CORE_PORT" || { fail "core не поднялся"; cat "$WORK/core.log"; exit 1; }

post() { curl -sS -X POST "$CORE$1" -H 'Content-Type: application/json' "${@:3}" -d "$2"; }
auth() { echo "Authorization: Bearer $1"; }
messages() { curl -sS "$BOT/dev/max/messages"; }
# texts of messages sent to our user, newest last
texts() { messages | jq -r '[.messages[] | select(.kind == "sent")] | .[].text'; }
count_sends() { messages | jq '[.messages[] | select(.kind == "sent")] | length'; }

press() { # payload [max_user_id]
  local who="${2:-$MAX_ID}"
  curl -sS -X POST "$BOT/webhooks/max" -H 'Content-Type: application/json' \
    -H "X-Max-Bot-Api-Secret: $WEBHOOK_SECRET" -d "{
      \"update_type\": \"message_callback\", \"timestamp\": $(date +%s)000, \"chat_id\": 555,
      \"callback\": {\"callback_id\": \"cb-$RANDOM\", \"payload\": \"$1\",
                     \"user\": {\"user_id\": $who, \"first_name\": \"Тест\", \"name\": \"Тест\"}},
      \"message\": {\"sender\": {\"user_id\": 1, \"is_bot\": true},
                    \"recipient\": {\"chat_id\": 555, \"user_id\": $who, \"chat_type\": \"dialog\"},
                    \"body\": {\"mid\": \"mid-$RANDOM\", \"seq\": 1, \"text\": \"...\"}}
    }"
}

step "1. Запись → «Вы записаны» в MAX"
ORG=$(post /dev/login '{"first_name":"Организатор"}' | jq -r .token)
ANNA=$(post /dev/login "{\"max_user_id\": $MAX_ID, \"first_name\": \"Анна\", \"is_author\": false}" | jq -r .token)
STARTS=$(TZ=Europe/Moscow date -d '+26 hours' +%Y-%m-%dT%H:%M:00+03:00)
EVENT=$(post /events "{\"title\":\"Настолки по пятницам\",\"description\":\"Играем\",\"category_id\":\"games\",
  \"tag_ids\":[\"t_boardgames\"],\"starts_at\":\"$STARTS\",\"duration_min\":120,\"city_id\":\"msk\",
  \"district\":\"Басманный\",\"address\":\"Покровка, 17\",\"capacity\":1}" -H "$(auth "$ORG")" | jq -r .id)
[[ "$EVENT" == event_* ]] && pass "событие $EVENT создано" || fail "событие не создано"
REG=$(post /registrations "{\"event_id\":\"$EVENT\"}" -H "$(auth "$ANNA")" | jq -r .id)
[[ "$REG" == registration_* ]] && pass "запись $REG" || fail "запись не создана"
sleep 1
if texts | grep -q 'Вы записаны'; then pass "бот отправил «Вы записаны»"; else fail "нет «Вы записаны»: $(texts)"; fi
if messages | jq -e --arg p "v1|cancel|$REG|$EVENT" '[.messages[] | .. | strings | select(. == $p)] | length > 0' >/dev/null; then
  pass "кнопка отмены несёт registration_id из Core Backend"
else fail "нет кнопки отмены с $REG"; fi

step "2. Часы вперёд: напоминание за сутки"
out=$(post /dev/clock '{"advance":"2h1m"}')
jq -e '.scheduler.queued | map(startswith("reminder_24h")) | any' <<<"$out" >/dev/null \
  && pass "планировщик поставил reminder_24h" || fail "reminder_24h не поставлен: $out"
[[ $(jq .delivery.sent <<<"$out") -ge 1 ]] && pass "доставлено боту" || fail "не доставлено: $out"

step "3. За 6 часов: «Подтвердите участие», нажатие «Приду» через бота"
out=$(post /dev/clock '{"advance":"18h"}')
jq -e '.scheduler.queued | map(startswith("confirmation_required")) | any' <<<"$out" >/dev/null \
  && pass "поставлен confirmation_required" || fail "нет confirmation_required: $out"
texts | grep -q 'Подтвердите участие' && pass "сообщение с кнопками в MAX" || fail "нет сообщения: $(texts)"
press "v1|confirm|$REG|$EVENT" >/dev/null
status=$(curl -sS "$CORE/events/$EVENT" -H "$(auth "$ANNA")" | jq -r .my_registration.status)
[[ "$status" == confirmed ]] && pass "Core Backend получил нажатие: запись confirmed" || fail "статус $status"
grep -q 'X-Api-Key\|unauthorized' "$WORK/core.log" && fail "core отверг ключ бота" || pass "бот прошёл по X-Api-Key"

step "4. Лист ожидания: освободилось место → «Занять место»"
BORIS_ID=$((MAX_ID + 1))
BORIS=$(post /dev/login "{\"max_user_id\": $BORIS_ID, \"first_name\": \"Борис\", \"is_author\": false}" | jq -r .token)
WL=$(post /registrations "{\"event_id\":\"$EVENT\"}" -H "$(auth "$BORIS")" | jq -r .status)
[[ "$WL" == waitlist ]] && pass "Борис в листе ожидания" || fail "Борис: $WL"
before=$(count_sends)
post "/registrations/$REG/cancel" '{"cancel_reason":"ill"}' -H "$(auth "$ANNA")" >/dev/null
sleep 1
offer=$(messages | jq -r --argjson id "$BORIS_ID" '[.messages[] | select(.kind == "sent" and .user_id == $id)] | last | .text // empty')
if grep -q 'Освободилось место' <<<"$offer"; then pass "Борису ушло «Освободилось место»"; else fail "нет предложения Борису: $(texts | tail -n 3)"; fi
[[ $(count_sends) -gt $before ]] || fail "новых сообщений нет"
payload=$(messages | jq -r --argjson id "$BORIS_ID" '[.messages[] | select(.kind == "sent" and .user_id == $id)] | last | .keyboard[][] | .payload // empty' | grep -m1 'accept')
[[ -n "$payload" ]] && pass "кнопка «Занять место»: $payload" || fail "нет кнопки принятия"
press "$payload" "$BORIS_ID" >/dev/null
status=$(curl -sS "$CORE/events/$EVENT" -H "$(auth "$BORIS")" | jq -r .my_registration.status)
[[ "$status" == registered || "$status" == confirmed ]] && pass "нажатие дошло: место Бориса ($status)" || fail "статус Бориса $status"

step "5. Статус диалога"
curl -sS -X POST "$BOT/webhooks/max" -H 'Content-Type: application/json' -H "X-Max-Bot-Api-Secret: $WEBHOOK_SECRET" \
  -d "{\"update_type\":\"bot_stopped\",\"timestamp\":$(date +%s)000,\"chat_id\":555,\"user\":{\"user_id\":$BORIS_ID,\"name\":\"Борис\"}}" >/dev/null
sleep 0.5
if grep -q 'bot status.*available=false' "$WORK/core.log"; then pass "bot_stopped дошёл до Core Backend"; else fail "статус не пришёл"; fi

step "6. Отмена мероприятия"
post "/events/$EVENT/cancel" '{"organizer_message":"Заболел ведущий"}' -H "$(auth "$ORG")" >/dev/null
sleep 1
failed=$(curl -sS "$CORE/dev/state" | jq '[.notifications[] | select(.status == "failed")] | length')
[[ "$failed" == 0 ]] && pass "ни одного отказа бота (контракт совпадает)" || fail "отказов: $failed — $(curl -sS "$CORE/dev/state" | jq -c '[.notifications[] | select(.status=="failed") | {type,last_error}]')"

echo
echo "${BOLD}Итог:${RESET} ${GREEN}${PASSED} ✓${RESET}  ${RED}${FAILED} ✗${RESET}"
if [[ $FAILED -gt 0 ]]; then
  echo; echo "Лог бота: "; tail -n 20 "$WORK/bot.log"; echo; echo "Лог core:"; tail -n 30 "$WORK/core.log"
  exit 1
fi
