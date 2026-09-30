import Icon from './Icon.jsx';
import { useSession } from '../store/session.jsx';
import { botUrl, openLink } from '../lib/max.js';

// при bot_available=false напоминания и подтверждения не придут.
export default function BotBanner() {
  const { user } = useSession();
  if (!user || user.bot_available !== false) return null;
  const url = botUrl();
  return (
    <div className="banner">
      <div className="container banner__inner">
        <Icon name="bell" size={16} />
        <span>Бот не может написать вам — напоминания и запрос подтверждения не придут. Откройте диалог с ботом в MAX.</span>
        {url && <button className="btn btn--soft btn--sm" onClick={() => openLink(url)}>Открыть бота</button>}
      </div>
    </div>
  );
}
