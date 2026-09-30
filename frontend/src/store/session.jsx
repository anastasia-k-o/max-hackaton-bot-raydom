import { createContext, useCallback, useContext, useEffect, useMemo, useState } from 'react';
import { auth, profile, USE_MOCKS, DEV_LOGIN, errorText } from '../api/index.js';
import { getInitData, isInsideMax, requestContact } from '../lib/max.js';
import { useUi } from './ui.jsx';

/** @typedef {import('../types/index.js').User} User */

// is_verified / is_author приходят только от сервера и в браузере не хранятся.
const Ctx = createContext(null);

export function SessionProvider({ children }) {
  const { toast, closeVerify } = useUi();
  const [status, setStatus] = useState('loading'); // loading | ready | outside_max | error
  /** @type {[User|null, Function]} */
  const [user, setUser] = useState(null);
  const [interests, setInterestsState] = useState([]);
  const [error, setError] = useState(null);

  const start = useCallback(async () => {
    // вне MAX нет initData; в dev, на моках и с VITE_DEV_LOGIN — демо-вход
    if (!isInsideMax() && !USE_MOCKS && !import.meta.env.DEV && !DEV_LOGIN) { setStatus('outside_max'); return; }
    setStatus('loading');
    try {
      const r = await auth.login(getInitData() || 'dev');
      setUser(r.user);
      const i = await profile.getInterests().catch(() => ({ tag_ids: [] }));
      setInterestsState(i.tag_ids || []);
      setStatus('ready');
    } catch (e) {
      setError(e);
      setStatus('error');
    }
  }, []);

  useEffect(() => { start(); }, [start]);

  const refresh = useCallback(async () => { setUser(await auth.getMe()); }, []);

  const actions = useMemo(() => ({
    retry: start,
    refresh,
    async verify() {
      const contact = await requestContact();
      if (!contact) { toast('MAX не передал подтверждённый номер', 'danger'); return false; }
      try {
        setUser(await auth.verify(contact));
        closeVerify();
        toast('Профиль подтверждён', 'success');
        return true;
      } catch (e) { toast(errorText(e), 'danger'); return false; }
    },
    async updateMe(patch) {
      try { setUser(await profile.updateProfile(patch)); return true; } catch (e) { toast(errorText(e), 'danger'); return false; }
    },
    async acceptConsent() {
      try { setUser(await profile.acceptConsent()); } catch (e) { toast(errorText(e), 'danger'); }
    },
    async setInterests(tagIds) {
      try {
        const r = await profile.setInterests(tagIds);
        setInterestsState(r.tag_ids);
        setUser((u) => ({ ...u, onboarding_completed: true }));
        return true;
      } catch (e) { toast(errorText(e), 'danger'); return false; }
    },
    skipOnboarding: () => profile.updateProfile({ onboarding_completed: true }).then(setUser).catch(() => {}),
    async demoPatch(patch) {
      if (!USE_MOCKS) return;
      const m = await import('../api/mocks/handlers.js');
      m.mockPatchMe(patch);
      await refresh();
    },
    async demoReset() {
      if (!USE_MOCKS) return;
      const m = await import('../api/mocks/handlers.js');
      m.resetMockDb();
      await start();
    },
  }), [start, refresh, toast, closeVerify]);

  const value = useMemo(() => ({ status, user, interests, error, ...actions }), [status, user, interests, error, actions]);
  return <Ctx.Provider value={value}>{children}</Ctx.Provider>;
}

export const useSession = () => useContext(Ctx);
