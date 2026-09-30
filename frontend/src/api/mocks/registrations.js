import { ago, inMin } from './util.js';

let seq = 100;
const reg = (o) => ({
  id: `registration_${seq++}`, queue_position: null, offer_expires_at: null, cancel_reason: null,
  cancelled_late: false, from_waitlist: false, created_at: ago(24), confirmed_at: null, cancelled_at: null, ...o,
});

export const REGISTRATIONS = [
  reg({ event_id: 'event_3', user_id: 'user_me', status: 'registered' }),
  reg({ event_id: 'event_9', user_id: 'user_me', status: 'confirmed', confirmed_at: ago(2) }),
  reg({ event_id: 'event_5', user_id: 'user_me', status: 'waitlist', queue_position: 2 }),
  reg({ event_id: 'event_14', user_id: 'user_me', status: 'offered', queue_position: 1, offer_expires_at: inMin(25) }),
  reg({ event_id: 'event_p1', user_id: 'user_me', status: 'confirmed', confirmed_at: ago(150) }),
  reg({ event_id: 'event_p2', user_id: 'user_me', status: 'cancelled', cancel_reason: 'plans_changed', cancelled_at: ago(320) }),
  // участники мероприятий автора — из них считается отчёт
  reg({ event_id: 'event_1', user_id: 'user_1', status: 'confirmed', confirmed_at: ago(3) }),
  reg({ event_id: 'event_1', user_id: 'user_2', status: 'cancelled', cancel_reason: 'plans_changed', cancelled_at: ago(10) }),
  reg({ event_id: 'event_1', user_id: 'user_4', status: 'cancelled', cancel_reason: 'ill', cancelled_late: true, cancelled_at: ago(1) }),
  ...['user_1', 'user_2', 'user_3', 'user_4', 'user_5', 'user_6', 'user_7'].map((u) => reg({ event_id: 'event_7', user_id: u, status: 'confirmed', confirmed_at: ago(5) })),
  ...['user_8', 'user_9', 'user_10', 'user_11'].map((u) => reg({ event_id: 'event_7', user_id: u, status: 'registered' })),
  reg({ event_id: 'event_7', user_id: 'user_12', status: 'cancelled', cancel_reason: 'plans_changed', cancelled_at: ago(30) }),
  reg({ event_id: 'event_7', user_id: 'user_13', status: 'cancelled', cancel_reason: 'ill', cancelled_late: false, cancelled_at: ago(12) }),
];
