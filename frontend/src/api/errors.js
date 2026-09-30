// Конверт ошибок как у бота (bot/api/openapi.yaml → ErrorResponse); пользователю — текст по коду.

export class ApiError extends Error {
  /**
   * @param {{status?: number, code?: string, message?: string, requestId?: string, details?: {field: string, message: string}[]}} p
   */
  constructor({ status = 0, code = 'internal_error', message = '', requestId, details = [] } = {}) {
    super(message || code);
    this.name = 'ApiError';
    this.status = status;
    this.code = code;
    this.requestId = requestId;
    this.details = details;
  }

  get retryable() {
    return this.code === 'upstream_unavailable' || this.code === 'timeout' || this.status >= 500;
  }

  fieldErrors() {
    return Object.fromEntries((this.details || []).map((d) => [d.field, d.message]));
  }
}

// Тексты совпадают по смыслу с bot/internal/render/texts.go.
const MESSAGES = {
  // [CONFIRMED]
  event_cancelled: 'Мероприятие отменено организатором.',
  registration_cancelled: 'Эта запись уже отменена.',
  offer_expired: 'Срок предложения истёк — место предложено следующему участнику.',
  not_found: 'Не удалось найти эту запись или мероприятие.',
  conflict: 'Сейчас это действие недоступно.',
  forbidden: 'Сейчас это действие недоступно.',
  invalid_request: 'Проверьте заполнение полей.',
  unauthorized: 'Сессия истекла. Откройте мини-приложение заново.',
  upstream_unavailable: 'Сервис временно недоступен, попробуйте ещё раз через пару минут.',
  internal_error: 'Что-то пошло не так. Попробуйте ещё раз.',
  payload_too_large: 'Слишком большой запрос.',
  // [PROPOSED]
  verification_required: 'Доступно только верифицированным пользователям MAX.',
  registration_closed: 'Запись закрыта — мероприятие скоро начнётся.',
  already_registered: 'Вы уже записаны на это мероприятие.',
  author_cannot_register: 'Автор не записывается на своё мероприятие — вы организатор.',
  capacity_below_registered: 'Нельзя сделать лимит меньше числа записавшихся.',
  timeout: 'Сервер долго не отвечает. Попробуйте ещё раз.',
};

// Синонимы из httpgw.mapErrorCode.
const ALIASES = {
  registration_not_found: 'not_found',
  event_not_found: 'not_found',
  waitlist_offer_expired: 'offer_expired',
  event_canceled: 'event_cancelled',
  registration_canceled: 'registration_cancelled',
};

export const normalizeCode = (code) => ALIASES[code] || code;

export function errorText(err) {
  if (err instanceof ApiError) {
    const code = normalizeCode(err.code);
    if (MESSAGES[code]) return MESSAGES[code];
    if (err.status >= 500) return MESSAGES.upstream_unavailable;
    return MESSAGES.conflict;
  }
  return MESSAGES.internal_error;
}
