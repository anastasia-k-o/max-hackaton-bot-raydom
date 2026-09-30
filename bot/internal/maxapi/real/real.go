// Package real implements maxapi.Client on top of the official MAX Go SDK.
//
// This is the only package in the project that imports
// github.com/max-messenger/max-bot-api-client-go. Everything it exports is
// expressed in maxapi/domain types, so the SDK cannot leak into application
// code.
//
// Protocol facts this adapter relies on (verified against the SDK source and
// its schema.yaml, v2.4.0):
//
//   - base host is platform-api2.max.ru over HTTPS; the v1 host is deprecated
//   - the token goes in a bare `Authorization` header, with no Bearer prefix
//   - POST /answers?callback_id=... answers a button press, and supplying a
//     `message` in the body replaces the original message, which is how stale
//     action buttons are removed
//   - a callback payload is limited to 1024 bytes
//   - HTTP 429 signals the rate limit
package real

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"time"

	maxbot "github.com/max-messenger/max-bot-api-client-go/v2"
	maxmodel "github.com/max-messenger/max-bot-api-client-go/v2/model"

	"hackatonBotMAX/internal/domain"
	"hackatonBotMAX/internal/maxapi"
)

// Config configures the real MAX adapter.
type Config struct {
	// Token is the bot token issued by @MasterBot.
	Token string
	// BaseURL overrides the API host. Empty means the SDK default
	// (https://platform-api2.max.ru). Tests point this at an httptest server.
	BaseURL string
	// Timeout bounds every individual API call.
	Timeout time.Duration
	// CAFile — путь к дополнительному корневому сертификату (PEM), которому
	// нужно доверять при соединении с MAX.
	//
	// Существует ради вполне конкретной ситуации: MAX использует сертификат
	// УЦ Минцифры России, которого нет в наборе Mozilla, поэтому на обычной
	// Linux-машине проверка не проходит.
	//
	// Почему отдельный параметр, а не SSL_CERT_FILE. Переменная окружения
	// действует на ВЕСЬ процесс: доверие, выданное ради MAX, автоматически
	// распространилось бы и на вызовы Core Backend, и на любой другой HTTPS.
	// Здесь доверие ограничено ровно одним соединением — с MAX и ни с чем
	// больше. Это самая узкая область действия, какая возможна.
	//
	// Сертификат ДОБАВЛЯЕТСЯ к системному набору, а не заменяет его.
	CAFile string
}

// Client is the SDK-backed maxapi.Client.
//
// Поля ниже SDK не использует — они нужны для двух вызовов, которые адаптер
// делает сам (GET /updates и GET /subscriptions). Причина такого исключения
// разобрана в polling.go.
type Client struct {
	api     *maxbot.Api
	timeout time.Duration

	baseURL    string
	token      string
	httpClient *http.Client

	// identity is the bot's own id and username, learned from the first
	// successful GetMe. An open_app button needs them to name the bot the
	// mini app is wired to; until they are known such buttons degrade to
	// plain links.
	identity atomic.Pointer[botIdentity]
}

// botIdentity is what an open_app button needs to know about the bot.
type botIdentity struct {
	UserID   int64
	Username string
}

