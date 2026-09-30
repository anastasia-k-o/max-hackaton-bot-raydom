// Мок бэкенда: те же пути и бизнес-правила; состояние — в localStorage.
import { ApiError } from '../errors.js';
import { CONFIRMED, DOCUMENTED, PROPOSED } from '../endpoints.js';
import { isRfc3339, toCityIso } from '../../lib/time.js';
import { cityById } from '../../lib/cities.js';
import * as categories from './categories.js';
import * as users from './users.js';
import * as events from './events.js';
import * as registrations from './registrations.js';
import * as moderation from './moderation.js';

const seed = { ...categories, ...users, ...events, ...registrations, ...moderation };

const DB_KEY = 'afisha.mock.db.v3';
const TTL_MS = 12 * 3600 * 1000; // даты моков относительны «сейчас»
const CLOSE_MIN = 60;            // запись и очередь закрываются за 1 ч
const CONFIRM_WINDOW_MIN = 360;  // окно подтверждения — 6 ч
const ACTIVE = ['registered', 'confirmed', 'waitlist', 'offered'];
const HOLDS_SEAT = ['registered', 'confirmed', 'offered'];

let db = load();

function fresh() {
  return {
    seeded_at: Date.now(),
    me: { ...seed.ME },
    interests: [],
    events: seed.EVENTS.map((e) => ({ ...e })),
    registrations: seed.REGISTRATIONS.map((r) => ({ ...r })),
    seq: 1000,
  };
}
function load() {
  try {
    const raw = JSON.parse(localStorage.getItem(DB_KEY));
    if (raw && Date.now() - raw.seeded_at < TTL_MS) return raw;
  } catch { /* noop */ }
  return fresh();
}
function save() { try { localStorage.setItem(DB_KEY, JSON.stringify(db)); } catch { /* noop */ } }

export function resetMockDb() { db = fresh(); save(); }

const now = () => Date.now();
const nowIso = () => toCityIso(now());
const minsTo = (iso) => (Date.parse(iso) - now()) / 60000;
const fail = (status, code, message, details) => { throw new ApiError({ status, code, message, details }); };
const tagById = Object.fromEntries(seed.CATEGORIES.flatMap((c) => c.tags.map((t) => [t.id, t])));
const nextId = (p) => `${p}_${++db.seq}`;
const miniAppUrl = (id) => `${location.origin}/app/${id}`;

function expireOffers() {
  db.registrations.forEach((r) => {
    if (r.status === 'offered' && r.offer_expires_at && Date.parse(r.offer_expires_at) < now()) {
      r.status = 'cancelled';
      r.cancel_reason = 'offer_expired';
      r.cancelled_at = r.offer_expires_at;
    }
  });
}

const rowsOf = (eventId) => db.registrations.filter((r) => r.event_id === eventId);
const registeredCount = (e) => e.base_registered + rowsOf(e.id).filter((r) => HOLDS_SEAT.includes(r.status)).length;
const waitlistCount = (e) => e.base_waitlist + rowsOf(e.id).filter((r) => r.status === 'waitlist').length;
const myActive = (eventId) => db.registrations.find((r) => r.event_id === eventId && r.user_id === db.me.id && ACTIVE.includes(r.status));

const eventRef = (e) => ({ id: e.id, title: e.title, starts_at: e.starts_at, address: e.address, mini_app_url: miniAppUrl(e.id) });

function catalogItem(e, authed) {
  return {
    id: e.id, title: e.title, category_id: e.category_id,
    tags: e.tag_ids.map((id) => tagById[id]).filter(Boolean),
    short_description: e.short_description, cover_url: e.cover_url, author_id: e.author_id,
    my_registration_status: authed ? (myActive(e.id)?.status || null) : null,
  };
}

function regDto(r, withEvent = false) {
  const { user_id, from_waitlist, ...rest } = r; // eslint-disable-line no-unused-vars
  const out = { ...rest };
  if (withEvent) {
    const e = db.events.find((x) => x.id === r.event_id);
    out.event = { ...eventRef(e), category_id: e.category_id, status: e.status, timezone: e.timezone };
  }
  return out;
}

