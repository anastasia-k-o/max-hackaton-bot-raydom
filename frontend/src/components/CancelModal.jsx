import { useState } from 'react';
import Modal from './Modal.jsx';
import { CANCEL_REASONS } from '../types/index.js';

export default function CancelModal({ title, waitlist, onClose, onConfirm }) {
  const [reason, setReason] = useState(null);
  const [busy, setBusy] = useState(false);
  const go = async () => { setBusy(true); await onConfirm(reason); setBusy(false); onClose(); };
  return (
    <Modal open onClose={onClose} title={waitlist ? 'Выйти из листа ожидания?' : 'Точно отменить запись?'}>
      <p className="muted">«{title}». {waitlist ? 'Ваша позиция в очереди будет потеряна.' : 'Место сразу уйдёт следующему из листа ожидания.'}</p>
      {!waitlist && (
        <>
          <div className="label">Причина (необязательно) — поможет автору</div>
          <div className="chips">
            {CANCEL_REASONS.map((r) => (
              <button key={r.code} className={`chip ${reason === r.code ? 'is-on' : ''}`} aria-pressed={reason === r.code}
                onClick={() => setReason(reason === r.code ? null : r.code)}>{r.label}</button>
            ))}
          </div>
        </>
      )}
      <div className="modal__actions">
        <button className="btn btn--ghost" onClick={onClose}>Оставить</button>
        <button className="btn btn--danger" onClick={go} disabled={busy}>{waitlist ? 'Выйти из очереди' : 'Отменить запись'}</button>
      </div>
    </Modal>
  );
}
