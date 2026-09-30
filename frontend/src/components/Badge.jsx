import Icon from './Icon.jsx';
import { REG_STATUS } from '../types/index.js';

const MAP = {
  [REG_STATUS.REGISTERED]: { cls: 'badge--accent-2', icon: 'check', label: 'Вы записаны' },
  [REG_STATUS.CONFIRMED]: { cls: 'badge--accent-2', icon: 'check', label: 'Подтверждено' },
  [REG_STATUS.WAITLIST]: { cls: 'badge--accent', icon: 'clock', label: 'В листе ожидания' },
  [REG_STATUS.OFFERED]: { cls: 'badge--accent badge--pulse', icon: 'bell', label: 'Место освободилось' },
  [REG_STATUS.CANCELLED]: { cls: 'badge--danger', icon: 'x', label: 'Отменено' },
  [REG_STATUS.ATTENDED]: { cls: '', icon: 'check', label: 'Посетил' },
  [REG_STATUS.NO_SHOW]: { cls: '', icon: 'x', label: 'Не пришёл' },
};

export default function Badge({ status, position, label, tone, icon }) {
  const m = MAP[status] || { cls: tone ? `badge--${tone}` : '', label };
  const text = label || (status === REG_STATUS.WAITLIST && position ? `${m.label} · ${position}-й` : m.label);
  return (
    <span className={`badge ${m.cls}`}>
      {(icon || m.icon) && <Icon name={icon || m.icon} size={12} stroke={2.2} />} {text}
    </span>
  );
}
