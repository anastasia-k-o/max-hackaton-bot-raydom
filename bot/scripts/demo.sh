#!/usr/bin/env bash
#
# Прогон всех восьми уведомлений через живого бота.
#
# Скрипт играет роль Core Backend: он вызывает POST /api/v1/notifications тем
# же контрактом, которым потом будет пользоваться настоящий бэкенд. Ничего
# специального для тестов в боте для этого не нужно — именно в этом и смысл.
#
# Что нужно: запущенный бот (make run) и ваш max_user_id.
#
# Как узнать max_user_id: напишите боту что-нибудь в MAX и посмотрите в логи
# бота — там будет строка с полем max_user_id. Скрипт запомнит его в .env
# (DEMO_MAX_USER_ID), чтобы больше не спрашивать.
#
# Мини-приложение НЕ требуется. Если MINI_APP_URL пуст, кнопка-ссылка просто
# не добавляется к сообщению — остальное работает как обычно.
#
# Использование:
#   bash scripts/demo.sh <max_user_id>          весь прогон: 8 уведомлений
#   bash scripts/demo.sh                        то же, id из .env
#   bash scripts/demo.sh --type waitlist_offer  один тип
#   bash scripts/demo.sh --watch                только следить за нажатиями
#   bash scripts/demo.sh --list                 список типов
#   bash scripts/demo.sh --help

set -uo pipefail

cd "$(dirname "$0")/.."

GREEN=$'\033[32m'; YELLOW=$'\033[33m'; RED=$'\033[31m'
DIM=$'\033[2m'; BOLD=$'\033[1m'; CYAN=$'\033[36m'; RESET=$'\033[0m'

TYPES=(
  registration_created
  reminder_24h
  confirmation_required
  confirmation_retry
  reminder_1h
  waitlist_offer
  event_updated
  event_cancelled
)

usage() {
  sed -n '2,26p' "$0" | sed 's/^# \{0,1\}//'
}

# --- разбор аргументов ----------------------------------------------------

USER_ID=""
ONLY_TYPE=""
WATCH_ONLY=0
PAUSE="${DEMO_PAUSE:-2}"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --help|-h) usage; exit 0 ;;
    --list)    printf '%s\n' "${TYPES[@]}"; exit 0 ;;
    --watch)   WATCH_ONLY=1; shift ;;
    --type)    ONLY_TYPE="${2:-}"; shift 2 ;;
    --pause)   PAUSE="${2:-2}"; shift 2 ;;
    -*)        echo "${RED}Неизвестный аргумент: $1${RESET}"; usage; exit 1 ;;
    *)         USER_ID="$1"; shift ;;
  esac
done

command -v curl >/dev/null || { echo "нужен curl"; exit 1; }
command -v jq   >/dev/null || { echo "нужен jq (apt install jq)"; exit 1; }

# Настройки: bot/.env, а если его нет — общий .env в корне репозитория.
ENV_FILE=.env
[[ -f "$ENV_FILE" ]] || ENV_FILE=../.env
if [[ ! -f "$ENV_FILE" ]]; then
  echo "${RED}Нет .env.${RESET} В корне репозитория выполните: make env"
  exit 1
fi

env_value() {
  grep -E "^$1=" "$ENV_FILE" 2>/dev/null | head -1 | cut -d= -f2- | tr -d '"'"'"' \r'
}

PORT="$(env_value HTTP_PORT)";        PORT="${PORT:-$(env_value BOT_PORT)}"; PORT="${PORT:-8080}"
API_KEY="$(env_value INTERNAL_API_KEY)"
MAX_MODE="$(env_value MAX_MODE)";     MAX_MODE="${MAX_MODE:-mock}"
MINI_APP_URL="$(env_value MINI_APP_URL)"
BASE="http://localhost:${PORT}"

if [[ -z "$API_KEY" ]]; then
  echo "${RED}INTERNAL_API_KEY в .env пуст.${RESET} Без него бот не примет уведомление."
  exit 1
fi

if [[ -z "$USER_ID" ]]; then
  USER_ID="$(env_value DEMO_MAX_USER_ID)"
fi

