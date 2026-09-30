import { useEffect, useMemo, useRef, useState } from 'react';
import { events as eventsApi, errorText, ApiError } from '../../api/index.js';
import { useSession } from '../../store/session.jsx';
import { useEvents } from '../../store/events.jsx';
import { useUi } from '../../store/ui.jsx';
import { categoryUi } from '../../lib/categories.js';
import { fromCityInputs, toCityInputs } from '../../lib/time.js';
import { cityById } from '../../lib/cities.js';
import EventCard from '../../components/EventCard.jsx';
import Icon from '../../components/Icon.jsx';
import { navigate } from '../../lib/router.js';

// название <= 80 ; address <= 512 — контракт бота
const LIMITS = { title: 80, short_description: 140, address: 512 };

const EMPTY = {
  title: '', short_description: '', description: '', category_id: '', tag_ids: [],
  date: '', time: '19:00', duration_min: 90, district: '', address: '', how_to_find: '',
  capacity: '', level: '', age_limit: '', bring: '', contact: '',
};

function toForm(e, { shiftWeek = false } = {}) {
  const ms = Date.parse(e.starts_at) + (shiftWeek ? 7 * 86400000 : 0);
  const { date, time } = toCityInputs(new Date(ms).toISOString(), cityById(e.city_id));
  return {
    title: e.title, short_description: e.short_description || '', description: e.description, category_id: e.category_id,
    tag_ids: e.tags.map((t) => t.id), date, time, duration_min: e.duration_min, district: e.district || '', address: e.address || '',
    how_to_find: e.how_to_find || '', capacity: e.capacity ?? '', level: e.level || '', age_limit: e.age_limit || '',
    bring: e.bring || '', contact: e.contact === 'Профиль автора в MAX' ? '' : e.contact || '',
  };
}

function toInput(f, city) {
  const trimOrNull = (s) => (s && s.trim() ? s.trim() : null);
  return {
    title: f.title.trim(),
    short_description: (f.short_description || f.description).trim().slice(0, LIMITS.short_description),
    description: f.description.trim(),
    category_id: f.category_id,
    tag_ids: f.tag_ids,
    starts_at: fromCityInputs(f.date, f.time, city),
    duration_min: Number(f.duration_min),
    city_id: city.id,
    district: f.district,
    address: f.address.trim(),
    how_to_find: f.how_to_find.trim(),
    capacity: f.capacity === '' ? null : Number(f.capacity),
    level: trimOrNull(f.level), age_limit: trimOrNull(f.age_limit), bring: trimOrNull(f.bring), contact: trimOrNull(f.contact),
  };
}

