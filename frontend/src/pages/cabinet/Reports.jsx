import { useState } from 'react';
import { events as eventsApi, reports as reportsApi, errorText } from '../../api/index.js';
import { useEvents } from '../../store/events.jsx';
import { useUi } from '../../store/ui.jsx';
import { useApi } from '../../store/useApi.js';
import { CANCEL_REASONS, REG_STATUS } from '../../types/index.js';
import Icon from '../../components/Icon.jsx';
import Modal from '../../components/Modal.jsx';
import Badge from '../../components/Badge.jsx';
import EmptyState from '../../components/EmptyState.jsx';
import { fmtDate, fmtTime, pct } from '../../lib/format.js';
import { navigate } from '../../lib/router.js';

const MSG_LIMIT = 2000; // лимит organizer_message у бота

const PARTICIPANT = {
  [REG_STATUS.CONFIRMED]: { label: 'Подтвердил', status: REG_STATUS.CONFIRMED },
  [REG_STATUS.REGISTERED]: { label: 'Не ответил', tone: '' },
  [REG_STATUS.WAITLIST]: { label: 'В листе ожидания', status: REG_STATUS.WAITLIST },
  [REG_STATUS.OFFERED]: { label: 'Место предложено', status: REG_STATUS.OFFERED },
  [REG_STATUS.CANCELLED]: { label: 'Отказался', tone: 'danger', icon: 'x' },
  [REG_STATUS.ATTENDED]: { label: 'Пришёл', status: REG_STATUS.ATTENDED },
  [REG_STATUS.NO_SHOW]: { label: 'Не пришёл', status: REG_STATUS.NO_SHOW },
};
const REASON = { ...Object.fromEntries(CANCEL_REASONS.map((r) => [r.code, r.label])), offer_expired: 'Не успел занять место', offer_declined: 'Отказался от места', none: 'Без причины' };

export default function Reports({ query }) {
  const { version } = useEvents();
  const mine = useApi(() => eventsApi.mine(), [version]);
  const list = mine.data?.items || [];
  const selectedId = query?.get('event') || list[0]?.id;
  const event = list.find((e) => e.id === selectedId);

  if (mine.error) return <EmptyState icon="info" title="Не удалось загрузить мероприятия" text={errorText(mine.error)} action={<button className="btn btn--soft" onClick={mine.reload}>Повторить</button>} />;
  if (!mine.data) return <div className="loader" />;
  if (!list.length) {
    return (
      <>
        <div className="content-head"><h1 className="page-title">Отчётность</h1></div>
        <EmptyState icon="chart" title="У вас пока нет мероприятий" text="Создайте первую встречу — здесь появятся записи, подтверждения и отказы."
          action={<a className="btn btn--primary" href="#/cabinet/create">Создать мероприятие</a>} />
      </>
    );
  }

  return (
    <div>
      <div className="content-head">
        <div>
          <h1 className="page-title">Отчётность</h1>
          <p className="muted">Записи, подтверждения за 6 часов, отказы и лист ожидания по вашим мероприятиям.</p>
        </div>
      </div>

      <div className="event-switch" role="tablist">
        {list.map((e) => (
          <button key={e.id} role="tab" aria-selected={e.id === selectedId} className={`event-switch__item ${e.id === selectedId ? 'is-on' : ''}`}
            onClick={() => navigate(`/cabinet/reports?event=${encodeURIComponent(e.id)}`)}>
            <span className="event-switch__title">{e.title}</span>
            <span className="muted small">{fmtDate(e.starts_at, e.timezone)}
              {e.status === 'moderation' && ' · на проверке'}{e.status === 'cancelled' && ' · отменено'}</span>
          </button>
        ))}
      </div>

      {event ? <Report event={event} onChanged={mine.reload} /> : <EmptyState title="Мероприятие не найдено" />}
    </div>
  );
}

