import { useCallback, useEffect, useRef, useState } from 'react';

/** deps перезапускают запрос. */
export function useApi(fn, deps = [], { enabled = true } = {}) {
  const [state, setState] = useState({ data: null, error: null, loading: enabled });
  const seq = useRef(0);
  const load = useCallback(async () => {
    if (!enabled) return;
    const my = ++seq.current;
    setState((s) => ({ ...s, loading: true, error: null }));
    try {
      const data = await fn();
      if (my === seq.current) setState({ data, error: null, loading: false });
    } catch (error) {
      if (my === seq.current) setState({ data: null, error, loading: false });
    }
  }, [enabled, ...deps]); // eslint-disable-line react-hooks/exhaustive-deps
  useEffect(() => { load(); }, [load]);
  return { ...state, reload: load };
}
