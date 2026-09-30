import { useEffect, useState } from 'react';
import { events as eventsApi, errorText } from '../api/index.js';
import { useSession } from '../store/session.jsx';
import { useEvents } from '../store/events.jsx';
import { useUi } from '../store/ui.jsx';
import { useApi } from '../store/useApi.js';
import { REG_STATUS } from '../types/index.js';
import Cover from '../components/Cover.jsx';
import Icon from '../components/Icon.jsx';
import CancelModal from '../components/CancelModal.jsx';
import EmptyState from '../components/EmptyState.jsx';
import VerificationGate from '../components/VerificationGate.jsx';
import OfferBanner from '../components/OfferBanner.jsx';
import { fmtDate, fmtTime, fmtDuration, seatsText, pct, initials } from '../lib/format.js';
import { minutesUntil } from '../lib/time.js';
import { downloadIcs } from '../lib/calendar.js';
import { share, openLink } from '../lib/max.js';
import { navigate } from '../lib/router.js';
import { cityById } from '../lib/cities.js';

const CONFIRM_WINDOW_MIN = 360; // ТЗ: подтверждение за 6 ч

/**
 * Таблица состояний кнопки — ТЗ, раздел 3.
 * @param {import('../types/index.js').EventDetails} e
 */
function actionState(e, user) {
  const reg = e.my_registration;
  if (e.author.id === user.id) return 'author';
  if (e.status === 'cancelled') return 'cancelled';
  if (reg?.status === REG_STATUS.OFFERED) return 'offered';
  const closed = minutesUntil(e.registration_closes_at) <= 0;
  if (reg?.status === REG_STATUS.REGISTERED || reg?.status === REG_STATUS.CONFIRMED) return closed ? 'registered_closed' : 'registered';
  if (closed) return 'closed';
  if (reg?.status === REG_STATUS.WAITLIST) return 'waitlisted';
  if (e.capacity != null && e.registered_count >= e.capacity) return 'full';
  return 'open';
}

export default function EventPage({ id }) {
  const { user } = useSession();
  const back = () => (history.length > 1 ? history.back() : navigate('/catalog'));

  return (
    <div className="container page">
      <button className="back" onClick={back}><Icon name="arrowLeft" size={16} /> Назад</button>
      <VerificationGate title="Подробности — после верификации">
        {user?.is_verified && <Details id={id} />}
      </VerificationGate>
    </div>
  );
}

function Details({ id }) {
  const { user } = useSession();
  const { toast } = useUi();
  const ev = useEvents();
  const { data: e, error, loading, reload } = useApi(() => eventsApi.get(id), [id, ev.version]);
  const [cancelOpen, setCancelOpen] = useState(false);

  useEffect(() => { eventsApi.trackView(id).catch(() => {}); }, [id]);

  if (error) {
    return error.status === 404
      ? <EmptyState title="Мероприятие не найдено" text="Возможно, его сняли с публикации." action={<a className="btn btn--soft" href="#/catalog">В каталог</a>} />
      : <EmptyState icon="info" title="Не удалось загрузить мероприятие" text={errorText(error)} action={<button className="btn btn--soft" onClick={reload}>Повторить</button>} />;
  }
  if (!e && loading) return <div className="loader" aria-label="Загрузка" />;
  if (!e) return null;

  const cat = ev.categoryById(e.category_id);
  const reg = e.my_registration;
  const state = actionState(e, user);
  const busy = ev.pending === e.id || (reg && ev.pending === reg.id);
  const canConfirm = reg?.status === REG_STATUS.REGISTERED && minutesUntil(e.starts_at) <= CONFIRM_WINDOW_MIN && state === 'registered';

  const onShare = async () => {
    const link = e.mini_app_url || location.href;
    const r = await share(e.title, link);
    eventsApi.trackView(e.id, 'share').catch(() => {});
    if (r === 'copied') toast('Ссылка скопирована');
  };
  const onComplain = async () => {
    try { await eventsApi.complain(e.id); toast('Спасибо! Афиша отправлена на проверку'); navigate('/catalog'); }
    catch (err) { toast(errorText(err), 'danger'); }
  };

  return (
    <div className="detail">
      <div className="detail__main">
        <Cover event={e} height={300} big />
        <div className="detail__head">
          <div className="card__cat">{cat?.name}</div>
          <h1 className="detail__title">{e.title}</h1>
          <div className="tags">{e.tags.map((t) => <span key={t.id} className="tag">{t.name}</span>)}</div>
        </div>

        <section className="block">
          <h3>О встрече</h3>
          <p>{e.description}</p>
        </section>

        <section className="facts">
          <Fact label="Что взять с собой" value={e.bring || '—'} />
          <Fact label="Уровень подготовки" value={e.level || 'Любой'} />
          <Fact label="Возраст" value={e.age_limit || 'Без ограничений'} />
          <Fact label="Как найти вход" value={e.how_to_find || '—'} />
        </section>

        <section className="block author">
          <div className="author__avatar">{initials(e.author.name)}</div>
          <div>
            <div className="muted small">Организатор</div>
            <div className="author__name">{e.author.name}</div>
          </div>
          <div className="author__contact">
            <span className="muted small">Контакт для вопросов</span>
            <span>{e.contact}</span>
          </div>
        </section>

        {state !== 'author' && (
          <button className="link link--muted" onClick={onComplain}><Icon name="flag" size={14} /> Пожаловаться на афишу</button>
        )}
      </div>

      <aside className="detail__side">
        <div className="panel sticky">
          <div className="panel__row">
            <Icon name="calendar" />
            <div><div className="panel__main">{fmtDate(e.starts_at, e.timezone)}</div><div className="muted small">Дата</div></div>
          </div>
          <div className="panel__row">
            <Icon name="clock" />
            <div><div className="panel__main">{fmtTime(e.starts_at, e.timezone)} · {fmtDuration(e.duration_min)}</div><div className="muted small">Начало и длительность</div></div>
          </div>
          <div className="panel__row">
            <Icon name="pin" />
            <div>
              <div className="panel__main">{e.address}</div>
              <div className="muted small">{e.district} район ·{' '}
                <button className="link" onClick={() => openLink(e.lat != null
                  ? `https://yandex.ru/maps/?pt=${e.lon},${e.lat}&z=16`
                  : `https://yandex.ru/maps/?text=${encodeURIComponent(`${cityById(e.city_id).name}, ${e.address}`)}`)}>на карте</button>
              </div>
            </div>
          </div>
          <div className="panel__row">
            <Icon name="users" />
            <div className="grow">
              <div className="panel__main">{seatsText(e)}</div>
              {e.capacity != null && <div className="meter" aria-label="Заполненность"><span style={{ width: `${pct(e.registered_count, e.capacity)}%` }} /></div>}
              {e.waitlist_count > 0 && <div className="muted small">В листе ожидания: {e.waitlist_count}</div>}
            </div>
          </div>

          <div className="panel__action">
            <Action state={state} e={e} reg={reg} busy={busy} canConfirm={canConfirm}
              onRegister={() => ev.register(e.id)}
              onConfirm={() => ev.confirm(reg)}
              onCancel={() => setCancelOpen(true)} />
          </div>

          <div className="panel__secondary">
            <button className="btn btn--ghost btn--sm" onClick={onShare}><Icon name="share" size={16} /> Поделиться</button>
            <button className="btn btn--ghost btn--sm" onClick={() => downloadIcs(e)}><Icon name="calendar" size={16} /> В календарь</button>
          </div>
        </div>
        <p className="hint">Запись закрывается в {fmtTime(e.registration_closes_at, e.timezone)}. Напоминания и запрос подтверждения за 6 часов придут от бота в MAX.</p>
      </aside>

      {cancelOpen && (
        <CancelModal title={e.title} waitlist={reg?.status === REG_STATUS.WAITLIST}
          onClose={() => setCancelOpen(false)} onConfirm={(reason) => ev.cancel(reg, reason)} />
      )}
    </div>
  );
}

