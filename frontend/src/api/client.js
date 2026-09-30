// Единая точка запросов: при USE_MOCKS ответы даёт mocks/handlers.js, страницы об этом не знают.
import { ApiError } from './errors.js';
import { API_BASE_URL, REQUEST_TIMEOUT_MS, USE_MOCKS } from './config.js';

let token = null;
export const setToken = (t) => { token = t; };

let mockServer = null;
async function mock() {
  if (!mockServer) mockServer = await import('./mocks/handlers.js');
  return mockServer;
}

/** Ключ идемпотентности (как request_id у бота). */
export function newRequestId() {
  const rnd = (globalThis.crypto?.randomUUID?.() || Math.random().toString(16).slice(2)).replace(/-/g, '').slice(0, 16);
  return `req-${rnd}`;
}

/**
 * @param {'GET'|'POST'|'PUT'|'PATCH'|'DELETE'} method
 * @param {string} path
 * @param {{body?: any, query?: Record<string, any>, requestId?: string}} [opts]
 */
export async function request(method, path, { body, query, requestId } = {}) {
  const rid = requestId || newRequestId();
  const qs = query ? toQuery(query) : '';

  if (USE_MOCKS) {
    const m = await mock();
    return m.handle({ method, path, query: Object.fromEntries(new URLSearchParams(qs.slice(1))), body, token, requestId: rid });
  }

  const ctrl = new AbortController();
  const timer = setTimeout(() => ctrl.abort(), REQUEST_TIMEOUT_MS);
  let res;
  try {
    res = await fetch(`${API_BASE_URL}${path}${qs}`, {
      method,
      signal: ctrl.signal,
      headers: {
        Accept: 'application/json',
        ...(body !== undefined ? { 'Content-Type': 'application/json' } : {}),
        ...(token ? { Authorization: `Bearer ${token}` } : {}),
        'X-Request-Id': rid,
      },
      body: body !== undefined ? JSON.stringify(body) : undefined,
    });
  } catch (e) {
    throw new ApiError({ status: 0, code: e.name === 'AbortError' ? 'timeout' : 'upstream_unavailable', message: e.message, requestId: rid });
  } finally {
    clearTimeout(timer);
  }

  if (res.status === 204) return null;
  const text = await res.text();
  let data = null;
  try { data = text ? JSON.parse(text) : null; } catch { /* не JSON */ }

  if (res.ok) return data;
  const err = data?.error || {};
  throw new ApiError({
    status: res.status,
    code: err.code || (res.status >= 500 ? 'upstream_unavailable' : 'conflict'),
    message: err.message || res.statusText,
    requestId: err.request_id || res.headers.get('X-Request-Id') || rid,
    details: err.details || [],
  });
}

function toQuery(q) {
  const p = new URLSearchParams();
  Object.entries(q).forEach(([k, v]) => {
    if (v === undefined || v === null || v === '' || v === false) return;
    if (Array.isArray(v)) { if (v.length) p.set(k, v.join(',')); } else p.set(k, String(v));
  });
  const s = p.toString();
  return s ? `?${s}` : '';
}
