package httpfailsafe

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"

	"github.com/failsafe-go/failsafe-go"
	"github.com/failsafe-go/failsafe-go/circuitbreaker"
	"github.com/failsafe-go/failsafe-go/retrypolicy"

	derrors "github.com/xynova/lappy-rag/internal/errors"
)

var (
	breakerMu sync.Mutex
	breakers  = map[string]circuitbreaker.CircuitBreaker[*http.Response]{}
)

// Do executes an HTTP request with retry and a per-dependency circuit breaker.
// The context must carry a deadline (C8 fail-closed).
func Do(ctx context.Context, client *http.Client, req *http.Request, dep string) (*http.Response, error) {
	if ctx == nil {
		return nil, derrors.New(derrors.CodeInvalid, "httpfailsafe.Do", "context is required")
	}
	if _, ok := ctx.Deadline(); !ok {
		return nil, derrors.New(derrors.CodeDeadline, "httpfailsafe.Do", "context deadline is required")
	}
	if client == nil {
		return nil, derrors.New(derrors.CodeInvalid, "httpfailsafe.Do", "http client is required")
	}
	if req == nil {
		return nil, derrors.New(derrors.CodeInvalid, "httpfailsafe.Do", "request is required")
	}

	req = req.WithContext(ctx)

	retry := retrypolicy.NewBuilder[*http.Response]().
		WithMaxRetries(3).
		HandleIf(func(resp *http.Response, err error) bool {
			if err != nil {
				if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
					return false
				}
				return true
			}
			if resp != nil && (resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500) {
				_ = resp.Body.Close()
				return true
			}
			return false
		}).
		Build()

	br := breakerFor(dep)
	executor := failsafe.With(retry, br)

	resp, err := executor.GetWithExecution(func(exec failsafe.Execution[*http.Response]) (*http.Response, error) {
		//nolint:gosec // G704: outbound URL is caller-controlled service client traffic.
		return client.Do(req)
	})
	if err != nil {
		return nil, derrors.Wrap(err, derrors.CodeUnavailable, "httpfailsafe.Do", fmt.Sprintf("%s request failed", dep))
	}
	return resp, nil
}

func breakerFor(dep string) circuitbreaker.CircuitBreaker[*http.Response] {
	breakerMu.Lock()
	defer breakerMu.Unlock()
	if b, ok := breakers[dep]; ok {
		return b
	}
	b := circuitbreaker.NewBuilder[*http.Response]().
		WithFailureThreshold(5).
		WithSuccessThreshold(2).
		Build()
	breakers[dep] = b
	return b
}
