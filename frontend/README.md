# Рядом — фронтенд мини-приложения MAX (десктоп)

React 18 + Vite, чистый CSS, без UI-библиотек. Светлая и тёмная тема (класс `.theme-dark` на `<html>`).

## Запуск

```bash
cd frontend
npm install
npm run dev        # http://localhost:5173 — демо-режим с mock-бэкендом
npm run build      # сборка в dist/
```

| Переменная | Назначение |
|---|---|
| `VITE_API_URL` | Корень Go-бэкенда (по умолчанию `/api`). Используется, когда в `src/api/config.js` выставлено `USE_MOCKS = false`. |
| `VITE_MAX_BOT_URL` | Ссылка на диалог с ботом в MAX (кнопка «Открыть бота» в баннере и на экране «Откройте в MAX»). |

Мини-приложение **никогда не обращается к боту** (`bot/`): его API закрыт серверными секретами, CORS нет.

## Режимы

- **Моки** (`USE_MOCKS = true` в `src/api/config.js`): запросы обрабатывает `src/api/mocks/handlers.js` — те же пути, коды ошибок и бизнес-правила (места, лист ожидания, предложение места с таймером, закрытие записи за 1 ч, подтверждение за 6 ч, модерация по стоп-словам, 403 для неверифицированных). Состояние хранится в `localStorage`. В профиле есть блок «Демо-режим»: сбросить верификацию, имитировать заблокированного бота, сбросить данные.
- **HTTP** (`USE_MOCKS = false`): вход только из MAX — `initData` уходит на `POST /auth/max`, подпись проверяет сервер. Вне MAX показывается экран «Откройте афишу в MAX».

## Контракт API

Имена полей — snake_case, 1:1 с контрактом бэкенда; интерфейс работает с DTO напрямую. Типы — `src/types/index.js` (JSDoc), пути — `src/api/endpoints.js`.

| Источник | Метод и путь | Используется |
|---|---|---|
| **CONFIRMED** — код `bot/internal/core/httpgw` | `POST /registrations/{registration_id}/confirm` | «Приду» |
| CONFIRMED | `POST /registrations/{registration_id}/cancel` (+ необязательный `cancel_reason`) | Отказ, выход из очереди |
| CONFIRMED | `POST /waitlist-offers/{registration_id}/accept` | «Занять место» |
| CONFIRMED | `POST /waitlist-offers/{registration_id}/decline` | «Отказаться» от места |
| **DOCUMENTED** — `bot/docs/INTEGRATION_MINIAPP.md §7` | `POST /registrations` `{event_id}` → 201 | Запись или лист ожидания |
| **PROPOSED** — `// TODO: backend endpoint missing` | `POST /auth/max`, `GET/PATCH /me`, `POST /me/verification`, `POST /me/consent`, `GET/PUT /me/interests`, `GET /me/registrations`, `GET /me/events`, `GET /categories`, `GET/POST /events`, `GET/PATCH /events/{id}`, `POST /events/{id}/cancel`, `POST /events/{id}/messages`, `GET /events/{id}/report`, `POST /events/{id}/views`, `POST /events/{id}/complaints`, `GET /recommendations`, `POST /ml/suggest-tags` | Остальное |

Правила, взятые из бота:

- тело действий с записью — `{registration_id, event_id, max_user_id, request_id, occurred_at}`, ответ `{status: accepted|noop, event?}`;
- ошибки — конверт `{error:{code,message,request_id,details[]}}`; коды `event_cancelled`, `registration_cancelled`, `offer_expired`, `not_found`, `conflict`, `forbidden`, `invalid_request`, `upstream_unavailable` переводятся в тексты в `src/api/errors.js`, `details` подсвечивают поля формы;
- `X-Request-Id` на каждом запросе (ключ идемпотентности), таймаут 5 с;
- время — RFC 3339 **со смещением города** (`2026-09-22T19:00:00+03:00`): бот печатает время в том смещении, что пришло. Даты показываются в поясе мероприятия, а не браузера (`src/lib/time.js`);
- id — строки без `|`; `title` ≤80 (ТЗ), `address` ≤512, `organizer_message` ≤2000;
- ссылка «Открыть афишу» из бота — путь `/app/{event_id}` → открывается карточка (нужен SPA-фолбэк на хостинге: все пути → `index.html`).

## Что где

| Раздел | Файл |
|---|---|
| Каталог (фильтры и сортировка на сервере, «Показать ещё») | `src/pages/Catalog.jsx` |
| Для вас (онбординг ≥3 тегов, рекомендации с `reason`) | `src/pages/ForYou.jsx` |
| Карточка (только верифицированным; все состояния кнопки + «место освободилось») | `src/pages/EventPage.jsx` |
| Мои записи: подтверждённые / ждут подтверждения / лист ожидания / отменённые / прошедшие | `src/pages/cabinet/MyEvents.jsx` |
| Создание, редактирование (`?edit=`), «Создать похожее» (`?from=`) | `src/pages/cabinet/CreateEvent.jsx` |
| Отчётность, сообщение участникам, отмена с сообщением | `src/pages/cabinet/Reports.jsx` |
| Профиль: верификация, связь с ботом, роль автора, тема, район, уведомления, интересы | `src/pages/cabinet/Profile.jsx` |

```
src/
  api/          config.js (USE_MOCKS, API_BASE_URL, все TODO интеграции), client.js (fetch, токен, X-Request-Id, ApiError),
                errors.js, endpoints.js, auth / profile / events / registrations / reports / recommendations,
                mocks/ (categories, users, events, registrations, moderation, handlers) — удаляется одной папкой
  types/        JSDoc-типы DTO с пометками CONFIRMED / DOCUMENTED / PROPOSED
  store/        theme.jsx (.theme-dark), session.jsx (пользователь с сервера), events.jsx
                (справочники, мои записи, действия), ui.jsx (тосты, окно верификации), useApi.js
  components/   EventCard, Badge, VerificationGate, ThemeToggle, OfferBanner, BotBanner,
                ConsentModal, OpenInMax, CancelModal, Modal (удержание фокуса), …
  lib/          cities.js (CITIES), max.js (MAX Bridge), time.js, format.js, router.js, calendar.js, categories.js
  styles/       theme.css (--color-bg/fg/accent/accent-2/muted), app.css
```

## Тема

Базовые переменные: `--color-bg`, `--color-fg`, `--color-accent`, `--color-accent-2`, `--color-muted` (+ `--color-surface`, `--color-on-accent`, `--color-danger` только для ошибок). Остальное выводится через `color-mix()`. Выбор хранится в `localStorage` (`afisha.theme`) — на бэкенде поля темы нет.
