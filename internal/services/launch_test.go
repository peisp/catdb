package services

import (
	"context"
	"testing"
	"time"
)

// pendingRequestID waits for Prompt to have registered its waiter and returns
// its id. The real front-end reads the id off the emitted event; the test has
// no window, and wailsbridge.Emit drops the event with no app, so it reads the
// broker's own table instead.
func pendingRequestID(t *testing.T, b *LaunchPasswordBroker) string {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		b.mu.Lock()
		for id := range b.waiting {
			b.mu.Unlock()
			return id
		}
		b.mu.Unlock()
		time.Sleep(time.Millisecond)
	}
	t.Fatal("no pending password request appeared")
	return ""
}

func TestLaunchPasswordBrokerSubmit(t *testing.T) {
	b := NewLaunchPasswordBroker()
	svc := NewSystemService(nil, b)

	type result struct {
		password string
		ok       bool
	}
	done := make(chan result, 1)
	go func() {
		pwd, ok := b.Prompt(context.Background(), "prod shop", "app")
		done <- result{pwd, ok}
	}()

	id := pendingRequestID(t, b)
	if err := svc.SubmitLaunchPassword(context.Background(), id, "typed", true); err != nil {
		t.Fatalf("SubmitLaunchPassword: %v", err)
	}

	select {
	case got := <-done:
		if got.password != "typed" || !got.ok {
			t.Errorf("Prompt = %q, %v; want \"typed\", true", got.password, got.ok)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Prompt did not return after the answer was submitted")
	}

	// The request is gone: a second answer has nowhere to land.
	if err := svc.SubmitLaunchPassword(context.Background(), id, "again", true); err == nil {
		t.Error("answering an already-answered request must fail")
	}
}

func TestLaunchPasswordBrokerCancel(t *testing.T) {
	b := NewLaunchPasswordBroker()
	svc := NewSystemService(nil, b)

	done := make(chan bool, 1)
	go func() {
		_, ok := b.Prompt(context.Background(), "prod shop", "app")
		done <- ok
	}()

	id := pendingRequestID(t, b)
	if err := svc.SubmitLaunchPassword(context.Background(), id, "", false); err != nil {
		t.Fatalf("SubmitLaunchPassword: %v", err)
	}
	select {
	case ok := <-done:
		if ok {
			t.Error("a cancelled prompt must report ok=false")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Prompt did not return after the cancel was submitted")
	}
}

func TestLaunchPasswordBrokerTimeout(t *testing.T) {
	b := NewLaunchPasswordBroker()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	pwd, ok := b.Prompt(ctx, "prod shop", "app")
	if ok || pwd != "" {
		t.Errorf("Prompt = %q, %v; want \"\", false on timeout", pwd, ok)
	}
	if time.Since(start) > time.Second {
		t.Errorf("Prompt waited %v, want the context deadline to end it", time.Since(start))
	}
	// It also cleaned up after itself: a late answer finds no waiter.
	b.mu.Lock()
	waiting := len(b.waiting)
	b.mu.Unlock()
	if waiting != 0 {
		t.Errorf("%d waiter(s) left behind after a timeout", waiting)
	}
}

func TestSubmitLaunchPasswordWithoutBroker(t *testing.T) {
	svc := NewSystemService(nil, nil)
	if err := svc.SubmitLaunchPassword(context.Background(), "launch-pw-1", "x", true); err == nil {
		t.Error("a service with no broker must report an error")
	}
	if err := NewSystemService(nil, NewLaunchPasswordBroker()).
		SubmitLaunchPassword(context.Background(), "nope", "x", true); err == nil {
		t.Error("an unknown request id must report an error")
	}
}