export default function CreateEvent({ query }) {
  const { user, updateMe } = useSession();
  const { toast } = useUi();
  const { categories, categoryById, tags: tagIndex, bump } = useEvents();
  const editId = query?.get('edit');
  const fromId = query?.get('from');
  const mode = editId ? 'edit' : fromId ? 'similar' : 'create';
  const city = cityById(user.city_id);

  const [f, setF] = useState(EMPTY);
  const [source, setSource] = useState(null);
  const [errors, setErrors] = useState({});
  const [done, setDone] = useState(null);
  const [busy, setBusy] = useState(false);
  const [loadError, setLoadError] = useState(null);

  useEffect(() => {
    setDone(null); setErrors({}); setLoadError(null); setSource(null);
    const id = editId || fromId;
    if (!id) { setF(EMPTY); return; }
    eventsApi.get(id)
      .then((e) => { setSource(e); setF(toForm(e, { shiftWeek: mode === 'similar' })); })
      .catch(setLoadError);
  }, [editId, fromId]); // eslint-disable-line react-hooks/exhaustive-deps

  const set = (k) => (e) => setF((s) => ({ ...s, [k]: e?.target ? e.target.value : e }));
  const toggleTag = (id) => setF((s) => ({ ...s, tag_ids: s.tag_ids.includes(id) ? s.tag_ids.filter((x) => x !== id) : [...s.tag_ids, id] }));

  const [suggestion, setSuggestion] = useState(null);
  const [dismissed, setDismissed] = useState(false);
  const reqSeq = useRef(0);
  useEffect(() => {
    if (mode === 'edit') return undefined;
    const text = `${f.title} ${f.description}`.trim();
    if (text.length < 4) { setSuggestion(null); return undefined; }
    const my = ++reqSeq.current;
    const t = setTimeout(() => {
      eventsApi.suggestTags(f.title, f.description)
        .then((s) => { if (my === reqSeq.current) { setSuggestion(s); setDismissed(false); } })
        .catch(() => {});
    }, 500);
    return () => clearTimeout(t);
  }, [f.title, f.description, mode]);
  const suggestionUseful = suggestion?.category_id &&
    (suggestion.category_id !== f.category_id || suggestion.tag_ids.some((t) => !f.tag_ids.includes(t)));
  const acceptSuggestion = () => setF((s) => ({
    ...s, category_id: suggestion.category_id,
    tag_ids: [...new Set([...s.tag_ids.filter((t) => tagIndex[t]?.category_id === suggestion.category_id), ...suggestion.tag_ids.filter((t) => tagIndex[t]?.category_id === suggestion.category_id)])],
  }));

  const preview = useMemo(() => ({
    id: 'preview', title: f.title, short_description: f.short_description || f.description.slice(0, 110),
    category_id: f.category_id || 'social', tags: f.tag_ids.map((id) => tagIndex[id]).filter(Boolean),
    cover_url: null, author_id: user.id, my_registration_status: null,
  }), [f, tagIndex, user.id]);

  if (!user.is_author) {
    return (
      <div className="locked">
        <div className="icon-tile"><Icon name="plus" size={22} /></div>
        <h2>Станьте автором</h2>
        <p className="muted">Роль «автор» включается в профиле. После этого можно публиковать встречи и смотреть отчётность.</p>
        <button className="btn btn--primary" onClick={() => updateMe({ is_author: true })}>Включить роль автора</button>
      </div>
    );
  }
  if (loadError) {
    return <div className="locked"><h2>Не удалось открыть мероприятие</h2><p className="muted">{errorText(loadError)}</p></div>;
  }
  if (mode === 'edit' && source && source.author.id !== user.id) {
    return <div className="locked"><h2>Это не ваше мероприятие</h2><p className="muted">Редактировать может только автор.</p></div>;
  }

  const validate = () => {
    const e = {};
    if (!f.title.trim()) e.title = 'Укажите название';
    else if (f.title.length > LIMITS.title) e.title = `До ${LIMITS.title} символов`;
    if (!f.description.trim()) e.description = 'Добавьте описание';
    if (!f.category_id) e.category_id = 'Выберите направление';
    if (!f.tag_ids.length) e.tag_ids = 'Выберите хотя бы один тег';
    if (!f.date || !f.time) e.starts_at = 'Укажите дату и время';
    else if (Date.parse(fromCityInputs(f.date, f.time, city)) < Date.now()) e.starts_at = 'Нельзя создать мероприятие в прошлом';
    if (!f.district) e.district = 'Выберите район';
    if (!f.address.trim()) e.address = 'Укажите адрес';
    else if (f.address.length > LIMITS.address) e.address = `До ${LIMITS.address} символов`;
    if (f.capacity !== '' && (!Number.isInteger(Number(f.capacity)) || Number(f.capacity) < 1)) e.capacity = 'Целое число больше 0';
    if (mode === 'edit' && source && f.capacity !== '' && Number(f.capacity) < source.registered_count) e.capacity = `Уже записано ${source.registered_count} — нельзя меньше`;
    setErrors(e);
    return !Object.keys(e).length;
  };

  const scrollToError = () => setTimeout(() => document.querySelector('.field--error')?.scrollIntoView({ behavior: 'smooth', block: 'center' }), 0);

  const submit = async (ev) => {
    ev.preventDefault();
    if (!validate()) { scrollToError(); return; }
    setBusy(true);
    try {
      const input = toInput(f, source ? cityById(source.city_id) : city);
      const saved = mode === 'edit' ? await eventsApi.update(source.id, input) : await eventsApi.create(input);
      bump();
      setDone({ event: saved });
      window.scrollTo({ top: 0, behavior: 'smooth' });
    } catch (err) {
      if (err instanceof ApiError && err.details?.length) { setErrors(err.fieldErrors()); scrollToError(); }
      toast(errorText(err), 'danger');
    } finally {
      setBusy(false);
    }
  };

  if (done) return <Done event={done.event} mode={mode} onNew={() => { setDone(null); setF(EMPTY); navigate('/cabinet/create'); }} />;

  const cat = categoryById(f.category_id);
  const title = { create: 'Создать мероприятие', similar: 'Похожее мероприятие', edit: 'Изменить мероприятие' }[mode];

  return (
    <div>
      <div className="content-head">
        <div>
          <h1 className="page-title">{title}</h1>
          <p className="muted">{mode === 'edit'
            ? 'Если изменить дату, время или адрес, все записавшиеся получат сообщение от бота.'
            : 'Займёт не больше 3 минут. Обязательные поля отмечены точкой.'}</p>
        </div>
      </div>

      <div className="create">
        <form className="form" onSubmit={submit} noValidate>
          <fieldset>
            <legend>Главное</legend>
            <Field label="Название" req error={errors.title} counter={`${f.title.length}/${LIMITS.title}`}>
              <input value={f.title} maxLength={LIMITS.title} onChange={set('title')} placeholder="Например, Йога в парке" />
            </Field>
            <Field label="Коротко для карточки" hint="Одна фраза — её видят в каталоге" error={errors.short_description} counter={`${f.short_description.length}/${LIMITS.short_description}`}>
              <input value={f.short_description} maxLength={LIMITS.short_description} onChange={set('short_description')} placeholder="Мягкая практика для новичков на свежем воздухе" />
            </Field>
            <Field label="Описание" req error={errors.description}>
              <textarea rows={5} value={f.description} onChange={set('description')} placeholder="Что будет происходить, для кого эта встреча" />
            </Field>

            {suggestionUseful && !dismissed && (
              <div className="ml">
                <div className="ml__head"><Icon name="sparkle" size={16} /> Подсказка модели</div>
                <div className="ml__body">
                  Направление: <b>{categoryById(suggestion.category_id)?.name}</b>
                  {suggestion.tag_ids.length > 0 && <> · теги: {suggestion.tag_ids.map((t) => <span key={t} className="tag">{tagIndex[t]?.name}</span>)}</>}
                </div>
                <div className="row gap-8">
                  <button type="button" className="btn btn--primary btn--sm" onClick={acceptSuggestion}>Принять</button>
                  <button type="button" className="btn btn--ghost btn--sm" onClick={() => setDismissed(true)}>Скрыть</button>
                </div>
              </div>
            )}

            <Field label="Направление" req error={errors.category_id}>
              <div className="chips">
                {categories.map((c) => (
                  <button type="button" key={c.id} aria-pressed={f.category_id === c.id} className={`chip ${f.category_id === c.id ? 'is-on' : ''}`}
                    onClick={() => setF((s) => ({ ...s, category_id: c.id, tag_ids: s.tag_ids.filter((t) => tagIndex[t]?.category_id === c.id) }))}>
                    <Icon name={categoryUi(c.id).icon} size={14} /> {categoryUi(c.id).short}
                  </button>
                ))}
              </div>
            </Field>
            {cat && (
              <Field label="Теги" req error={errors.tag_ids}>
                <div className="chips">
                  {cat.tags.map((t) => (
                    <button type="button" key={t.id} aria-pressed={f.tag_ids.includes(t.id)} className={`chip chip--sm ${f.tag_ids.includes(t.id) ? 'is-on' : ''}`} onClick={() => toggleTag(t.id)}>{t.name}</button>
                  ))}
                </div>
              </Field>
            )}
          </fieldset>

          <fieldset>
            <legend>Когда и где</legend>
            <div className="form__row form__row--3">
              <Field label="Дата" req error={errors.starts_at}>
                <input type="date" value={f.date} min={toCityInputs(new Date().toISOString(), city).date} onChange={set('date')} />
              </Field>
              <Field label="Начало" req hint={`Время: ${city.name} (UTC${city.offset})`}>
                <input type="time" value={f.time} onChange={set('time')} />
              </Field>
              <Field label="Длительность" req>
                <select value={f.duration_min} onChange={set('duration_min')}>
                  {[30, 45, 60, 75, 90, 120, 150, 180, 240].map((m) => <option key={m} value={m}>{m < 60 ? `${m} мин` : `${Math.floor(m / 60)} ч${m % 60 ? ` ${m % 60} мин` : ''}`}</option>)}
                </select>
              </Field>
            </div>
            <div className="form__row form__row--2">
              <Field label="Район" req error={errors.district}>
                <select value={f.district} onChange={set('district')}>
                  <option value="">Выберите район</option>
                  {city.districts.map((d) => <option key={d}>{d}</option>)}
                </select>
              </Field>
              <Field label="Адрес" req error={errors.address} hint="Координаты определит геокодер на сервере">
                <input value={f.address} maxLength={LIMITS.address} onChange={set('address')} placeholder="Улица, дом, ориентир" />
              </Field>
            </div>
            <Field label="Как найти вход">
              <input value={f.how_to_find} onChange={set('how_to_find')} placeholder="Второй этаж, налево от лестницы" />
            </Field>
          </fieldset>

          <fieldset>
            <legend>Детали</legend>
            <div className="form__row form__row--3">
              <Field label="Лимит мест" error={errors.capacity} hint={mode === 'edit' && source ? `Записано: ${source.registered_count}. Пусто — без ограничений` : 'Пусто — без ограничений'}>
                <input type="number" min={mode === 'edit' && source ? Math.max(1, source.registered_count) : 1} value={f.capacity} onChange={set('capacity')} placeholder="∞" />
              </Field>
              <Field label="Уровень">
                <select value={f.level} onChange={set('level')}>
                  <option value="">Любой</option><option>Начальный</option><option>Средний</option><option>Продвинутый</option>
                </select>
              </Field>
              <Field label="Возраст">
                <select value={f.age_limit} onChange={set('age_limit')}>
                  <option value="">Без ограничений</option><option>6+</option><option>12+</option><option>14+</option><option>16+</option><option>18+</option><option>55+</option>
                </select>
              </Field>
            </div>
            <Field label="Что взять с собой">
              <input value={f.bring} onChange={set('bring')} placeholder="Коврик, воду, удобную одежду" />
            </Field>
            <Field label="Контакт для вопросов" hint="Пусто — ваш профиль в MAX">
              <input value={f.contact} onChange={set('contact')} placeholder="@username или телефон" />
            </Field>
            <Field label="Обложка" hint="Без неё — заглушка по направлению">
              <div className="upload"><Icon name="plus" size={16} /> Загрузить изображение <span className="muted small">(нет эндпоинта загрузки)</span></div>
            </Field>
          </fieldset>

          <div className="form__submit">
            <span className="muted small">{mode === 'edit' ? 'Уменьшить лимит ниже числа записавшихся нельзя.' : 'Перед публикацией афиша проходит автоматическую проверку на платные и рекламные события.'}</span>
            <button className="btn btn--primary btn--lg" disabled={busy}>{busy ? 'Сохраняем…' : mode === 'edit' ? 'Сохранить изменения' : 'Опубликовать'}</button>
          </div>
        </form>

        <aside className="create__preview">
          <div className="sticky">
            <div className="label">Предпросмотр карточки в каталоге</div>
            <EventCard event={preview} preview />
            <p className="hint">В каталоге показываем только общую информацию. Место, время и число мест видны в подробной карточке верифицированным пользователям.</p>
          </div>
        </aside>
      </div>
    </div>
  );
}

