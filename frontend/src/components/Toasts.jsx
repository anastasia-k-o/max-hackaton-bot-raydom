import { useUi } from '../store/ui.jsx';
import Icon from './Icon.jsx';

export default function Toasts() {
  const { toasts } = useUi();
  return (
    <div className="toasts" aria-live="polite">
      {toasts.map((t) => (
        <div key={t.id} className={`toast toast--${t.tone}`}>
          <Icon name={t.tone === 'danger' ? 'info' : 'check'} size={16} /> {t.text}
        </div>
      ))}
    </div>
  );
}
