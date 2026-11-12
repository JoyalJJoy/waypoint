package httpx

import (
	"context"
	"net/http"
)

type ctxKey struct{}

type HTTPContext struct {
	Request  *http.Request
	Response http.ResponseWriter
}

// WithHTTP attaches the raw request and response writer to the request
// context. Huma's auto-generated OpenAPI is nice, but occasionally we need the
// underlying http.ResponseWriter (for cookies) or *http.Request (for cookies
// and origin checks).
func WithHTTP(ctx context.Context, req *http.Request, w http.ResponseWriter) context.Context {
	return context.WithValue(ctx, ctxKey{}, HTTPContext{Request: req, Response: w})
}

func FromContext(ctx context.Context) (HTTPContext, bool) {
	v, ok := ctx.Value(ctxKey{}).(HTTPContext)
	return v, ok
}
