import { useEffect, useState } from 'react';

const parse = () => {
  const raw = window.location.hash.replace(/^#\/?/, '') || 'catalog';
  const [path, query = ''] = raw.split('?');
  return { parts: path.split('/').filter(Boolean), query: new URLSearchParams(query) };
};

/** mini_app_url из бота (/app/{event_id}) → #/event/{event_id}. */
export function consumeDeepLink(startParam) {
  const m = window.location.pathname.match(/\/app\/([^/?#]+)\/?$/);
  const eventId = m ? decodeURIComponent(m[1]) : startParam;
  if (!eventId || window.location.hash.startsWith('#/event/')) return;
  const base = m ? window.location.pathname.slice(0, m.index + 1) : window.location.pathname;
  window.history.replaceState(null, '', `${base}#/event/${encodeURIComponent(eventId)}`);
}

export function useRoute() {
  const [route, setRoute] = useState(parse);
  useEffect(() => {
    const on = () => { setRoute(parse()); window.scrollTo(0, 0); };
    window.addEventListener('hashchange', on);
    return () => window.removeEventListener('hashchange', on);
  }, []);
  return route;
}

export const navigate = (to) => { window.location.hash = to.startsWith('#') ? to : `#${to}`; };
