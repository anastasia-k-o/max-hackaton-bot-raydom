#!/usr/bin/env bash
#
# Дымовой тест режима long polling — без токена и без интернета.
#
# scripts/smoke.sh проверяет полный цикл через webhook: событие подаётся в бота
# HTTP-запросом. Здесь проверяется противоположное направление — то, которое
# webhook не задействует вовсе: бот сам ходит за событиями.
#
# Что происходит:
#
#   1. поднимается подменный MAX на localhost (Python, без зависимостей);
#   2. бот запускается с MAX_MODE=real и MAX_UPDATES_MODE=polling, но
#      MAX_BASE_URL указывает на подменный сервер — настоящий MAX не трогается;
#   3. подменный MAX отдаёт на GET /updates пачку: «нажата кнопка» и
#      «пользователь остановил бота»;
#   4. проверяется, что бот его забрал, разобрал, дошёл до StubCore и ответил
#      на callback; что marker продвинулся; что POST /webhooks/max в этом
#      режиме отдаёт 404.
#
# Проверяется именно связка в cmd/bot/main.go — единственная часть, которую
# модульные тесты не покрывают.
#
# Запуск:  bash scripts/smoke-polling.sh

set -euo pipefail

cd "$(dirname "$0")/.."

PORT="${SMOKE_PORT:-18081}"
FAKE_MAX_PORT="${SMOKE_FAKE_MAX_PORT:-18099}"
API_KEY="smoke-internal-key"

GREEN=$'\033[32m'; RED=$'\033[31m'; DIM=$'\033[2m'; BOLD=$'\033[1m'; RESET=$'\033[0m'

PASSED=0
FAILED=0

ok()   { echo "  ${GREEN}✓${RESET} $1"; PASSED=$((PASSED + 1)); }
fail() { echo "  ${RED}✗${RESET} $1"; FAILED=$((FAILED + 1)); }

command -v curl   >/dev/null || { echo "нужен curl"; exit 1; }
command -v jq     >/dev/null || { echo "нужен jq"; exit 1; }
command -v python3 >/dev/null || { echo "нужен python3"; exit 1; }
command -v go     >/dev/null || { echo "нужен go"; exit 1; }

WORK_DIR="$(mktemp -d)"
FAKE_MAX_PID=""
BOT_PID=""

cleanup() {
  [[ -n "$BOT_PID" ]]      && kill "$BOT_PID"      2>/dev/null || true
  [[ -n "$FAKE_MAX_PID" ]] && kill "$FAKE_MAX_PID" 2>/dev/null || true
  wait 2>/dev/null || true
  rm -rf "$WORK_DIR"
}
trap cleanup EXIT

# --- подменный MAX --------------------------------------------------------
#
# Отдаёт ровно те четыре ручки, которые бот вызывает в этом сценарии, и ведёт
# журнал вызовов, чтобы проверки могли на него опереться.

cat > "$WORK_DIR/fake_max.py" <<'PYTHON'
import json, sys, threading
from http.server import BaseHTTPRequestHandler, HTTPServer
from urllib.parse import urlparse, parse_qs

PORT = int(sys.argv[1])
LOG = sys.argv[2]

lock = threading.Lock()
state = {"updates_calls": 0, "markers": [], "answers": [], "messages": []}

# Два события в одной пачке: нажата кнопка «Приду», а другой пользователь
# остановил бота.
STOPPED = {
    "update_type": "bot_stopped",
    "timestamp": 1758556801000,
    "chat_id": 777,
    "user": {"user_id": 987654321, "name": "Ушедший пользователь"},
}

UPDATE = {
    "update_type": "message_callback",
    "timestamp": 1758556800000,
    "chat_id": 555,
    "message": {
        "recipient": {"chat_id": 555, "user_id": 123456789, "chat_type": "dialog"},
        "body": {"mid": "mid-smoke-1", "text": "Подтвердите участие"},
    },
    "callback": {
        "timestamp": 1758556800000,
        "callback_id": "cb-smoke-1",
        "payload": "v1|confirm|registration_15|event_42",
        "user": {"user_id": 123456789, "name": "Тестовый пользователь"},
    },
}


def dump():
    with open(LOG, "w") as handle:
        json.dump(state, handle)


