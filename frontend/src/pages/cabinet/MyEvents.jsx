import { useState } from 'react';
import { useEvents } from '../../store/events.jsx';
import { REG_STATUS, CANCEL_REASONS } from '../../types/index.js';
import Icon from '../../components/Icon.jsx';
import Badge from '../../components/Badge.jsx';
import EmptyState from '../../components/EmptyState.jsx';
import CancelModal from '../../components/CancelModal.jsx';
import OfferBanner from '../../components/OfferBanner.jsx';
import { fmtDate, fmtTime, fmtShort, fmtDay, fmtMonth } from '../../lib/format.js';
import { minutesUntil } from '../../lib/time.js';
import { navigate } from '../../lib/router.js';

const reasonLabel = (code) => CANCEL_REASONS.find((r) => r.code === code)?.label
  || { offer_expired: 'Истёк срок предложения', offer_declined: 'Отказ от места' }[code] || null;

export default function MyEvents() {
  const ev = useEvents();
  const [tab, setTab] = useState('confirmed');
  const [showPast, setShowPast] = useState(false);
  const [cancel, setCancel] = useState(null);

  const byDate = (a, b) => Date.parse(a.event.starts_at) - Date.parse(b.event.starts_at);
  const future = ev.mine.filter((r) => Date.parse(r.event.starts_at) > Date.now()).sort(byDate);
  const past = ev.mine.filter((r) => Date.parse(r.event.starts_at) <= Date.now()).sort((a, b) => -byDate(a, b));
  const offered = future.filter((r) => r.status === REG_STATUS.OFFERED);

  const TABS = [
    { id: 'confirmed', label: 'Подтверждённые', items: future.filter((r) => r.status === REG_STATUS.CONFIRMED) },
    { id: 'registered', label: 'Ждут подтверждения', items: future.filter((r) => r.status === REG_STATUS.REGISTERED) },
    { id: 'waitlist', label: 'Хочу пойти · лист ожидания', items: future.filter((r) => r.status === REG_STATUS.WAITLIST || r.status === REG_STATUS.OFFERED) },
    { id: 'cancelled', label: 'Отменённые', items: future.filter((r) => r.status === REG_STATUS.CANCELLED) },
  ];
  const current = TABS.find((t) => t.id === tab);

  return (
    <div>
      <div className="content-head">
        <div>
          <h1 className="page-title">Мои записи</h1>
          <p className="muted">Ближайшие сверху. Отказаться можно в одно действие — место сразу уйдёт следующему.</p>
        </div>
      </div>

      {offered.map((r) => (
        <div key={r.id} className="mb-16">
          <div className="small muted mb-4">«{r.event.title}», {fmtDate(r.event.starts_at, r.event.timezone)}</div>
          <OfferBanner registration={r} tz={r.event.timezone} />
        </div>
      ))}

      <div className="tabs" role="tablist">
        {TABS.map((t) => (
          <button key={t.id} role="tab" aria-selected={tab === t.id} className={tab === t.id ? 'is-on' : ''} onClick={() => setTab(t.id)}>
            {t.label} <span className="count">{t.items.length}</span>
          </button>
        ))}
      </div>

      {!ev.mineLoaded ? <div className="loader" /> : current.items.length ? (
        <div className="rows">
          {current.items.map((r) => (
            <Row key={r.id} r={r} busy={ev.pending === r.id}
              onConfirm={() => ev.confirm(r)}
              onCancel={() => setCancel(r)} />
          ))}
        </div>
      ) : (
        <EmptyState icon="ticket" title={EMPTY[tab].title} text={EMPTY[tab].text}
          action={tab !== 'cancelled' && <a className="btn btn--soft" href="#/catalog">Открыть каталог</a>} />
      )}

      <div className="collapse">
        <button className="collapse__head" aria-expanded={showPast} onClick={() => setShowPast(!showPast)}>
          <span>Прошедшие <span className="count">{past.length}</span></span>
          <Icon name="chevronDown" size={18} style={{ transform: showPast ? 'rotate(180deg)' : 'none', transition: 'transform .2s' }} />
        </button>
        {showPast && (
          <div className="past">
            {past.length ? past.map((r) => (
              <div key={r.id} className="past__row">
                <span className="muted">{fmtShort(r.event.starts_at, r.event.timezone)}</span>
                <span>{r.event.title}</span>
                {/* В MVP «подтверждён» после события = посещение (ТЗ, раздел 9) */}
                {r.status === REG_STATUS.CONFIRMED || r.status === REG_STATUS.ATTENDED
                  ? <Badge status={REG_STATUS.ATTENDED} />
                  : r.status === REG_STATUS.CANCELLED ? <Badge label="Отменено вами" icon="x" /> : <Badge status={r.status} />}
              </div>
            )) : <p className="muted">История посещений появится после первого мероприятия.</p>}
          </div>
        )}
      </div>

      {cancel && (
        <CancelModal title={cancel.event.title} waitlist={cancel.status === REG_STATUS.WAITLIST || cancel.status === REG_STATUS.OFFERED}
          onClose={() => setCancel(null)} onConfirm={(reason) => ev.cancel(cancel, reason)} />
      )}
    </div>
  );
}

