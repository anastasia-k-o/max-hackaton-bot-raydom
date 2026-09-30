import Icon from './Icon.jsx';
import { useTheme } from '../store/theme.jsx';

export default function ThemeToggle({ variant = 'icon' }) {
  const { theme, setTheme, toggle } = useTheme();
  if (variant === 'segmented') {
    return (
      <div className="seg" role="radiogroup" aria-label="Тема">
        <button role="radio" aria-checked={theme === 'light'} className={theme === 'light' ? 'is-on' : ''} onClick={() => setTheme('light')}><Icon name="sun" size={15} /> Светлая</button>
        <button role="radio" aria-checked={theme === 'dark'} className={theme === 'dark' ? 'is-on' : ''} onClick={() => setTheme('dark')}><Icon name="moon" size={15} /> Тёмная</button>
      </div>
    );
  }
  const label = theme === 'dark' ? 'Светлая тема' : 'Тёмная тема';
  return (
    <button className="icon-btn" onClick={toggle} aria-label={label} title={label}>
      <Icon name={theme === 'dark' ? 'sun' : 'moon'} />
    </button>
  );
}
