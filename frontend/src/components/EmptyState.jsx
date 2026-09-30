import Icon from './Icon.jsx';

export default function EmptyState({ icon = 'search', title, text, action }) {
  return (
    <div className="empty">
      <div className="empty__icon"><Icon name={icon} size={22} /></div>
      <div className="empty__title">{title}</div>
      {text && <p className="muted">{text}</p>}
      {action}
    </div>
  );
}