// New builds a real MAX client.
//
// It does not perform any network I/O: verifying the token is a separate,
// explicit step (GetMe) that main runs at startup so the failure is loud and
// attributable.
func New(cfg Config) (*Client, error) {
	if strings.TrimSpace(cfg.Token) == "" {
		return nil, fmt.Errorf("real max client: %w", maxapi.ErrUnauthorized)
	}

	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}

	httpClient := &http.Client{Timeout: timeout}

	if cfg.CAFile != "" {
		transport, err := transportWithExtraRoot(cfg.CAFile)
		if err != nil {
			return nil, fmt.Errorf("real max client: %w", err)
		}
		httpClient.Transport = transport
	}

	// The SDK reports MAX errors by their JSON body and drops the HTTP
	// status. For "the user blocked the bot" the status is the one thing the
	// MAX schema documents (403 on /messages), so the adapter records it
	// itself. The wrapper passes everything else through untouched, including
	// the scoped TLS trust configured above.
	inner := httpClient.Transport
	if inner == nil {
		inner = http.DefaultTransport
	}
	httpClient.Transport = statusRecorder{next: inner}

	opts := []maxbot.Opt{
		maxbot.WithHTTPClient(httpClient),
	}
	if cfg.BaseURL != "" {
		opts = append(opts, maxbot.WithBaseURL(cfg.BaseURL))
	}

	api, err := maxbot.NewApi(cfg.Token, opts...)
	if err != nil {
		return nil, fmt.Errorf("real max client: %w", err)
	}

	baseURL := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if baseURL == "" {
		// Тот же хост, что берёт SDK по умолчанию (maxbot.DefaultHostV2).
		baseURL = "https://" + maxbot.DefaultHostV2
	}

	return &Client{
		api:        api,
		timeout:    timeout,
		baseURL:    baseURL,
		token:      cfg.Token,
		httpClient: httpClient,
	}, nil
}

// Mode implements maxapi.Client.
func (c *Client) Mode() string { return "real" }

// GetMe fetches the bot profile. Startup and /ready both use it to prove the
// token works.
func (c *Client) GetMe(ctx context.Context) (*maxapi.BotInfo, error) {
	ctx, cancel := c.withTimeout(ctx)
	defer cancel()

	info, err := c.api.Bots.GetMyInfo(ctx)
	if err != nil {
		return nil, translateError("get_me", err)
	}

	if info.UserID != 0 {
		c.identity.Store(&botIdentity{UserID: info.UserID, Username: info.Username})
	}

	return &maxapi.BotInfo{
		UserID:    info.UserID,
		Name:      info.FirstName,
		Username:  info.Username,
		IsBot:     info.IsBot,
		RawSource: "max",
	}, nil
}

// SendMessage delivers a rendered message to a MAX user.
func (c *Client) SendMessage(ctx context.Context, req maxapi.SendMessageRequest) (*maxapi.SentMessage, error) {
	if req.UserID == 0 && req.ChatID == 0 {
		return nil, &maxapi.Error{
			Op:      "send_message",
			Message: "either user_id or chat_id must be set",
		}
	}

	ctx, cancel := c.withTimeout(ctx)
	defer cancel()

	message := maxbot.NewMessage().SetText(req.Message.Text)
	if req.UserID != 0 {
		message = message.SetUser(req.UserID)
	}
	if req.ChatID != 0 {
		message = message.SetChat(req.ChatID)
	}
	if req.Message.Format != domain.FormatPlain {
		message = message.SetFormat(maxmodel.TextFormat(req.Message.Format))
	}
	if req.Message.DisableLinkPreview {
		message = message.SetDisableLinkPreview(true)
	}
	if keyboard := buildKeyboard(req.Message.Keyboard, c.identity.Load()); keyboard != nil {
		message = message.AddKeyboard(keyboard)
	}

	ctx, status := withStatusSlot(ctx)
	result, err := c.api.Messages.Send(ctx, message)
	if err != nil {
		// 403 on /messages: "user suspended bot or it doesn't have access to
		// chat" (schema.yaml). No retry will fix that, and the caller must
		// be able to tell it apart from MAX being down.
		if status.code() == http.StatusForbidden {
			return nil, &maxapi.Error{
				Op:         "send_message",
				StatusCode: http.StatusForbidden,
				Message:    "MAX refused delivery to this user",
				Err:        fmt.Errorf("%w: %v", maxapi.ErrRecipientUnavailable, err),
			}
		}
		return nil, translateError("send_message", err)
	}

	return &maxapi.SentMessage{
		MessageID: result.Message.Body.Mid,
		ChatID:    result.Message.Recipient.ChatID,
		UserID:    result.Message.Recipient.UserID,
	}, nil
}

