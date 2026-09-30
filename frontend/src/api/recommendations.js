import { request } from './client.js';
import { PROPOSED } from './endpoints.js';

/** @typedef {import('../types/index.js').CatalogItem} CatalogItem */

export const recommendations = {
  /** @returns {Promise<{items: {event: CatalogItem, reason: string}[], mode: 'personal'|'popular'}>} */
  list: (cityId) => request('GET', PROPOSED.recommendations(), { query: { city_id: cityId } }),
};
