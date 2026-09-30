package http

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"hackatonBotMAX/internal/callback"
	"hackatonBotMAX/internal/contracts"
	"hackatonBotMAX/internal/domain"
	"hackatonBotMAX/internal/maxapi"
)

// Тесты соответствия Postman-коллекции фактическому контракту.
//
// Коллекция — такой же артефакт контракта, как api/openapi.yaml, и так же
// обязана ему соответствовать. Эти тесты берут тела запросов из коллекции,
// подставляют переменные ровно так, как это делает Postman (текстовая замена
// `{{var}}`), и скармливают результат тем же самым декодерам, что и боевые
// handlers.
//
// Ловят они вполне конкретный класс ошибок, который уже случился: числовой
// идентификатор, записанный в теле как строка (`"max_user_id": "{{var}}"`).
// Такое тело уведомления даёт 400, а тело webhook — 200 «undecodable_update»,
// то есть выглядит успехом, хотя callback не дошёл до CoreGateway. Второе
// особенно коварно, поэтому проверка декодирования здесь строгая.

type postmanCollection struct {
	Info struct {
		Name string `json:"name"`
	} `json:"info"`
	Item     []postmanFolder   `json:"item"`
	Variable []postmanVariable `json:"variable"`
}

type postmanFolder struct {
	Name string         `json:"name"`
	Item []postmanEntry `json:"item"`
}

type postmanEntry struct {
	Name    string `json:"name"`
	Request struct {
		Method string `json:"method"`
		Header []struct {
			Key   string `json:"key"`
			Value string `json:"value"`
		} `json:"header"`
		URL struct {
			Raw string `json:"raw"`
		} `json:"url"`
		Body *struct {
			Raw string `json:"raw"`
		} `json:"body"`
	} `json:"request"`
}

type postmanVariable struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type postmanEnvironment struct {
	Values []struct {
		Key     string `json:"key"`
		Value   string `json:"value"`
		Enabled bool   `json:"enabled"`
	} `json:"values"`
}

var placeholderPattern = regexp.MustCompile(`\{\{(\w+)\}\}`)

func repoRoot() string { return filepath.Join("..", "..", "..") }

func loadCollection(t *testing.T) (postmanCollection, map[string]string) {
	t.Helper()

	raw, err := os.ReadFile(filepath.Join(repoRoot(), "postman", "MAX-Bot.postman_collection.json"))
	if err != nil {
		t.Fatalf("коллекция Postman должна существовать: %v", err)
	}

	var collection postmanCollection
	if err := json.Unmarshal(raw, &collection); err != nil {
		t.Fatalf("коллекция Postman должна быть валидным JSON: %v", err)
	}

	envRaw, err := os.ReadFile(filepath.Join(repoRoot(), "postman", "Local.postman_environment.json"))
	if err != nil {
		t.Fatalf("окружение Postman должно существовать: %v", err)
	}

	var environment postmanEnvironment
	if err := json.Unmarshal(envRaw, &environment); err != nil {
		t.Fatalf("окружение Postman должно быть валидным JSON: %v", err)
	}

	values := map[string]string{}
	for _, v := range environment.Values {
		if v.Enabled {
			values[v.Key] = v.Value
		}
	}
	// Переменные коллекции дополняют окружение (request_id, callback_id и т.п.).
	for _, v := range collection.Variable {
		if _, exists := values[v.Key]; !exists {
			values[v.Key] = v.Value
		}
	}

	return collection, values
}

// substitute повторяет подстановку Postman: чисто текстовая замена без
// какой-либо оглядки на JSON. Именно поэтому число, записанное в коллекции в
// кавычках, и уходит на сервер строкой.
func substitute(t *testing.T, body string, values map[string]string) string {
	t.Helper()

	var missing []string
	out := placeholderPattern.ReplaceAllStringFunc(body, func(match string) string {
		key := placeholderPattern.FindStringSubmatch(match)[1]
		value, ok := values[key]
		if !ok {
			missing = append(missing, key)
			return match
		}
		return value
	})

	if len(missing) > 0 {
		t.Fatalf("в коллекции используются переменные, которых нет ни в окружении, ни в самой коллекции: %v", missing)
	}
	return out
}

func forEachRequest(t *testing.T, folderName string, fn func(t *testing.T, entry postmanEntry, body string)) {
	t.Helper()

	collection, values := loadCollection(t)
	found := false

	for _, folder := range collection.Item {
		if folder.Name != folderName {
			continue
		}
		found = true
		for _, entry := range folder.Item {
			if entry.Request.Body == nil {
				continue
			}
			t.Run(entry.Name, func(t *testing.T) {
				fn(t, entry, substitute(t, entry.Request.Body.Raw, values))
			})
		}
	}

	if !found {
		t.Fatalf("в коллекции нет папки %q", folderName)
	}
}

