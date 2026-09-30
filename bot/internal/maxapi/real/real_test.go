package real

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"hackatonBotMAX/internal/domain"
	"hackatonBotMAX/internal/maxapi"
)

// These tests never touch the real MAX API. They point the SDK at an
// httptest.Server and assert on the HTTP calls it makes, which is how the
// adapter's understanding of the protocol stays verifiable without a token.
//
// Protocol expectations asserted here come from the SDK source and its
// schema.yaml (v2.4.0).

func newTestClient(t *testing.T, handler http.Handler) (*Client, *httptest.Server) {
	t.Helper()

	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	client, err := New(Config{Token: "test-token", BaseURL: server.URL, Timeout: 2 * time.Second})
	if err != nil {
		t.Fatalf("New(): %v", err)
	}
	return client, server
}

func TestNewRequiresAToken(t *testing.T) {
	_, err := New(Config{})
	if !errors.Is(err, maxapi.ErrUnauthorized) {
		t.Fatalf("error = %v, want ErrUnauthorized", err)
	}
}

// TestAuthorizationHeaderShape pins a detail that is easy to get wrong: MAX
// takes the bare token, with no "Bearer " prefix.
func TestAuthorizationHeaderShape(t *testing.T) {
	var gotAuth string
	client, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_, _ = io.WriteString(w, `{"user_id":1,"first_name":"Bot","username":"test_bot","is_bot":true}`)
	}))

	if _, err := client.GetMe(context.Background()); err != nil {
		t.Fatalf("GetMe(): %v", err)
	}
	if gotAuth != "test-token" {
		t.Errorf("Authorization = %q, want the bare token", gotAuth)
	}
	if strings.HasPrefix(gotAuth, "Bearer") {
		t.Error("MAX does not use a Bearer prefix")
	}
}

func TestGetMe(t *testing.T) {
	var gotPath string
	client, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = io.WriteString(w, `{
			"user_id": 4242,
			"first_name": "Афиша",
			"username": "afisha_bot",
			"is_bot": true
		}`)
	}))

	info, err := client.GetMe(context.Background())
	if err != nil {
		t.Fatalf("GetMe(): %v", err)
	}
	if gotPath != "/me" {
		t.Errorf("path = %q, want /me", gotPath)
	}
	if info.UserID != 4242 || info.Username != "afisha_bot" || info.Name != "Афиша" {
		t.Errorf("info = %+v", info)
	}
}

// TestSendMessageShape asserts the request the SDK builds: the recipient goes
// in the query string, and the inline keyboard is an attachment.
func TestSendMessageShape(t *testing.T) {
	var (
		gotPath   string
		gotMethod string
		gotUserID string
		gotBody   map[string]any
	)

	client, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotMethod = r.Method
		gotUserID = r.URL.Query().Get("user_id")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)

		_, _ = io.WriteString(w, `{"message":{
			"recipient":{"chat_id":555,"user_id":123456789,"chat_type":"dialog"},
			"body":{"mid":"mid-real-1","seq":1,"text":"ok"}
		}}`)
	}))

	sent, err := client.SendMessage(context.Background(), maxapi.SendMessageRequest{
		UserID: 123456789,
		Message: domain.Message{
			Text: "Подтвердите участие",
			Keyboard: &domain.Keyboard{Rows: []domain.ButtonRow{{
				{Kind: domain.ButtonCallback, Text: "Приду", Payload: "v1|confirm|registration_15"},
				{Kind: domain.ButtonLink, Text: "Открыть афишу", URL: "https://example.ru/app"},
			}}},
			DisableLinkPreview: true,
		},
	})
	if err != nil {
		t.Fatalf("SendMessage(): %v", err)
	}
	if sent.MessageID != "mid-real-1" {
		t.Errorf("message id = %q", sent.MessageID)
	}

	if gotMethod != http.MethodPost || gotPath != "/messages" {
		t.Errorf("%s %s, want POST /messages", gotMethod, gotPath)
	}
	if gotUserID != "123456789" {
		t.Errorf("user_id query = %q", gotUserID)
	}
	if gotBody["text"] != "Подтвердите участие" {
		t.Errorf("body text = %v", gotBody["text"])
	}

	attachments, ok := gotBody["attachments"].([]any)
	if !ok || len(attachments) != 1 {
		t.Fatalf("attachments = %v, want one inline keyboard", gotBody["attachments"])
	}

	attachment := attachments[0].(map[string]any)
	if attachment["type"] != "inline_keyboard" {
		t.Errorf("attachment type = %v, want inline_keyboard", attachment["type"])
	}

	payload := attachment["payload"].(map[string]any)
	rows := payload["buttons"].([]any)
	if len(rows) != 1 {
		t.Fatalf("got %d keyboard rows, want 1", len(rows))
	}

	buttons := rows[0].([]any)
	if len(buttons) != 2 {
		t.Fatalf("got %d buttons, want 2", len(buttons))
	}

	first := buttons[0].(map[string]any)
	if first["type"] != "callback" || first["payload"] != "v1|confirm|registration_15" {
		t.Errorf("callback button = %v", first)
	}
	// The label must not be what the bot reads back as a command.
	if first["payload"] == first["text"] {
		t.Error("button text is being used as the payload")
	}

	second := buttons[1].(map[string]any)
	if second["type"] != "link" || second["url"] != "https://example.ru/app" {
		t.Errorf("link button = %v", second)
	}
}

