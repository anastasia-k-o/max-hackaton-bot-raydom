import Icon from './Icon.jsx';
import { categoryUi } from '../lib/categories.js';

export default function Cover({ event, height = 168, big = false }) {
  if (event.cover_url) {
    return <div className="cover" style={{ height, backgroundImage: `url(${event.cover_url})`, backgroundSize: 'cover', backgroundPosition: 'center' }} />;
  }
  return (
    <div className={`cover ${big ? 'cover--big' : ''}`} style={{ height }}>
      <Icon name={categoryUi(event.category_id).icon} size={big ? 88 : 52} stroke={1.1} className="cover__icon" />
      <span className="cover__ring" />
    </div>
  );
}