# --- бот на связи? --------------------------------------------------------

if ! curl -sf "${BASE}/health" -o /dev/null; then
  echo
  echo "${RED}Бот не отвечает на ${BASE}/health${RESET}"
  echo "Запустите его в другом терминале:  make run"
  echo
  exit 1
fi

READY="$(curl -s "${BASE}/ready")"
UPDATES_MODE="$(jq -r '.updates_mode // "?"' <<<"$READY")"
MAX_STATUS="$(jq -r '.dependencies[] | select(.name=="max") | .status' <<<"$READY")"
CORE_MODE_RUNNING="$(jq -r '.dependencies[] | select(.name=="core") | .mode' <<<"$READY")"

echo
if [[ "$WATCH_ONLY" == "1" ]]; then
  echo "${BOLD}Наблюдение за действиями${RESET}"
else
  echo "${BOLD}Прогон уведомлений${RESET}"
fi
echo "${DIM}бот: ${BASE} · MAX: ${MAX_MODE} · события: ${UPDATES_MODE} · Core: ${CORE_MODE_RUNNING}${RESET}"

if [[ "$MAX_STATUS" != "ok" ]]; then
  echo
  echo "${YELLOW}Внимание: /ready сообщает о проблеме с MAX:${RESET}"
  jq -r '.dependencies[] | select(.name=="max") | "  " + (.error // "нет описания")' <<<"$READY"
  echo "  Сообщения, скорее всего, не дойдут. Проверьте: bash scripts/max-info.sh"
fi

if [[ -z "$MINI_APP_URL" && "$WATCH_ONLY" == "0" ]]; then
  echo "${DIM}MINI_APP_URL пуст — кнопка-ссылка на мини-приложение не добавляется. Это нормально.${RESET}"
fi

# --- max_user_id ----------------------------------------------------------

need_user_id() {
  echo
  echo "${BOLD}Нужен ваш max_user_id.${RESET}"
  echo
  if [[ "$MAX_MODE" == "real" ]]; then
    echo "  1. Откройте бота в MAX и напишите ему что угодно."
    echo "  2. В логах бота (там, где запущен make run) появится строка с полем"
    echo "     ${CYAN}max_user_id${RESET} — это и есть ваш идентификатор."
    echo "  3. Запустите: ${BOLD}bash scripts/demo.sh <этот_id>${RESET}"
    echo
    echo "  ${DIM}Найти бота в MAX:  bash scripts/max-info.sh${RESET}"
  else
    echo "  В режиме MAX_MODE=mock сообщения никуда не уходят, поэтому годится"
    echo "  любое положительное число, например 123456789."
  fi
  echo
}

if [[ "$WATCH_ONLY" == "0" ]]; then
  if [[ -z "$USER_ID" ]]; then
    need_user_id
    exit 1
  fi
  if ! [[ "$USER_ID" =~ ^[0-9]+$ ]]; then
    echo "${RED}max_user_id должен быть целым числом, получено: ${USER_ID}${RESET}"
    echo "${DIM}В контракте это integer/int64, не строка.${RESET}"
    exit 1
  fi
fi

# --- тела уведомлений -----------------------------------------------------
#
# Мероприятия взяты из моков мини-приложения (frontend/src/api/mocks/events.js):
# те же id, названия и адреса. Благодаря этому «Открыть афишу» ведёт на
# существующую карточку, а не на «мероприятие не найдено».
#
# Время — в смещении Москвы, как в моках фронта, и никогда не в UTC: бот
# печатает время в том смещении, в котором оно пришло, и отвергает «Z».
# Москва живёт в UTC+3 круглый год, поэтому смещение можно зафиксировать.

CITY_OFFSET="+03:00"
CITY_OFFSET_SECONDS=$((3 * 3600))

# Форматирует момент «сейчас + N секунд» как время в городе.
# Работает и с GNU date (Linux), и с BSD date (macOS).
city_iso_in() {
  local ts=$(( $(date +%s) + $1 + CITY_OFFSET_SECONDS ))
  date -u -d "@${ts}" "+%Y-%m-%dT%H:%M:%S${CITY_OFFSET}" 2>/dev/null \
    || date -u -r "${ts}" "+%Y-%m-%dT%H:%M:%S${CITY_OFFSET}"
}

