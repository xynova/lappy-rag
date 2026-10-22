package meili

import (
	"context"
	"testing"
	"time"

	derrors "github.com/xynova/lappy-rag/internal/errors"
)

func TestWaitTaskRequiresDeadline(t *testing.T) {
	t.Parallel()

	client := &Client{host: "http://127.0.0.1:7700"}
	err := client.waitTask(context.Background(), 1)
	if err == nil {
		t.Fatal("expected missing-deadline error")
	}
	if derrors.CodeOf(err) != derrors.CodeDeadline {
		t.Fatalf("code=%q want=%q err=%v", derrors.CodeOf(err), derrors.CodeDeadline, err)
	}
}

func TestWaitTaskNilContext(t *testing.T) {
	t.Parallel()

	client := &Client{host: "http://127.0.0.1:7700"}
	var ctx context.Context
	err := client.waitTask(ctx, 1) //nolint:staticcheck // SA1012: waitTask must reject a nil context
	if err == nil {
		t.Fatal("expected nil-context error")
	}
	if derrors.CodeOf(err) != derrors.CodeInvalid {
		t.Fatalf("code=%q want=%q err=%v", derrors.CodeOf(err), derrors.CodeInvalid, err)
	}
}

func TestWaitTaskZeroUIDNoOp(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	client := &Client{host: "http://127.0.0.1:7700"}
	if err := client.waitTask(ctx, 0); err != nil {
		t.Fatalf("zero uid: %v", err)
	}
}
