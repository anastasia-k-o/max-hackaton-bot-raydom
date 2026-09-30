import { createContext, useCallback, useContext, useMemo, useState } from 'react';

const Ctx = createContext(null);
let seq = 0;

export function UiProvider({ children }) {
  const [toasts, setToasts] = useState([]);
  const [verifyOpen, setVerifyOpen] = useState(false);

  const toast = useCallback((text, tone = 'default') => {
    const id = ++seq;
    setToasts((t) => [...t, { id, text, tone }]);
    setTimeout(() => setToasts((t) => t.filter((x) => x.id !== id)), 3400);
  }, []);

  const value = useMemo(() => ({
    toasts, toast, verifyOpen,
    openVerify: () => setVerifyOpen(true),
    closeVerify: () => setVerifyOpen(false),
  }), [toasts, toast, verifyOpen]);

  return <Ctx.Provider value={value}>{children}</Ctx.Provider>;
}

export const useUi = () => useContext(Ctx);