// TestPostmanBodiesAreValidJSONAfterSubstitution — базовая проверка: снятие
// кавычек с `{{var}}` не должно ломать разбор тела.
func TestPostmanBodiesAreValidJSONAfterSubstitution(t *testing.T) {
	collection, values := loadCollection(t)

	for _, folder := range collection.Item {
		for _, entry := range folder.Item {
			if entry.Request.Body == nil {
				continue
			}
			name := folder.Name + " / " + entry.Name
			t.Run(name, func(t *testing.T) {
				body := substitute(t, entry.Request.Body.Raw, values)
				var parsed any
				if err := json.Unmarshal([]byte(body), &parsed); err != nil {
					t.Fatalf("после подстановки переменных тело перестало быть валидным JSON: %v\n%s", err, body)
				}
			})
		}
	}
}

// TestPostmanNotificationBodiesMatchTheContract прогоняет тела уведомлений
// через тот же декодер, что и handler: DisallowUnknownFields плюс Validate.
//
// Именно здесь ловится `"max_user_id": "{{max_user_id}}"` — строка в поле,
// объявленном как int64.
func TestPostmanNotificationBodiesMatchTheContract(t *testing.T) {
	forEachRequest(t, "Notifications", func(t *testing.T, entry postmanEntry, body string) {
		var request contracts.NotificationRequestV1

		decoder := json.NewDecoder(bytes.NewReader([]byte(body)))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&request); err != nil {
			t.Fatalf("тело не декодируется в NotificationRequestV1: %v\n%s", err, body)
		}

		if request.Recipient.MaxUserID <= 0 {
			t.Errorf("recipient.max_user_id = %d; в JSON это должно быть число без кавычек",
				request.Recipient.MaxUserID)
		}

		// Запросы, намеренно демонстрирующие ошибку, валидацию не проходят —
		// это их задача. Но декодироваться они обязаны: иначе они проверяют
		// не то, что заявлено в их названии.
		if strings.Contains(entry.Name, "Ошибка") {
			return
		}
		if err := request.Validate(); err != nil {
			t.Errorf("тело не проходит валидацию контракта: %v\n%s", err, body)
		}
	})
}

// TestPostmanWebhookBodiesMatchTheContract прогоняет фикстуры webhook через
// maxapi.DecodeUpdate.
//
// Проверка идентичности здесь ключевая: строковый user_id не вызывает ошибки
// на уровне HTTP (бот отвечает 200 «ignored»), поэтому единственный способ
// заметить поломку — убедиться, что из Update извлекается ненулевой
// идентификатор пользователя.
func TestPostmanWebhookBodiesMatchTheContract(t *testing.T) {
	forEachRequest(t, "MAX Webhook Simulation", func(t *testing.T, entry postmanEntry, body string) {
		update, err := maxapi.DecodeUpdate([]byte(body))
		if err != nil {
			t.Fatalf("фикстура не декодируется как MAX Update: %v\n%s", err, body)
		}

		if update.Type == "" {
			t.Fatal("в фикстуре отсутствует update_type")
		}

		if update.ActorUserID() == 0 {
			t.Errorf("ActorUserID() = 0: числовые user_id в фикстуре записаны строками, "+
				"и бот молча отбросит такое событие\n%s", body)
		}

		if update.Timestamp <= 0 {
			t.Errorf("timestamp = %d; в JSON это должно быть число без кавычек", update.Timestamp)
		}

		if update.Type == maxapi.UpdateMessageCallback {
			if update.Callback == nil || update.Callback.CallbackID == "" {
				t.Error("фикстура message_callback без callback_id: бот не сможет ответить на нажатие")
			}
			if update.Callback != nil && update.Callback.User.UserID == 0 {
				t.Error("callback.user.user_id = 0; это единственный доверенный источник идентичности")
			}
		}
	})
}

