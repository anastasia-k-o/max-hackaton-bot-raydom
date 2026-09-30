// Все пути API. Мини-приложение не обращается к боту напрямую — только к бэкенду.

const enc = encodeURIComponent;

// [CONFIRMED] bot/internal/core/httpgw: тело {registration_id, event_id, max_user_id, request_id, occurred_at}, ответ {status: accepted|noop}
export const CONFIRMED = {
  confirmRegistration: (registrationId) => `/registrations/${enc(registrationId)}/confirm`,
  cancelRegistration: (registrationId) => `/registrations/${enc(registrationId)}/cancel`,
  acceptWaitlistOffer: (registrationId) => `/waitlist-offers/${enc(registrationId)}/accept`,
  declineWaitlistOffer: (registrationId) => `/waitlist-offers/${enc(registrationId)}/decline`,
  health: () => '/health',
};

// [DOCUMENTED] bot/docs/INTEGRATION_MINIAPP.md §7
export const DOCUMENTED = {
  createRegistration: () => '/registrations',
};

// [PROPOSED] нет в bot/; поля — «Структура проекта», раздел 10
export const PROPOSED = {
  // сервер проверяет подпись initData
  auth: () => '/auth/max',
  me: () => '/me',
  // подтверждённый номер из WebApp.requestContact()
  verification: () => '/me/verification',
  consent: () => '/me/consent',
  interests: () => '/me/interests',
  myRegistrations: () => '/me/registrations',
  myEvents: () => '/me/events',
  categories: () => '/categories',
  // каталог: только общие поля
  events: () => '/events',
  // полная карточка, только is_verified
  event: (id) => `/events/${enc(id)}`,
  // отмена → бот event_cancelled
  cancelEvent: (id) => `/events/${enc(id)}/cancel`,
  // рассылка → бот event_updated.organizer_message
  eventMessages: (id) => `/events/${enc(id)}/messages`,
  eventReport: (id) => `/events/${enc(id)}/report`,
  eventViews: (id) => `/events/${enc(id)}/views`,
  complaints: (id) => `/events/${enc(id)}/complaints`,
  recommendations: () => '/recommendations',
  suggestTags: () => '/ml/suggest-tags',
};
