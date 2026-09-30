// Только оформление; направления и теги приходят с сервера (GET /categories).
export const CATEGORY_UI = {
  sport: { icon: 'sport', short: 'Спорт' },
  creative: { icon: 'creative', short: 'Творчество' },
  mind: { icon: 'mind', short: 'Знания' },
  games: { icon: 'games', short: 'Игры' },
  nature: { icon: 'nature', short: 'Прогулки' },
  social: { icon: 'social', short: 'Общение' },
};
export const categoryUi = (id) => CATEGORY_UI[id] || { icon: 'sparkle', short: id };
