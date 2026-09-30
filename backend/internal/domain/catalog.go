package domain

import (
	"regexp"
	"strings"
)

// Tag is an interest label inside a category.
type Tag struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Category groups tags. Mirrors frontend/src/api/mocks/categories.js.
type Category struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Tags []Tag  `json:"tags"`
}

func tag(id, name string) Tag { return Tag{ID: id, Name: name} }

// Categories is the fixed directory of categories and tags.
var Categories = []Category{
	{ID: "sport", Name: "Спорт и движение", Tags: []Tag{tag("t_yoga", "йога"), tag("t_run", "бег"), tag("t_tennis", "теннис"), tag("t_bike", "велосипед"), tag("t_dance", "танцы"), tag("t_nordic", "скандинавская ходьба"), tag("t_beginners", "новичкам")}},
	{ID: "creative", Name: "Творчество", Tags: []Tag{tag("t_draw", "рисование"), tag("t_photo", "фотография"), tag("t_music", "музыка"), tag("t_craft", "рукоделие"), tag("t_theatre", "театр"), tag("t_writing", "письмо")}},
	{ID: "mind", Name: "Знания и книги", Tags: []Tag{tag("t_bookclub", "книжный клуб"), tag("t_science", "наука"), tag("t_history", "история"), tag("t_lectures", "лекции"), tag("t_languages", "языки"), tag("t_philosophy", "философия")}},
	{ID: "games", Name: "Игры", Tags: []Tag{tag("t_boardgames", "настолки"), tag("t_chess", "шахматы"), tag("t_quiz", "квизы"), tag("t_mafia", "мафия"), tag("t_go", "го")}},
	{ID: "nature", Name: "Прогулки и природа", Tags: []Tag{tag("t_walks", "прогулки"), tag("t_tours", "экскурсии"), tag("t_hiking", "походы"), tag("t_volunteer", "волонтёрство"), tag("t_routes", "городские маршруты")}},
	{ID: "social", Name: "Общение", Tags: []Tag{tag("t_speaking", "разговорный клуб"), tag("t_meet", "знакомства"), tag("t_network", "нетворкинг"), tag("t_students", "для студентов"), tag("t_seniors", "старшему поколению"), tag("t_newcomers", "недавно переехал")}},
}

var tagIndex = func() map[string]Tag {
	out := map[string]Tag{}
	for _, c := range Categories {
		for _, t := range c.Tags {
			out[t.ID] = t
		}
	}
	return out
}()

// TagByID finds a tag.
func TagByID(id string) (Tag, bool) {
	t, ok := tagIndex[id]
	return t, ok
}

// IsCategory reports whether id names a category.
func IsCategory(id string) bool {
	for _, c := range Categories {
		if c.ID == id {
			return true
		}
	}
	return false
}

// TagsOf resolves tag ids, skipping unknown ones.
func TagsOf(ids []string) []Tag {
	out := make([]Tag, 0, len(ids))
	for _, id := range ids {
		if t, ok := tagIndex[id]; ok {
			out = append(out, t)
		}
	}
	return out
}

// StopWords send an announcement to moderation instead of the catalog.
// Mirrors frontend/src/api/mocks/moderation.js.
var StopWords = []string{"оплата", "оплатить", "₽", "руб", "стоимость", "цена", "курсы", "реклама", "скидка", "промокод"}

var linkPattern = regexp.MustCompile(`https?://`)

// ModerationFlags lists what in the title or description needs a human look.
func ModerationFlags(title, description string) []string {
	text := strings.ToLower(title + " " + description)
	flags := []string{}
	for _, w := range StopWords {
		if strings.Contains(text, w) {
			flags = append(flags, w)
		}
	}
	if linkPattern.MatchString(text) {
		flags = append(flags, "ссылка")
	}
	return flags
}

// TagKeywords are word stems that suggest a tag, for POST /ml/suggest-tags.
// Mirrors ML_KEYWORDS in frontend/src/api/mocks/moderation.js: a keyword
// stand-in until the ML service exists.
var TagKeywords = map[string][]string{
	"t_yoga":       {"йог", "асан", "медитац"},
	"t_run":        {"бег", "пробежк"},
	"t_tennis":     {"теннис"},
	"t_bike":       {"велосипед", "вело"},
	"t_dance":      {"танц", "сальс", "бачат"},
	"t_nordic":     {"скандинав"},
	"t_beginners":  {"новичк", "с нуля", "начинающ"},
	"t_draw":       {"рису", "скетч", "акварел"},
	"t_photo":      {"фото", "съёмк"},
	"t_music":      {"музык", "гитар", "хор"},
	"t_craft":      {"вязан", "рукодел"},
	"t_theatre":    {"театр", "импровиз"},
	"t_writing":    {"стих", "писательск"},
	"t_bookclub":   {"книг", "книжн", "читаем"},
	"t_science":    {"наук", "научн", "физик", "биолог"},
	"t_history":    {"истор", "краевед"},
	"t_lectures":   {"лекци"},
	"t_languages":  {"англ", "язык", "english"},
	"t_philosophy": {"философ"},
	"t_boardgames": {"настол", "игротек"},
	"t_chess":      {"шахмат"},
	"t_quiz":       {"квиз", "викторин"},
	"t_mafia":      {"мафи"},
	"t_walks":      {"прогулк", "гуляем"},
	"t_tours":      {"экскурс"},
	"t_hiking":     {"поход", "тропа"},
	"t_volunteer":  {"волонт", "субботник"},
	"t_routes":     {"маршрут", "улиц"},
	"t_speaking":   {"разговорн", "speaking"},
	"t_meet":       {"знакомств", "познаком"},
	"t_network":    {"нетворк", "карьер"},
	"t_students":   {"студент"},
	"t_seniors":    {"55+", "пенсион", "долголет"},
	"t_newcomers":  {"переех"},
}

// CategoryOfTag finds the category a tag belongs to.
func CategoryOfTag(tagID string) string {
	for _, c := range Categories {
		for _, t := range c.Tags {
			if t.ID == tagID {
				return c.ID
			}
		}
	}
	return ""
}