/** @param {{event: import('../../types/index.js').EventDetails}} props */
function Report({ event, onChanged }) {
  const { toast } = useUi();
  const { bump } = useEvents();
  const { data: r, error, reload } = useApi(() => reportsApi.get(event.id), [event.id]);
  const [msgOpen, setMsgOpen] = useState(false);
  const [cancelOpen, setCancelOpen] = useState(false);
  const [msg, setMsg] = useState('');
  const [busy, setBusy] = useState(false);
  const id = encodeURIComponent(event.id);
  const cancelled = event.status === 'cancelled';

  const send = async (fn, ok) => {
    setBusy(true);
    try { const out = await fn(); toast(ok(out), 'success'); return true; } catch (e) { toast(errorText(e), 'danger'); return false; } finally { setBusy(false); }
  };

  const funnel = r ? [
    { label: 'Просмотры', value: r.views },
    { label: 'Записи', value: r.registrations_total },
    { label: 'Подтверждения', value: r.confirmed },
  ] : [];

  return (
    <>
      <div className="report-head">
        <div>
          <h2 className="report-head__title">{event.title}</h2>
          <div className="row-card__meta">
            <span><Icon name="clock" size={14} /> {fmtDate(event.starts_at, event.timezone)}, {fmtTime(event.starts_at, event.timezone)}</span>
            <span><Icon name="pin" size={14} /> {event.address}</span>
          </div>
        </div>
        <div className="row gap-8 wrap">
          {event.status === 'published' && <a className="btn btn--ghost btn--sm" href={`#/event/${id}`}>Открыть афишу</a>}
          {!cancelled && <a className="btn btn--ghost btn--sm" href={`#/cabinet/create?edit=${id}`}><Icon name="edit" size={16} /> Изменить</a>}
          <button className="btn btn--ghost btn--sm" onClick={() => setMsgOpen(true)} disabled={cancelled}><Icon name="message" size={16} /> Написать участникам</button>
          <a className="btn btn--ghost btn--sm" href={`#/cabinet/create?from=${id}`}><Icon name="copy" size={16} /> Создать похожее</a>
          {!cancelled && <button className="btn btn--ghost btn--sm btn--danger-text" onClick={() => setCancelOpen(true)}>Отменить</button>}
        </div>
      </div>

      {error ? <EmptyState icon="info" title="Не удалось загрузить отчёт" text={errorText(error)} action={<button className="btn btn--soft" onClick={reload}>Повторить</button>} /> : !r ? <div className="loader" /> : (
        <>
          <div className="stats">
            <Stat label="Просмотры карточки" value={r.views} note="интерес к афише" />
            <Stat label="Записались" value={r.registrations_total} note="всего за всё время" />
            <Stat label="Сейчас записано" value={r.capacity ? `${r.registered_now} / ${r.capacity}` : r.registered_now}
              note={r.capacity ? `заполнено на ${pct(r.registered_now, r.capacity)}%` : 'без ограничения мест'} meter={r.capacity ? pct(r.registered_now, r.capacity) : null} />
            <Stat label="Подтвердили" value={r.confirmed} note="нажали «Приду» за 6 ч" />
            <Stat label="Отказались" value={r.cancelled_total} note={`из них поздних: ${r.cancelled_late}`} />
            <Stat label="Не ответили" value={r.no_answer} note="без реакции на подтверждение" />
            <Stat label="В листе ожидания" value={r.waitlist_size} note={`получили место из очереди: ${r.from_waitlist}`} />
            <Stat label="Конверсия" value={`${pct(r.confirmed, r.views)}%`} note="просмотр → подтверждение" />
          </div>

          <div className="report-grid">
            <section className="panel">
              <h3 className="panel__title">Воронка</h3>
              <div className="funnel">
                {funnel.map((s, i) => (
                  <div key={s.label} className="funnel__row">
                    <div className="funnel__label"><span>{s.label}</span><b>{s.value}</b></div>
                    <div className="funnel__bar"><span style={{ width: `${Math.max(2, pct(s.value, funnel[0].value || 1))}%` }} /></div>
                    {i > 0 && <div className="muted small">{pct(s.value, funnel[i - 1].value)}% от предыдущего шага</div>}
                  </div>
                ))}
              </div>
            </section>

            <section className="panel">
              <h3 className="panel__title">Причины отказов</h3>
              {r.cancelled_total ? (
                <div className="reasons">
                  {Object.entries(r.cancel_reasons).sort((a, b) => b[1] - a[1]).map(([code, n]) => (
                    <div key={code} className="reasons__row">
                      <span>{REASON[code] || code}</span>
                      <div className="reasons__bar"><span style={{ width: `${pct(n, r.cancelled_total)}%` }} /></div>
                      <b>{n}</b>
                    </div>
                  ))}
                  {r.cancelled_late > 0 && <p className="muted small">Поздний отказ — меньше чем за 1 час до начала. Это не наказание, а данные для вас.</p>}
                </div>
              ) : <p className="muted">Отказов пока нет.</p>}
            </section>
          </div>

          <section className="panel mt-16">
            <div className="panel__title-row">
              <h3 className="panel__title">Участники</h3>
              <span className="muted small">Имена из MAX — для переклички на входе</span>
            </div>
            {r.participants.length ? (
              <table className="table">
                <thead><tr><th>#</th><th>Участник</th><th>Статус</th><th>Комментарий</th></tr></thead>
                <tbody>
                  {r.participants.map((p, i) => {
                    const s = PARTICIPANT[p.status] || { label: p.status };
                    const note = [p.cancel_reason && REASON[p.cancel_reason], p.cancelled_late && 'поздний отказ'].filter(Boolean).join(' · ');
                    return (
                      <tr key={p.registration_id}>
                        <td className="muted" data-label="#">{i + 1}</td>
                        <td data-label="Участник">{p.name}</td>
                        <td data-label="Статус">
                          <div className="row gap-8 wrap">
                            <Badge status={s.status} label={s.label} tone={s.tone} icon={s.icon} />
                            {!p.bot_available && <Badge label="Нет связи с ботом" icon="bell" tone="danger" />}
                          </div>
                        </td>
                        <td className="muted" data-label="Комментарий">{note || '—'}</td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            ) : <p className="muted">Пока никто не записался.</p>}
          </section>
        </>
      )}

      <Modal open={msgOpen} onClose={() => setMsgOpen(false)} title="Сообщение участникам">
        <p className="muted">Бот отправит сообщение всем записанным. Например: «Перенесли в зал 2».</p>
        <textarea className="textarea" rows={4} value={msg} onChange={(e) => setMsg(e.target.value)} maxLength={MSG_LIMIT} placeholder="Текст сообщения" />
        <div className="field__hint">{msg.length}/{MSG_LIMIT}</div>
        <div className="modal__actions">
          <button className="btn btn--ghost" onClick={() => setMsgOpen(false)}>Отмена</button>
          <button className="btn btn--primary" disabled={!msg.trim() || busy}
            onClick={async () => { if (await send(() => eventsApi.message(event.id, msg.trim()), (o) => `Отправлено участникам: ${o?.notified_count ?? '—'}`)) { setMsg(''); setMsgOpen(false); } }}>Отправить</button>
        </div>
      </Modal>

      <CancelEventModal open={cancelOpen} busy={busy} onClose={() => setCancelOpen(false)}
        onConfirm={async (text) => {
          if (await send(() => eventsApi.cancel(event.id, text), (o) => `Мероприятие отменено. Бот сообщит участникам: ${o?.notified_count ?? '—'}`)) {
            setCancelOpen(false); bump(); onChanged();
          }
        }} />
    </>
  );
}

function CancelEventModal({ open, busy, onClose, onConfirm }) {
  const [text, setText] = useState('');
  return (
    <Modal open={open} onClose={onClose} title="Отменить мероприятие?">
      <p className="muted">Всем записанным и стоящим в очереди бот отправит сообщение об отмене. Это действие нельзя отменить.</p>
      <div className="label">Сообщение участникам (необязательно)</div>
      <textarea className="textarea" rows={3} value={text} maxLength={MSG_LIMIT} onChange={(e) => setText(e.target.value)} placeholder="Например: из-за штормового предупреждения занятие не состоится" />
      <div className="modal__actions">
        <button className="btn btn--ghost" onClick={onClose}>Не отменять</button>
        <button className="btn btn--danger" disabled={busy} onClick={() => onConfirm(text.trim())}>Отменить мероприятие</button>
      </div>
    </Modal>
  );
}

function Stat({ label, value, note, meter }) {
  return (
    <div className="stat">
      <div className="stat__label">{label}</div>
      <div className="stat__value">{value}</div>
      {meter != null && <div className="meter"><span style={{ width: `${meter}%` }} /></div>}
      <div className="stat__note">{note}</div>
    </div>
  );
}
