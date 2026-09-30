import { useEffect, useState } from 'react';
import { events as eventsApi, errorText } from '../api/index.js';
import { cityById } from '../lib/cities.js';
import { useEvents } from '../store/events.jsx';
import { useSession } from '../store/session.jsx';
import { useApi } from '../store/useApi.js';
import { categoryUi } from '../lib/categories.js';
import EventCard from '../components/EventCard.jsx';
import EmptyState from '../components/EmptyState.jsx';
import Icon from '../components/Icon.jsx';
import { plural } from '../lib/format.js';

// Фильтры и сортировка — на сервере
const WHEN = [
  { id: '', label: 'Любая дата' },
  { id: 'today', label: 'Сегодня' },
  { id: 'tomorrow', label: 'Завтра' },
  { id: 'weekend', label: 'Выходные' },
  { id: 'week', label: 'Эта неделя' },
];
const DAYPART = [
  { id: '', label: 'Любое время' },
  { id: 'morning', label: 'Утро' },
  { id: 'day', label: 'День' },
  { id: 'evening', label: 'Вечер' },
];
const SORTS = [
  { id: 'starts_at', label: 'Ближайшие' },
  { id: 'published_at', label: 'Новые' },
  { id: 'popularity', label: 'Популярные' },
];
const PAGE_SIZE = 9;

function useDebounced(value, ms = 300) {
  const [v, setV] = useState(value);
  useEffect(() => { const t = setTimeout(() => setV(value), ms); return () => clearTimeout(t); }, [value, ms]);
  return v;
}

