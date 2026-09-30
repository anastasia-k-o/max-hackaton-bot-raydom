import { useState } from 'react';
import Icon from './Icon.jsx';
import { useEvents } from '../store/events.jsx';
import { categoryUi } from '../lib/categories.js';
// минимум 3 интереса
export default function InterestPicker({ initial = [], onSave, onSkip, saveLabel = 'Готово', compact = false }) {
  const { categories } = useEvents();
  const [sel, setSel] = useState(new Set(initial));
  const [busy, setBusy] = useState(false);
  const toggle = (id) => setSel((s) => { const n = new Set(s); n.has(id) ? n.delete(id) : n.add(id); return n; });
  const enough = sel.size >= 3;
  return (
    <div className={`picker ${compact ? 'picker--compact' : ''}`}>
      <div className="picker__grid">
        {categories.map((c) => (
          <div key={c.id} className="picker__group">
            <div className="picker__head"><span className="picker__dot"><Icon name={categoryUi(c.id).icon} size={16} /></span>{c.name}</div>
            <div className="chips">
              {c.tags.map((t) => (
                <button key={t.id} className={`chip ${sel.has(t.id) ? 'is-on' : ''}`} aria-pressed={sel.has(t.id)} onClick={() => toggle(t.id)}>
                  {sel.has(t.id) && <Icon name="check" size={13} stroke={2.4} />} {t.name}
                </button>
              ))}
            </div>
          </div>
        ))}
      </div>
      <div className="picker__bar">
        <span className="muted" aria-live="polite">{enough ? `Выбрано: ${sel.size}` : `Выбрано ${sel.size} из 3 минимум`}</span>
        <div className="row gap-8">
          {onSkip && <button className="btn btn--ghost" onClick={onSkip}>Пропустить</button>}
          <button className="btn btn--primary" disabled={!enough || busy}
            onClick={async () => { setBusy(true); await onSave([...sel]); setBusy(false); }}>{saveLabel}</button>
        </div>
      </div>
    </div>
  );
}
