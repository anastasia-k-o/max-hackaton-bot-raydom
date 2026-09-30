import { createContext, useCallback, useContext, useEffect, useMemo, useState } from 'react';
import { events as eventsApi, registrations as regApi, errorText, newRequestId } from '../api/index.js';
import { useSession } from './session.jsx';
import { useUi } from './ui.jsx';
import { haptic } from '../lib/max.js';

/** @typedef {import('../types/index.js').Registration} Registration */

const Ctx = createContext(null);

export function EventsProvider({ children }) {
  const { user, status } = useSession();
  const { toast } = useUi();
  const [categories, setCategories] = useState([]);
  /** @type {[Registration[], Function]} */
  const [mine, setMine] = useState([]);
  const [mineLoaded, setMineLoaded] = useState(false);
  const [pending, setPending] = useState(null);
  const [version, setVersion] = useState(0); // после изменений страницы перезапрашивают данные

  useEffect(() => { eventsApi.categories().then((r) => setCategories(r.items)).catch(() => {}); }, []);

  const refreshMine = useCallback(async () => {
    if (!user?.is_verified) { setMine([]); setMineLoaded(true); return; }
    try { setMine((await regApi.mine()).items); } catch { /* noop */ }
    setMineLoaded(true);
  }, [user?.is_verified]);

  useEffect(() => { if (status === 'ready') refreshMine(); }, [status, refreshMine]);

  const tags = useMemo(() => Object.fromEntries(categories.flatMap((c) => c.tags.map((t) => [t.id, { ...t, category_id: c.id }]))), [categories]);
  const categoryById = useCallback((id) => categories.find((c) => c.id === id), [categories]);

  const run = useCallback(async (key, fn, okText) => {
    if (pending) return null;
    setPending(key);
    try {
      const out = await fn(newRequestId());
      if (okText) toast(typeof okText === 'function' ? okText(out) : okText, 'success');
      haptic('medium');
      return out;
    } catch (e) {
      toast(errorText(e), 'danger');
      return null;
    } finally {
      setPending(null);
      await refreshMine();
      setVersion((v) => v + 1);
    }
  }, [pending, toast, refreshMine]);

  const actions = useMemo(() => ({
    register: (eventId) => run(eventId, (rid) => regApi.create(eventId, rid), (reg) => (reg.status === 'waitlist'
      ? `Мест нет — вы в листе ожидания, позиция ${reg.queue_position}`
      : 'Вы записаны. Бот пришлёт детали в MAX')),
    confirm: (reg) => run(reg.id, (rid) => regApi.confirm(reg, user, rid), 'Участие подтверждено'),
    cancel: (reg, reason) => run(reg.id, (rid) => regApi.cancel(reg, user, rid, reason),
      reg.status === 'waitlist' || reg.status === 'offered' ? 'Вы вышли из листа ожидания' : 'Запись отменена. Место уйдёт следующему в очереди'),
    acceptOffer: (reg) => run(reg.id, (rid) => regApi.acceptOffer(reg, user, rid), 'Место ваше! Вы записаны'),
    declineOffer: (reg) => run(reg.id, (rid) => regApi.declineOffer(reg, user, rid), 'Вы отказались от места'),
  }), [run, user]);

  const value = useMemo(() => ({
    categories, tags, categoryById, mine, mineLoaded, pending, version, refreshMine,
    bump: () => setVersion((v) => v + 1), ...actions,
  }), [categories, tags, categoryById, mine, mineLoaded, pending, version, refreshMine, actions]);

  return <Ctx.Provider value={value}>{children}</Ctx.Provider>;
}

export const useEvents = () => useContext(Ctx);
