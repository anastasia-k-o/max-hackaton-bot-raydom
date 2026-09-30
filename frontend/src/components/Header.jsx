import Icon from './Icon.jsx';
import ThemeToggle from './ThemeToggle.jsx';
import { useSession } from '../store/session.jsx';
import { useUi } from '../store/ui.jsx';
import { navigate } from '../lib/router.js';
import { initials } from '../lib/format.js';

const NAV = [
  { id: 'catalog', label: 'Каталог', icon: 'calendar' },
  { id: 'for-you', label: 'Для вас', icon: 'sparkle' },
  { id: 'cabinet', label: 'Личный кабинет', icon: 'user' },
];

export default function Header({ section }) {
  const { user } = useSession();
  const { openVerify } = useUi();
  const name = user ? [user.first_name, user.last_name].filter(Boolean).join(' ') : '';
  return (
    <header className="header">
      <div className="container header__inner">
        <a className="logo" href="#/catalog" aria-label="На главную">
          <img className="logo__img" src="/logo_icon.png" alt="Рядом" width="26" height="26" />
          <span className="logo__text">Рядом</span>
        </a>
        <nav className="nav" aria-label="Разделы">
          {NAV.map((n) => (
            <a key={n.id} href={`#/${n.id}`} className={`nav__link ${section === n.id ? 'is-active' : ''}`} aria-current={section === n.id ? 'page' : undefined}>
              <Icon name={n.icon} size={22} className="nav__icon" />
              <span className="nav__text">{n.label}</span>
            </a>
          ))}
        </nav>
        <div className="header__right">
          {user && !user.is_verified && (
            <button className="btn btn--soft btn--sm" onClick={openVerify}>
              <Icon name="shield" size={16} /> Пройти верификацию
            </button>
          )}
          <ThemeToggle />
          {user && (
            <button className="avatar" onClick={() => navigate('/cabinet/profile')} title={name} aria-label="Профиль">
              {user.photo_url ? <img src={user.photo_url} alt="" /> : initials(name)}
              {user.is_verified && <span className="avatar__badge"><Icon name="check" size={10} stroke={3} /></span>}
            </button>
          )}
        </div>
      </div>
    </header>
  );
}
