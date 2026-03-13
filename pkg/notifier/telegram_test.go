package notifier

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// newTestNotifier creates a TelegramNotifier pointed at the given server URL.
// Using the struct's apiBase field (instead of a package-level var) means each
// test has its own isolated state and no inter-test races are possible.
func newTestNotifier(apiURL string) *TelegramNotifier {
	return &TelegramNotifier{
		Token:      "test-token",
		ChatID:     "123456",
		apiBase:    apiURL,
		client:     &http.Client{Timeout: 3 * time.Second},
		pollClient: &http.Client{Timeout: 3 * time.Second},
	}
}

func TestNotify_SendsCorrectPayload(t *testing.T) {
	var (
		mu       sync.Mutex
		received map[string]string
		arrived  = make(chan struct{}, 1)
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]string
		json.NewDecoder(r.Body).Decode(&payload)
		mu.Lock()
		received = payload
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
		select {
		case arrived <- struct{}{}:
		default:
		}
	}))
	defer srv.Close()

	n := newTestNotifier(srv.URL)
	n.Notify("hello world")

	select {
	case <-arrived:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for Notify to reach the server")
	}

	mu.Lock()
	defer mu.Unlock()

	if received["text"] != "hello world" {
		t.Errorf("text: got %q, want %q", received["text"], "hello world")
	}
	if received["chat_id"] != "123456" {
		t.Errorf("chat_id: got %q, want %q", received["chat_id"], "123456")
	}
	if received["parse_mode"] != "Markdown" {
		t.Errorf("parse_mode: got %q, want Markdown", received["parse_mode"])
	}
}

func TestNotify_SkipsWhenTokenEmpty(t *testing.T) {
	var called int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&called, 1)
	}))
	defer srv.Close()

	n := &TelegramNotifier{
		Token:   "",
		ChatID:  "123456",
		apiBase: srv.URL,
		client:  &http.Client{Timeout: time.Second},
	}
	n.Notify("should not send")

	time.Sleep(100 * time.Millisecond)

	if atomic.LoadInt32(&called) != 0 {
		t.Error("Notify should be a no-op when token is empty")
	}
}

func TestStartListening_StopsOnContextCancel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"ok":     true,
			"result": []interface{}{},
		})
	}))
	defer srv.Close()

	n := newTestNotifier(srv.URL)
	ctx, cancel := context.WithCancel(context.Background())

	stopped := make(chan struct{})
	n.StartListening(ctx, func(cmd string, args []string) string { return "" })

	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
		// The goroutine uses http.NewRequestWithContext so it exits promptly.
		time.Sleep(100 * time.Millisecond)
		close(stopped)
	}()

	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("goroutine did not stop after context cancellation")
	}
}

func TestStartListening_ProcessesCommand(t *testing.T) {
	var callCount int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&callCount, 1) == 1 {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"ok": true,
				"result": []map[string]interface{}{
					{
						"update_id": 1,
						"message": map[string]interface{}{
							"chat": map[string]interface{}{"id": float64(123456)},
							"text": "/ping",
						},
					},
				},
			})
		} else {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"ok":     true,
				"result": []interface{}{},
			})
		}
	}))
	defer srv.Close()

	n := newTestNotifier(srv.URL)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	commandDone := make(chan string, 1)
	n.StartListening(ctx, func(cmd string, args []string) string {
		commandDone <- cmd
		return ""
	})

	select {
	case got := <-commandDone:
		if got != "/ping" {
			t.Errorf("command: got %q, want /ping", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for command to be processed")
	}
}

func TestStartListening_IgnoresOtherChatIDs(t *testing.T) {
	var handlerCalled int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"ok": true,
			"result": []map[string]interface{}{
				{
					"update_id": 1,
					"message": map[string]interface{}{
						// Different chat ID — must be ignored.
						"chat": map[string]interface{}{"id": float64(999999)},
						"text": "/ping",
					},
				},
			},
		})
	}))
	defer srv.Close()

	n := newTestNotifier(srv.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	n.StartListening(ctx, func(cmd string, args []string) string {
		atomic.AddInt32(&handlerCalled, 1)
		return ""
	})

	<-ctx.Done()

	if atomic.LoadInt32(&handlerCalled) != 0 {
		t.Error("handler should not be called for messages from other chat IDs")
	}
}
