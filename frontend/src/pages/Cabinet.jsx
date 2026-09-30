import { useSession } from '../store/session.jsx';
import { useEvents } from '../store/events.jsx';
import Icon from '../components/Icon.jsx';
import VerificationGate from '../components/VerificationGate.jsx';
import MyEvents from './cabinet/MyEvents.jsx';
import CreateEvent from './cabinet/CreateEvent.jsx';
import Reports from './cabinet/Reports.jsx';
import Profile from './cabinet/Profile.jsx';
import { initials } from '../lib/format.js';
import { ACTIVE_STATUSES } from '../types/index.js';

const TABS = [
  { id: 'bookings', label: 'Мои записи', icon: 'ticket' },
  { id: 'create', label: 'Создать мероприятие', icon: 'plus' },
  { id: 'reports', label: 'Отчётность', icon: 'chart' },
  { id: 'profile', label: 'Профиль и интересы', icon: 'user' },
];

export default function Cabinet({ tab = 'bookings', query }) {
  const { user } = useSession();
  const { mine } = useEvents();
  const active = TABS.find((t) => t.id === tab) ? tab : 'bookings';
  const upcoming = mine.filter((r) => ACTIVE_STATUSES.includes(r.status) && Date.parse(r.event?.starts_at) > Date.now()).length;
  const name = [user.first_name, user.last_name].filter(Boolean).join(' ');

  return (
    <div className="container page cabinet">
      <aside className="cabinet__nav">
        <div className="me">
          <div className="me__avatar">{initials(name)}</div>
          <div>
            <div className="me__name">{name}</div>
            <div className={`me__status ${user.is_verified ? 'is-ok' : ''}`}>
              <Icon name={user.is_verified ? 'shield' : 'info'} size={13} />
              {user.is_verified ? 'Профиль подтверждён' : 'Не верифицирован'}
            </div>
          </div>
        </div>
        <nav className="side-nav" aria-label="Личный кабинет">
          {TABS.map((t) => (
            <a key={t.id} href={`#/cabinet/${t.id}`} className={`side-nav__item ${active === t.id ? 'is-active' : ''}`} aria-current={active === t.id ? 'page' : undefined}>
              <Icon name={t.icon} size={18} />
              <span>{t.label}</span>
              {t.id === 'bookings' && upcoming > 0 && <span className="count">{upcoming}</span>}
            </a>
          ))}
        </nav>
      </aside>

      <section className="cabinet__content">
        {active === 'profile' ? <Profile /> : (
          <VerificationGate title="Раздел доступен после верификации" text="Записи, создание мероприятий и отчётность доступны только подтверждённому профилю MAX.">
            {active === 'bookings' && <MyEvents />}
            {active === 'create' && <CreateEvent query={query} />}
            {active === 'reports' && <Reports query={query} />}
          </VerificationGate>
        )}
      </section>
    </div>
  );
}
