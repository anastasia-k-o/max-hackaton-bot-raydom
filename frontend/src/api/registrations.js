import { request } from './client.js';
import { CONFIRMED, DOCUMENTED, PROPOSED } from './endpoints.js';

/**
 * @typedef {import('../types/index.js').Registration} Registration
 * @typedef {import('../types/index.js').ActionResult} ActionResult
 */

// Тело 1:1 с httpgw.requestBody; max_user_id сервер обязан сверять с токеном сессии.
const actionBody = (reg, user, requestId, extra = {}) => ({
  registration_id: reg.id,
  event_id: reg.event_id,
  max_user_id: user.max_user_id,
  request_id: requestId,
  occurred_at: new Date().toISOString(),
  ...extra,
});

export const registrations = {
  /**
   * Мест нет → сервер ставит в лист ожидания (status=waitlist).
   * @returns {Promise<Registration>} 201
   */
  create: (eventId, requestId) => request('POST', DOCUMENTED.createRegistration(), { body: { event_id: eventId, request_id: requestId }, requestId }),

  /** @returns {Promise<{items: Registration[]}>} */
  mine: () => request('GET', PROPOSED.myRegistrations()),

  /** @returns {Promise<ActionResult>} */
  confirm: (reg, user, requestId) => request('POST', CONFIRMED.confirmRegistration(reg.id), { body: actionBody(reg, user, requestId), requestId }),

  /**
   * cancel_reason — расширение контракта бота.
   * @returns {Promise<ActionResult>}
   */
  cancel: (reg, user, requestId, reason) => request('POST', CONFIRMED.cancelRegistration(reg.id), {
    body: actionBody(reg, user, requestId, reason ? { cancel_reason: reason } : {}), requestId,
  }),

  /** 410 offer_expired, если срок вышел. @returns {Promise<ActionResult>} */
  acceptOffer: (reg, user, requestId) => request('POST', CONFIRMED.acceptWaitlistOffer(reg.id), { body: actionBody(reg, user, requestId), requestId }),

  /** @returns {Promise<ActionResult>} */
  declineOffer: (reg, user, requestId) => request('POST', CONFIRMED.declineWaitlistOffer(reg.id), { body: actionBody(reg, user, requestId), requestId }),
};
