import { useEffect, useRef } from 'react';
import Icon from './Icon.jsx';

const FOCUSABLE = 'button:not([disabled]), [href], input:not([disabled]), select, textarea, [tabindex]:not([tabindex="-1"])';

export default function Modal({ open, onClose, title, children, width = 440, dismissible = true }) {
  const box = useRef(null);
  useEffect(() => {
    if (!open) return undefined;
    const prev = document.activeElement;
    const first = box.current?.querySelector(FOCUSABLE);
    (first || box.current)?.focus();
    const onKey = (e) => {
      if (e.key === 'Escape' && dismissible) onClose?.();
      if (e.key !== 'Tab' || !box.current) return;
      const items = [...box.current.querySelectorAll(FOCUSABLE)];
      if (!items.length) return;
      const [a, z] = [items[0], items[items.length - 1]];
      if (e.shiftKey && document.activeElement === a) { e.preventDefault(); z.focus(); }
      else if (!e.shiftKey && document.activeElement === z) { e.preventDefault(); a.focus(); }
    };
    document.addEventListener('keydown', onKey);
    document.body.style.overflow = 'hidden';
    return () => { document.removeEventListener('keydown', onKey); document.body.style.overflow = ''; prev?.focus?.(); };
  }, [open, onClose, dismissible]);
  if (!open) return null;
  return (
    <div className="modal" onMouseDown={(e) => dismissible && e.target === e.currentTarget && onClose?.()}>
      <div ref={box} tabIndex={-1} className="modal__box" style={{ maxWidth: width }} role="dialog" aria-modal="true" aria-label={title}>
        {dismissible && <button className="icon-btn modal__close" onClick={onClose} aria-label="Закрыть"><Icon name="x" /></button>}
        {title && <h2 className="modal__title">{title}</h2>}
        {children}
      </div>
    </div>
  );
}
