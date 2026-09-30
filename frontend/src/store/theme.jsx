import { createContext, useCallback, useContext, useEffect, useState } from 'react';

const KEY = 'afisha.theme';
const Ctx = createContext(null);

const initial = () => (document.documentElement.classList.contains('theme-dark') ? 'dark' : 'light');

export function ThemeProvider({ children }) {
  const [theme, setThemeState] = useState(initial);
  useEffect(() => {
    document.documentElement.classList.toggle('theme-dark', theme === 'dark');
    try { localStorage.setItem(KEY, theme); } catch { /* noop */ }
  }, [theme]);
  const setTheme = useCallback((t) => setThemeState(t === 'dark' ? 'dark' : 'light'), []);
  const toggle = useCallback(() => setThemeState((t) => (t === 'dark' ? 'light' : 'dark')), []);
  return <Ctx.Provider value={{ theme, setTheme, toggle }}>{children}</Ctx.Provider>;
}

export const useTheme = () => useContext(Ctx);
