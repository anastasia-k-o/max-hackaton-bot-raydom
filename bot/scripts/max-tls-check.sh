#!/usr/bin/env bash
#
# Диагностика TLS-соединения с MAX API.
#
# Запускать, когда curl или бот говорят что-то вроде:
#   SSL certificate problem: unable to get local issuer certificate
#   x509: certificate signed by unknown authority
#
# Почти всегда это состояние хранилища корневых сертификатов на этой машине,
# а не проблема MAX. Скрипт показывает реальную цепочку и называет причину.
#
# Токен не требуется и не читается.
#
# Запуск:  bash scripts/max-tls-check.sh

set -uo pipefail

HOST="${MAX_TLS_HOST:-platform-api2.max.ru}"
PORT=443

GREEN=$'\033[32m'; YELLOW=$'\033[33m'; RED=$'\033[31m'; BOLD=$'\033[1m'; DIM=$'\033[2m'; RESET=$'\033[0m'

ok()   { printf '  %s✓%s %s\n' "$GREEN" "$RESET" "$1"; }
warn() { printf '  %s!%s %s\n' "$YELLOW" "$RESET" "$1"; }
bad()  { printf '  %s✗%s %s\n' "$RED" "$RESET" "$1"; }

command -v openssl >/dev/null || { echo "нужен openssl: sudo apt install openssl"; exit 1; }

echo
echo "${BOLD}1. Хранилище сертификатов${RESET}"

BUNDLE=""
for candidate in \
  /etc/ssl/certs/ca-certificates.crt \
  /etc/pki/tls/certs/ca-bundle.crt \
  /etc/ssl/ca-bundle.pem
do
  if [[ -f "$candidate" ]]; then BUNDLE="$candidate"; break; fi
done

if [[ -z "$BUNDLE" ]]; then
  bad "системный набор корневых сертификатов не найден"
  echo
  echo "  ${BOLD}Причина найдена.${RESET} В системе нет пакета ca-certificates."
  echo "  Исправление:"
  echo "      sudo apt update && sudo apt install -y ca-certificates"
  echo "      sudo update-ca-certificates"
  echo
  exit 1
fi

COUNT="$(grep -c 'BEGIN CERTIFICATE' "$BUNDLE" 2>/dev/null || echo 0)"
ok "набор: ${BUNDLE} (${COUNT} сертификатов)"
if [[ "$COUNT" -lt 50 ]]; then
  warn "подозрительно мало — набор может быть обрезанным или устаревшим"
fi

if [[ -n "${SSL_CERT_FILE:-}" ]]; then
  warn "переменная SSL_CERT_FILE=${SSL_CERT_FILE} переопределяет набор и для curl, и для Go"
fi

echo
echo "${BOLD}2. Соединение с ${HOST}:${PORT}${RESET}"

CHAIN="$(mktemp)"; trap 'rm -f "$CHAIN"' EXIT

if ! timeout 20 openssl s_client -connect "${HOST}:${PORT}" -servername "$HOST" \
     -showcerts </dev/null >"$CHAIN" 2>&1; then
  if ! grep -q 'BEGIN CERTIFICATE' "$CHAIN"; then
    bad "соединение не установлено"
    echo
    echo "  Похоже на проблему сети, а не сертификатов. Проверьте доступность:"
    echo "      ping -c2 ${HOST}"
    echo
    tail -5 "$CHAIN" | sed 's/^/      /'
    exit 1
  fi
fi

VERIFY_LINE="$(grep -E '^Verify return code:' "$CHAIN" | tail -1)"
LEAF_ISSUER="$(grep -E '^issuer=' "$CHAIN" | head -1 | sed 's/^issuer=//')"
ROOT_LINE="$(grep -E '^ *[0-9]+ s:' "$CHAIN" | tail -1)"
ROOT_ISSUER="$(grep -E '^ *[0-9]+ i:' "$CHAIN" | tail -1 | sed 's/^ *[0-9]* i://')"

echo "  цепочка получена"
[[ -n "$LEAF_ISSUER" ]] && echo "    сертификат выдан:  ${BOLD}${LEAF_ISSUER}${RESET}"
[[ -n "$ROOT_ISSUER" ]] && echo "    верхний в цепочке: ${ROOT_ISSUER}"
[[ -n "$VERIFY_LINE" ]] && echo "    ${VERIFY_LINE}"

echo
echo "${BOLD}3. Диагноз${RESET}"

