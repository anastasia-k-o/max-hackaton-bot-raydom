const tag = (id, name) => ({ id, name });
export const CATEGORIES = [
  { id: 'sport', name: 'Спорт и движение', tags: [tag('t_yoga', 'йога'), tag('t_run', 'бег'), tag('t_tennis', 'теннис'), tag('t_bike', 'велосипед'), tag('t_dance', 'танцы'), tag('t_nordic', 'скандинавская ходьба'), tag('t_beginners', 'новичкам')] },
  { id: 'creative', name: 'Творчество', tags: [tag('t_draw', 'рисование'), tag('t_photo', 'фотография'), tag('t_music', 'музыка'), tag('t_craft', 'рукоделие'), tag('t_theatre', 'театр'), tag('t_writing', 'письмо')] },
  { id: 'mind', name: 'Знания и книги', tags: [tag('t_bookclub', 'книжный клуб'), tag('t_science', 'наука'), tag('t_history', 'история'), tag('t_lectures', 'лекции'), tag('t_languages', 'языки'), tag('t_philosophy', 'философия')] },
  { id: 'games', name: 'Игры', tags: [tag('t_boardgames', 'настолки'), tag('t_chess', 'шахматы'), tag('t_quiz', 'квизы'), tag('t_mafia', 'мафия'), tag('t_go', 'го')] },
  { id: 'nature', name: 'Прогулки и природа', tags: [tag('t_walks', 'прогулки'), tag('t_tours', 'экскурсии'), tag('t_hiking', 'походы'), tag('t_volunteer', 'волонтёрство'), tag('t_routes', 'городские маршруты')] },
  { id: 'social', name: 'Общение', tags: [tag('t_speaking', 'разговорный клуб'), tag('t_meet', 'знакомства'), tag('t_network', 'нетворкинг'), tag('t_students', 'для студентов'), tag('t_seniors', 'старшему поколению'), tag('t_newcomers', 'недавно переехал')] },
];