function Fact({ label, value }) {
  return <div className="fact"><div className="muted small">{label}</div><div>{value}</div></div>;
}

function Action({ state, e, reg, busy, canConfirm, onRegister, onConfirm, onCancel }) {
  switch (state) {
    case 'open':
      return <button className="btn btn--primary btn--block btn--lg" onClick={onRegister} disabled={busy}>Записаться</button>;
    case 'full':
      return (
        <>
          <button className="btn btn--primary btn--block btn--lg" onClick={onRegister} disabled={busy}>Встать в лист ожидания</button>
          <div className="status-note">Мест нет. Вы будете {e.waitlist_count + 1}-м в очереди — бот предложит место, если оно освободится.</div>
        </>
      );
    case 'registered':
      return (
        <>
          <div className="status status--accent-2"><Icon name="check" size={16} stroke={2.4} />
            {reg.status === REG_STATUS.CONFIRMED ? 'Вы записаны · участие подтверждено' : 'Вы записаны'}</div>
          {canConfirm && <button className="btn btn--primary btn--block" onClick={onConfirm} disabled={busy}>Приду — подтвердить участие</button>}
          <button className="btn btn--ghost btn--block" onClick={onCancel} disabled={busy}>Отменить запись</button>
        </>
      );
    case 'registered_closed':
      return <div className="status status--accent-2"><Icon name="check" size={16} stroke={2.4} /> Вы записаны · скоро начало</div>;
    case 'offered':
      return <OfferBanner registration={reg} tz={e.timezone} />;
    case 'waitlisted':
      return (
        <>
          <div className="status status--accent"><Icon name="clock" size={16} /> Вы в листе ожидания, позиция {reg.queue_position}</div>
          <button className="btn btn--ghost btn--block" onClick={onCancel} disabled={busy}>Выйти из очереди</button>
        </>
      );
    case 'closed':
      return <div className="status"><Icon name="lock" size={16} /> Запись закрыта — событие скоро начнётся</div>;
    case 'cancelled':
      return <div className="status status--danger"><Icon name="x" size={16} /> Мероприятие отменено</div>;
    case 'author':
      return (
        <>
          <div className="status"><Icon name="user" size={16} /> Вы организатор</div>
          <a className="btn btn--soft btn--block" href={`#/cabinet/reports?event=${encodeURIComponent(e.id)}`}><Icon name="chart" size={16} /> Отчётность</a>
          {e.status !== 'cancelled' && <a className="btn btn--ghost btn--block" href={`#/cabinet/create?edit=${encodeURIComponent(e.id)}`}><Icon name="edit" size={16} /> Изменить</a>}
        </>
      );
    default:
      return null;
  }
}