// EditMessage rewrites an already-delivered message.
func (c *Client) EditMessage(ctx context.Context, req maxapi.EditMessageRequest) error {
	if strings.TrimSpace(req.MessageID) == "" {
		return &maxapi.Error{Op: "edit_message", Message: "message_id is required"}
	}

	ctx, cancel := c.withTimeout(ctx)
	defer cancel()

	if _, err := c.api.Messages.EditMessage(ctx, req.MessageID, buildBody(req.Message, c.identity.Load())); err != nil {
		return translateError("edit_message", err)
	}
	return nil
}

// AnswerCallback acknowledges a button press.
//
// MAX keeps a spinner on the button until the bot answers, so this must happen
// on every callback path, including the failure branches.
func (c *Client) AnswerCallback(ctx context.Context, req maxapi.AnswerCallbackRequest) error {
	if strings.TrimSpace(req.CallbackID) == "" {
		return &maxapi.Error{Op: "answer_callback", Message: "callback_id is required"}
	}

	ctx, cancel := c.withTimeout(ctx)
	defer cancel()

	answer := maxmodel.CallbackAnswer{}
	if req.Notification != "" {
		notification := req.Notification
		answer.Notification = &notification
	}
	if req.Message != nil {
		body := buildBody(*req.Message, c.identity.Load())
		answer.Message = &body
	}

	if _, err := c.api.Messages.AnswerOnCallback(ctx, req.CallbackID, answer); err != nil {
		return translateError("answer_callback", err)
	}
	return nil
}

// withTimeout bounds a call, never extending an already-tighter deadline.
func (c *Client) withTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, c.timeout)
}

// buildBody converts a neutral message into the SDK's message body.
func buildBody(message domain.Message, app *botIdentity) maxmodel.NewMessageBody {
	body := maxmodel.NewMessageBody{
		Text:        message.Text,
		Attachments: []maxmodel.Attachment{},
	}
	if message.Format != domain.FormatPlain {
		body.Format = maxmodel.TextFormat(message.Format)
	}
	if keyboard := buildKeyboard(message.Keyboard, app); keyboard != nil {
		body.Attachments = append(body.Attachments, keyboard.Build())
	}
	return body
}

// buildKeyboard converts a neutral keyboard into the SDK's builder.
//
// Returns nil for an empty keyboard so that an edit can deliberately strip all
// buttons: that is how a stale "Приду / Не смогу" pair is retired once the
// action has been taken.
//
// app is the bot's identity, needed for open_app buttons. Without it an
// open_app button degrades to a link to its fallback URL, or is dropped when
// there is none; a row left empty is not sent, because MAX rejects the whole
// message over an empty row.
func buildKeyboard(keyboard *domain.Keyboard, app *botIdentity) *maxmodel.Keyboard {
	if keyboard.IsEmpty() {
		return nil
	}

	built := maxmodel.NewKeyboard()
	rows := 0
	for _, row := range keyboard.Rows {
		buttons := make([]maxmodel.Button, 0, len(row))
		for _, button := range row {
			if converted, ok := convertButton(button, app); ok {
				buttons = append(buttons, converted)
			}
		}
		if len(buttons) == 0 {
			continue
		}
		target := built.AddRow()
		for _, converted := range buttons {
			target.AddButton(converted)
		}
		rows++
	}
	if rows == 0 {
		return nil
	}
	return built
}

// convertButton maps one neutral button onto the SDK type. ok is false when
// the button cannot be sent at all.
func convertButton(button domain.Button, app *botIdentity) (maxmodel.Button, bool) {
	switch button.Kind {
	case domain.ButtonLink:
		return maxmodel.Button{Type: maxmodel.ButtonLink, Text: button.Text, URL: button.URL}, true
	case domain.ButtonCallback:
		return maxmodel.Button{Type: maxmodel.ButtonCallback, Text: button.Text, Payload: button.Payload}, true
	case domain.ButtonOpenApp:
		if app != nil && app.UserID != 0 {
			// web_app is the bot's public name per the schema; contact_id
			// is its id. Both come from GetMe, so nothing about the bot
			// needs to be configured by hand.
			return maxmodel.Button{
				Type:      maxmodel.ButtonOpenApp,
				Text:      button.Text,
				WebApp:    app.Username,
				ContactID: app.UserID,
				Payload:   button.Payload,
			}, true
		}
		if button.URL != "" {
			return maxmodel.Button{Type: maxmodel.ButtonLink, Text: button.Text, URL: button.URL}, true
		}
	}
	return maxmodel.Button{}, false
}

