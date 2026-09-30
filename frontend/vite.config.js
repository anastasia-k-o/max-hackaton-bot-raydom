import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

export default defineConfig({
  plugins: [react()],
  base: '/', // абсолютные пути: ссылки из бота вида /app/{event_id} должны находить ассеты
  // /api → Go-бэкенд на том же адресе, что и страница: CORS не нужен.
  server: { port: 5173, open: true, proxy: { '/api': process.env.VITE_PROXY_TARGET || 'http://localhost:8090' } },
  preview: { proxy: { '/api': process.env.VITE_PROXY_TARGET || 'http://localhost:8090' } },
});