# Как at(days, hour, min) в моках фронта: дата через N дней, время ровно HH:MM.
city_at() {
  local days="$1" hour="$2" min="${3:-0}"
  local day
  day="$(city_iso_in $(( days * 86400 )) | cut -c1-10)"
  printf '%sT%02d:%02d:00%s' "$day" "$hour" "$min" "$CITY_OFFSET"
}

RUN_ID="$(date +%s)"

# Мини-приложение открывает карточку по пути /app/{event_id}; MINI_APP_URL
# указывается уже с /app на конце.
event_url() {
  [[ -n "$MINI_APP_URL" ]] && printf '%s/%s' "${MINI_APP_URL%/}" "$1"
}

# Тело для одного типа. Поля ровно те, которых требует контракт: registration
# нужен всем типам, кроме event_updated и event_cancelled, а waitlist_offer
# дополнительно требует data.offer_expires_at.
build_body() {
  local type="$1"
  local event_id title address starts_at data_block registration_block

  case "$type" in
    registration_created)
      event_id="event_1"; title="Йога в парке"
      address="Парк «Усадьба Трубецких», ул. Усачёва, 1а, поляна у пруда"
      starts_at="$(city_at 2 9)" ;;
    reminder_24h)
      event_id="event_3"; title="Настолки по пятницам"
      address="Антикафе, ул. Покровка, 17, стр. 1"
      starts_at="$(city_at 1 18 30)" ;;
    confirmation_required|confirmation_retry)
      event_id="event_6"; title="Разговорный клуб английского"
      address="Коворкинг, ул. Большая Никитская, 24/1"
      starts_at="$(city_at 2 19)" ;;
    reminder_1h)
      event_id="event_13"; title="Лекция: как устроена память"
      address="Лекторий, ул. Тверская, 12, стр. 2"
      starts_at="$(city_iso_in 3600)" ;;
    waitlist_offer)
      event_id="event_5"; title="Скетчинг в Замоскворечье"
      address="Лаврушинский пер., у выхода из метро «Третьяковская»"
      starts_at="$(city_at 5 12)" ;;
    event_updated)
      event_id="event_8"; title="Прогулка по купеческому Замоскворечью"
      address="Пятницкая ул., у выхода из метро «Новокузнецкая»"
      starts_at="$(city_at 7 12)" ;;
    event_cancelled)
      event_id="event_9"; title="Фотопрогулка на закате"
      address="Парк «Зарядье», ул. Варварка, 6, у Парящего моста"
      starts_at="$(city_at 3 17 30)" ;;
    *)
      echo "${RED}Неизвестный тип уведомления: ${type}${RESET}" >&2
      echo "Допустимые значения:" >&2
      printf '  %s\n' "${TYPES[@]}" >&2
      return 1
      ;;
  esac

  data_block=""
  case "$type" in
    waitlist_offer)
      data_block=", \"data\": {\"offer_expires_at\": \"$(city_iso_in $(( 6 * 3600 )))\"}"
      ;;
    event_updated)
      data_block=', "data": {"changes": ['
      data_block+='{"field": "time", "old": "11:00", "new": "12:00"}'
      data_block+='], "organizer_message": "Сбор на час позже: экскурсовод задерживается."}'
      ;;
    event_cancelled)
      data_block=', "data": {"organizer_message": "Отменяем из-за штормового предупреждения. Перенесём, как только позволит погода."}'
      ;;
  esac

  registration_block=""
  case "$type" in
    event_updated|event_cancelled) ;;
    *) registration_block=", \"registration\": {\"id\": \"registration_demo_${event_id}\"}" ;;
  esac

  local mini_app="" url
  url="$(event_url "$event_id")"
  [[ -n "$url" ]] && mini_app=", \"mini_app_url\": \"${url}\""

  cat <<JSON
{
  "request_id": "demo_${RUN_ID}_${type}",
  "type": "${type}",
  "recipient": {"max_user_id": ${USER_ID}},
  "event": {
    "id": "${event_id}",
    "title": "${title}",
    "starts_at": "${starts_at}",
    "address": "${address}"${mini_app}
  }${registration_block}${data_block}
}
JSON
}

