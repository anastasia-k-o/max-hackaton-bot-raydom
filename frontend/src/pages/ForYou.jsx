import { errorText } from '../api/index.js';
import { useSession } from '../store/session.jsx';
import { useEvents } from '../store/events.jsx';
import { useRecommendations } from '../features/recommendations/useRecommendations.js';
import { MIN_INTERESTS } from '../features/recommendations/recommendations.js';
import EventCard from '../components/EventCard.jsx';
import InterestPicker from '../components/InterestPicker.jsx';
import EmptyState from '../components/EmptyState.jsx';
import Icon from '../components/Icon.jsx';
import { navigate } from '../lib/router.js';

const matchText = (matched) => {
  const names = matched.slice(0, 3).map((t) => t.name).join(', ');
  return `Совпадает: ${names}${matched.length > 3 ? ` и ещё ${matched.length - 3}` : ''}`;
};

export default function ForYou() {
  const { interests, setInterests } = useSession();
  const { tags } = useEvents();
  const { items, loading, error, hasEnoughInterests, reload } = useRecommendations();

  if (!hasEnoughInterests) {
    return (
      <div className="container page">
        <div className="onboarding">
          <div className="onboarding__head">
            <span className="eyebrow"><Icon name="sparkle" size={14} /> Для вас</span>
            <h1 className="page-title">Что вам интересно?</h1>
            <p className="muted">Выберите минимум {MIN_INTERESTS} тега — подберём встречи, где совпадает больше всего ваших интересов. Теги можно поменять в профиле.</p>
          </div>
          <InterestPicker initial={interests} onSave={setInterests} saveLabel="Показать рекомендации" />
        </div>
      </div>
    );
  }

  return (
    <div className="container page">
      <div className="page-head">
        <div>
          <span className="eyebrow"><Icon name="sparkle" size={14} /> Для вас</span>
          <h1 className="page-title">Рекомендации</h1>
          <p className="muted">Подобрано по вашим интересам: чем больше совпадающих тегов, тем выше встреча, при равенстве — ближайшие.</p>
        </div>
        <button className="btn btn--soft" onClick={() => navigate('/cabinet/profile')}>Изменить интересы</button>
      </div>

      <div className="chips mb-24">{interests.map((t) => <span key={t} className="chip chip--static">{tags[t]?.name || t}</span>)}</div>

      {error ? (
        <EmptyState icon="info" title="Не удалось загрузить рекомендации" text={errorText(error)}
          action={<button className="btn btn--soft" onClick={reload}>Повторить</button>} />
      ) : loading ? (
        <div className="grid">{Array.from({ length: 3 }).map((_, i) => <div key={i} className="card skeleton" />)}</div>
      ) : items.length ? (
        <div className="grid">{items.map((r) => <EventCard key={r.event.id} event={r.event} reason={matchText(r.matched)} />)}</div>
      ) : (
        <EmptyState icon="sparkle" title="Пока нет рекомендаций по вашим интересам" text="Попробуйте добавить ещё интересы или загляните в каталог."
          action={(
            <div className="row gap-8 center wrap">
              <button className="btn btn--primary" onClick={() => navigate('/cabinet/profile')}>Добавить интересы</button>
              <a className="btn btn--soft" href="#/catalog">Открыть каталог</a>
            </div>
          )} />
      )}
    </div>
  );
}
