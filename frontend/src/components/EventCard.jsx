import Cover from './Cover.jsx';
import Icon from './Icon.jsx';
import Badge from './Badge.jsx';
import { useSession } from '../store/session.jsx';
import { useUi } from '../store/ui.jsx';
import { useEvents } from '../store/events.jsx';
import { navigate } from '../lib/router.js';

/**
 * Только общая информация 
 * @param {{event: import('../types/index.js').CatalogItem, reason?: string, preview?: boolean}} props
 */
export default function EventCard({ event, reason, preview = false }) {
  const { user } = useSession();
  const { openVerify } = useUi();
  const { categoryById } = useEvents();
  const verified = !!user?.is_verified;
  const locked = !verified && !preview;
  const status = verified ? event.my_registration_status : null;

  const open = () => {
    if (preview) return;
    if (!verified) return openVerify();
    navigate(`/event/${event.id}`);
  };

  return (
    <article
      className={`card ${preview ? 'card--preview' : ''}`}
      onClick={open}
      role={preview ? undefined : 'link'}
      tabIndex={preview ? undefined : 0}
      onKeyDown={(e) => (e.key === 'Enter' ? open() : null)}
      aria-label={locked ? `${event.title} — подробности после верификации` : event.title}
    >
      <div className="card__media">
        <Cover event={event} />
        <div className="card__badges">
          {status && <Badge status={status} />}
          {verified && event.author_id === user.id && !preview && <Badge label="Ваше" icon="user" />}
        </div>
        {locked && (
          <span className="card__lock" title="Подробности доступны после верификации"><Icon name="lock" size={14} /></span>
        )}
      </div>
      <div className="card__body">
        <div className="card__cat">{categoryById(event.category_id)?.name || '—'}</div>
        <h3 className="card__title">{event.title || 'Название мероприятия'}</h3>
        <p className="card__summary">{event.short_description || 'Короткое описание появится здесь'}</p>
        {reason && <div className="card__reason"><Icon name="sparkle" size={13} /> {reason}</div>}
        <div className="card__foot">
          <div className="tags">{event.tags.slice(0, 3).map((t) => <span key={t.id} className="tag">{t.name}</span>)}</div>
          {!preview && <span className="card__more">{!locked && 'Подробнее'} <Icon name="arrowRight" size={14} /></span>}
        </div>
      </div>
    </article>
  );
}
