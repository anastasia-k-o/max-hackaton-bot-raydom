// Вход: POST /auth/max, подпись initData проверяет Go-бэкенд; в режиме разработки init_data='dev' — демо-пользователь.
// TODO: base URL from env — задать VITE_API_URL при деплое.
// TODO: backend — загрузка обложки (cover_url), тема в профиле (опционально).
// Моки или Go-бэкенд (backend/). VITE_USE_MOCKS=false — ходить в бэкенд; по умолчанию моки,
// чтобы `npm run dev` работал без бэкенда. Docker и `make run-front` в корне выставляют false.
export const USE_MOCKS = (import.meta.env.VITE_USE_MOCKS ?? 'true') !== 'false';
// Вход без MAX в собранном приложении: вне MAX оно входит как DEMO_MAX_USER_ID бэкенда
// (только при DEV_MODE=true на бэкенде). Для проверки, пока мини-приложение не привязано к боту.
export const DEV_LOGIN = import.meta.env.VITE_DEV_LOGIN === 'true';
export const API_BASE_URL = (import.meta.env.VITE_API_URL || '/api').replace(/\/+$/, '');
export const REQUEST_TIMEOUT_MS = 5000;
