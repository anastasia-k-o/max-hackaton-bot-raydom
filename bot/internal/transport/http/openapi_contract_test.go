package http

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"hackatonBotMAX/internal/domain"
)

// The specification is explicit: "Не оставляй документацию, которая описывает
// несуществующие endpoints" and "Проверь OpenAPI на соответствие реальным
// handlers". These tests make that a build failure rather than a review note.
//
// They are deliberately textual rather than a full schema validation: pulling
// in an OpenAPI toolchain for a hackathon MVP would cost more than it catches,
// while drift between a route constant and the document is exactly the failure
// this project is trying to avoid.

func readOpenAPI(t *testing.T) string {
	t.Helper()

	path := filepath.Join("..", "..", "..", "api", "openapi.yaml")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("api/openapi.yaml must exist and be readable: %v", err)
	}
	return string(raw)
}

// TestOpenAPIDocumentsEveryRoute: a route the code serves but the document
// omits is a contract the Mini App team cannot discover.
func TestOpenAPIDocumentsEveryRoute(t *testing.T) {
	document := readOpenAPI(t)

	routes := []string{
		PathHealth,
		PathReady,
		PathNotifications,
		PathWebhook,
		PathDevMaxMessages,
		PathDevCoreActions,
	}

	for _, route := range routes {
		if !strings.Contains(document, "\n  "+route+":") {
			t.Errorf("api/openapi.yaml does not document the route %q", route)
		}
	}
}

// TestOpenAPIDescribesNoPhantomRoutes: the reverse direction. A documented
// path the router never registers sends integrators chasing a 404.
func TestOpenAPIDescribesNoPhantomRoutes(t *testing.T) {
	document := readOpenAPI(t)

	known := map[string]bool{
		PathHealth:         true,
		PathReady:          true,
		PathNotifications:  true,
		PathWebhook:        true,
		PathDevMaxMessages: true,
		PathDevCoreActions: true,
	}

	inPaths := false
	for _, line := range strings.Split(document, "\n") {
		if line == "paths:" {
			inPaths = true
			continue
		}
		if inPaths && len(line) > 0 && line[0] != ' ' {
			break // left the paths block
		}
		if !inPaths || !strings.HasPrefix(line, "  /") {
			continue
		}
		path := strings.TrimSuffix(strings.TrimSpace(line), ":")
		if !known[path] {
			t.Errorf("api/openapi.yaml documents %q, which the router does not serve", path)
		}
	}
}

// TestOpenAPIListsEveryNotificationType keeps the enum in the document honest
// against domain.AllNotificationTypes.
func TestOpenAPIListsEveryNotificationType(t *testing.T) {
	document := readOpenAPI(t)

	for _, notificationType := range domain.AllNotificationTypes() {
		if !strings.Contains(document, "- "+string(notificationType)) {
			t.Errorf("api/openapi.yaml does not list the notification type %q in its enum", notificationType)
		}
	}
}

// TestOpenAPIUsesTheRealHeaderNames guards the one confusion the spec warns
// about twice: the internal API key and the MAX webhook secret are different
// mechanisms, and the document must name both correctly.
func TestOpenAPIUsesTheRealHeaderNames(t *testing.T) {
	document := readOpenAPI(t)

	for _, header := range []string{HeaderInternalAPIKey, HeaderMaxSecret, HeaderRequestID} {
		if !strings.Contains(document, header) {
			t.Errorf("api/openapi.yaml does not mention the header %q", header)
		}
	}
}

// TestEnvExampleCoversEveryConfiguredVariable: a variable the code reads but
// .env.example omits is a five-minute-onboarding promise broken.
func TestEnvExampleCoversEveryConfiguredVariable(t *testing.T) {
	path := filepath.Join("..", "..", "..", ".env.example")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf(".env.example must exist: %v", err)
	}
	content := string(raw)

	// The list is read from the code rather than written out here: a
	// hand-kept list goes stale the first time someone adds a variable, and
	// then the test keeps passing while .env.example quietly falls behind.
	readsEnv := regexp.MustCompile(`(?:getEnvDefault|getEnvInt|getEnvDuration|os\.Getenv)\("([A-Z0-9_]+)"`)
	sources := []string{
		filepath.Join("..", "..", "config", "config.go"),
		filepath.Join("..", "..", "..", "cmd", "bot", "main.go"),
	}

	seen := map[string]bool{}
	for _, source := range sources {
		code, err := os.ReadFile(source)
		if err != nil {
			t.Fatalf("read %s: %v", source, err)
		}
		for _, match := range readsEnv.FindAllStringSubmatch(string(code), -1) {
			seen[match[1]] = true
		}
	}
	if len(seen) < 10 {
		t.Fatalf("found only %d variables in the code; the pattern is probably out of date", len(seen))
	}

	for key := range seen {
		if !strings.Contains(content, key+"=") {
			t.Errorf(".env.example does not document %s, which the code reads", key)
		}
	}
}

// TestEnvExampleHasNoRealCredentials is the "проверь, что .env.example не
// содержит реальных credentials" check, automated.
func TestEnvExampleHasNoRealCredentials(t *testing.T) {
	path := filepath.Join("..", "..", "..", ".env.example")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf(".env.example must exist: %v", err)
	}

	for _, line := range strings.Split(string(raw), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		key, value, found := strings.Cut(trimmed, "=")
		if !found {
			continue
		}
		value = strings.TrimSpace(value)

		// Secret-bearing variables must be empty or an obvious placeholder.
		switch strings.TrimSpace(key) {
		case "MAX_BOT_TOKEN", "MAX_WEBHOOK_SECRET", "CORE_API_KEY":
			if value != "" {
				t.Errorf("%s must be empty in .env.example, found %q", key, value)
			}
		case "INTERNAL_API_KEY":
			if value != "change-me" {
				t.Errorf("INTERNAL_API_KEY should be the placeholder \"change-me\", found %q", value)
			}
		}
	}
}