function details(e) {
  const mine = myActive(e.id);
  return {
    ...eventRef(e),
    category_id: e.category_id,
    tags: e.tag_ids.map((id) => tagById[id]).filter(Boolean),
    short_description: e.short_description, description: e.description,
    duration_min: e.duration_min, timezone: e.timezone, city_id: e.city_id, district: e.district,
    how_to_find: e.how_to_find, lat: e.lat, lon: e.lon,
    capacity: e.capacity, registered_count: registeredCount(e), waitlist_count: waitlistCount(e),
    registration_closes_at: toCityIso(Date.parse(e.starts_at) - CLOSE_MIN * 60000),
    level: e.level, age_limit: e.age_limit, bring: e.bring,
    author: { id: e.author_id, name: e.author_name }, contact: e.contact, cover_url: e.cover_url,
    status: e.status, moderation_flags: e.moderation_flags, published_at: e.published_at,
    my_registration: mine ? regDto(mine) : null,
  };
}

const requireAuth = (ctx) => { if (!ctx.token) fail(401, 'unauthorized', 'session token is missing'); };
const requireVerified = (ctx) => { requireAuth(ctx); if (!db.me.is_verified) fail(403, 'verification_required', 'user is not verified'); };
const getEvent = (id) => db.events.find((e) => e.id === id) || fail(404, 'not_found', `event ${id} not found`);
const getMyReg = (id) => {
  const r = db.registrations.find((x) => x.id === id);
  if (!r || r.user_id !== db.me.id) fail(404, 'not_found', `registration ${id} not found`);
  return r;
};
const requireAuthor = (ctx, e) => { requireVerified(ctx); if (e.author_id !== db.me.id) fail(403, 'forbidden', 'not the author'); };

/** Освободилось место: первому из очереди — предложение с таймером (30 мин, ближе к началу — 15). */
function releaseSeat(e) {
  const queue = rowsOf(e.id).filter((r) => r.status === 'waitlist').sort((a, b) => a.queue_position - b.queue_position);
  // Первый в очереди — «прочий» участник без строки в демо: место уходит ему, остальные сдвигаются
  if (e.base_waitlist > 0 && (!queue.length || queue[0].queue_position > 1)) {
    e.base_waitlist -= 1;
    e.base_registered += 1;
    queue.forEach((r) => { r.queue_position -= 1; });
    return;
  }
  if (queue.length) {
    const next = queue[0];
    next.status = 'offered';
    next.offer_expires_at = toCityIso(now() + (minsTo(e.starts_at) < CONFIRM_WINDOW_MIN ? 15 : 30) * 60000);
    queue.slice(1).forEach((r) => { r.queue_position -= 1; });
  }
}

function validateEvent(b, partial = false) {
  const d = [];
  const need = (f, cond, msg) => { if ((!partial || b[f] !== undefined) && !cond) d.push({ field: f, message: msg }); };
  need('title', typeof b.title === 'string' && b.title.trim().length > 0 && b.title.length <= 80, 'обязательно, до 80 символов');
  need('short_description', b.short_description === undefined || b.short_description.length <= 140, 'до 140 символов');
  need('description', typeof b.description === 'string' && b.description.trim().length > 0, 'обязательно');
  need('category_id', seed.CATEGORIES.some((c) => c.id === b.category_id), 'выберите направление');
  need('tag_ids', Array.isArray(b.tag_ids) && b.tag_ids.length > 0 && b.tag_ids.every((t) => tagById[t]), 'выберите хотя бы один тег');
  need('starts_at', isRfc3339(b.starts_at) && Date.parse(b.starts_at) > now(), 'RFC 3339 со смещением, не в прошлом');
  need('duration_min', Number.isInteger(b.duration_min) && b.duration_min > 0, 'длительность в минутах');
  need('city_id', cityById(b.city_id).id === b.city_id, 'неизвестный город');
  need('district', cityById(b.city_id).districts.includes(b.district), 'выберите район');
  need('address', typeof b.address === 'string' && b.address.trim().length > 0 && b.address.length <= 512, 'обязательно, до 512 символов');
  need('capacity', b.capacity === null || (Number.isInteger(b.capacity) && b.capacity > 0), 'целое число больше 0 или пусто');
  if (d.length) fail(400, 'invalid_request', `${d[0].field}: ${d[0].message}${d.length > 1 ? ` (and ${d.length - 1} more problem(s))` : ''}`, d);
}

