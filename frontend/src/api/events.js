import { request } from './client.js';
import { PROPOSED } from './endpoints.js';

/**
 * @typedef {import('../types/index.js').CatalogItem} CatalogItem
 * @typedef {import('../types/index.js').EventDetails} EventDetails
 * @typedef {import('../types/index.js').EventInput} EventInput
 * @typedef {import('../types/index.js').Page} Page
 */

export const events = {
  /** @returns {Promise<{items: {id: string, name: string, tags: {id: string, name: string}[]}[]}>} */
  categories: () => request('GET', PROPOSED.categories()),

  /**
   * @param {{city_id: string, q?: string, category_id?: string, tag_ids?: string[], date?: 'today'|'tomorrow'|'weekend'|'week',
   *   daypart?: 'morning'|'day'|'evening', district?: string, has_seats?: boolean,
   *   sort?: 'starts_at'|'published_at'|'popularity', page?: number, page_size?: number}} query
   * @returns {Promise<Page & {items: CatalogItem[]}>}
   */
  list: (query) => request('GET', PROPOSED.events(), { query }),

  /** @returns {Promise<EventDetails>} 403 verification_required для неверифицированных */
  get: (id) => request('GET', PROPOSED.event(id)),

  /** @param {EventInput} input  @returns {Promise<EventDetails>} */
  create: (input) => request('POST', PROPOSED.events(), { body: input }),

  /** @param {Partial<EventInput>} patch  @returns {Promise<EventDetails & {notified_count: number}>} */
  update: (id, patch) => request('PATCH', PROPOSED.event(id), { body: patch }),

  /** organizer_message ≤2000 → бот event_cancelled */
  cancel: (id, organizerMessage) => request('POST', PROPOSED.cancelEvent(id), { body: { organizer_message: organizerMessage || undefined } }),

  /** organizer_message ≤2000 → бот event_updated */
  message: (id, organizerMessage) => request('POST', PROPOSED.eventMessages(id), { body: { organizer_message: organizerMessage } }),

  /** @returns {Promise<{items: EventDetails[]}>} */
  mine: () => request('GET', PROPOSED.myEvents()),

  trackView: (id, type = 'view') => request('POST', PROPOSED.eventViews(id), { body: { type } }),
  complain: (id, text = '') => request('POST', PROPOSED.complaints(id), { body: { text } }),

  /** @returns {Promise<{category_id: string|null, tag_ids: string[], confidence: number}>} */
  suggestTags: (title, description) => request('POST', PROPOSED.suggestTags(), { body: { title, description } }),
};