// translateError converts SDK errors into maxapi.Error, classifying which
// failures are worth retrying.
//
// The distinction matters upstream: a temporary failure yields "попробуйте
// ещё раз", a permanent one yields a different message and a loud log line.
func translateError(op string, err error) error {
	if err == nil {
		return nil
	}

	// Ошибка, уже выраженная в наших терминах, возвращается как есть. Её
	// строят прямые вызовы из polling.go, где классификация (Temporary,
	// StatusCode) выполнена по HTTP-статусу и точнее любой догадки по тексту.
	//
	// Завернуть её во второй *maxapi.Error было бы тихой потерей: errors.As
	// находит внешнюю ошибку, у которой Temporary равен false, и опрос
	// перестал бы повторять запрос после 429 или 503.
	var alreadyTranslated *maxapi.Error
	if errors.As(err, &alreadyTranslated) {
		return err
	}

	// Проверять доверие к сертификату нужно раньше сетевых ошибок: SDK
	// заворачивает сбой TLS в NetworkError, а это ввело бы в заблуждение.
	// Недоверенный корневой сертификат — не «временный сбой сети»: повтор не
	// поможет ни через секунду, ни через час, и сообщение «попробуйте ещё
	// раз» отправило бы человека ждать вместо того, чтобы чинить хранилище
	// сертификатов.
	if certErr := asCertificateError(err); certErr != nil {
		return &maxapi.Error{
			Op:        op,
			Temporary: false,
			// Обе ошибки в цепочке: certErr несёт ErrUntrustedCertificate,
			// по которому main отличает этот случай, а err сохраняет
			// исходную причину для логов. Оставить здесь только err значило
			// бы, что errors.Is не находит маркер, а сообщение при старте
			// выродится в невнятное «MAX API check failed».
			Err:     fmt.Errorf("%w: %w", certErr, err),
			Message: certErr.Error(),
		}
	}

	var timeoutErr *maxbot.TimeoutError
	if errors.As(err, &timeoutErr) {
		return &maxapi.Error{Op: op, Temporary: true, Err: err, Message: "request timed out"}
	}

	var networkErr *maxbot.NetworkError
	if errors.As(err, &networkErr) {
		return &maxapi.Error{Op: op, Temporary: true, Err: err, Message: "network failure"}
	}

	var apiErr *maxbot.Error
	if errors.As(err, &apiErr) {
		translated := &maxapi.Error{
			Op:      op,
			Code:    apiErr.Code,
			Message: apiErr.Message,
			Err:     err,
		}
		// The SDK surfaces the API's error code but not the HTTP status, so
		// classification keys off the code vocabulary documented by MAX.
		switch {
		case strings.Contains(strings.ToLower(apiErr.Err), "unauthorized"),
			strings.Contains(strings.ToLower(apiErr.Code), "unauthorized"):
			translated.StatusCode = http.StatusUnauthorized
			translated.Err = fmt.Errorf("%w: %v", maxapi.ErrUnauthorized, err)
		case strings.Contains(strings.ToLower(apiErr.Err), "too many"),
			strings.Contains(strings.ToLower(apiErr.Code), "too.many"):
			translated.StatusCode = http.StatusTooManyRequests
			translated.Temporary = true
		case apiErr.IsAttachmentNotReady():
			translated.Temporary = true
		}
		return translated
	}

	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return &maxapi.Error{Op: op, Temporary: true, Err: err, Message: "context cancelled"}
	}

	return &maxapi.Error{Op: op, Err: err, Message: err.Error()}
}

