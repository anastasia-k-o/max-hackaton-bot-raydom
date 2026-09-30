# syntax=docker/dockerfile:1

# Сборка: Vite вшивает переменные VITE_* в код, поэтому они передаются как
# аргументы сборки, а не как переменные окружения контейнера.
FROM node:20-alpine AS build
WORKDIR /app
COPY package.json package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY . .
ARG VITE_MAX_BOT_URL=""
ARG VITE_API_URL="/api"
# false — мини-приложение работает с Core Backend; true — на встроенных моках.
ARG VITE_USE_MOCKS="false"
# true — вне MAX входить без initData (нужен DEV_MODE=true у Core Backend).
ARG VITE_DEV_LOGIN="false"
ENV VITE_MAX_BOT_URL=${VITE_MAX_BOT_URL} VITE_API_URL=${VITE_API_URL} VITE_USE_MOCKS=${VITE_USE_MOCKS} \
    VITE_DEV_LOGIN=${VITE_DEV_LOGIN}
RUN npm run build

# Раздача: nginx отдаёт статику и проксирует /api в Core Backend, поэтому
# мини-приложение и API живут на одном адресе и CORS не нужен.
FROM nginx:1.27-alpine
COPY nginx.conf /etc/nginx/conf.d/default.conf
COPY --from=build /app/dist /usr/share/nginx/html
EXPOSE 80
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD wget -q -O /dev/null http://127.0.0.1/ || exit 1
