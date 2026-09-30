
/**
 
 * @typedef {Object} EventRef
 * @property {string} id            ≤256 символов, без «|»
 * @property {string} title         ≤512 символов
 * @property {string} starts_at     RFC 3339 СО СМЕЩЕНИЕМ города, напр. 2026-09-22T19:00:00+03:00
 * @property {string} [address]     ≤512 символов
 * @property {string} [mini_app_url] абсолютная ссылка на карточку, напр. https://…/app/event_42
 */

/** [PROPOSED] */
export const EVENT_STATUS = Object.freeze({
  DRAFT: 'draft',
  MODERATION: 'moderation',
  PUBLISHED: 'published',
  CANCELLED: 'cancelled',
  PAST: 'past',
});

/**
 * @typedef {Object} Tag
 * @property {string} id
 * @property {string} name
 */

/**
 * [PROPOSED] Без времени, адреса и мест — требование ТЗ.
 * @typedef {Object} CatalogItem
 * @property {string} id
 * @property {string} title
 * @property {string} category_id
 * @property {Tag[]} tags
 * @property {string} short_description
 * @property {string|null} cover_url
 * @property {string} author_id
 * @property {RegistrationStatus|null} my_registration_status  активная запись текущего пользователя
 */

/**
 * [PROPOSED] Только для is_verified, иначе 403 verification_required.
 * @typedef {EventRef & {
 *   category_id: string, tags: Tag[], short_description: string, description: string,
 *   duration_min: number, timezone: string, city_id: string, district: string,
 *   how_to_find: string, lat: number|null, lon: number|null,
 *   capacity: number|null, registered_count: number, waitlist_count: number,
 *   registration_closes_at: string, level: string|null, age_limit: string|null, bring: string|null,
 *   author: {id: string, name: string}, contact: string, cover_url: string|null,
 *   status: string, moderation_flags?: string[], published_at: string,
 *   my_registration: Registration|null
 * }} EventDetails
 */

/**
 * [PROPOSED]
 * @typedef {Object} EventInput
 * @property {string} title              ≤80 (ТЗ) — бот допускает ≤512
 * @property {string} short_description  ≤140
 * @property {string} description
 * @property {string} category_id
 * @property {string[]} tag_ids
 * @property {string} starts_at          RFC 3339 со смещением города
 * @property {number} duration_min
 * @property {string} city_id          id из src/lib/cities.js
 * @property {string} district
 * @property {string} address            ≤512 (ограничение бота)
 * @property {string} how_to_find
 * @property {number|null} capacity      null = без ограничения
 * @property {string|null} level
 * @property {string|null} age_limit
 * @property {string|null} bring
 * @property {string|null} contact       null = профиль автора в MAX
 */

/**
 * [PROPOSED] ТЗ, раздел 9. Запись и место в очереди — одна сущность (один registration_id).
 * @typedef {'registered'|'confirmed'|'waitlist'|'offered'|'cancelled'|'attended'|'no_show'} RegistrationStatus
 */
export const REG_STATUS = Object.freeze({
  REGISTERED: 'registered', // записан, ждёт подтверждения за 6 ч
  CONFIRMED: 'confirmed',   // нажал «Приду» (или записался < 6 ч до начала)
  WAITLIST: 'waitlist',
  OFFERED: 'offered',       // место предложено, действует до offer_expires_at
  CANCELLED: 'cancelled',
  ATTENDED: 'attended',     // этап 2 — отметка явки
  NO_SHOW: 'no_show',       // этап 2
});

export const ACTIVE_STATUSES = [REG_STATUS.REGISTERED, REG_STATUS.CONFIRMED, REG_STATUS.WAITLIST, REG_STATUS.OFFERED];

/** [PROPOSED] */
export const CANCEL_REASONS = Object.freeze([
  { code: 'ill', label: 'Заболел' },
  { code: 'plans_changed', label: 'Изменились планы' },
  { code: 'other', label: 'Другое' },
]);

/**
 * @typedef {Object} Registration
 * @property {string} id                       [CONFIRMED] registration_id, ≤256, без «|»
 * @property {string} event_id
 * @property {RegistrationStatus} status
 * @property {number|null} queue_position
 * @property {string|null} offer_expires_at     [CONFIRMED] RFC 3339 (waitlist_offer)
 * @property {string|null} cancel_reason
 * @property {boolean} cancelled_late           отмена < 1 ч до начала
 * @property {string} created_at
 * @property {string|null} confirmed_at
 * @property {string|null} cancelled_at
 * @property {EventRef & {category_id: string, status: string}} [event]  в списке «Мои записи»
 */

/**
 * [CONFIRMED] Ответ на действие с записью (httpgw.responseBody).
 * @typedef {Object} ActionResult
 * @property {'accepted'|'noop'} status
 * @property {string} [message]
 * @property {EventRef} [event]
 */

/**
 * [PROPOSED] Бот знает только max_user_id.
 * @typedef {Object} User
 * @property {string} id
 * @property {number} max_user_id              [CONFIRMED] int64
 * @property {string} first_name
 * @property {string} [last_name]
 * @property {string|null} photo_url
 * @property {boolean} is_verified             флаг от сервера, НЕ из браузера
 * @property {boolean} is_author
 * @property {string} city_id
 * @property {string|null} district
 * @property {boolean} bot_available           false — бот заблокирован / диалог не начат
 * @property {boolean} onboarding_completed
 * @property {string|null} consent_accepted_at
 * @property {{reminders: boolean, recommendations: boolean}} notification_settings
 */

/**
 * [PROPOSED] ТЗ, раздел 8.
 * @typedef {Object} EventReport
 * @property {string} event_id
 * @property {number} views
 * @property {number} registrations_total
 * @property {number} registered_now
 * @property {number|null} capacity
 * @property {number} confirmed
 * @property {number} no_answer
 * @property {number} cancelled_total
 * @property {number} cancelled_late
 * @property {Object<string, number>} cancel_reasons   код причины → количество
 * @property {number} waitlist_size
 * @property {number} from_waitlist
 * @property {{name: string, status: RegistrationStatus, cancel_reason: string|null, cancelled_late: boolean, bot_available: boolean}[]} participants
 */

/**
 * [CONFIRMED]
 * @typedef {Object} ErrorEnvelope
 * @property {{code: string, message: string, request_id?: string, details?: {field: string, message: string}[]}} error
 */

/**
 * @typedef {Object} Page
 * @property {Array} items
 * @property {number} page
 * @property {number} page_size
 * @property {number} total
 */
