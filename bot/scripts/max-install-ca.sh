#!/usr/bin/env bash
#
# Получение корневого сертификата УЦ Минцифры для соединения с MAX.
#
# Скрипт появился после того, как «простая» цепочка команд с curl и openssl
# тихо записала в .pem страницу редиректа на 146 байт, а выяснилось это лишь
# при старте бота. Здесь каждый шаг проверяется, и ни один неудачный результат
# не проходит дальше молча.
#
# Что делает:
#   1. Берёт корень — из файла, который вы уже скачали, или из сети.
#   2. Проверяет, что это действительно сертификат, а не страница ошибки.
#   3. Приводит к формату PEM.
#   4. Показывает subject, issuer и отпечаток для сверки.
#   5. Проверяет, что цепочка MAX этим корнем действительно проверяется.
#   6. Печатает готовую строку для .env.
#
# Запуск:
#   bash scripts/max-install-ca.sh                  # скачать самому
#   bash scripts/max-install-ca.sh ~/Downloads/root.cer   # взять готовый файл

set -uo pipefail

cd "$(dirname "$0")/.."

HOST="${MAX_TLS_HOST:-platform-api2.max.ru}"
CERT_DIR="certs"
PEM="${CERT_DIR}/russian_trusted_root_ca.pem"

GREEN=$'\033[32m'; YELLOW=$'\033[33m'; RED=$'\033[31m'; BOLD=$'\033[1m'; DIM=$'\033[2m'; RESET=$'\033[0m'

ok()   { printf '  %s✓%s %s\n' "$GREEN" "$RESET" "$1"; }
warn() { printf '  %s!%s %s\n' "$YELLOW" "$RESET" "$1"; }
bad()  { printf '  %s✗%s %s\n' "$RED" "$RESET" "$1"; }

command -v openssl >/dev/null || { echo "нужен openssl: sudo apt install openssl"; exit 1; }

mkdir -p "$CERT_DIR"

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

# --- как понять, что перед нами сертификат ---------------------------------
#
# Главный урок предыдущей попытки: проверять надо содержимое, а не код
# возврата curl. Сервер может ответить «200 OK» и отдать HTML.

to_pem() {
  # Печатает PEM на stdout, если вход — сертификат(ы) в любом из форматов.
  #
  # Порядок важен. Файл может содержать НЕСКОЛЬКО сертификатов (Минцифры
  # раздаёт набор «корень + промежуточный» одним .pem), и терять второй
  # нельзя — иначе цепочка не соберётся. Поэтому набор обрабатывается
  # отдельной веткой, а не через `openssl x509`, который берёт только первый.
  local src="$1"

  if grep -q 'BEGIN CERTIFICATE' "$src" 2>/dev/null; then
    # Проверяем, что это разбирается, но на выход отдаём файл целиком.
    if openssl x509 -inform PEM -in "$src" -noout 2>/dev/null; then
      cat "$src"
      return 0
    fi
  fi

  openssl x509 -inform DER -in "$src" 2>/dev/null && return 0
  return 1
}

count_certs() {
  grep -c 'BEGIN CERTIFICATE' "$1" 2>/dev/null || echo 0
}

describe_input() {
  local src="$1"
  local size; size="$(wc -c <"$src" 2>/dev/null || echo 0)"
  echo "    размер: ${size} байт"
  if command -v file >/dev/null; then
    echo "    тип:    $(file -b "$src")"
  fi
  echo "    начало файла:"
  head -c 180 "$src" | sed 's/^/      /'
  echo
}

# --- 1. Получение ----------------------------------------------------------

echo
echo "${BOLD}1. Получение корневого сертификата${RESET}"

RAW="${WORK}/root.bin"
GOT=0

