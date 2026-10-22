package mcpserver

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/xynova/lappy-rag/internal/observability"
)

func listenAndServe(ctx context.Context, httpServer *http.Server, logger *observability.Logger, onShutdown func()) error {
	if httpServer == nil {
		return errors.New("http server is required")
	}

	errCh := make(chan error, 1)
	go func() {
		logger.Infof("mcp-server listening on %s", httpServer.Addr)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-errCh:
		if err != nil {
			return err
		}
	case <-sigCh:
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		return err
	}
	if onShutdown != nil {
		onShutdown()
	}
	signal.Stop(sigCh)
	return nil
}
