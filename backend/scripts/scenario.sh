#!/usr/bin/env bash
#
# Проверка всей цепочки в настоящем MAX, в одиночку и без ожидания.
#
# Вы одновременно организатор (им играет скрипт) и участник (вы в MAX).
# Скрипт создаёт мероприятия, записывает вас, двигает часы Core Backend
# вперёд — и напоминания, которые пришли бы завтра, приходят сейчас.
# Там, где нужно нажать кнопку в MAX, скрипт ждёт Enter.
#
# Что должно быть запущено: вся система (make up в корне репозитория) с
# MAX_MODE=real и DEV_MODE=true в .env.
#
# Запуск:  make scenario   (из корня)
#     или  bash scripts/scenario.sh [ваш max_user_id]
# Без аргумента id берётся из DEMO_MAX_USER_ID в общем .env (make scenario).

set -uo pipefail

CORE="${CORE_URL:-http://localhost:8090}"
HERE="$(cd "$(dirname "$0")/.." && pwd)"

GREEN=$'\033[32m'; RED=$'\033[31m'; YELLOW=$'\033[33m'; BOLD=$'\033[1m'; DIM=$'\033[2m'; RESET=$'\033[0m'
ok()   { echo "  ${GREEN}✓${RESET} $*"; }
bad()  { echo "  ${RED}✗${RESET} $*"; }
step() { echo; echo "${BOLD}$*${RESET}"; }
look() { echo "  ${YELLOW}→ в MAX:${RESET} $*"; }
pause() { read -r -p "  ${DIM}$* — Enter, чтобы продолжить${RESET} " _; }

for tool in curl jq; do command -v "$tool" >/dev/null || { echo "нужен $tool"; exit 1; }; done

MAX_ID="${1:-${DEMO_MAX_USER_ID:-}}"
for f in "$HERE/../.env" "$HERE/../bot/.env"; do
  [[ -z "$MAX_ID" && -f "$f" ]] || continue
  MAX_ID="$(grep -E '^DEMO_MAX_USER_ID=' "$f" | tail -n1 | cut -d= -f2 | tr -d '"'"'"' ')"
done
if ! [[ "$MAX_ID" =~ ^[0-9]+$ ]]; then
  echo "Нужен ваш max_user_id: bash scripts/scenario.sh 123456789"
  echo "Его видно в логе бота после любого вашего сообщения боту (поле max_user_id)."
  exit 1
fi

if ! curl -fsS "$CORE/health" >/dev/null 2>&1; then
  echo "${RED}Core Backend не отвечает на $CORE.${RESET} Запустите систему: make up"; exit 1
fi
if ! curl -fsS "$CORE/dev/state" >/dev/null 2>&1; then
  echo "${RED}/dev выключен.${RESET} Нужно DEV_MODE=true в .env"; exit 1
fi

post() { curl -sS -X POST "$CORE$1" -H 'Content-Type: application/json' "${@:3}" -d "$2"; }
auth() { echo "Authorization: Bearer $1"; }
clock() { # advance
  local out; out=$(post /dev/clock "{\"advance\":\"$1\"}")
  echo "  ${DIM}часы Core Backend: $(jq -r .now <<<"$out") (сдвиг $(jq -r .clock_offset <<<"$out"))${RESET}"
  jq -r '.scheduler.queued[]? | "  ✓ в очередь: " + .' <<<"$out"
  local failed; failed=$(jq -r '.delivery.failed' <<<"$out")
  [[ "$failed" != 0 && "$failed" != null ]] && bad "бот отказал в $failed сообщении(ях) — смотрите GET $CORE/dev/state"
}
my_status() { curl -sS "$CORE/events/$1" -H "$(auth "$ME")" | jq -r '.my_registration.status // "нет записи"'; }
starts_in() { TZ=Europe/Moscow date -d "$1" +%Y-%m-%dT%H:%M:00+03:00; }
new_event() { # title, starts_in, capacity
  post /events "{\"title\":\"$1\",\"description\":\"Тестовое мероприятие из scenario.sh\",
    \"category_id\":\"games\",\"tag_ids\":[\"t_boardgames\"],\"starts_at\":\"$(starts_in "$2")\",
    \"duration_min\":120,\"city_id\":\"msk\",\"district\":\"Басманный\",
    \"address\":\"Антикафе, ул. Покровка, 17\",\"capacity\":$3}" -H "$(auth "$ORG")" | jq -r .id
}

