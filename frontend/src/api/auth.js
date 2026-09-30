import { request, setToken } from './client.js';
import { PROPOSED } from './endpoints.js';

/** @typedef {import('../types/index.js').User} User */

export const auth = {
  /**
   * @returns {Promise<{token: string, user: User}>}
   */
  async login(initData) {
    const r = await request('POST', PROPOSED.auth(), { body: { init_data: initData } });
    setToken(r.token);
    return r;
  },
  /** @returns {Promise<User>} */
  getMe: () => request('GET', PROPOSED.me()),
  /** @returns {Promise<{is_verified: boolean}>} */
  getVerificationStatus: () => request('GET', PROPOSED.me()).then((u) => ({ is_verified: !!u.is_verified })),
  /** @param {{phone: string, authDate: number, hash: string}} contact ответ WebApp.requestContact()  @returns {Promise<User>} */
  verify: (contact) => request('POST', PROPOSED.verification(), { body: { phone: contact?.phone, auth_date: contact?.authDate, hash: contact?.hash } }),
};
