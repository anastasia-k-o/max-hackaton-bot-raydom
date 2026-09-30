import { useState } from 'react';
import Modal from './Modal.jsx';
import Icon from './Icon.jsx';
import { useSession } from '../store/session.jsx';

// 152-ФЗ: согласие при первом входе
export default function ConsentModal() {
  const { user, acceptConsent } = useSession();
  const [busy, setBusy] = useState(false);
  if (!user || user.consent_accepted_at) return null;
  return (
    <Modal open dismissible={false} title="Прежде чем начать">
      <p className="muted">
        Мы используем имя и идентификатор из профиля MAX, выбранные интересы и район, чтобы показывать афишу,
        записывать на встречи и присылать напоминания через бота. Точный адрес и геопозицию не храним.
        Данные хранятся в России.
      </p>
      <ul className="checklist">
        <li><Icon name="check" size={16} /> Имя из MAX видит организатор встречи, на которую вы записались</li>
        <li><Icon name="check" size={16} /> Уведомления можно отключить в профиле</li>
      </ul>
      <button className="btn btn--primary btn--block" disabled={busy} onClick={async () => { setBusy(true); await acceptConsent(); setBusy(false); }}>
        Согласен на обработку персональных данных
      </button>
    </Modal>
  );
}
