# Проверка через Postman

Три сценария, от полностью локального до настоящего бота MAX. Каждый доведён
до конца: что нажать, что должно прийти в ответ, и что делать, если пришло
другое.

---

## Подготовка

### Импорт

В Postman: **Import** → перетащите оба файла:

```
postman/MAX-Bot.postman_collection.json
postman/Local.postman_environment.json
```

Выберите окружение **MAX Bot — Local** в правом верхнем углу. Без этого
переменные вроде `{{base_url}}` не подставятся.

### Папки коллекции

| Папка | Назначение |
|---|---|
| **Health** | пробы состояния — начните отсюда |
| **Notifications** | Core Backend → Bot: все 8 типов + проверки ошибок |
| **MAX Webhook Simulation** | MAX → Bot: имитация событий и нажатий кнопок |
| **Mock MAX Inspection** | что бот «отправил» в MAX |
| **Stub Core Inspection** | что бот передал бы в Core Backend |

### Переменные окружения

| Переменная | Значение по умолчанию |
|---|---|
| `base_url` | `http://localhost:8080` |
| `internal_api_key` | `change-me` |
| `max_webhook_secret` | `dev-webhook-secret` |
| `max_user_id` | `123456789` |
| `event_id` | `event_42` |
| `registration_id` | `registration_15` |
| `event_starts_at` | `2026-09-22T19:00:00+03:00` |
| `offer_expires_at` | `2026-09-22T16:00:00+03:00` |
| `mini_app_url` | `https://afisha.example.ru/app/event_42` |

`request_id` и `callback_id` генерируются автоматически перед каждым
запросом — иначе защита от дублей превратила бы второе нажатие «Send» в
непонятный `duplicate`.

Значения `internal_api_key` и `max_webhook_secret` должны совпадать с тем, что
в `.env` бота. Значения по умолчанию совпадают с тем, что подставляет
`make run` без `.env`.

---

## Сценарий 1 — полностью на заглушках

Ни токена, ни backend, ни интернета.

### Запуск

```bash
make run
```

Или явно:

```bash
APP_ENV=dev MAX_MODE=mock CORE_MODE=stub \
INTERNAL_API_KEY=change-me MAX_WEBHOOK_SECRET=dev-webhook-secret \
go run ./cmd/bot
```

### Шаг 1. Health

**Health → GET /health**

```json
{ "status": "ok", "version": "dev", "uptime_seconds": 3 }
```

**Health → GET /ready**

```json
{
  "status": "ready",
  "dependencies": [
    { "name": "max",  "mode": "mock", "status": "ok" },
    { "name": "core", "mode": "stub", "status": "ok" }
  ]
}
```

Видно `mock` и `stub` — заглушки активны.

### Шаг 2. Отправьте уведомление

**Notifications → 3. confirmation_required**

Ожидается **202**:

```json
{
  "request_id": "req_3f2a...",
  "status": "sent",
  "message_id": "mock-mid-1"
}
```

### Шаг 3. Посмотрите, что «отправилось» в MAX

**Mock MAX Inspection → GET сообщения MockMAX**

```json
{
  "count": 1,
  "messages": [
    {
      "seq": 1,
      "kind": "sent",
      "message_id": "mock-mid-1",
      "user_id": 123456789,
      "text": "❓ Подтвердите участие\n\n«Йога в парке» начинается сегодня в 19:00.",
      "keyboard": [
        [
          { "kind": "callback", "text": "Приду",           "payload": "v1|confirm|registration_15|event_42" },
          { "kind": "callback", "text": "Не смогу прийти", "payload": "v1|cancel|registration_15|event_42" }
        ]
      ]
    }
  ]
}
```

Обратите внимание на разделение: `text` — это подпись для человека, `payload` —
команда для бота. Текст кнопки можно переписать перед демо, и ничего не
сломается.

### Шаг 4. Пройдите остальные типы

**Notifications**, запросы 1–8. После каждого смотрите
**Mock MAX Inspection → GET сообщения MockMAX**.

Стоит обратить внимание:

- `6. waitlist_offer` — в тексте появляется «Предложение действует до…»;
- `7. event_updated` — перечислены изменения и сообщение организатора,
  кнопок действий нет;
- `8. event_cancelled` — только ссылка на афишу, подтверждать нечего.

### Шаг 5. Проверьте ошибки

**Notifications → 9. Ошибка: неизвестный тип** → **400**, и в `details`
перечислены все проблемные поля сразу.

**Notifications → 10. Ошибка: нет API-ключа** → **401**.

### Шаг 6. Идемпотентность

**Notifications → 11. Идемпотентность** — отправьте **дважды**.

Первый раз: `202`, `"status": "sent"`.
Второй раз: `200`, `"status": "duplicate"`, `"duplicate": true`.

В `GET /dev/max/messages` при этом добавится только одно сообщение.

> Ограничение: дедупликация живёт в памяти процесса с TTL 30 минут. После
> перезапуска бота тот же `request_id` снова пройдёт как новый.

---

## Сценарий 2 — MockMAX плюс имитация webhook

