package render

// Every user-facing string in the project lives in this file.
//
// Two reasons. First, copy gets rewritten the night before a demo, and that
// should be one file to edit, not a grep across handlers. Second, it is the
// cheapest possible hedge against localisation: adding a second language means
// turning this struct into a map keyed by locale, not touching business logic.
// No i18n framework, just a boundary that does not have to be undone later.

// buttonTexts holds button labels. Labels are never used as commands: the
// command travels in the callback payload (internal/callback).
type buttonTexts struct {
	OpenMiniApp   string
	OpenEvent     string
	CancelBooking string
	CannotAttend  string
	WillAttend    string
	TakeSeat      string
	DeclineSeat   string
	Route         string
}

// headings are the first line of each message kind.
type headings struct {
	RegistrationCreated  string
	Reminder24h          string
	ConfirmationRequired string
	ConfirmationRetry    string
	Reminder1h           string
	WaitlistOffer        string
	EventUpdated         string
	EventCancelled       string

	Confirmed    string
	Cancelled    string
	SeatTaken    string
	SeatDeclined string
	AlreadyDone  string
	Oops         string
}

// notices are the short toasts shown on the button itself after a press.
type notices struct {
	Confirmed    string
	Cancelled    string
	SeatTaken    string
	SeatDeclined string
	Conflict     string
	Expired      string
	Unavailable  string
	Unknown      string
}

// texts is the whole vocabulary of the bot.
type texts struct {
	Buttons  buttonTexts
	Headings headings
	Notices  notices

	Greeting        string
	FreeTextReply   string
	HelpReply       string
	AddressPrefix   string
	OfferValidUntil string

	ChangesIntro  string
	ChangeTime    string
	ChangeAddress string
	ChangeTitle   string
	ChangeOther   string
	OrganizerNote string

	ErrEventCancelled        string
	ErrRegistrationCancelled string
	ErrOfferExpired          string
	ErrNotFound              string
	ErrConflict              string
	ErrUnavailable           string
	ErrUnknownAction         string
}

// ru is the Russian vocabulary. It is the only locale for now, by design.
var ru = texts{
	Buttons: buttonTexts{
		OpenMiniApp:   "Открыть афишу",
		OpenEvent:     "Открыть афишу",
		CancelBooking: "Отменить запись",
		CannotAttend:  "Не смогу прийти",
		WillAttend:    "Приду",
		TakeSeat:      "Занять место",
		DeclineSeat:   "Отказаться",
		Route:         "Маршрут",
	},
	Headings: headings{
		RegistrationCreated:  "✅ Вы записаны",
		Reminder24h:          "⏰ Напоминание",
		ConfirmationRequired: "❓ Подтвердите участие",
		ConfirmationRetry:    "❓ Подтвердите участие",
		Reminder1h:           "📍 Скоро начало",
		WaitlistOffer:        "🔥 Освободилось место",
		EventUpdated:         "⚠️ Мероприятие изменено",
		EventCancelled:       "❌ Мероприятие отменено",

		Confirmed:    "✅ Участие подтверждено",
		Cancelled:    "✅ Запись отменена",
		SeatTaken:    "✅ Место ваше",
		SeatDeclined: "👌 Предложение отклонено",
		AlreadyDone:  "ℹ️ Уже сделано",
		Oops:         "⚠️ Не получилось",
	},
	Notices: notices{
		Confirmed:    "Участие подтверждено",
		Cancelled:    "Запись отменена",
		SeatTaken:    "Место закреплено за вами",
		SeatDeclined: "Предложение отклонено",
		Conflict:     "Действие сейчас недоступно",
		Expired:      "Срок предложения истёк",
		Unavailable:  "Сервис временно недоступен",
		Unknown:      "Кнопка устарела",
	},

	Greeting: "Привет! 👋\n\n" +
		"Я буду присылать напоминания о ваших мероприятиях и помогать подтверждать участие.\n\n" +
		"Выбирать мероприятия можно в мини-приложении.",

	FreeTextReply: "Для выбора мероприятий откройте мини-приложение.\n" +
		"Здесь я буду присылать уведомления и принимать подтверждения участия.",

	HelpReply: "Я присылаю напоминания о мероприятиях, на которые вы записались, " +
		"и принимаю ваши ответы по кнопкам под сообщениями.\n\n" +
		"Каталог мероприятий, поиск и запись — в мини-приложении.",

	AddressPrefix:   "Адрес:",
	OfferValidUntil: "Предложение действует до",

	ChangesIntro:  "Что изменилось:",
	ChangeTime:    "время",
	ChangeAddress: "адрес",
	ChangeTitle:   "название",
	ChangeOther:   "детали",
	OrganizerNote: "Сообщение организатора:",

	ErrEventCancelled:        "Мероприятие отменено организатором, подтверждать участие не нужно.",
	ErrRegistrationCancelled: "Эта запись уже отменена.",
	ErrOfferExpired:          "Срок предложения истёк — место предложено следующему участнику.",
	ErrNotFound:              "Не удалось найти эту запись. Проверьте список мероприятий в мини-приложении.",
	ErrConflict:              "Сейчас это действие недоступно. Проверьте статус записи в мини-приложении.",
	ErrUnavailable:           "Сервис временно недоступен, попробуйте ещё раз через пару минут.",
	ErrUnknownAction:         "Эта кнопка больше не работает. Откройте мини-приложение, чтобы управлять записью.",
}
