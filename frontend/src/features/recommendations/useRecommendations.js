import { useMemo } from 'react';
import { events as eventsApi } from '../../api/index.js';
import { useSession } from '../../store/session.jsx';
import { useEvents } from '../../store/events.jsx';
import { useApi } from '../../store/useApi.js';
import { cityById } from '../../lib/cities.js';
import { MIN_INTERESTS, recommend } from './recommendations.js';

const PAGE_SIZE = 50;
const MAX_PAGES = 5;

async function loadCandidates(cityId, tagIds) {
  const all = [];
  for (let page = 1; page <= MAX_PAGES; page++) {
    const r = await eventsApi.list({ city_id: cityId, tag_ids: tagIds, sort: 'starts_at', page, page_size: PAGE_SIZE });
    all.push(...r.items);
    if (!r.items.length || all.length >= r.total) break;
  }
  return all;
}

export function useRecommendations(limit = 20) {
  const { user, interests } = useSession();
  const { version } = useEvents();
  const hasEnoughInterests = interests.length >= MIN_INTERESTS;
  const cityId = cityById(user?.city_id).id;
  const { data, error, loading, reload } = useApi(
    () => loadCandidates(cityId, interests),
    [cityId, interests.join(), version],
    { enabled: hasEnoughInterests },
  );

  const items = useMemo(() => {
    if (!data || !hasEnoughInterests) return [];
    const pool = data.filter((e) => e.author_id !== user?.id && !e.my_registration_status);
    return recommend(pool, interests, limit);
  }, [data, interests, hasEnoughInterests, user?.id, limit]);

  return { items, loading: hasEnoughInterests && loading && !data, error, hasEnoughInterests, reload };
}