func TestSendMessageRequiresARecipient(t *testing.T) {
	client, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))

	if _, err := client.SendMessage(context.Background(), maxapi.SendMessageRequest{}); err == nil {
		t.Fatal("a message with no recipient should be rejected before the network")
	}
}

// TestAnswerCallbackReplacesTheMessage covers the mechanism that retires stale
// buttons: POST /answers with a `message` body.
func TestAnswerCallbackReplacesTheMessage(t *testing.T) {
	var (
		gotPath       string
		gotCallbackID string
		gotBody       map[string]any
	)

	client, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotCallbackID = r.URL.Query().Get("callback_id")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_, _ = io.WriteString(w, `{"success":true}`)
	}))

	replacement := domain.Message{Text: "✅ Участие подтверждено"}
	err := client.AnswerCallback(context.Background(), maxapi.AnswerCallbackRequest{
		CallbackID:   "cb-abc",
		Notification: "Участие подтверждено",
		Message:      &replacement,
	})
	if err != nil {
		t.Fatalf("AnswerCallback(): %v", err)
	}

	if gotPath != "/answers" {
		t.Errorf("path = %q, want /answers", gotPath)
	}
	if gotCallbackID != "cb-abc" {
		t.Errorf("callback_id = %q", gotCallbackID)
	}
	if gotBody["notification"] != "Участие подтверждено" {
		t.Errorf("notification = %v", gotBody["notification"])
	}

	message, ok := gotBody["message"].(map[string]any)
	if !ok {
		t.Fatalf("message = %v, want the replacement body", gotBody["message"])
	}
	if message["text"] != "✅ Участие подтверждено" {
		t.Errorf("replacement text = %v", message["text"])
	}
	// An empty keyboard means "no buttons", which is the point.
	if attachments, ok := message["attachments"].([]any); ok && len(attachments) != 0 {
		t.Errorf("the replacement should carry no keyboard, got %v", attachments)
	}
}

func TestAnswerCallbackRequiresACallbackID(t *testing.T) {
	client, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))

	if err := client.AnswerCallback(context.Background(), maxapi.AnswerCallbackRequest{}); err == nil {
		t.Fatal("an answer with no callback id should be rejected before the network")
	}
}

func TestEditMessage(t *testing.T) {
	var (
		gotMethod    string
		gotPath      string
		gotMessageID string
	)

	client, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotMessageID = r.URL.Query().Get("message_id")
		_, _ = io.WriteString(w, `{"success":true}`)
	}))

	err := client.EditMessage(context.Background(), maxapi.EditMessageRequest{
		MessageID: "mid-1",
		Message:   domain.Message{Text: "обновлено"},
	})
	if err != nil {
		t.Fatalf("EditMessage(): %v", err)
	}
	if gotMethod != http.MethodPut || gotPath != "/messages" {
		t.Errorf("%s %s, want PUT /messages", gotMethod, gotPath)
	}
	if gotMessageID != "mid-1" {
		t.Errorf("message_id = %q", gotMessageID)
	}
}