Полный цикл «уведомление → нажатие → действие → обновление сообщения» без
единого внешнего вызова. Это сценарии A и B из технического задания.

### Шаг 1. Очистите журналы

**Mock MAX Inspection → DELETE очистить журнал MockMAX**
**Stub Core Inspection → DELETE очистить журнал StubCore**

### Шаг 2. Отправьте уведомление

**Notifications → 3. confirmation_required** → `202`.

### Шаг 3. Посмотрите payload кнопки

**Mock MAX Inspection → GET сообщения MockMAX**

Запомните payload кнопки «Приду»: `v1|confirm|registration_15|event_42`.

### Шаг 4. Имитируйте нажатие

**MAX Webhook Simulation → 3. callback «Приду»**

Тело запроса повторяет то, что реально присылает MAX:

```json
{
  "update_type": "message_callback",
  "timestamp": 1758556800000,
  "chat_id": 555,
  "callback": {
    "callback_id": "cb-...",
    "payload": "v1|confirm|registration_15|event_42",
    "user": { "user_id": 123456789, "first_name": "Алексей", "name": "Алексей" }
  },
  "message": {
    "sender": { "user_id": 1, "is_bot": true },
    "recipient": { "chat_id": 555, "user_id": 123456789, "chat_type": "dialog" },
    "body": { "mid": "mock-mid-1", "seq": 1, "text": "Подтвердите участие" }
  }
}
```

Ожидается **200**:

```json
{ "status": "handled", "update_type": "message_callback", "result": "ok" }
```

### Шаг 5. Проверьте StubCoreGateway

**Stub Core Inspection → GET действия StubCore**

```json
{
  "count": 1,
  "actions": [
    {
      "seq": 1,
      "kind": "confirm_registration",
      "registration_id": "registration_15",
      "event_id": "event_42",
      "max_user_id": 123456789,
      "request_id": "req-9217b3805210af54",
      "occurred_at": "2026-09-22T16:00:00Z"
    }
  ]
}
```

Это ровно то, что получит ваш Core Backend после `CORE_MODE=http`.

`max_user_id` здесь взят из `callback.user.user_id` — того, кто нажал кнопку, —
а не из payload. Payload проходит через клиент и доверять ему в вопросе
идентичности нельзя.

### Шаг 6. Проверьте обновление сообщения

**Mock MAX Inspection → GET сообщения MockMAX**

Последняя запись:

```json
{
  "seq": 2,
  "kind": "callback_answer",
  "callback_id": "cb-...",
  "notification": "Участие подтверждено",
  "text": "✅ Участие подтверждено\n\nЙога в парке\n22 сентября, 19:00",
  "keyboard": [
    [ { "kind": "link", "text": "Открыть афишу", "url": "https://afisha.example.ru/app" } ]
  ]
}
```

Главное: в клавиатуре осталась только ссылка. Кнопки «Приду» и «Не смогу»
сняты, и повторно вызвать действие уже нельзя. Это требование ТЗ «старые
action-кнопки больше не должны провоцировать повторное действие».

### Шаг 7. Остальные кнопки

Запросы 4, 5, 6 в папке **MAX Webhook Simulation** — отмена, занять место,
отказаться. Каждый раз смотрите оба журнала.

Для `wl_accept` заменяющее сообщение будет «✅ Место ваше — Вы записаны на
«Йога в парке».»

### Шаг 8. Устойчивость

| Запрос | Ожидаемо |
|---|---|
| **7. Неверный webhook secret** | `401`, в журналах ничего не появилось |
| **8. Устаревшая кнопка** | `200`, `result: "bad_payload"`, StubCore пуст, пользователю «Эта кнопка больше не работает» |
| **9. Текст кнопки как команда** | `200`, `bad_payload` — текст командой не является |
| **10. Неизвестный тип события** | `200`, `status: "ignored"` — MAX не будет повторять доставку |

Ни один из них не должен давать 500.

### То же самое одной командой

```bash
make smoke
```

Скрипт проходит весь сценарий на отдельном порту и печатает результат каждого
шага. Удобно для CI и для быстрой проверки после правок.

---

## Сценарий 3 — настоящий MAX плюс заглушка Core

Самый интересный режим: реальные сообщения в мессенджере, но backend ещё
заглушка. Именно так вы увидите бота глазами пользователя, не дожидаясь Core
Backend.

### Шаг 1. Токен