function Done({ event, mode, onNew }) {
  const moderation = event.status === 'moderation';
  const title = mode === 'edit' ? 'Изменения сохранены' : moderation ? 'Отправлено на проверку' : 'Мероприятие опубликовано';
  let text = 'Афиша уже в каталоге. Записавшиеся получат напоминания от бота, а вы увидите цифры в отчётности.';
  if (moderation) text = `Автофильтр нашёл подозрительное (${(event.moderation_flags || []).join(', ')}). Каталог — только для бесплатных открытых событий, поэтому афиша появится после ручной проверки.`;
  if (mode === 'edit') text = event.notified_count ? `Бот сообщит об изменениях участникам: ${event.notified_count}.` : 'Важные поля не менялись — участников не беспокоим.';
  const id = encodeURIComponent(event.id);
  return (
    <div className="success">
      <div className={`icon-tile ${moderation ? '' : 'icon-tile--accent-2'}`}><Icon name={moderation ? 'info' : 'check'} size={26} stroke={2.2} /></div>
      <h1 className="page-title">{title}</h1>
      <p className="muted">{text}</p>
      <div className="row gap-8 center wrap">
        {!moderation && <a className="btn btn--primary" href={`#/event/${id}`}>Открыть афишу</a>}
        <a className="btn btn--soft" href={`#/cabinet/reports?event=${id}`}>Отчётность</a>
        <a className="btn btn--ghost" href={`#/cabinet/create?from=${id}`}>Создать похожее</a>
        <button className="btn btn--ghost" onClick={onNew}>Новое</button>
      </div>
    </div>
  );
}

function Field({ label, req, error, hint, counter, children }) {
  return (
    <div className={`field ${error ? 'field--error' : ''}`}>
      <span className="field__label">
        {label}{req && <i className="req" aria-label="обязательно" />}
        {counter && <span className="field__counter">{counter}</span>}
      </span>
      {children}
      <span className={`field__help ${error ? 'field__error' : 'field__hint'}`} aria-hidden={error || hint ? undefined : true}>{error || hint || ''}</span>
    </div>
  );
}
