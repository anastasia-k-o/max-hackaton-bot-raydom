import { toCityIso } from '../../lib/time.js';
import { CITIES } from '../../lib/cities.js';

export const CITY = CITIES[0];
const H = 3600 * 1000;
const D = 24 * H;

export function at(days, hour, min = 0) {
  const base = toCityIso(Date.now() + days * D).slice(0, 10);
  return `${base}T${String(hour).padStart(2, '0')}:${String(min).padStart(2, '0')}:00${CITY.offset}`;
}
export const inMin = (m) => toCityIso(Date.now() + m * 60000);
export const ago = (h) => toCityIso(Date.now() - h * H);
