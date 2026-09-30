import { useSession } from '../../store/session.jsx';
import { useUi } from '../../store/ui.jsx';
import { USE_MOCKS } from '../../api/index.js';
import { CITIES, cityById } from '../../lib/cities.js';
import InterestPicker from '../../components/InterestPicker.jsx';
import ThemeToggle from '../../components/ThemeToggle.jsx';
import Icon from '../../components/Icon.jsx';

export default function Profile() {
  const s = useSession();
  const { openVerify, toast } = useUi();
  const { user } = s;
  const city = cityById(user.city_id);

  return (
    <div>
      <div className="content-head">
        <div>
          <h1 className="page-title">Профиль и интересы</h1>
          <p className="muted">Аккаунт привязан к профилю MAX — отдельной регистрации нет.</p>
        </div>
      </div>

      <div className="settings">
        <section className="panel">
          <h3 className="panel__title">Верификация и бот</h3>
          {user.is_verified ? (
            <div className="status status--accent-2"><Icon name="shield" size={16} /> Профиль подтверждён средствами MAX</div>
          ) : (
            <>
              <p className="muted">Без верификации доступен только каталог с общей информацией.</p>
              <button className="btn btn--primary" onClick={openVerify}><Icon name="shield" size={16} /> Пройти верификацию</button>
            </>
          )}
        </section>

        <section className="panel">
          <h3 className="panel__title">Роль и оформление</h3>
          <SettingRow title="Я автор" text="Публиковать мероприятия и смотреть отчётность">
            <Toggle checked={user.is_author} label="Роль автора" onChange={(v) => s.updateMe({ is_author: v })} />
          </SettingRow>
          <SettingRow title="Тема" text="Хранится на этом устройстве">
            <ThemeToggle variant="segmented" />
          </SettingRow>
          <SettingRow title="Город">
            <label className="select">
              <select value={city.id} aria-label="Город"
                onChange={(e) => e.target.value !== city.id && s.updateMe({ city_id: e.target.value, district: null })}>
                {CITIES.map((c) => <option key={c.id} value={c.id}>{c.name}</option>)}
              </select>
              <Icon name="chevronDown" size={16} />
            </label>
          </SettingRow>
          <SettingRow title="Район" text="Для фильтра и бонуса в рекомендациях">
            <label className="select">
              <select value={user.district || ''} aria-label="Район" onChange={(e) => s.updateMe({ district: e.target.value || null })}>
                <option value="">Не указан</option>
                {city.districts.map((d) => <option key={d}>{d}</option>)}
              </select>
              <Icon name="chevronDown" size={16} />
            </label>
          </SettingRow>
        </section>

        <section className="panel">
          <h3 className="panel__title">Уведомления бота</h3>
          <SettingRow title="Напоминания и подтверждения" text="За 24 ч, 6 ч и 1 ч до начала">
            <Toggle checked={user.notification_settings.reminders} label="Напоминания"
              onChange={(v) => s.updateMe({ notification_settings: { ...user.notification_settings, reminders: v } })} />
          </SettingRow>
          <SettingRow title="Подборка рекомендаций" text="Не чаще раза в неделю">
            <Toggle checked={user.notification_settings.recommendations} label="Подборка"
              onChange={(v) => s.updateMe({ notification_settings: { ...user.notification_settings, recommendations: v } })} />
          </SettingRow>
        </section>
      </div>

      <section className="panel mt-16">
        <h3 className="panel__title">Мои интересы</h3>
        <p className="muted mb-16">По ним строится раздел «Для вас»: выше показываются встречи с большим числом совпавших тегов. Нужно минимум 3.</p>
        <InterestPicker compact initial={s.interests} saveLabel="Сохранить интересы"
          onSave={async (ids) => { if (await s.setInterests(ids)) toast('Интересы сохранены', 'success'); }} />
      </section>

      {USE_MOCKS && (
        <section className="panel panel--dashed mt-16">
          <h3 className="panel__title">Демо-режим</h3>
          <p className="muted small">Кнопки видны только при USE_MOCKS = true (src/api/config.js) и меняют состояние моков.</p>
          <div className="row gap-8 wrap">
            {user.is_verified && <button className="btn btn--ghost btn--sm" onClick={() => s.demoPatch({ is_verified: false })}>Сбросить верификацию</button>}
            <button className="btn btn--ghost btn--sm" onClick={() => s.demoPatch({ bot_available: !user.bot_available })}>
              {user.bot_available ? 'Имитировать: бот заблокирован' : 'Имитировать: бот доступен'}
            </button>
            <button className="btn btn--ghost btn--sm" onClick={s.demoReset}><Icon name="refresh" size={14} /> Сбросить демо-данные</button>
          </div>
        </section>
      )}
    </div>
  );
}

function SettingRow({ title, text, children }) {
  return (
    <div className="setting">
      <div><div className="setting__title">{title}</div><div className="muted small">{text}</div></div>
      {children}
    </div>
  );
}

function Toggle({ checked, onChange, label }) {
  return (
    <label className="switch">
      <input type="checkbox" checked={checked} aria-label={label} onChange={(e) => onChange(e.target.checked)} />
      <span className="switch__track" />
    </label>
  );
}