# Что пользователь должен увидеть. Это и есть критерий приёмки: если в MAX
# пришло не то, что здесь написано, значит сломано.
expectation() {
  case "$1" in
    registration_created)  echo "«✅ Вы записаны» + кнопка «Отменить запись»" ;;
    reminder_24h)          echo "«⏰ Напоминание» на завтра + «Не смогу прийти»" ;;
    confirmation_required) echo "${BOLD}две кнопки действия: «Приду» и «Не смогу прийти»${RESET}" ;;
    confirmation_retry)    echo "тот же запрос повторно, те же две кнопки" ;;
    reminder_1h)           echo "«📍 Скоро начало» + ссылка «Маршрут» + «Не смогу прийти»" ;;
    waitlist_offer)        echo "${BOLD}«освободилось место» + «Занять место» / «Отказаться»${RESET}, со сроком" ;;
    event_updated)         echo "что изменилось (время) + сообщение организатора, ${DIM}без кнопок действия${RESET}" ;;
    event_cancelled)       echo "отмена + причина, ${DIM}без кнопок действия${RESET}" ;;
  esac
}

# --- отправка -------------------------------------------------------------

send_one() {
  local type="$1" body response code status message_id

  body="$(build_body "$type")" || return 1

  response="$(curl -s -w $'\n%{http_code}' -X POST "${BASE}/api/v1/notifications" \
    -H 'Content-Type: application/json' \
    -H "X-Internal-Api-Key: ${API_KEY}" \
    -d "$body")"

  code="$(tail -1 <<<"$response")"
  response="$(sed '$d' <<<"$response")"

  printf '  %-22s ' "$type"

  case "$code" in
    202|200)
      status="$(jq -r '.status // "?"' <<<"$response" 2>/dev/null)"
      message_id="$(jq -r '.message_id // ""' <<<"$response" 2>/dev/null)"
      if [[ "$status" == "duplicate" ]]; then
        echo "${YELLOW}дубликат${RESET} ${DIM}(тот же request_id уже обрабатывался)${RESET}"
      else
        echo "${GREEN}✓${RESET} $(expectation "$type") ${DIM}${message_id}${RESET}"
      fi
      return 0
      ;;
    400)
      echo "${RED}400${RESET} — контракт не принял тело:"
      jq -r '.error.details[]? | "      " + .field + ": " + .message' <<<"$response" 2>/dev/null \
        || jq -r '"      " + (.error.message // .)' <<<"$response" 2>/dev/null
      return 1
      ;;
    401)
      echo "${RED}401${RESET} — INTERNAL_API_KEY не подошёл"
      return 1
      ;;
    502)
      echo "${RED}502${RESET} — бот не смог отправить сообщение в MAX"
      jq -r '"      " + (.error.message // "")' <<<"$response" 2>/dev/null
      echo "      ${DIM}Частая причина: max_user_id не тот, или пользователь не открывал диалог с ботом.${RESET}"
      echo "      ${DIM}Подробности — в логах бота.${RESET}"
      return 1
      ;;
    *)
      echo "${RED}HTTP ${code}${RESET}"
      echo "      $response"
      return 1
      ;;
  esac
}

# --- наблюдение за нажатиями ----------------------------------------------
#
# StubCoreGateway складывает в память всё, что бот передал бы настоящему
# бэкенду. Пока Core Backend не существует, это и есть способ увидеть вторую
# половину цикла.