const moderationFlags = (b) => {
  const text = `${b.title || ''} ${b.description || ''}`.toLowerCase();
  const hits = seed.STOP_WORDS.filter((w) => text.includes(w));
  if (/https?:\/\//.test(text)) hits.push('ссылка');
  return hits;
};

const r = (method, template, handler) => {
  const src = typeof template === 'function' ? template(':p') : template;
  const re = new RegExp(`^${src.replace(/[.*+?^${}()|[\]\\]/g, '\\$&').replace('%3Ap', '([^/]+)').replace(':p', '([^/]+)')}$`);
  return { method, re, handler };
};

const ROUTES = [
  r('GET', CONFIRMED.health, () => ({ status: 'ok' })),

  r('POST', PROPOSED.auth, () => ({ token: 'mock-session-token', user: db.me })),
  r('GET', PROPOSED.me, (_, __, ctx) => { requireAuth(ctx); return db.me; }),
  r('PATCH', PROPOSED.me, (_, body, ctx) => {
    requireAuth(ctx);
    const allowed = ['is_author', 'city_id', 'district', 'notification_settings', 'onboarding_completed', 'bot_available' /* только демо */];
    allowed.forEach((k) => { if (body[k] !== undefined) db.me[k] = body[k]; });
    if (body.is_author && !db.me.is_verified) fail(403, 'verification_required', 'author role requires verification');
    return db.me;
  }),
  r('POST', PROPOSED.verification, (_, __, ctx) => { requireAuth(ctx); db.me.is_verified = true; return db.me; }),
  r('POST', PROPOSED.consent, (_, __, ctx) => { requireAuth(ctx); db.me.consent_accepted_at = nowIso(); return db.me; }),
  r('GET', PROPOSED.interests, (_, __, ctx) => { requireAuth(ctx); return { tag_ids: db.interests }; }),
  r('PUT', PROPOSED.interests, (_, body, ctx) => {
    requireAuth(ctx);
    const ids = (body.tag_ids || []).filter((t) => tagById[t]);
    if (ids.length && ids.length < 3) fail(400, 'invalid_request', 'tag_ids: minimum 3', [{ field: 'tag_ids', message: 'минимум 3 тега' }]);
    db.interests = ids;
    db.me.onboarding_completed = true;
    return { tag_ids: ids };
  }),

  r('GET', PROPOSED.categories, () => ({ items: seed.CATEGORIES })),

  r('GET', PROPOSED.events, (_, __, ctx, q) => {
    const needle = (q.q || '').trim().toLowerCase();
    const tagIds = q.tag_ids ? String(q.tag_ids).split(',') : [];
    const list = db.events
      .filter((e) => e.status === 'published' && Date.parse(e.starts_at) > now())
      .filter((e) => !q.city_id || e.city_id === q.city_id)
      .filter((e) => !needle || `${e.title} ${e.short_description} ${e.description}`.toLowerCase().includes(needle))
      .filter((e) => !q.category_id || e.category_id === q.category_id)
      .filter((e) => !tagIds.length || tagIds.some((t) => e.tag_ids.includes(t)))
      .filter((e) => matchDate(e.starts_at, q.date) && matchDaypart(e.starts_at, q.daypart))
      .filter((e) => !q.district || e.district === q.district)
      .filter((e) => !q.has_seats || e.capacity == null || registeredCount(e) < e.capacity)
      .sort((a, b) => {
        if (q.sort === 'published_at') return Date.parse(b.published_at) - Date.parse(a.published_at);
        if (q.sort === 'popularity') return b.popularity - a.popularity;
        return Date.parse(a.starts_at) - Date.parse(b.starts_at);
      });
    const page = Math.max(1, Number(q.page) || 1);
    const size = Math.min(50, Number(q.page_size) || 12);
    return { items: list.slice((page - 1) * size, page * size).map((e) => catalogItem(e, !!ctx.token)), page, page_size: size, total: list.length };
  }),
  r('POST', PROPOSED.events, (_, body, ctx) => {
    requireVerified(ctx);
    if (!db.me.is_author) fail(403, 'forbidden', 'author role is off');
    validateEvent(body);
    const flags = moderationFlags(body);
    const e = {
      ...body, id: nextId('event'), timezone: cityById(body.city_id).timezone, lat: null, lon: null, cover_url: null,
      base_registered: 0, base_waitlist: 0, views: 0, popularity: 0, published_at: nowIso(),
      author_id: db.me.id, author_name: `${db.me.first_name} ${db.me.last_name?.[0] || ''}.`.trim(),
      contact: body.contact || 'Профиль автора в MAX',
      status: flags.length ? 'moderation' : 'published', moderation_flags: flags,
    };
    db.events.unshift(e);
    return details(e);
  }),
  r('GET', PROPOSED.event, (id, _, ctx) => { requireVerified(ctx); return details(getEvent(id)); }),
  r('PATCH', PROPOSED.event, (id, body, ctx) => {
    const e = getEvent(id);
    requireAuthor(ctx, e);
    const merged = { ...e, ...body };
    validateEvent(merged);
    if (merged.capacity != null && merged.capacity < registeredCount(e)) {
      fail(409, 'capacity_below_registered', 'capacity is below active registrations', [{ field: 'capacity', message: `не меньше ${registeredCount(e)}` }]);
    }
    const important = ['starts_at', 'address', 'title'].filter((k) => body[k] !== undefined && body[k] !== e[k]);
    Object.assign(e, body);
    return { ...details(e), notified_count: important.length ? rowsOf(e.id).filter((x) => ACTIVE.includes(x.status)).length + e.base_registered : 0 };
  }),
  r('POST', PROPOSED.cancelEvent, (id, body, ctx) => {
    const e = getEvent(id);
    requireAuthor(ctx, e);
    if ((body.organizer_message || '').length > 2000) fail(400, 'invalid_request', 'organizer_message too long', [{ field: 'organizer_message', message: 'до 2000 символов' }]);
    e.status = 'cancelled';
    return { status: 'accepted', notified_count: rowsOf(e.id).filter((x) => ACTIVE.includes(x.status)).length + e.base_registered + e.base_waitlist };
  }),
  r('POST', PROPOSED.eventMessages, (id, body, ctx) => {
    const e = getEvent(id);
    requireAuthor(ctx, e);
    const msg = (body.organizer_message || '').trim();
    if (!msg || msg.length > 2000) fail(400, 'invalid_request', 'organizer_message: 1..2000', [{ field: 'organizer_message', message: 'от 1 до 2000 символов' }]);
    return { status: 'accepted', notified_count: rowsOf(e.id).filter((x) => HOLDS_SEAT.includes(x.status)).length + e.base_registered };
  }),
  r('GET', PROPOSED.eventReport, (id, _, ctx) => {
    const e = getEvent(id);
    requireAuthor(ctx, e);
    const rows = rowsOf(e.id);
    const cancelled = rows.filter((x) => x.status === 'cancelled');
    const reasons = cancelled.reduce((acc, x) => { const k = x.cancel_reason || 'none'; acc[k] = (acc[k] || 0) + 1; return acc; }, {});
    const users = Object.fromEntries(seed.OTHER_USERS.map((u) => [u.id, u]));
    return {
      event_id: e.id, views: e.views,
      registrations_total: rows.filter((x) => x.status !== 'waitlist').length + e.base_registered,
      registered_now: registeredCount(e), capacity: e.capacity,
      confirmed: rows.filter((x) => x.status === 'confirmed').length,
      no_answer: rows.filter((x) => x.status === 'registered').length,
      cancelled_total: cancelled.length, cancelled_late: cancelled.filter((x) => x.cancelled_late).length,
      cancel_reasons: reasons, waitlist_size: waitlistCount(e), from_waitlist: rows.filter((x) => x.from_waitlist).length,
      participants: rows.map((x) => ({
        registration_id: x.id, name: users[x.user_id]?.name || 'Участник', status: x.status,
        cancel_reason: x.cancel_reason, cancelled_late: x.cancelled_late, bot_available: users[x.user_id]?.bot_available ?? true,
      })),
    };
  }),
  r('POST', PROPOSED.eventViews, (id) => { const e = db.events.find((x) => x.id === id); if (e) e.views += 1; return null; }),
  r('POST', PROPOSED.complaints, (id, _, ctx) => { requireVerified(ctx); getEvent(id); return { status: 'accepted' }; }),
  r('GET', PROPOSED.myEvents, (_, __, ctx) => {
    requireVerified(ctx);
    return { items: db.events.filter((e) => e.author_id === db.me.id).sort((a, b) => Date.parse(a.starts_at) - Date.parse(b.starts_at)).map(details) };
  }),

  r('GET', PROPOSED.myRegistrations, (_, __, ctx) => {
    requireVerified(ctx);
    return { items: db.registrations.filter((x) => x.user_id === db.me.id).map((x) => regDto(x, true)) };
  }),

  // [DOCUMENTED]
  r('POST', DOCUMENTED.createRegistration, (_, body, ctx) => {
    requireVerified(ctx);
    const e = getEvent(body.event_id);
    if (e.status === 'cancelled') fail(409, 'event_cancelled', 'event is cancelled');
    if (e.author_id === db.me.id) fail(409, 'author_cannot_register', 'author is the organiser');
    if (minsTo(e.starts_at) < CLOSE_MIN) fail(409, 'registration_closed', 'registration closes 1h before start');
    if (myActive(e.id)) fail(409, 'already_registered', 'active registration exists');
    const full = e.capacity != null && registeredCount(e) >= e.capacity;
    const soon = minsTo(e.starts_at) < CONFIRM_WINDOW_MIN; // < 6 ч — сразу подтверждено
    const row = {
      id: nextId('registration'), event_id: e.id, user_id: db.me.id,
      status: full ? 'waitlist' : soon ? 'confirmed' : 'registered',
      queue_position: full ? waitlistCount(e) + 1 : null, offer_expires_at: null, cancel_reason: null,
      cancelled_late: false, from_waitlist: false, created_at: nowIso(), confirmed_at: !full && soon ? nowIso() : null, cancelled_at: null,
    };
    db.registrations.push(row);
    return regDto(row);
  }),

  // [CONFIRMED]
  r('POST', CONFIRMED.confirmRegistration, (id, _, ctx) => {
    requireVerified(ctx);
    const row = getMyReg(id);
    const e = getEvent(row.event_id);
    if (e.status === 'cancelled') fail(409, 'event_cancelled', 'event is cancelled');
    if (row.status === 'cancelled') fail(409, 'registration_cancelled', 'registration is cancelled');
    if (row.status === 'confirmed') return { status: 'noop', event: eventRef(e) };
    if (row.status !== 'registered') fail(409, 'conflict', `cannot confirm from ${row.status}`);
    row.status = 'confirmed';
    row.confirmed_at = nowIso();
    return { status: 'accepted', event: eventRef(e) };
  }),
  r('POST', CONFIRMED.cancelRegistration, (id, body, ctx) => {
    requireVerified(ctx);
    const row = getMyReg(id);
    const e = getEvent(row.event_id);
    if (row.status === 'cancelled') return { status: 'noop', event: eventRef(e) };
    if (e.status === 'cancelled') fail(409, 'event_cancelled', 'event is cancelled');
    const heldSeat = HOLDS_SEAT.includes(row.status);
    row.status = 'cancelled';
    row.cancelled_at = nowIso();
    row.cancel_reason = body.cancel_reason || null;
    row.cancelled_late = minsTo(e.starts_at) < CLOSE_MIN;
    if (heldSeat) releaseSeat(e);
    return { status: 'accepted', event: eventRef(e) };
  }),
  r('POST', CONFIRMED.acceptWaitlistOffer, (id, _, ctx) => {
    requireVerified(ctx);
    const row = getMyReg(id);
    const e = getEvent(row.event_id);
    if (e.status === 'cancelled') fail(409, 'event_cancelled', 'event is cancelled');
    if (row.cancel_reason === 'offer_expired') fail(410, 'offer_expired', `offer for ${id} expired`);
    if (['registered', 'confirmed'].includes(row.status)) return { status: 'noop', event: eventRef(e) };
    if (row.status !== 'offered') fail(409, 'conflict', `no active offer for ${id}`);
    const soon = minsTo(e.starts_at) < CONFIRM_WINDOW_MIN;
    Object.assign(row, { status: soon ? 'confirmed' : 'registered', confirmed_at: soon ? nowIso() : null, from_waitlist: true, queue_position: null, offer_expires_at: null });
    return { status: 'accepted', event: eventRef(e) };
  }),
  r('POST', CONFIRMED.declineWaitlistOffer, (id, _, ctx) => {
    requireVerified(ctx);
    const row = getMyReg(id);
    const e = getEvent(row.event_id);
    if (row.status === 'cancelled') return { status: 'noop', event: eventRef(e) };
    if (row.status !== 'offered') fail(409, 'conflict', `no active offer for ${id}`);
    Object.assign(row, { status: 'cancelled', cancelled_at: nowIso(), cancel_reason: 'offer_declined' });
    releaseSeat(e);
    return { status: 'accepted', event: eventRef(e) };
  }),

  r('GET', PROPOSED.recommendations, (_, __, ctx, q) => recommend(!!ctx.token, q.city_id)),
  r('POST', PROPOSED.suggestTags, (_, body) => suggest(body.title || '', body.description || '')),
];

function recommend(authed, cityId) {
  const interests = authed ? db.interests : [];
  const weekMs = 7 * 24 * 3600 * 1000;
  const personal = interests.length > 0;
  const items = db.events
    .filter((e) => e.status === 'published' && Date.parse(e.starts_at) > now() && e.author_id !== db.me.id && !(authed && myActive(e.id)))
    .filter((e) => !cityId || e.city_id === cityId)
    .map((e) => {
      const matched = e.tag_ids.filter((t) => interests.includes(t));
      const soon = Date.parse(e.starts_at) - now() < weekMs;
      const freshAd = now() - Date.parse(e.published_at) < 24 * 3600 * 1000;
      const full = e.capacity != null && registeredCount(e) >= e.capacity;
      // ТЗ, раздел 4: веса тегов + близость по времени + район − нет мест
      let score = personal ? matched.length * 3 : e.popularity / 10;
      if (soon) score += 1.5;
      if (freshAd) score += 1;
      if (db.me.district && e.district === db.me.district) score += 1;
      if (full) score -= 2;
      const reason = matched.length
        ? `Вам интересно: ${matched.slice(0, 2).map((t) => `«${tagById[t].name}»`).join(', ')}`
        : freshAd ? 'Новая афиша' : 'Популярно сейчас';
      return { event: catalogItem(e, authed), score, matched: matched.length, reason };
    })
    .filter((x) => !personal || x.matched > 0 || x.score > 3)
    .sort((a, b) => b.score - a.score)
    .map(({ event, reason }) => ({ event, reason }));
  return { items, mode: personal ? 'personal' : 'popular' };
}

function suggest(title, description) {
  const text = ` ${`${title} ${description}`.toLowerCase()} `;
  if (text.trim().length < 4) return { category_id: null, tag_ids: [], confidence: 0 };
  const tagIds = Object.entries(seed.ML_KEYWORDS).filter(([, keys]) => keys.some((k) => text.includes(k))).map(([id]) => id);
  const score = {};
  tagIds.forEach((t) => { const c = seed.CATEGORIES.find((x) => x.tags.some((y) => y.id === t)); score[c.id] = (score[c.id] || 0) + 1; });
  const best = Object.entries(score).sort((a, b) => b[1] - a[1])[0];
  return { category_id: best?.[0] || null, tag_ids: tagIds.slice(0, 4), confidence: best ? Math.min(0.95, 0.5 + best[1] * 0.15) : 0 };
}

function matchDate(iso, when) {
  if (!when) return true;
  const day = (ms) => toCityIso(ms).slice(0, 10);
  const target = day(Date.parse(iso));
  const diff = Math.round((Date.parse(`${target}T00:00:00Z`) - Date.parse(`${day(now())}T00:00:00Z`)) / 86400000);
  if (when === 'today') return diff === 0;
  if (when === 'tomorrow') return diff === 1;
  if (when === 'week') return diff >= 0 && diff < 7;
  if (when === 'weekend') { const wd = new Date(`${target}T12:00:00Z`).getUTCDay(); return diff >= 0 && diff < 7 && (wd === 0 || wd === 6); }
  return true;
}
function matchDaypart(iso, part) {
  if (!part) return true;
  const h = Number(toCityIso(Date.parse(iso)).slice(11, 13));
  if (part === 'morning') return h < 12;
  if (part === 'day') return h >= 12 && h < 17;
  return h >= 17;
}

export async function handle({ method, path, query, body, token, requestId }) {
  await new Promise((res) => setTimeout(res, 180));
  expireOffers();
  const route = ROUTES.find((x) => x.method === method && x.re.test(path));
  if (!route) throw new ApiError({ status: 404, code: 'not_found', message: `no route matches ${method} ${path}`, requestId });
  const param = decodeURIComponent(path.match(route.re)[1] || '');
  try {
    const out = route.handler(param, body || {}, { token }, query || {});
    save();
    return out === undefined ? null : JSON.parse(JSON.stringify(out));
  } catch (e) {
    if (e instanceof ApiError) { e.requestId = requestId; throw e; }
    throw new ApiError({ status: 500, code: 'internal_error', message: String(e?.message || e), requestId });
  }
}

export function mockPatchMe(patch) { Object.assign(db.me, patch); save(); }
