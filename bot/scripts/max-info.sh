#!/usr/bin/env bash
#
# Кто наш бот и кому сейчас уходят его события.
#
# Отвечает на два вопроса, которые возникают, когда токен выдали организаторы
# и бота вы не создавали:
#
#   1. Как этот бот называется в MAX и как его найти (GET /me).
#   2. Не подписан ли на его webhook кто-то ещё (GET /subscriptions).
#
# Второе важнее, чем кажется: подписок может быть несколько одновременно, и
# тогда одно нажатие кнопки уедет на все зарегистрированные адреса — включая
# сервер другой команды.
#
# Токен читается из .env и никогда не печатается.
#
# Запуск:  bash scripts/max-info.sh

set -euo pipefail

cd "$(dirname "$0")/.."

API="${MAX_BASE_URL:-https://platform-api2.max.ru}"

GREEN=$'\033[32m'; YELLOW=$'\033[33m'; RED=$'\033[31m'; DIM=$'\033[2m'; BOLD=$'\033[1m'; RESET=$'\033[0m'

command -v curl >/dev/null || { echo "нужен curl"; exit 1; }

if [[ ! -f .env ]]; then
  echo "${RED}Нет .env.${RESET} Выполните: cp .env.example .env"
  exit 1
fi

# Читаем токен, не вынося его в окружение дочерних процессов и не печатая.
TOKEN="$(grep -E '^MAX_BOT_TOKEN=' .env | head -1 | cut -d= -f2- | tr -d '"'"'"' \r' || true)"

if [[ -z "$TOKEN" ]]; then
  echo "${RED}MAX_BOT_TOKEN в .env пуст.${RESET}"
  echo "Вставьте токен, который выдали организаторы."
  exit 1
fi

HAVE_JQ=0
command -v jq >/dev/null && HAVE_JQ=1

# Тот же дополнительный корень, что использует бот.
#
# Без него curl берёт системное хранилище, куда корень Минцифры намеренно не
# клался, и падает с «unable to get local issuer certificate». Это не сбой:
# ровно так и выглядит доверие, ограниченное одним приложением. Скрипт просто
# пользуется той же настройкой, что и бот.
#
# Замечание про --cacert: он ЗАМЕНЯЕТ набор корней, а не дополняет. Для
# обращения к одному известному хосту это уместно.
CA_ARGS=()
CA_FILE="$(grep -E '^MAX_CA_FILE=' .env 2>/dev/null | head -1 | cut -d= -f2- | tr -d '"'"'"' \r' || true)"

if [[ -n "$CA_FILE" ]]; then
  if [[ -f "$CA_FILE" ]]; then
    CA_ARGS=(--cacert "$CA_FILE")
    echo "${DIM}используется MAX_CA_FILE: ${CA_FILE}${RESET}"
  else
    echo "${YELLOW}MAX_CA_FILE указывает на несуществующий файл: ${CA_FILE}${RESET}"
  fi
fi

# --- 1. Кто мы ------------------------------------------------------------

echo
echo "${BOLD}Профиль бота${RESET} ${DIM}GET ${API}/me${RESET}"

ME_FILE="$(mktemp)"
trap 'rm -f "$ME_FILE" "${SUBS_FILE:-}" "${CURL_ERR:-}"' EXIT

CURL_ERR="$(mktemp)"
ME_CODE="$(curl -sS "${CA_ARGS[@]}" -o "$ME_FILE" -w '%{http_code}' \
  "${API}/me" -H "Authorization: ${TOKEN}" 2>"$CURL_ERR")" || ME_CODE=""
ME_CODE="${ME_CODE:-000}"

if [[ "$ME_CODE" == "401" ]]; then
  echo "  ${RED}401 Unauthorized — MAX не принял токен.${RESET}"
  echo "  Проверьте, что в .env скопирован весь токен, без пробелов и переносов,"
  echo "  и что перед ним нет префикса Bearer."
  exit 1
fi

