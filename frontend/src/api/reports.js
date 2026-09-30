import { request } from './client.js';
import { PROPOSED } from './endpoints.js';

/** @typedef {import('../types/index.js').EventReport} EventReport */

export const reports = {
  /** @returns {Promise<EventReport>} */
  get: (eventId) => request('GET', PROPOSED.eventReport(eventId)),
};