Получите токен у [@MasterBot](https://max.ru/MasterBot) в MAX.
Подробности — [MAX_SETUP.md](MAX_SETUP.md), раздел 2.

### Шаг 2. Правка `.env`

```env
APP_ENV=dev
HTTP_PORT=8080

MAX_MODE=real
MAX_BOT_TOKEN=<ваш токен>
MAX_WEBHOOK_SECRET=<сгенерируйте: openssl rand -hex 32>

CORE_MODE=stub

INTERNAL_API_KEY=change-me
MINI_APP_URL=https://afisha.example.ru/app
```

Изменились только `MAX_MODE`, `MAX_BOT_TOKEN` и `MAX_WEBHOOK_SECRET`.
Бизнес-код не трогали.

### Шаг 3. Запуск и проверка токена

```bash
make run
```

В логе должно появиться:

```json
{"level":"INFO","msg":"max token verified","bot_user_id":4242,"bot_username":"..."}
```

Если вместо этого процесс завершился с «MAX rejected the bot token» — токен
неверный. Бот намеренно не стартует со сломанной конфигурацией.

**Health → GET /ready** теперь показывает `"mode": "real"`.

### Шаг 4. Туннель

```bash
ngrok http 8080
```

Скопируйте `https://...ngrok-free.app`.

MAX доставляет webhook только на HTTPS и только на порты 80, 8080, 443, 8443
или 16384–32383.

### Шаг 5. Подписка на webhook

```bash
curl -X POST https://platform-api2.max.ru/subscriptions \
  -H "Authorization: $MAX_BOT_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "url": "https://ВАШ-АДРЕС.ngrok-free.app/webhooks/max",
    "secret": "ВАШ_MAX_WEBHOOK_SECRET",
    "update_types": ["bot_started", "message_created", "message_callback"],
    "version": "0.0.1"
  }'
```

Проверка:

```bash
curl -s https://platform-api2.max.ru/subscriptions -H "Authorization: $MAX_BOT_TOKEN" | jq
```

### Шаг 6. Узнайте свой `max_user_id`

Откройте диалог с ботом в MAX и напишите что угодно. Бот ответит fallback-
сообщением, а в его логах появится:

```json
{"level":"INFO","msg":"http request","update_type":"message_created","max_user_id":987654321,...}
```

Это ваш ID. Впишите его в переменную окружения Postman `max_user_id`.

Заодно это подтверждает, что webhook работает в обе стороны.

### Шаг 7. Отправьте себе сообщение

В Postman поменяйте `internal_api_key` и `max_webhook_secret` на значения из
своего `.env`, затем:

**Notifications → 3. confirmation_required** → **Send**

Сообщение придёт **в MAX, на телефон**:

```
❓ Подтвердите участие

«Йога в парке» начинается сегодня в 19:00.

[Приду]  [Не смогу прийти]
```

### Шаг 8. Нажмите кнопку в мессенджере

Нажмите «Приду» в MAX.

Произойдёт следующее:

1. MAX отправит `message_callback` на ваш ngrok-адрес.
2. Бот проверит секрет, разберёт payload, вызовет StubCoreGateway.
3. Бот ответит на callback и заменит сообщение.
4. **Сообщение в мессенджере изменится** на «✅ Участие подтверждено», кнопки
   действий исчезнут.

### Шаг 9. Проверьте StubCore

**Stub Core Inspection → GET действия StubCore**

Там будет `confirm_registration` с вашим настоящим `max_user_id`.

Это и есть ключевая демонстрация: **настоящий MAX работает, а Core Backend
всё ещё заглушка**. Когда появится настоящий backend, поменяется только
`CORE_MODE=http` — ни строки бизнес-кода.

---

## Сценарий 4 — подключение настоящего Core Backend

Когда backend будет готов:

```env
CORE_MODE=http
CORE_BASE_URL=https://api.afisha.example.ru/v1
CORE_API_KEY=<ключ>
```

Перезапустите бота. Проверьте **Health → GET /ready**: у зависимости `core`
должно быть `"mode": "http"` и `"status": "ok"` (для этого backend должен
отвечать на `GET /health`).

Дальше всё как в сценарии 3, но действия уходят в настоящий backend. Заметьте:
`/dev/core/actions` теперь вернёт 404 — StubCore больше нет, и инспектировать
нечего.

Контракт, который должен реализовать backend, — в
[INTEGRATION_MINIAPP.md](INTEGRATION_MINIAPP.md), раздел 6.

---

## Если что-то идёт не так

### 401 на `/api/v1/notifications`

`internal_api_key` в Postman не совпадает с `INTERNAL_API_KEY` в `.env`.

### 401 на `/webhooks/max`

`max_webhook_secret` в Postman не совпадает с `MAX_WEBHOOK_SECRET` в `.env`.
Это **другой** секрет, не тот, что выше.

### 404 на `/dev/...`

Одно из трёх: `APP_ENV` не `dev`; либо `MAX_MODE=real` (MockMAX нет — нечего
показывать); либо `CORE_MODE=http` (StubCore нет).

### 400 с непонятными деталями

Смотрите `error.details` — там перечислены все проблемные поля сразу, с
указанием, что именно не так. Самое частое: `starts_at` без смещения часового
пояса.

### `duplicate` вместо `sent`

Этот `request_id` уже использовался. В коллекции он генерируется автоматически;
если вы правили тело вручную — поменяйте `request_id`.

### Сообщение не пришло в настоящий MAX

1. `max_user_id` верный и числовой?
2. Вы открывали диалог с ботом? MAX не даёт писать первым.
3. Смотрите логи бота: `notification sent` или `max send failed`.

Полный troubleshooting — [MAX_SETUP.md](MAX_SETUP.md), раздел 9.
