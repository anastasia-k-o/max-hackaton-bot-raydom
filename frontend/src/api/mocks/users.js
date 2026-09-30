import { CITY } from './util.js';

export const ME = {
  id: 'user_me',
  max_user_id: 100000001,
  first_name: 'Пётр',
  last_name: 'Смирнов',
  photo_url: null,
  is_verified: true,
  is_author: true,
  city_id: CITY.id,
  district: 'Хамовники',
  bot_available: true,
  onboarding_completed: false,
  consent_accepted_at: null,
  notification_settings: { reminders: true, recommendations: true },
};

const PEOPLE = ['Мария Л.', 'Дмитрий К.', 'Алина В.', 'Сергей П.', 'Ольга Н.', 'Игорь Т.', 'Екатерина Ж.', 'Артём Б.', 'Наталья Ф.', 'Павел Д.', 'Ксения М.', 'Роман Г.', 'Юлия С.'];
export const OTHER_USERS = PEOPLE.map((name, i) => ({ id: `user_${i + 1}`, name, bot_available: i !== 10 }));