// TestPostmanCallbackPayloadsDecode проверяет, что payload в фикстурах — это
// реальные payload, которые производит кодек, а не выдуманные строки.
func TestPostmanCallbackPayloadsDecode(t *testing.T) {
	collection, values := loadCollection(t)

	seenActions := map[domain.ActionType]bool{}

	for _, folder := range collection.Item {
		if folder.Name != "MAX Webhook Simulation" {
			continue
		}
		for _, entry := range folder.Item {
			if entry.Request.Body == nil {
				continue
			}
			body := substitute(t, entry.Request.Body.Raw, values)

			update, err := maxapi.DecodeUpdate([]byte(body))
			if err != nil || update.Callback == nil {
				continue
			}

			payload, err := callback.Decode(update.Callback.Payload)
			if err != nil {
				// Фикстуры с заведомо плохим payload существуют намеренно.
				if strings.Contains(entry.Name, "Устаревшая") || strings.Contains(entry.Name, "не является командой") {
					continue
				}
				t.Errorf("%s: payload %q не декодируется: %v",
					entry.Name, update.Callback.Payload, err)
				continue
			}
			seenActions[payload.Action] = true
		}
	}

	for _, action := range domain.AllActionTypes() {
		if !seenActions[action] {
			t.Errorf("в коллекции нет фикстуры нажатия для действия %q", action)
		}
	}
}

// TestPostmanCoversEveryNotificationType: коллекция обещает «примеры всех
// типов уведомлений», и это обещание стоит проверять.
func TestPostmanCoversEveryNotificationType(t *testing.T) {
	collection, values := loadCollection(t)

	seen := map[string]bool{}
	for _, folder := range collection.Item {
		if folder.Name != "Notifications" {
			continue
		}
		for _, entry := range folder.Item {
			if entry.Request.Body == nil {
				continue
			}
			var request contracts.NotificationRequestV1
			if err := json.Unmarshal([]byte(substitute(t, entry.Request.Body.Raw, values)), &request); err != nil {
				continue
			}
			seen[request.Type] = true
		}
	}

	for _, notificationType := range domain.AllNotificationTypes() {
		if !seen[string(notificationType)] {
			t.Errorf("в коллекции нет примера для типа уведомления %q", notificationType)
		}
	}
}

// TestPostmanUsesTheRealRoutesAndHeaders сверяет пути и заголовки коллекции с
// константами роутера.
func TestPostmanUsesTheRealRoutesAndHeaders(t *testing.T) {
	collection, _ := loadCollection(t)

	known := map[string]bool{
		PathHealth:         true,
		PathReady:          true,
		PathNotifications:  true,
		PathWebhook:        true,
		PathDevMaxMessages: true,
		PathDevCoreActions: true,
	}

	for _, folder := range collection.Item {
		for _, entry := range folder.Item {
			raw := entry.Request.URL.Raw
			path := strings.TrimPrefix(raw, "{{base_url}}")
			if !known[path] {
				t.Errorf("%s / %s обращается к %q, чего роутер не обслуживает",
					folder.Name, entry.Name, path)
			}

			// Уведомления обязаны нести внутренний ключ, webhook — секрет MAX.
			// Единственное исключение — запросы, специально проверяющие отказ.
			headers := map[string]string{}
			for _, h := range entry.Request.Header {
				headers[h.Key] = h.Value
			}

			if path == PathNotifications && !strings.Contains(entry.Name, "нет API-ключа") {
				if headers[HeaderInternalAPIKey] == "" {
					t.Errorf("%s: нет заголовка %s", entry.Name, HeaderInternalAPIKey)
				}
			}
			if path == PathWebhook && headers[HeaderMaxSecret] == "" {
				t.Errorf("%s: нет заголовка %s", entry.Name, HeaderMaxSecret)
			}
		}
	}
}

// TestPostmanEnvironmentMatchesDefaults: значения по умолчанию в окружении
// должны совпадать с тем, что подставляет `make run` без .env, иначе первый
// же запрос новичка вернёт 401.
func TestPostmanEnvironmentMatchesDefaults(t *testing.T) {
	_, values := loadCollection(t)

	makefile, err := os.ReadFile(filepath.Join(repoRoot(), "Makefile"))
	if err != nil {
		t.Skipf("Makefile недоступен: %v", err)
	}

	checks := []struct {
		variable string
		envVar   string
	}{
		{"internal_api_key", "INTERNAL_API_KEY"},
		{"max_webhook_secret", "MAX_WEBHOOK_SECRET"},
	}

	for _, check := range checks {
		value := values[check.variable]
		if value == "" {
			t.Errorf("в окружении Postman нет переменной %s", check.variable)
			continue
		}
		if !strings.Contains(string(makefile), fmt.Sprintf("%s=%s", check.envVar, value)) {
			t.Errorf("значение %s=%q в окружении Postman не совпадает с %s в `make run`",
				check.variable, value, check.envVar)
		}
	}
}