if [[ $# -ge 1 ]]; then
  SRC="$1"
  if [[ ! -f "$SRC" ]]; then
    bad "файл не найден: ${SRC}"
    exit 1
  fi
  cp "$SRC" "$RAW"
  ok "взят локальный файл: ${SRC}"
  GOT=1
else
  command -v curl >/dev/null || { echo "нужен curl"; exit 1; }

  # -L обязателен: адрес отвечает редиректом, и без него скачивается
  # страница-заглушка вместо сертификата.
  # --fail не даёт записать тело ответа при HTTP-ошибке.
  # Адреса проверены по актуальным инструкциям (mos.ru/cert, gosuslugi.ru/crt).
  # Первым идёт готовый PEM-набор: в нём сразу и корень, и промежуточный
  # сертификат, что избавляет от отдельной загрузки Sub CA.
  for URL in \
    "https://gu-st.ru/content/Other/doc/russiantrustedca.pem" \
    "https://gu-st.ru/content/Other/doc/russian_trusted_root_ca.cer"
  do
    echo "    пробую ${URL}"
    if curl -fsSLk --max-time 30 -o "$RAW" "$URL" 2>/dev/null && [[ -s "$RAW" ]]; then
      if to_pem "$RAW" >/dev/null 2>&1; then
        ok "скачан сертификат"
        GOT=1
        break
      fi
      warn "ответ получен, но это не сертификат:"
      describe_input "$RAW"
    else
      warn "загрузка не удалась"
    fi
  done
fi

# Источник 2: хранилище сертификатов Windows.
#
# В WSL это самый надёжный путь. Корень Минцифры на Windows обычно уже
# установлен (иначе бы не работал браузер), а powershell.exe вызывается из
# WSL напрямую — никакой сети не требуется.
if [[ "$GOT" -ne 1 ]] && command -v powershell.exe >/dev/null 2>&1; then
  echo
  echo "    пробую хранилище сертификатов Windows"
  PS_OUT="${WORK}/from-windows.pem"
  powershell.exe -NoProfile -Command \
    "Get-ChildItem Cert:\\LocalMachine\\Root, Cert:\\CurrentUser\\Root |
     Where-Object { \$_.Subject -match 'Russian Trusted' } |
     ForEach-Object {
       '-----BEGIN CERTIFICATE-----'
       [Convert]::ToBase64String(\$_.RawData, 'InsertLineBreaks')
       '-----END CERTIFICATE-----'
     }" 2>/dev/null | tr -d '\r' > "$PS_OUT"

  if [[ -s "$PS_OUT" ]] && to_pem "$PS_OUT" >/dev/null 2>&1; then
    cp "$PS_OUT" "$RAW"
    ok "корень взят из хранилища Windows ($(count_certs "$PS_OUT") шт.)"
    GOT=1
  else
    warn "в хранилище Windows подходящий сертификат не найден"
  fi
fi

# Источник 3: сам сервер MAX.
#
# Сервер обязан прислать промежуточные сертификаты, а корень — по желанию.
# Многие российские сервисы его присылают именно потому, что у клиентов его
# часто нет. Если прислал — качать неоткуда не нужно.
if [[ "$GOT" -ne 1 ]]; then
  echo
  echo "    пробую извлечь корень из цепочки ${HOST}"
  CHAIN_RAW="${WORK}/chain.txt"
  timeout 25 openssl s_client -connect "${HOST}:443" -servername "$HOST" \
    -showcerts </dev/null > "$CHAIN_RAW" 2>&1

  awk '/BEGIN CERTIFICATE/,/END CERTIFICATE/' "$CHAIN_RAW" > "${WORK}/chain.pem" 2>/dev/null

  if [[ -s "${WORK}/chain.pem" ]]; then
    # Берём последний сертификат цепочки и проверяем, самоподписанный ли он:
    # только такой является корнем.
    csplit -sz -f "${WORK}/c" -b '%02d.pem' "${WORK}/chain.pem" '/BEGIN CERTIFICATE/' '{*}' 2>/dev/null
    for candidate in $(ls -r "${WORK}"/c*.pem 2>/dev/null); do
      sub="$(openssl x509 -in "$candidate" -noout -subject 2>/dev/null | sed 's/^subject=//')"
      iss="$(openssl x509 -in "$candidate" -noout -issuer  2>/dev/null | sed 's/^issuer=//')"
      if [[ -n "$sub" && "$sub" == "$iss" ]]; then
        cp "$candidate" "$RAW"
        ok "корень найден прямо в цепочке сервера"
        GOT=1
        break
      fi
    done
    [[ "$GOT" -ne 1 ]] && warn "сервер прислал цепочку без корневого сертификата"
  else
    warn "цепочку получить не удалось"
  fi
fi

if [[ "$GOT" -ne 1 ]]; then
  bad "получить корневой сертификат автоматически не вышло"
  echo
  echo "  ${BOLD}Скачайте вручную через браузер Windows${RESET} — там этот корень"
  echo "  обычно уже доверенный, поэтому загрузка пройдёт без проблем:"
  echo
  echo "    1. Откройте https://www.gosuslugi.ru/crt"
  echo "       (либо прямая ссылка на набор:"
  echo "        https://gu-st.ru/content/Other/doc/russiantrustedca.pem )"
  echo "    2. Скачайте корневой сертификат"
  echo "    3. Передайте путь скрипту, например:"
  echo "         bash scripts/max-install-ca.sh /mnt/c/Users/<вы>/Downloads/russian_trusted_root_ca.cer"
  echo
  echo "  ${DIM}Из WSL диск C: доступен как /mnt/c${RESET}"
  echo
  exit 1
fi

# --- 2. Преобразование -----------------------------------------------------

echo
echo "${BOLD}2. Формат${RESET}"

if ! to_pem "$RAW" > "${WORK}/root.pem" 2>/dev/null; then
  bad "не удалось разобрать файл как сертификат"
  describe_input "$RAW"
  exit 1
fi

if ! grep -q 'BEGIN CERTIFICATE' "${WORK}/root.pem"; then
  bad "после преобразования PEM-блока нет"
  exit 1
fi
ok "сертификатов в формате PEM получено: $(count_certs "${WORK}/root.pem")"

# --- 3. Что именно мы получили --------------------------------------------

echo
echo "${BOLD}3. Содержимое сертификата${RESET}"

SUBJECT="$(openssl x509 -in "${WORK}/root.pem" -noout -subject | sed 's/^subject=//')"
ISSUER="$(openssl x509 -in "${WORK}/root.pem" -noout -issuer | sed 's/^issuer=//')"
FINGERPRINT="$(openssl x509 -in "${WORK}/root.pem" -noout -fingerprint -sha256 | sed 's/^.*=//')"
DATES="$(openssl x509 -in "${WORK}/root.pem" -noout -dates | tr '\n' ' ')"

echo "    subject:  ${SUBJECT}"
echo "    issuer:   ${ISSUER}"
echo "    срок:     ${DATES}"
echo "    SHA-256:  ${BOLD}${FINGERPRINT}${RESET}"

if [[ "$SUBJECT" == "$ISSUER" ]]; then
  ok "subject совпадает с issuer — это корневой самоподписанный сертификат"
else
  warn "subject и issuer различаются: это промежуточный сертификат, а не корень"
  warn "он тоже может сработать, но правильнее взять именно корень"
fi

echo
echo "  ${YELLOW}Сверьте отпечаток SHA-256${RESET} с опубликованным на официальной"
echo "  странице Госуслуг, открытой в браузере Windows. Это независимый канал:"
echo "  сам файл скачан по соединению, доверие к которому мы как раз и"
echo "  настраиваем."

# --- 4. Проверка на живой цепочке -----------------------------------------

echo
echo "${BOLD}4. Проверка на цепочке ${HOST}${RESET}"

VERIFY="$(timeout 25 openssl s_client -connect "${HOST}:443" -servername "$HOST" \
  -CAfile "${WORK}/root.pem" </dev/null 2>&1 | grep -E '^Verify return code:' | tail -1)"

if [[ "$VERIFY" == *"0 (ok)"* ]]; then
  ok "цепочка MAX успешно проверяется этим корнем"
else
  bad "цепочка этим корнем не проверяется"
  echo "    ${VERIFY:-нет ответа}"
  echo
  echo "  Возможно, скачан не тот сертификат, либо нужен ещё промежуточный."
  echo "  Сертификат всё равно сохранён — посмотрите цепочку целиком:"
  echo "      bash scripts/max-tls-check.sh"
  echo
fi

# --- 5. Сохранение ---------------------------------------------------------

echo
echo "${BOLD}5. Сохранение${RESET}"

if [[ -f "$PEM" ]] && ! cmp -s "${WORK}/root.pem" "$PEM"; then
  cp "$PEM" "${PEM}.bak"
  warn "прежний файл сохранён как ${PEM}.bak"
fi

cp "${WORK}/root.pem" "$PEM"
ok "записан ${PEM}"

echo
echo "${BOLD}Осталось добавить строку в .env:${RESET}"
echo
echo "    MAX_CA_FILE=$(pwd)/${PEM}"
echo
echo "Затем:  make run"
echo
echo "${DIM}certs/ уже в .gitignore — это настройка машины, а не часть проекта.${RESET}"
echo
