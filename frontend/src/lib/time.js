// RFC 3339 со смещением города: бот печатает время в смещении из starts_at, UTC («Z») нельзя.

import { CITIES } from './cities.js';

const pad = (n) => String(n).padStart(2, '0');

export function toCityIso(ms, city = CITIES[0]) {
  const d = new Date(ms + city.offsetMin * 60000);
  return `${d.getUTCFullYear()}-${pad(d.getUTCMonth() + 1)}-${pad(d.getUTCDate())}T${pad(d.getUTCHours())}:${pad(d.getUTCMinutes())}:00${city.offset}`;
}

export const fromCityInputs = (date, time, city = CITIES[0]) => `${date}T${time}:00${city.offset}`;

export function toCityInputs(iso, city = CITIES[0]) {
  const s = toCityIso(Date.parse(iso), city);
  return { date: s.slice(0, 10), time: s.slice(11, 16) };
}

export const isRfc3339 = (s) => typeof s === 'string' && /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}(:\d{2}(\.\d+)?)?(Z|[+-]\d{2}:\d{2})$/.test(s) && !Number.isNaN(Date.parse(s));

export const minutesUntil = (iso) => (Date.parse(iso) - Date.now()) / 60000;
