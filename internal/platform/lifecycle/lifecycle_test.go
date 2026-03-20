package lifecycle

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"syscall"
	"testing"
	"time"
)

func TestRun_ShutsDownWhenTaskFails(t *testing.T) {
	t.Parallel()

	server := newFakeServer()
	taskStarted := make(chan struct{})
	taskCalls := 0

	err := Run(Config{
		ServiceName: "payments",
		Port:        8082,
		Logger:      testLogger(),
		Server:      server,
		SignalCh:    make(chan os.Signal),
	}, Task{
		Name: "consumer",
		Run: func(ctx context.Context) error {
			taskCalls++
			close(taskStarted)
			return errors.New("boom")
		},
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	select {
	case <-taskStarted:
	case <-time.After(time.Second):
		t.Fatal("task did not start")
	}

	if taskCalls != 1 {
		t.Fatalf("task calls = %d, want 1", taskCalls)
	}
	if server.listenCalls != 1 {
		t.Fatalf("ListenAndServe calls = %d, want 1", server.listenCalls)
	}
	if server.shutdownCalls != 1 {
		t.Fatalf("Shutdown calls = %d, want 1", server.shutdownCalls)
	}
}

func TestRun_ShutsDownOnSignal(t *testing.T) {
	t.Parallel()

	server := newFakeServer()
	sigCh := make(chan os.Signal, 1)

	go func() {
		time.Sleep(10 * time.Millisecond)
		sigCh <- syscall.SIGTERM
	}()

	err := Run(Config{
		ServiceName: "wallets",
		Port:        8083,
		Logger:      testLogger(),
		Server:      server,
		SignalCh:    sigCh,
	}, Task{
		Name: "consumer",
		Run: func(ctx context.Context) error {
			<-ctx.Done()
			return nil
		},
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	if server.shutdownCalls != 1 {
		t.Fatalf("Shutdown calls = %d, want 1", server.shutdownCalls)
	}
}

func TestRun_ValidatesConfig(t *testing.T) {
	t.Parallel()

	err := Run(Config{ServiceName: "svc"})
	if err == nil {
		t.Fatal("expected config error")
	}
}

type fakeServer struct {
	listenCalls   int
	shutdownCalls int
	shutdownCh    chan struct{}
}

func newFakeServer() *fakeServer {
	return &fakeServer{shutdownCh: make(chan struct{})}
}

func (s *fakeServer) ListenAndServe() error {
	s.listenCalls++
	<-s.shutdownCh
	return http.ErrServerClosed
}

func (s *fakeServer) Shutdown(_ context.Context) error {
	s.shutdownCalls++
	select {
	case <-s.shutdownCh:
	default:
		close(s.shutdownCh)
	}
	return nil
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