if [[ "$ME_CODE" == "000" ]]; then
  echo "  ${RED}соединение не установлено${RESET}"
  if grep -qi 'certificate' "$CURL_ERR" 2>/dev/null; then
    echo "  ${DIM}$(head -1 "$CURL_ERR")${RESET}"
  fi
  echo
  if [[ ${#CA_ARGS[@]} -eq 0 ]]; then
    echo "  Вероятная причина: MAX_CA_FILE не задан в .env, а системное"
    echo "  хранилище не содержит корня УЦ Минцифры. Бот в этом случае"
    echo "  работает (у него свой набор), а curl — нет."
    echo
    echo "  Получить корень:  bash scripts/max-install-ca.sh"
    echo "  Диагностика TLS:  bash scripts/max-tls-check.sh"
  else
    echo "  MAX_CA_FILE задан, но соединение всё равно не вышло."
    echo "  Диагностика:  bash scripts/max-tls-check.sh"
  fi
  echo
  exit 1
fi

if [[ "$ME_CODE" != "200" ]]; then
  echo "  ${RED}HTTP ${ME_CODE}${RESET}"
  cat "$ME_FILE"; echo
  exit 1
fi

if [[ "$HAVE_JQ" == "1" ]]; then
  USERNAME="$(jq -r '.username // empty' "$ME_FILE")"
  NAME="$(jq -r '.first_name // .name // empty' "$ME_FILE")"
  USER_ID="$(jq -r '.user_id // empty' "$ME_FILE")"
  DESCRIPTION="$(jq -r '.description // empty' "$ME_FILE")"

  echo "  ${GREEN}✓${RESET} имя:        ${BOLD}${NAME}${RESET}"
  echo "    user_id:     ${USER_ID}"
  if [[ -n "$USERNAME" ]]; then
    echo "    username:    ${BOLD}@${USERNAME}${RESET}"
    echo
    echo "  ${BOLD}Как открыть бота в MAX:${RESET}"
    echo "    • поиск в приложении: ${BOLD}@${USERNAME}${RESET}"
    echo "    • прямая ссылка:      ${BOLD}https://max.ru/${USERNAME}${RESET}"
  else
    echo "    ${YELLOW}username не задан.${RESET}"
    echo "    Найти такого бота поиском нельзя — попросите у организаторов"
    echo "    прямую ссылку или пусть они зададут username через @MasterBot."
  fi
  [[ -n "$DESCRIPTION" ]] && echo "    описание:    ${DESCRIPTION}"
else
  echo "  ${DIM}(jq не установлен — сырой ответ)${RESET}"
  cat "$ME_FILE"; echo
  echo "  Поле ${BOLD}username${RESET} — это и есть имя для поиска в MAX: @username"
fi

# --- 2. Кому уходят события ----------------------------------------------

echo
echo "${BOLD}Подписки на webhook${RESET} ${DIM}GET ${API}/subscriptions${RESET}"

SUBS_FILE="$(mktemp)"
SUBS_CODE="$(curl -sS "${CA_ARGS[@]}" -o "$SUBS_FILE" -w '%{http_code}' \
  "${API}/subscriptions" -H "Authorization: ${TOKEN}" 2>/dev/null)" || SUBS_CODE=""
SUBS_CODE="${SUBS_CODE:-000}"

if [[ "$SUBS_CODE" != "200" ]]; then
  echo "  ${RED}HTTP ${SUBS_CODE}${RESET}"
  cat "$SUBS_FILE"; echo
  exit 1
fi

if [[ "$HAVE_JQ" == "1" ]]; then
  COUNT="$(jq '.subscriptions | length' "$SUBS_FILE")"

  if [[ "$COUNT" == "0" ]]; then
    echo "  Подписок нет — события webhook сейчас никуда не уходят."
    echo "  ${DIM}Это нормальное состояние до того, как вы зарегистрируете свой адрес.${RESET}"
  else
    echo "  Найдено подписок: ${BOLD}${COUNT}${RESET}"
    jq -r '.subscriptions[] | "    • \(.url)\n      типы: \(.update_types // ["все"] | join(", "))"' "$SUBS_FILE"

    if [[ "$COUNT" -gt 0 ]]; then
      echo
      echo "  ${YELLOW}Внимание.${RESET} Токен выдан организаторами, значит этим ботом"
      echo "  могут пользоваться и другие команды. Подписок может быть несколько"
      echo "  одновременно, и тогда ${BOLD}одно нажатие кнопки уйдёт на все адреса${RESET} —"
      echo "  включая чужие серверы."
      echo
      echo "  Если адрес в списке не ваш, не удаляйте его молча: сначала"
      echo "  уточните у организаторов, общий это бот или ваш собственный."
    fi
  fi
else
  cat "$SUBS_FILE"; echo
fi

echo
echo "${DIM}Токен в выводе не печатается.${RESET}"
echo
