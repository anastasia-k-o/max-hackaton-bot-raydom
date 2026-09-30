import Icon from './Icon.jsx';
import { useSession } from '../store/session.jsx';
import { useUi } from '../store/ui.jsx';

// UX-замок; настоящую защиту даёт сервер (403 verification_required).
export default function VerificationGate({ children, title = 'Доступно после верификации', text, aside }) {
  const { user } = useSession();
  const { openVerify } = useUi();
  if (user?.is_verified) return children;
  return (
    <div className="gate">
      {aside}
      <div className="gate__body">
        <div className="icon-tile"><Icon name="lock" size={22} /></div>
        <h2 className="gate__title">{title}</h2>
        <p className="muted">{text || 'Место, время, свободные места и запись видят только подтверждённые пользователи MAX.'}</p>
        <button className="btn btn--primary" onClick={openVerify}><Icon name="shield" size={16} /> Пройти верификацию</button>
      </div>
    </div>
  );
}
