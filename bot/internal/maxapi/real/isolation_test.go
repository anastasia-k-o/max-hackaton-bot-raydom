package real_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"hackatonBotMAX/internal/core"
	"hackatonBotMAX/internal/core/httpgw"
	"hackatonBotMAX/internal/maxapi/real"
)

// Проверка области действия MAX_CA_FILE.
//
// Вопрос, ради которого написан этот файл: насколько далеко распространяется
// доверие к дополнительному корневому сертификату? Утверждение в документации
// — «только на соединения с MAX» — здесь превращается в проверяемое.
//
// Тест внешний (пакет real_test), потому что ему нужны одновременно адаптер
// MAX и адаптер Core Backend: изоляция между ними и есть предмет проверки.

// TestExtraRootDoesNotLeakToOtherAdapters — главное утверждение.
//
// Один и тот же процесс, один и тот же сервер с самоподписанным
// сертификатом. Клиент MAX, которому передан этот корень, соединение
// устанавливает. Клиент Core Backend в том же процессе — нет.
//
// Если эта проверка когда-нибудь упадёт, значит доверие утекло за пределы
// MAX: например, кто-то перешёл на общий транспорт или начал выставлять
// SSL_CERT_FILE. Такую утечку иначе заметили бы очень нескоро.
func TestExtraRootDoesNotLeakToOtherAdapters(t *testing.T) {
	server, caFile := newSelfSignedServer(t)

	t.Run("клиент MAX с MAX_CA_FILE доверяет", func(t *testing.T) {
		client, err := real.New(real.Config{
			Token:   "test-token",
			BaseURL: server.URL,
			CAFile:  caFile,
			Timeout: 3 * time.Second,
		})
		if err != nil {
			t.Fatalf("real.New(): %v", err)
		}

		info, err := client.GetMe(context.Background())
		if err != nil {
			t.Fatalf("соединение с MAX должно устанавливаться: %v", err)
		}
		if info.Username != "scoped_test_bot" {
			t.Errorf("username = %q", info.Username)
		}
	})

	t.Run("клиент MAX без MAX_CA_FILE не доверяет", func(t *testing.T) {
		client, err := real.New(real.Config{
			Token:   "test-token",
			BaseURL: server.URL,
			Timeout: 3 * time.Second,
		})
		if err != nil {
			t.Fatalf("real.New(): %v", err)
		}

		if _, err := client.GetMe(context.Background()); err == nil {
			t.Fatal("без указанного корня соединение не должно устанавливаться")
		}
	})

	t.Run("клиент Core Backend не доверяет тому же корню", func(t *testing.T) {
		// Ключевой подтест. Core Backend получает адрес того же сервера, но
		// про MAX_CA_FILE не знает ничего — и знать не должен.
		gateway, err := httpgw.New(httpgw.Config{
			BaseURL: server.URL,
			APIKey:  "core-key",
			Timeout: 3 * time.Second,
		})
		if err != nil {
			t.Fatalf("httpgw.New(): %v", err)
		}

		err = gateway.Ping(context.Background())
		if err == nil {
			t.Fatal("доверие утекло: Core Backend принял сертификат, " +
				"выданный корнем, который добавлялся только для MAX")
		}
		if core.CodeOf(err) != core.CodeUnavailable {
			t.Errorf("код ошибки = %q, ожидался unavailable", core.CodeOf(err))
		}
	})

	t.Run("обычный http.Client в этом же процессе не доверяет", func(t *testing.T) {
		// Страховка от SSL_CERT_FILE и прочих способов задеть весь процесс:
		// клиент по умолчанию обязан остаться с системным набором корней.
		client := &http.Client{Timeout: 3 * time.Second}
		resp, err := client.Get(server.URL + "/me")
		if err == nil {
			_ = resp.Body.Close()
			t.Fatal("доверие утекло на уровень процесса: сертификат принял " +
				"клиент по умолчанию")
		}
	})
}

// TestExtraRootIsNotPersistedAnywhere: настройка живёт в памяти процесса.
//
// Ни системное хранилище, ни переменные окружения не затрагиваются —
// остановили бота, и доверия больше нет.
func TestExtraRootIsNotPersistedAnywhere(t *testing.T) {
	before := os.Getenv("SSL_CERT_FILE")
	beforeDir := os.Getenv("SSL_CERT_DIR")

	_, caFile := newSelfSignedServer(t)

	if _, err := real.New(real.Config{Token: "t", CAFile: caFile}); err != nil {
		t.Fatalf("real.New(): %v", err)
	}

	if got := os.Getenv("SSL_CERT_FILE"); got != before {
		t.Errorf("SSL_CERT_FILE изменилась с %q на %q — настройка не должна "+
			"выходить за пределы клиента MAX", before, got)
	}
	if got := os.Getenv("SSL_CERT_DIR"); got != beforeDir {
		t.Errorf("SSL_CERT_DIR изменилась с %q на %q", beforeDir, got)
	}

	// Системное хранилище тоже должно остаться нетронутым.
	for _, path := range []string{
		"/usr/local/share/ca-certificates/russian_trusted_root_ca.crt",
	} {
		if _, err := os.Stat(path); err == nil {
			t.Errorf("в системном хранилище появился файл %s — "+
				"MAX_CA_FILE не должен ничего туда класть", path)
		}
	}
}

// newSelfSignedServer поднимает HTTPS-сервер, изображающий MAX, с
// сертификатом от корня, которого нет в системном наборе. Возвращает сервер и
// путь к файлу этого корня в формате PEM.
func newSelfSignedServer(t *testing.T) (*httptest.Server, string) {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}

	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			Country:      []string{"RU"},
			Organization: []string{"Test Ministry"},
			CommonName:   "Test Trusted Root CA",
		},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              []string{"localhost"},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
	}

	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})

	pair, err := tlsKeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}

	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Отвечает и на /me (MAX), и на /health (Core Backend) — чтобы
		// единственной причиной отказа оставался сертификат, а не маршрут.
		_, _ = io.WriteString(w,
			`{"user_id":4242,"first_name":"Scoped","username":"scoped_test_bot","is_bot":true,"status":"ok"}`)
	}))
	server.TLS = newTLSConfig(pair)
	server.StartTLS()
	t.Cleanup(server.Close)

	caFile := filepath.Join(t.TempDir(), "extra-root.pem")
	if err := os.WriteFile(caFile, certPEM, 0o600); err != nil {
		t.Fatal(err)
	}

	return server, caFile
}

func tlsKeyPair(certPEM, keyPEM []byte) (tls.Certificate, error) {
	return tls.X509KeyPair(certPEM, keyPEM)
}

func newTLSConfig(pair tls.Certificate) *tls.Config {
	return &tls.Config{
		Certificates: []tls.Certificate{pair},
		MinVersion:   tls.VersionTLS12,
	}
}
