import { useRoute } from './lib/router.js';
import { useSession } from './store/session.jsx';
import { errorText, USE_MOCKS } from './api/index.js';
import Header from './components/Header.jsx';
import BotBanner from './components/BotBanner.jsx';
import VerifyModal from './components/VerifyModal.jsx';
import ConsentModal from './components/ConsentModal.jsx';
import OpenInMax from './components/OpenInMax.jsx';
import EmptyState from './components/EmptyState.jsx';
import Toasts from './components/Toasts.jsx';
import Catalog from './pages/Catalog.jsx';
import ForYou from './pages/ForYou.jsx';
import EventPage from './pages/EventPage.jsx';
import Cabinet from './pages/Cabinet.jsx';

export default function App() {
  const { parts, query } = useRoute();
  const { status, error, retry } = useSession();
  const [section, param] = parts;

  let page;
  if (status === 'outside_max') page = <OpenInMax />;
  else if (status === 'loading') page = <div className="container page"><div className="loader" aria-label="Загрузка" /></div>;
  else if (status === 'error') {
    page = (
      <div className="container page">
        <EmptyState icon="info" title="Не удалось войти" text={errorText(error)}
          action={<button className="btn btn--soft" onClick={retry}>Повторить</button>} />
      </div>
    );
  } else if (section === 'event' && param) page = <EventPage id={decodeURIComponent(param)} />;
  else if (section === 'for-you') page = <ForYou />;
  else if (section === 'cabinet') page = <Cabinet tab={param} query={query} />;
  else page = <Catalog />;

  const navSection = section === 'event' ? 'catalog' : section || 'catalog';

  return (
    <>
      <Header section={navSection} />
      <BotBanner />
      <main key={`${section}/${param || ''}`} className="main">{page}</main>
      <footer className="footer">
        <div className="container footer__inner">
          <span>Рядом · бесплатные встречи по интересам</span>
          <span className="muted">{USE_MOCKS ? 'Демо-режим: данные mock-бэкенда' : 'Мини-приложение для MAX'}</span>
        </div>
      </footer>
      {status === 'ready' && <ConsentModal />}
      <VerifyModal />
      <Toasts />
    </>
  );
}
