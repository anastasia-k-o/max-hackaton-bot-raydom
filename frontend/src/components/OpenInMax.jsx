import Icon from './Icon.jsx';
import { botUrl } from '../lib/max.js';

export default function OpenInMax() {
  const url = botUrl();
  return (
    <div className="container page">
      <div className="locked">
        <div className="icon-tile"><Icon name="external" size={22} /></div>
        <h2>Откройте афишу в MAX</h2>
        <p className="muted">Мини-приложение получает ваш профиль от MAX при запуске. В обычном браузере войти нельзя.</p>
        {url && <a className="btn btn--primary" href={url}>Открыть в MAX</a>}
      </div>
    </div>
  );
}
