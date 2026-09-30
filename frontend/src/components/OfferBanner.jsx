import { useEffect, useState } from 'react';
import Icon from './Icon.jsx';
import { useEvents } from '../store/events.jsx';
import { fmtTime } from '../lib/format.js';

const left = (iso) => Math.max(0, Math.floor((Date.parse(iso) - Date.now()) / 1000));
const mmss = (s) => `${Math.floor(s / 60)}:${String(s % 60).padStart(2, '0')}`;

export default function OfferBanner({ registration, compact = false, tz }) {
  const { acceptOffer, declineOffer, pending } = useEvents();
  const [secs, setSecs] = useState(() => left(registration.offer_expires_at));
  useEffect(() => {
    const t = setInterval(() => setSecs(left(registration.offer_expires_at)), 1000);
    return () => clearInterval(t);
  }, [registration.offer_expires_at]);
  const busy = pending === registration.id;
  const expired = secs === 0;

  return (
    <div className={`offer ${compact ? 'offer--compact' : ''}`} role="status">
      <div className="offer__text">
        <Icon name="bell" size={18} />
        <div>
          <b>{expired ? 'Срок предложения истёк' : 'Для вас освободилось место'}</b>
          <div className="small">
            {expired ? 'Место предложено следующему участнику.' : <>Ответьте до {fmtTime(registration.offer_expires_at, tz)} · осталось <span className="mono">{mmss(secs)}</span></>}
          </div>
        </div>
      </div>
      {!expired && (
        <div className="offer__actions">
          <button className="btn btn--primary btn--sm" disabled={busy} onClick={(e) => { e.stopPropagation(); acceptOffer(registration); }}>Занять место</button>
          <button className="btn btn--ghost btn--sm" disabled={busy} onClick={(e) => { e.stopPropagation(); declineOffer(registration); }}>Отказаться</button>
        </div>
      )}
    </div>
  );
}
