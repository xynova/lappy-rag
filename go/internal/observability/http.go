package observability

import (
	"fmt"
	"net/http"
)

// TraceHTTP wraps an HTTP handler with a root operation trace span.
func TraceHTTP(serviceName, operationName string, next http.Handler) http.Handler {
	if next == nil {
		panic("next handler cannot be nil")
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, span := WithOpenTelemetryTrace(r.Context(), serviceName, operationName)
		recorder := &statusRecorder{ResponseWriter: w, statusCode: http.StatusOK}
		next.ServeHTTP(recorder, r.WithContext(ctx))

		var err error
		if recorder.statusCode >= http.StatusInternalServerError {
			err = fmt.Errorf("http status %d", recorder.statusCode)
		}
		EndSpanWithStatus(span, &err)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	statusCode int
}

func (r *statusRecorder) WriteHeader(statusCode int) {
	r.statusCode = statusCode
	r.ResponseWriter.WriteHeader(statusCode)
}