const EMPTY = {
  confirmed: { title: 'Нет подтверждённых записей', text: 'Бот попросит подтвердить участие за 6 часов до начала.' },
  registered: { title: 'Нет записей, ждущих подтверждения', text: 'Найдите встречу в каталоге или в рекомендациях.' },
  waitlist: { title: 'Вы не стоите в очередях', text: 'Если на интересном событии нет мест — встаньте в лист ожидания, бот сообщит, когда место освободится.' },
  cancelled: { title: 'Отменённых записей нет', text: 'Здесь появятся записи, от которых вы отказались. Повторно записаться можно, если остались места.' },
};

function Row({ r, busy, onConfirm, onCancel }) {
  const e = r.event;
  const canConfirm = r.status === REG_STATUS.REGISTERED && minutesUntil(e.starts_at) <= 360 && minutesUntil(e.starts_at) > 60;
  const canCancel = [REG_STATUS.REGISTERED, REG_STATUS.CONFIRMED, REG_STATUS.WAITLIST].includes(r.status);
  const eventCancelled = e.status === 'cancelled';
  return (
    <div className="row-card" onClick={() => navigate(`/event/${encodeURIComponent(e.id)}`)}>
      <div className="datebox"><span>{fmtMonth(e.starts_at, e.timezone)}</span><b>{fmtDay(e.starts_at, e.timezone)}</b></div>
      <div className="row-card__main">
        <div className="row-card__title">{e.title}</div>
        <div className="row-card__meta">
          <span><Icon name="clock" size={14} /> {fmtDate(e.starts_at, e.timezone)}, {fmtTime(e.starts_at, e.timezone)}</span>
          {e.address && <span><Icon name="pin" size={14} /> {e.address}</span>}
          {r.status === REG_STATUS.CANCELLED && reasonLabel(r.cancel_reason) && <span>Причина: {reasonLabel(r.cancel_reason)}</span>}
        </div>
      </div>
      <div className="row-card__side" onClick={(ev) => ev.stopPropagation()}>
        {eventCancelled ? <Badge label="Мероприятие отменено" tone="danger" icon="x" /> : <Badge status={r.status} position={r.queue_position} />}
        {!eventCancelled && (
          <div className="row gap-8">
            {canConfirm && <button className="btn btn--primary btn--sm" disabled={busy} onClick={onConfirm}>Приду</button>}
            {canCancel && <button className="btn btn--ghost btn--sm" disabled={busy} onClick={onCancel}>{r.status === REG_STATUS.WAITLIST ? 'Выйти из очереди' : 'Отказаться'}</button>}
            {r.status === REG_STATUS.CANCELLED && <a className="btn btn--ghost btn--sm" href={`#/event/${encodeURIComponent(e.id)}`}>Записаться снова</a>}
          </div>
        )}
      </div>
    </div>
  );
}
