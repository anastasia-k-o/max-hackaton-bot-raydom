package mock

import (
	"context"

	"hackatonBotMAX/internal/observability"
)

// requestIDFrom pulls the correlation id off the context so recorded entries
// can be matched against the request that produced them.
func requestIDFrom(ctx context.Context) string {
	return observability.RequestIDFrom(ctx)
}