watch_actions() {
  local seen=0 actions count

  echo
  echo "${BOLD}Жду нажатий кнопок.${RESET} ${DIM}Ctrl+C — выход.${RESET}"
  echo "${DIM}Нажмите кнопку в MAX: сообщение должно смениться, а здесь появится${RESET}"
  echo "${DIM}то, что бот передал бы в Core Backend.${RESET}"
  echo

  if ! curl -sf "${BASE}/dev/core/actions" -o /dev/null; then
    echo "${YELLOW}Эндпойнт /dev/core/actions недоступен.${RESET}"
    echo "Он поднимается только при APP_ENV=dev и CORE_MODE=stub."
    return 1
  fi

  while true; do
    actions="$(curl -s "${BASE}/dev/core/actions")"
    count="$(jq -r '.count // 0' <<<"$actions" 2>/dev/null || echo 0)"

    if [[ "$count" -gt "$seen" ]]; then
      jq -r --argjson from "$seen" '
        .actions[$from:][] |
        if .kind == "bot_status" then
          "  \u001b[36m→\u001b[0m \u001b[1mbot_status\u001b[0m  " +
          (if .available then "бот может писать" else "бот НЕ может писать" end) +
          "  (" + .reason + ")  max_user_id=" + (.max_user_id | tostring)
        else
          "  \u001b[32m→\u001b[0m \u001b[1m" + .kind + "\u001b[0m  " +
          "registration=" + .registration_id +
          (if .event_id then "  event=" + .event_id else "" end) +
          "  max_user_id=" + (.max_user_id | tostring)
        end
      ' <<<"$actions" 2>/dev/null
      seen="$count"
    fi
    sleep 1
  done
}

# --- сценарии -------------------------------------------------------------

if [[ "$WATCH_ONLY" == "1" ]]; then
  watch_actions
  exit $?
fi

# Запоминаем id, чтобы в следующий раз не передавать его руками.
if ! grep -qE '^DEMO_MAX_USER_ID=[0-9]' "$ENV_FILE" 2>/dev/null; then
  if grep -qE '^DEMO_MAX_USER_ID=' "$ENV_FILE"; then
    sed -i "s/^DEMO_MAX_USER_ID=.*/DEMO_MAX_USER_ID=${USER_ID}/" "$ENV_FILE"
  else
    printf '\n# Ваш max_user_id для scripts/demo.sh (только для разработки).\nDEMO_MAX_USER_ID=%s\n' "$USER_ID" >> "$ENV_FILE"
  fi
  echo "${DIM}max_user_id сохранён в ${ENV_FILE} как DEMO_MAX_USER_ID${RESET}"
fi

FAILED=0

if [[ -n "$ONLY_TYPE" ]]; then
  echo
  send_one "$ONLY_TYPE" || FAILED=1
  echo
  [[ "$FAILED" == "0" ]] && echo "${DIM}Следить за нажатиями:  bash scripts/demo.sh --watch${RESET}"
  echo
  exit "$FAILED"
fi

echo
echo "Отправляю восемь уведомлений на max_user_id=${BOLD}${USER_ID}${RESET}"
echo "${DIM}пауза между сообщениями: ${PAUSE}с${RESET}"
echo

for type in "${TYPES[@]}"; do
  send_one "$type" || FAILED=1
  sleep "$PAUSE"
done

echo
if [[ "$FAILED" != "0" ]]; then
  echo "${RED}Часть уведомлений не ушла.${RESET} Смотрите сообщения выше и логи бота."
  echo
  exit 1
fi

echo "${GREEN}Все восемь отправлены.${RESET}"
echo
echo "${BOLD}Что проверить в MAX:${RESET}"
echo "  • пришло восемь сообщений, каждое читается без пояснений;"
echo "  • у первых шести есть кнопки действия, у двух последних — нет;"
echo "  • «Открыть афишу» ведёт на ту же карточку в мини-приложении"
echo "    (кнопка есть, если задан MINI_APP_URL или MAX_MINIAPP_BUTTON=open_app);"
echo "  • даты и время выглядят по-русски и в вашем часовом поясе;"
echo "  • нажмите «Приду» — сообщение ${BOLD}заменится${RESET} на подтверждение"
echo "    и кнопок действия в нём не останется;"
echo "  • нажмите «Приду» второй раз в старом сообщении — бот не должен"
echo "    выполнить действие повторно."
echo
echo "${BOLD}Вторая половина цикла${RESET} — то, что ушло бы в Core Backend:"
echo "  bash scripts/demo.sh --watch"
echo "  ${DIM}или разово:  curl -s localhost:${PORT}/dev/core/actions | jq${RESET}"
echo
