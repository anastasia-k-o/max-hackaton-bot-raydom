import { useState } from 'react';
import Modal from './Modal.jsx';
import Icon from './Icon.jsx';
import { useSession } from '../store/session.jsx';
import { useUi } from '../store/ui.jsx';
import { isInsideMax } from '../lib/max.js';

export default function VerifyModal() {
  const { verify } = useSession();
  const { verifyOpen, closeVerify } = useUi();
  const [busy, setBusy] = useState(false);
  const run = async () => { setBusy(true); await verify(); setBusy(false); };
  return (
    <Modal open={verifyOpen} onClose={closeVerify}>
      <div className="verify">
        <div className="icon-tile"><Icon name="shield" size={28} /></div>
        <h2 className="modal__title">Пройдите верификацию</h2>
        <p className="muted">
          Подробности встречи — место, время и свободные места — видят только подтверждённые пользователи MAX.
          Так на встречи приходят реальные люди.
        </p>
        <ul className="checklist">
          <li><Icon name="check" size={16} /> Полная карточка мероприятия</li>
          <li><Icon name="check" size={16} /> Запись в одно касание и лист ожидания</li>
          <li><Icon name="check" size={16} /> Публикация своих встреч</li>
        </ul>
        <button className="btn btn--primary btn--block" onClick={run} disabled={busy}>
          {busy ? 'Проверяем…' : 'Подтвердить номер в MAX'}
        </button>
        <p className="hint">
          {isInsideMax()
            ? 'MAX попросит поделиться подтверждённым номером, проверку выполнит сервер. Цифровой ID — при внедрении.'
            : 'Демо-режим: приложение открыто вне MAX, ответ MAX имитируется.'}
        </p>
      </div>
    </Modal>
  );
}
