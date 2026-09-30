import { CITIES } from './cities.js';

// Даты — в поясе мероприятия, как у бота, а не в поясе браузера.
const cache = new Map();
const fmt = (opts, tz) => {
  const key = JSON.stringify(opts) + tz;
  if (!cache.has(key)) cache.set(key, new Intl.DateTimeFormat('ru-RU', { ...opts, timeZone: tz }));
  return cache.get(key);
};
const tzOf = (tz) => tz || CITIES[0].timezone;

export const fmtDate = (iso, tz) => fmt({ day: 'numeric', month: 'long', weekday: 'short' }, tzOf(tz)).format(new Date(iso));
export const fmtTime = (iso, tz) => fmt({ hour: '2-digit', minute: '2-digit' }, tzOf(tz)).format(new Date(iso));
export const fmtShort = (iso, tz) => fmt({ day: 'numeric', month: 'short' }, tzOf(tz)).format(new Date(iso));
export const fmtDay = (iso, tz) => fmt({ day: 'numeric' }, tzOf(tz)).format(new Date(iso));
export const fmtMonth = (iso, tz) => fmt({ month: 'short' }, tzOf(tz)).format(new Date(iso)).replace('.', '');
export const fmtDateTime = (iso, tz) => `${fmtDate(iso, tz)}, ${fmtTime(iso, tz)}`;

export const fmtDuration = (min) => {
  const h = Math.floor(min / 60);
  const m = min % 60;
  if (!h) return `${m} мин`;
  return m ? `${h} ч ${m} мин` : `${h} ч`;
};

export const plural = (n, one, few, many) => {
  const n10 = n % 10, n100 = n % 100;
  if (n10 === 1 && n100 !== 11) return one;
  if (n10 >= 2 && n10 <= 4 && (n100 < 10 || n100 >= 20)) return few;
  return many;
};

/** @param {{capacity: number|null, registered_count: number}} e */
export const seatsLeft = (e) => (e.capacity == null ? Infinity : Math.max(0, e.capacity - e.registered_count));

export const seatsText = (e) => {
  if (e.capacity == null) return 'Без ограничения мест';
  const left = seatsLeft(e);
  if (left === 0) return 'Мест нет';
  return `Осталось ${left} из ${e.capacity}`;
};

export const pct = (a, b) => (b ? Math.round((a / b) * 100) : 0);

export const initials = (name = '') => name.split(' ').filter(Boolean).map((p) => p[0]).slice(0, 2).join('').toUpperCase();