class Handler(BaseHTTPRequestHandler):
    def log_message(self, *args):
        pass

    def send_json(self, payload, status=200):
        body = json.dumps(payload).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_GET(self):
        url = urlparse(self.path)
        query = parse_qs(url.query)

        if url.path == "/me":
            return self.send_json({
                "user_id": 425847267,
                "first_name": "Подменный бот",
                "username": "fake_bot",
                "is_bot": True,
            })

        if url.path == "/subscriptions":
            # Подписок нет — именно то состояние, при котором polling применим.
            return self.send_json({"subscriptions": []})

        if url.path == "/updates":
            with lock:
                state["updates_calls"] += 1
                state["markers"].append(query.get("marker", [None])[0])
                first = state["updates_calls"] == 1
                dump()
            if first:
                return self.send_json({"updates": [UPDATE, STOPPED], "marker": 2})
            # Дальше пусто. Настоящий MAX держал бы соединение, но здесь
            # задача обратная: не задерживать тест.
            return self.send_json({"updates": [], "marker": 2})

        return self.send_json({"code": "not.found"}, status=404)

    def do_POST(self):
        url = urlparse(self.path)
        length = int(self.headers.get("Content-Length") or 0)
        raw = self.rfile.read(length) if length else b"{}"
        try:
            body = json.loads(raw)
        except ValueError:
            body = {"_unparsed": raw.decode("utf-8", "replace")}

        with lock:
            if url.path == "/answers":
                state["answers"].append(body)
            elif url.path == "/messages":
                state["messages"].append(body)
            dump()

        return self.send_json({"success": True})


dump()
HTTPServer(("127.0.0.1", PORT), Handler).serve_forever()
PYTHON

LOG_FILE="$WORK_DIR/fake_max.json"

echo
echo "${BOLD}Дымовой тест: long polling${RESET}"
echo "${DIM}подменный MAX на :${FAKE_MAX_PORT}, бот на :${PORT}${RESET}"
echo

python3 "$WORK_DIR/fake_max.py" "$FAKE_MAX_PORT" "$LOG_FILE" &
FAKE_MAX_PID=$!

for _ in {1..40}; do
  curl -sf "http://127.0.0.1:${FAKE_MAX_PORT}/me" -o /dev/null && break
  sleep 0.25
done

echo "1. Старт бота в режиме polling"

# Секрет webhook намеренно НЕ задан: в этом режиме он не требуется, и старт
# без него — часть проверяемого поведения.
# Собираем бинарник, а не запускаем через `go run`: go run не передаёт SIGTERM
# дочернему процессу, и проверка штатной остановки проверяла бы не бота, а
# обёртку.
go build -o "$WORK_DIR/bot" ./cmd/bot

env -i PATH="$PATH" HOME="$HOME" \
  APP_ENV=dev \
  HTTP_PORT="$PORT" \
  LOG_LEVEL=debug \
  MAX_MODE=real \
  MAX_UPDATES_MODE=polling \
  MAX_BOT_TOKEN=fake-token-for-smoke \
  MAX_BASE_URL="http://127.0.0.1:${FAKE_MAX_PORT}" \
  MAX_POLL_TIMEOUT=1 \
  CORE_MODE=stub \
  INTERNAL_API_KEY="$API_KEY" \
  "$WORK_DIR/bot" > "$WORK_DIR/bot.log" 2>&1 &
BOT_PID=$!

READY=0
for _ in {1..120}; do
  if curl -sf "http://127.0.0.1:${PORT}/health" -o /dev/null; then READY=1; break; fi
  if ! kill -0 "$BOT_PID" 2>/dev/null; then break; fi
  sleep 0.5
done

if [[ "$READY" != "1" ]]; then
  fail "бот не поднялся"
  echo
  echo "${DIM}--- лог бота ---${RESET}"
  cat "$WORK_DIR/bot.log"
  exit 1
fi
ok "бот стартовал без MAX_WEBHOOK_SECRET (в polling он не нужен)"

if grep -q "режим получения событий: polling" "$WORK_DIR/bot.log"; then
  ok "в логе объявлен режим polling"
else
  fail "в логе нет объявления режима polling"
fi

if grep -q "подписок на webhook нет" "$WORK_DIR/bot.log"; then
  ok "подписки проверены при старте"
else
  fail "проверка подписок при старте не выполнена"
fi

echo
echo "2. /ready сообщает режим"

READY_BODY="$(curl -s "http://127.0.0.1:${PORT}/ready")"
if [[ "$(jq -r '.updates_mode' <<<"$READY_BODY")" == "polling" ]]; then
  ok "updates_mode = polling"
else
  fail "updates_mode = $(jq -r '.updates_mode' <<<"$READY_BODY")"
fi

echo
echo "3. Маршрут webhook в этом режиме не поднят"

WEBHOOK_CODE="$(curl -s -o /dev/null -w '%{http_code}' -X POST \
  "http://127.0.0.1:${PORT}/webhooks/max" -d '{"update_type":"bot_started"}')"
if [[ "$WEBHOOK_CODE" == "404" ]]; then
  ok "POST /webhooks/max → 404 (проверять подпись было бы нечем)"
else
  fail "POST /webhooks/max → ${WEBHOOK_CODE}, ожидался 404"
fi

echo
echo "4. Бот сам забрал событие"

ACTIONS=""
for _ in {1..40}; do
  ACTIONS="$(curl -s "http://127.0.0.1:${PORT}/dev/core/actions")"
  [[ "$(jq -r '.count // 0' <<<"$ACTIONS")" -ge 1 ]] && break
  sleep 0.25
done

if [[ "$(jq -r '.count // 0' <<<"$ACTIONS")" -ge 1 ]]; then
  ok "StubCore получил действие, пришедшее через опрос"