// ErrUntrustedCertificate — сертификат MAX не проходит проверку доверия на
// этой машине.
//
// Практически всегда это проблема локального хранилища корневых
// сертификатов, а не MAX. Два типичных случая:
//
//   - в системе нет или устарел пакет ca-certificates (частое состояние
//     свежего WSL или «тонкого» контейнера);
//   - сертификат выдан удостоверяющим центром, которого нет в наборе
//     Mozilla, и тогда его корень нужно добавить в доверенные явно.
//
// Есть и третий вариант, о котором стоит помнить: корпоративный прокси или
// антивирус, подменяющий TLS. Тогда в цепочке будет виден его собственный
// центр, и это уже вопрос к тому, кто настраивал машину.
var ErrUntrustedCertificate = errors.New("max: TLS-сертификат не прошёл проверку доверия")

// asCertificateError распознаёт сбой проверки сертификата в любой обёртке.
//
// Разбор идёт по типам ошибок crypto/x509, а не по тексту: формулировки
// меняются от версии к версии и от локали, а типы стабильны. Проверка по
// подстроке оставлена только как последний рубеж для случаев, когда ошибка
// пришла уже «расплющенной» в строку.
func asCertificateError(err error) error {
	if err == nil {
		return nil
	}

	var unknownAuthority x509.UnknownAuthorityError
	if errors.As(err, &unknownAuthority) {
		return fmt.Errorf("%w: неизвестный удостоверяющий центр (%v). "+
			"Проверьте цепочку: bash scripts/max-tls-check.sh",
			ErrUntrustedCertificate, unknownAuthority.Cert.Issuer.CommonName)
	}

	var hostnameError x509.HostnameError
	if errors.As(err, &hostnameError) {
		return fmt.Errorf("%w: сертификат выписан не на этот хост (%s)",
			ErrUntrustedCertificate, hostnameError.Host)
	}

	var invalid x509.CertificateInvalidError
	if errors.As(err, &invalid) {
		return fmt.Errorf("%w: %v", ErrUntrustedCertificate, invalid)
	}

	var recordErr *tls.CertificateVerificationError
	if errors.As(err, &recordErr) {
		return fmt.Errorf("%w: проверка цепочки не удалась. "+
			"Проверьте цепочку: bash scripts/max-tls-check.sh", ErrUntrustedCertificate)
	}

	// Последний рубеж: ошибка уже потеряла тип по дороге.
	text := strings.ToLower(err.Error())
	for _, marker := range []string{
		"x509:",
		"certificate signed by unknown authority",
		"unable to get local issuer certificate",
		"certificate verify failed",
	} {
		if strings.Contains(text, marker) {
			return fmt.Errorf("%w. Проверьте цепочку: bash scripts/max-tls-check.sh",
				ErrUntrustedCertificate)
		}
	}

	return nil
}

// transportWithExtraRoot строит транспорт, доверяющий системным корням плюс
// одному дополнительному из файла.
//
// Ключевое слово — «плюс». Пул начинается с копии системного набора, и новый
// корень добавляется к нему. Замена набора целиком сломала бы проверку
// сертификатов всего остального, и такую поломку заметили бы нескоро.
func transportWithExtraRoot(caFile string) (*http.Transport, error) {
	pem, err := os.ReadFile(caFile)
	if err != nil {
		return nil, fmt.Errorf("не удалось прочитать MAX_CA_FILE %q: %w", caFile, err)
	}

	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		// Системный набор недоступен — начинаем с пустого. Соединение с MAX
		// после этого будет доверять только указанному корню, что для
		// единственного адресата этого клиента приемлемо.
		pool = x509.NewCertPool()
	}

	if !pool.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf(
			"в MAX_CA_FILE %q нет ни одного сертификата в формате PEM (размер файла: %d байт).\n"+
				"  Посмотреть, что в файле на самом деле:  head -c 200 %s\n"+
				"  Скачать и проверить корень:             bash scripts/max-install-ca.sh\n"+
				"  Частая причина: вместо сертификата скачалась страница ошибки или редиректа",
			caFile, len(pem), caFile)
	}

	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{
		RootCAs:    pool,
		MinVersion: tls.VersionTLS12,
	}
	return transport, nil
}