echo "${BOLD}Сценарий в MAX для пользователя ${MAX_ID}${RESET}"
echo "${DIM}Часы Core Backend сейчас будут сдвигаться вперёд. В конце они вернутся к реальному времени.${RESET}"
post /dev/clock '{"reset":true}' >/dev/null

ORG=$(post /dev/login '{"first_name":"Клуб","last_name":"Настолок"}' | jq -r .token)
ME=$(post /dev/login "{\"max_user_id\": $MAX_ID, \"is_author\": false}" | jq -r .token)
[[ "$ORG" != null && "$ME" != null ]] || { bad "не удалось войти"; exit 1; }

step "1. Запись на мероприятие через сутки с небольшим"
EV=$(new_event "Настолки по пятницам" "+26 hours" 10)
REG=$(post /registrations "{\"event_id\":\"$EV\"}" -H "$(auth "$ME")" | jq -r .id)
ok "мероприятие $EV, запись $REG"
look "«✅ Вы записаны» с кнопками «Открыть афишу» и «Отменить запись»"
pause "Пришло?"

step "2. Через 2 часа до начала останется 24 часа"
clock 2h1m
look "напоминание «завтра» с кнопкой «Не смогу прийти»"
pause "Пришло?"

step "3. Ещё 18 часов: до начала 6 часов — просим подтвердить"
clock 18h
look "«Подтвердите участие» с кнопками «Приду» и «Не смогу»"
pause "Нажмите в MAX «Приду»"
status=$(my_status "$EV")
[[ "$status" == confirmed ]] && ok "Core Backend получил нажатие: запись подтверждена" \
  || bad "статус записи: $status (бот запущен с CORE_MODE=http?)"

step "4. Ещё 5 часов: до начала час"
clock 5h
look "«Скоро начало» с кнопкой «Маршрут»"
pause "Пришло?"
post /dev/clock '{"reset":true}' >/dev/null

step "5. Лист ожидания"
EV2=$(new_event "Мафия в антикафе" "+3 days" 1)
DEMO=$(post "/dev/events/$EV2/fill" '{"count":1}' | jq -r '.registration_ids[0]')
status=$(post /registrations "{\"event_id\":\"$EV2\"}" -H "$(auth "$ME")" | jq -r .status)
ok "единственное место занял демо-участник, вы — ${status}"
echo "  ${DIM}(в очередь бот ничего не пишет — это видно только в мини-приложении)${RESET}"
post "/dev/registrations/$DEMO/cancel" '{}' >/dev/null
ok "демо-участник отменил запись"
look "«🔥 Освободилось место» с кнопками «Занять место» и «Отказаться»"
pause "Нажмите «Занять место»"
status=$(my_status "$EV2")
[[ "$status" == registered || "$status" == confirmed ]] && ok "место ваше ($status)" || bad "статус: $status"

step "6. Организатор перенёс мероприятие"
NEW=$(starts_in "+3 days +1 hour")
post "/events/$EV2" "{\"starts_at\":\"$NEW\",\"organizer_message\":\"Переносим на час позже, простите!\"}" \
  -X PATCH -H "$(auth "$ORG")" | jq -r '"  ✓ изменено, уведомлено: \(.notified_count)"'
look "«Мероприятие изменено»: старое и новое время и сообщение организатора"
pause "Пришло?"

step "7. Организатор отменил мероприятие"
post "/events/$EV2/cancel" '{"organizer_message":"Ведущий заболел, соберёмся на следующей неделе."}' \
  -H "$(auth "$ORG")" | jq -r '"  ✓ отменено, уведомлено: \(.notified_count)"'
look "«Мероприятие отменено» с сообщением организатора"
pause "Пришло?"

step "8. Кнопка после отмены"
echo "  Нажмите «Не смогу прийти» под любым старым сообщением про «Мафию»."
look "«Мероприятие отменено организатором…» — Core Backend ответил event_cancelled"
pause "Проверили?"

echo
echo "${BOLD}Готово.${RESET} Часы вернулись к реальному времени. Что ушло в бота: $CORE/dev/state?payload=1"
