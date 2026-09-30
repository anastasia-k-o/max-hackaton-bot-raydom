import { request } from './client.js';
import { PROPOSED } from './endpoints.js';

/** @typedef {import('../types/index.js').User} User */

export const profile = {
  /** @returns {Promise<User>} */
  getProfile: () => request('GET', PROPOSED.me()),
  /** @param {Partial<Pick<User, 'is_author'|'city_id'|'district'|'notification_settings'|'onboarding_completed'>>} patch  @returns {Promise<User>} */
  updateProfile: (patch) => request('PATCH', PROPOSED.me(), { body: patch }),
  /** @returns {Promise<User>} */
  acceptConsent: () => request('POST', PROPOSED.consent(), { body: { accepted: true } }),
  /** @returns {Promise<{tag_ids: string[]}>} */
  getInterests: () => request('GET', PROPOSED.interests()),
  /** @returns {Promise<{tag_ids: string[]}>} */
  setInterests: (tagIds) => request('PUT', PROPOSED.interests(), { body: { tag_ids: tagIds } }),
};