// TestUnauthorizedIsPermanent: an invalid token must not be retried forever,
// and must be distinguishable so startup can fail with a clear message.
func TestUnauthorizedIsPermanent(t *testing.T) {
	client, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"code":"verify.token","error":"unauthorized","message":"Invalid access_token"}`)
	}))

	_, err := client.GetMe(context.Background())
	if err == nil {
		t.Fatal("expected an error")
	}
	if !errors.Is(err, maxapi.ErrUnauthorized) {
		t.Errorf("error = %v, want it to wrap ErrUnauthorized", err)
	}
	if maxapi.IsTemporary(err) {
		t.Error("an invalid token is permanent, not temporary")
	}
}

// TestNetworkFailureIsTemporary: the bot should tell the user to try again,
// not that the action is impossible.
func TestNetworkFailureIsTemporary(t *testing.T) {
	client, err := New(Config{Token: "t", BaseURL: "http://127.0.0.1:1", Timeout: time.Second})
	if err != nil {
		t.Fatalf("New(): %v", err)
	}

	_, sendErr := client.SendMessage(context.Background(), maxapi.SendMessageRequest{
		UserID:  1,
		Message: domain.Message{Text: "hi"},
	})
	if sendErr == nil {
		t.Fatal("expected a network error")
	}
	if !maxapi.IsTemporary(sendErr) {
		t.Errorf("error = %v, want it classified as temporary", sendErr)
	}
}

func TestTimeoutIsTemporary(t *testing.T) {
	release := make(chan struct{})
	defer close(release)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		select {
		case <-release:
		case <-time.After(5 * time.Second):
		}
	}))
	t.Cleanup(server.Close)

	client, err := New(Config{Token: "t", BaseURL: server.URL, Timeout: 100 * time.Millisecond})
	if err != nil {
		t.Fatalf("New(): %v", err)
	}

	if _, err := client.GetMe(context.Background()); err == nil {
		t.Fatal("expected a timeout")
	} else if !maxapi.IsTemporary(err) {
		t.Errorf("error = %v, want it classified as temporary", err)
	}
}

func TestModeIsReal(t *testing.T) {
	client, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	if client.Mode() != "real" {
		t.Errorf("Mode() = %q", client.Mode())
	}
}

// TestEmptyKeyboardProducesNoAttachment: the keyboard must actually disappear
// when the renderer omits it, or retired buttons would linger.
func TestEmptyKeyboardProducesNoAttachment(t *testing.T) {
	for _, keyboard := range []*domain.Keyboard{
		nil,
		{},
		{Rows: []domain.ButtonRow{}},
		{Rows: []domain.ButtonRow{{}}},
	} {
		if built := buildKeyboard(keyboard, nil); built != nil {
			t.Errorf("buildKeyboard(%+v) = %+v, want nil", keyboard, built)
		}
	}
}

// compile-time check that the adapter satisfies the port.
var _ maxapi.Client = (*Client)(nil)

// TestCAFileAddsToSystemRootsRatherThanReplacingThem — главная гарантия
// MAX_CA_FILE.
//
// Замена системного набора вместо дополнения сломала бы проверку сертификатов
// всех остальных адресатов, и заметили бы это далеко не сразу. Тест
// сравнивает размер пула до и после: он должен вырасти на добавленный корень,
// а не схлопнуться до единицы.
func TestCAFileAddsToSystemRootsRatherThanReplacingThem(t *testing.T) {
	systemPool, err := x509.SystemCertPool()
	if err != nil {
		t.Skipf("системный набор недоступен: %v", err)
	}
	systemCount := len(systemPool.Subjects())
	if systemCount == 0 {
		t.Skip("системный набор пуст — сравнивать не с чем")
	}

	caFile := filepath.Join(t.TempDir(), "extra-root.pem")
	writeSelfSignedRoot(t, caFile)

	transport, err := transportWithExtraRoot(caFile)
	if err != nil {
		t.Fatalf("transportWithExtraRoot(): %v", err)
	}

	pool := transport.TLSClientConfig.RootCAs
	if pool == nil {
		t.Fatal("RootCAs не заполнен")
	}

	got := len(pool.Subjects())
	if got <= systemCount {
		t.Errorf("корней в пуле %d, системных было %d — добавленный корень не попал в пул",
			got, systemCount)
	}
	if got < systemCount {
		t.Errorf("пул уменьшился с %d до %d: системные корни были заменены, а не дополнены",
			systemCount, got)
	}
	if transport.TLSClientConfig.InsecureSkipVerify {
		t.Error("InsecureSkipVerify должен оставаться выключенным")
	}
	if transport.TLSClientConfig.MinVersion < tls.VersionTLS12 {
		t.Error("минимальная версия TLS не должна быть ниже 1.2")
	}
}

func TestCAFileRejectsUnusableInput(t *testing.T) {
	t.Run("файла нет", func(t *testing.T) {
		if _, err := transportWithExtraRoot(filepath.Join(t.TempDir(), "нет.pem")); err == nil {
			t.Fatal("ожидалась ошибка чтения")
		}
	})

	t.Run("файл без PEM-сертификатов", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "мусор.pem")
		if err := os.WriteFile(path, []byte("это не сертификат"), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := transportWithExtraRoot(path)
		if err == nil {
			t.Fatal("ожидалась ошибка разбора")
		}

		// Сообщение должно быть действенным, а не просто констатировать
		// провал. Проверяется ровно то, чего не хватало в реальном случае:
		// размер файла (146 байт сразу выдают страницу ошибки) и куда идти
		// дальше.
		message := err.Error()
		for _, fragment := range []string{
			path,                // какой именно файл
			"байт",              // размер — главная улика
			"max-install-ca.sh", // что делать
			"страница ошибки",   // самая частая причина
		} {
			if !strings.Contains(message, fragment) {
				t.Errorf("в сообщении нет %q, получено: %v", fragment, err)
			}
		}
	})
}

// TestNewWithBadCAFileFailsFast: ошибка в сертификате должна проявиться при
// создании клиента, а не на первом сетевом вызове.
func TestNewWithBadCAFileFailsFast(t *testing.T) {
	_, err := New(Config{Token: "t", CAFile: filepath.Join(t.TempDir(), "отсутствует.pem")})
	if err == nil {
		t.Fatal("ожидалась ошибка при создании клиента")
	}
}

// writeSelfSignedRoot создаёт самоподписанный корень, изображающий сторонний
// удостоверяющий центр.
func writeSelfSignedRoot(t *testing.T, path string) {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}

	template := x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Test Extra Root CA", Organization: []string{"Test"}},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}

	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}

	encoded := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
}