else
  fail "действие не дошло до StubCore"
  echo "${DIM}--- лог бота ---${RESET}"
  tail -30 "$WORK_DIR/bot.log"
fi

if [[ "$(jq -r '.actions[0].kind' <<<"$ACTIONS")" == "confirm_registration" ]]; then
  ok "действие разобрано как confirm_registration"
else
  fail "kind = $(jq -r '.actions[0].kind' <<<"$ACTIONS")"
fi

if [[ "$(jq -r '.actions[0].registration_id' <<<"$ACTIONS")" == "registration_15" ]]; then
  ok "передан registration_id=registration_15"
else
  fail "registration_id = $(jq -r '.actions[0].registration_id' <<<"$ACTIONS")"
fi

if [[ "$(jq -r '.actions[0].max_user_id' <<<"$ACTIONS")" == "123456789" ]]; then
  ok "передан max_user_id=123456789 (из тела события, не из payload)"
else
  fail "max_user_id = $(jq -r '.actions[0].max_user_id' <<<"$ACTIONS")"
fi

echo
echo "5. Ответ на нажатие ушёл обратно в MAX"

for _ in {1..20}; do
  [[ "$(jq -r '.answers | length' "$LOG_FILE")" -ge 1 ]] && break
  sleep 0.25
done

if [[ "$(jq -r '.answers | length' "$LOG_FILE")" -ge 1 ]]; then
  ok "подменный MAX получил POST /answers"
else
  fail "ответ на callback не отправлен — у пользователя крутился бы спиннер"
fi

if jq -e '.answers[0].message.text | test("подтверждено")' "$LOG_FILE" >/dev/null 2>&1; then
  ok "сообщение заменено на подтверждение"
else
  fail "в ответе нет заменяющего сообщения"
fi

if jq -e '[.answers[0].message.attachments // [] | .[] | select(.type == "inline_keyboard")] | length == 0' \
     "$LOG_FILE" >/dev/null 2>&1; then
  ok "в заменяющем сообщении не осталось кнопок действий"
else
  fail "кнопки действий остались — их можно нажать повторно"
fi

echo
echo "6. Статус диалога уходит в Core Backend"

# bot_stopped из той же пачки: бот больше не может писать этому пользователю,
# и Core Backend должен об этом узнать (во фронте это bot_available=false).
STATUS=""
for _ in {1..20}; do
  STATUS="$(curl -s "http://127.0.0.1:${PORT}/dev/core/actions" | jq -c '[.actions[] | select(.kind == "bot_status")][0] // empty')"
  [[ -n "$STATUS" ]] && break
  sleep 0.25
done

if [[ -n "$STATUS" ]] && jq -e '.available == false and .reason == "bot_stopped" and .max_user_id == 987654321' <<<"$STATUS" >/dev/null; then
  ok "bot_stopped → в Core ушло available=false для max_user_id=987654321"
else
  fail "статус диалога не дошёл до Core: ${STATUS:-нет записи bot_status}"
fi

echo
echo "7. Marker продвигается"

# Первый запрос идёт без marker: по схеме MAX это «всё с последней фиксации».
# Следующие должны нести marker=2 — тем самым фиксируя обработанные события.
if [[ "$(jq -r '.markers[0] // "null"' "$LOG_FILE")" == "null" ]]; then
  ok "первый опрос без marker (не marker=0)"
else
  fail "первый опрос нёс marker=$(jq -r '.markers[0]' "$LOG_FILE")"
fi

if jq -e '[.markers[1:][] | select(. == "2")] | length >= 1' "$LOG_FILE" >/dev/null 2>&1; then
  ok "последующие опросы несут marker=2 (пачка зафиксирована)"
else
  fail "marker не продвинулся: $(jq -c '.markers' "$LOG_FILE")"
fi

echo
echo "8. Остановка"

kill -TERM "$BOT_PID" 2>/dev/null || true
STOPPED=0
for _ in {1..40}; do
  if ! kill -0 "$BOT_PID" 2>/dev/null; then STOPPED=1; break; fi
  sleep 0.25
done
BOT_PID=""

if [[ "$STOPPED" == "1" ]]; then
  ok "процесс завершился по SIGTERM, не дожидаясь конца опроса"
else
  fail "процесс не завершился за 10 секунд"
fi

if grep -q "polling stopped" "$WORK_DIR/bot.log"; then
  ok "цикл опроса остановлен штатно"
else
  fail "в логе нет штатной остановки опроса"
fi

echo
if [[ "$FAILED" -gt 0 ]]; then
  echo "${RED}Провалено проверок: ${FAILED}${RESET} (пройдено: ${PASSED})"
  echo
  echo "${DIM}--- лог бота ---${RESET}"
  cat "$WORK_DIR/bot.log"
  exit 1
fi

echo "${GREEN}Все проверки пройдены${RESET} (${PASSED})."
echo "${DIM}Настоящий MAX в этом тесте не вызывался.${RESET}"
echo
