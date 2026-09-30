export const MIN_INTERESTS = 3;

/**
 * @param {import('../../types/index.js').CatalogItem[]} events  
 * @param {string[]} userInterests  id тегов
 * @returns {{event: import('../../types/index.js').CatalogItem, matched: {id: string, name: string}[], score: number}[]}
 */
export function recommend(events, userInterests, limit = 20) {
  if (!userInterests || userInterests.length < MIN_INTERESTS) return [];
  const wanted = new Set(userInterests);
  // score = число совпавших тегов; 
  return events
    .filter((e) => e.tags?.length)
    .map((event, order) => {
      const matched = event.tags.filter((t) => wanted.has(t.id));
      return { event, matched, score: matched.length, order };
    })
    .filter((x) => x.score > 0)
    .sort((a, b) => b.score - a.score || a.order - b.order)
    .slice(0, limit)
    .map(({ event, matched, score }) => ({ event, matched, score }));
}