ALL_ISSUERS="$(grep -E '^ *[0-9]+ i:|^issuer=' "$CHAIN" | tr 'A-Z' 'a-z')"

if grep -q 'Verify return code: 0 (ok)' "$CHAIN"; then
  ok "openssl доверяет цепочке — системный набор в порядке"
  echo
  echo "  Если curl при этом всё равно ругается, он использует другой набор."
  echo "  Проверьте:  curl -v https://${HOST}/me 2>&1 | grep -i 'CAfile\\|CApath'"
  echo
  exit 0
fi

# Корень от Минцифры: типичная ситуация для российских сервисов.
if echo "$ALL_ISSUERS" | grep -qE 'russian trusted|ministry of digital|минцифры'; then
  bad "цепочка завершается российским корневым центром, которого нет в наборе Mozilla"
  echo
  echo "  ${BOLD}Это не поломка.${RESET} Сертификат выпущен УЦ Минцифры России, и в"
  echo "  стандартный набор ca-certificates он не входит. Нужно добавить его корень"
  echo "  в доверенные."
  echo
  echo "  ${YELLOW}Прежде чем устанавливать, стоит понимать цену.${RESET} Доверенный"
  echo "  корневой центр может выпустить сертификат на ЛЮБОЙ домен, и система"
  echo "  такому сертификату поверит. Устанавливать корень на всю систему —"
  echo "  решение с последствиями за пределами этого проекта."
  echo
  echo "  ${BOLD}Вариант A (узкий, рекомендуется):${RESET} доверие только для бота."
  echo "    mkdir -p certs"
  echo "    curl -k -o certs/russian_trusted_root_ca.cer \\"
  echo "      https://gu-st.ru/content/lib/russian_trusted_root_ca.cer"
  echo "    openssl x509 -inform DER -in certs/russian_trusted_root_ca.cer \\"
  echo "      -out certs/russian_trusted_root_ca.pem 2>/dev/null || \\"
  echo "      cp certs/russian_trusted_root_ca.cer certs/russian_trusted_root_ca.pem"
  echo
  echo "    Затем в .env:"
  echo "        MAX_CA_FILE=\${PWD}/certs/russian_trusted_root_ca.pem"
  echo
  echo "    MAX_CA_FILE добавляет этот корень к системному набору и действует"
  echo "    ТОЛЬКО на соединение с MAX. Переменная SSL_CERT_FILE тоже сработала"
  echo "    бы, но она влияет на весь процесс: доверие, выданное ради MAX,"
  echo "    распространилось бы и на Core Backend, и на любой другой HTTPS."
  echo
  echo "    Для проверки тем же curl:"
  echo "        curl --cacert certs/russian_trusted_root_ca.pem \\"
  echo "          https://${HOST}/me -H \"Authorization: \$MAX_BOT_TOKEN\""
  echo
  echo "    ${DIM}certs/ добавьте в .gitignore: это локальная настройка машины.${RESET}"
  echo
  echo "  ${BOLD}Вариант B (на всю систему):${RESET} только если понимаете последствия."
  echo "    sudo cp certs/russian_trusted_root_ca.pem /usr/local/share/ca-certificates/russian_trusted_root_ca.crt"
  echo "    sudo update-ca-certificates"
  echo
  exit 2
fi

# Корпоративный перехват TLS.
if echo "$ALL_ISSUERS" | grep -qE 'kaspersky|eset|avast|bitdefender|fortinet|zscaler|palo alto|netskope|proxy|firewall'; then
  bad "TLS перехватывается прокси или антивирусом"
  echo
  echo "  В цепочке виден центр средства защиты, а не настоящий УЦ MAX."
  echo "  Такой перехват подменяет сертификаты всех сайтов, и его корень"
  echo "  обычно уже стоит в хранилище Windows, но не в WSL."
  echo
  echo "  Что делать: экспортируйте корень перехватчика и добавьте его"
  echo "  так же, как описано в docs/MAX_SETUP.md, либо отключите перехват"
  echo "  для домена ${HOST} в настройках защитного ПО."
  echo
  exit 2
fi

bad "цепочка не проходит проверку, и корень не опознан"
echo
echo "  Первое, что стоит попробовать — обновить набор сертификатов:"
echo "      sudo apt update && sudo apt install --reinstall -y ca-certificates"
echo "      sudo update-ca-certificates"
echo
echo "  Если не помогло, покажите эту цепочку целиком:"
echo "      openssl s_client -connect ${HOST}:${PORT} -servername ${HOST} </dev/null 2>&1 | head -40"
echo
exit 2
