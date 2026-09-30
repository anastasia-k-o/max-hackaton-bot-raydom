// список городов; добавлять сюда по мере масштабирования
export const CITIES = [
  {
    id: 'msk',
    name: 'Москва',
    timezone: 'Europe/Moscow',
    offset: '+03:00',
    offsetMin: 180,
    districts: ['Хамовники', 'Басманный', 'Тверской', 'Пресненский', 'Замоскворечье', 'Таганский', 'Сокольники', 'Останкинский'],
  },
];

export const cityById = (id) => CITIES.find((c) => c.id === id) || CITIES[0];