export default function Catalog() {
  const { categories, categoryById, tags: tagIndex, version } = useEvents();
  const { user } = useSession();
  const [q, setQ] = useState('');
  const [cat, setCat] = useState('');
  const [tagIds, setTagIds] = useState([]);
  const [date, setDate] = useState('');
  const [daypart, setDaypart] = useState('');
  const [district, setDistrict] = useState('');
  const [hasSeats, setHasSeats] = useState(false);
  const [sort, setSort] = useState('starts_at');
  const [pages, setPages] = useState(1);
  const debouncedQ = useDebounced(q.trim());
  const city = cityById(user?.city_id);

  const query = { city_id: city.id, q: debouncedQ, category_id: cat, tag_ids: tagIds, date, daypart, district, has_seats: hasSeats, sort, page: 1, page_size: PAGE_SIZE * pages };
  const { data, error, loading, reload } = useApi(() => eventsApi.list(query), [JSON.stringify(query), user?.is_verified, version]);

  useEffect(() => setPages(1), [debouncedQ, cat, tagIds, date, daypart, district, hasSeats, sort]);

  const tagPool = cat ? categoryById(cat)?.tags || [] : [];
  const active = [
    q && { k: 'q', label: `«${q}»`, clear: () => setQ('') },
    cat && { k: 'cat', label: categoryById(cat)?.name, clear: () => { setCat(''); setTagIds([]); } },
    ...tagIds.map((t) => ({ k: `t-${t}`, label: tagIndex[t]?.name, clear: () => setTagIds(tagIds.filter((x) => x !== t)) })),
    date && { k: 'date', label: WHEN.find((w) => w.id === date).label, clear: () => setDate('') },
    daypart && { k: 'dp', label: DAYPART.find((w) => w.id === daypart).label, clear: () => setDaypart('') },
    district && { k: 'd', label: `${district} район`, clear: () => setDistrict('') },
    hasSeats && { k: 'free', label: 'Есть места', clear: () => setHasSeats(false) },
  ].filter(Boolean);
  const resetAll = () => { setQ(''); setCat(''); setTagIds([]); setDate(''); setDaypart(''); setDistrict(''); setHasSeats(false); };

  const items = data?.items || [];
  const total = data?.total ?? 0;

  return (
    <div className="container page">
      <section className="hero">
        <div>
          <h1 className="hero__title">Бесплатные встречи по интересам</h1>
          <p className="hero__sub">Книжные клубы, йога, настолки, прогулки — всё в одном месте, с записью через MAX.</p>
        </div>
        <label className="search">
          <Icon name="search" />
          <input value={q} onChange={(e) => setQ(e.target.value)} placeholder="Поиск по названию и описанию" aria-label="Поиск" />
          {q && <button className="icon-btn icon-btn--sm" onClick={() => setQ('')} aria-label="Очистить"><Icon name="x" size={16} /></button>}
        </label>
      </section>

      <div className="cats" aria-label="Направления">
        <button className={`cat ${!cat ? 'is-on' : ''}`} aria-pressed={!cat} onClick={() => { setCat(''); setTagIds([]); }}>Все</button>
        {categories.map((c) => (
          <button key={c.id} className={`cat ${cat === c.id ? 'is-on' : ''}`} aria-pressed={cat === c.id}
            onClick={() => { setCat(cat === c.id ? '' : c.id); setTagIds([]); }}>
            <Icon name={categoryUi(c.id).icon} size={16} /> {categoryUi(c.id).short}
          </button>
        ))}
      </div>

      {tagPool.length > 0 && (
        <div className="chips chips--tags">
          {tagPool.map((t) => (
            <button key={t.id} className={`chip chip--sm ${tagIds.includes(t.id) ? 'is-on' : ''}`} aria-pressed={tagIds.includes(t.id)}
              onClick={() => setTagIds(tagIds.includes(t.id) ? tagIds.filter((x) => x !== t.id) : [...tagIds, t.id])}>{t.name}</button>
          ))}
        </div>
      )}

      <div className="toolbar">
        <div className="toolbar__filters">
          <Select value={date} onChange={setDate} options={WHEN} label="Дата" />
          <Select value={daypart} onChange={setDaypart} options={DAYPART} label="Время суток" />
          <Select value={district} onChange={setDistrict} label="Район"
            options={[{ id: '', label: 'Все районы' }, ...city.districts.map((d) => ({ id: d, label: d }))]} />
          <label className="switch">
            <input type="checkbox" checked={hasSeats} onChange={(e) => setHasSeats(e.target.checked)} />
            <span className="switch__track" /> Только со свободными местами
          </label>
        </div>
        <div className="toolbar__sort">
          <span className="muted">Сортировка</span>
          <div className="seg" role="radiogroup" aria-label="Сортировка">
            {SORTS.map((s) => (
              <button key={s.id} role="radio" aria-checked={sort === s.id} className={sort === s.id ? 'is-on' : ''} onClick={() => setSort(s.id)}>{s.label}</button>
            ))}
          </div>
        </div>
      </div>

      {active.length > 0 && (
        <div className="active-filters">
          {active.map((f) => (
            <span key={f.k} className="pill">{f.label}<button onClick={f.clear} aria-label={`Убрать фильтр ${f.label}`}><Icon name="x" size={12} stroke={2.2} /></button></span>
          ))}
          <button className="link" onClick={resetAll}>Сбросить всё</button>
        </div>
      )}

      <div className="section-head">
        <h2>Афиша</h2>
        {data && <span className="muted">{total} {plural(total, 'мероприятие', 'мероприятия', 'мероприятий')}</span>}
      </div>

      {error ? (
        <EmptyState icon="info" title="Не удалось загрузить афишу" text={errorText(error)}
          action={<button className="btn btn--soft" onClick={reload}>Повторить</button>} />
      ) : !data && loading ? (
        <div className="grid">{Array.from({ length: 6 }).map((_, i) => <div key={i} className="card skeleton" />)}</div>
      ) : items.length ? (
        <>
          <div className="grid">{items.map((e) => <EventCard key={e.id} event={e} />)}</div>
          {items.length < total && (
            <div className="more"><button className="btn btn--ghost" disabled={loading} onClick={() => setPages(pages + 1)}>{loading ? 'Загружаем…' : 'Показать ещё'}</button></div>
          )}
        </>
      ) : (
        <EmptyState title="Ничего не нашлось" text="Попробуйте убрать часть фильтров."
          action={<button className="btn btn--soft" onClick={resetAll}>Сбросить фильтры</button>} />
      )}
    </div>
  );
}

function Select({ value, onChange, options, label }) {
  return (
    <label className="select">
      <select value={value} onChange={(e) => onChange(e.target.value)} aria-label={label}>
        {options.map((o) => <option key={o.id} value={o.id}>{o.label}</option>)}
      </select>
      <Icon name="chevronDown" size={16} />
    </label>
  );
}
