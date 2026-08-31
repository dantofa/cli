package retry

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"syscall"
	"testing"
	"time"
)

func TestTransientClassifiesTransportFailures(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		// The exact shape client-go produced from CI: a *url.Error wrapping io.EOF.
		{"url error wrapping EOF", &url.Error{
			Op: "Get", URL: "https://x.k8s.ondigitalocean.com/apis", Err: io.EOF,
		}, true},
		{"bare EOF", io.EOF, true},
		{"unexpected EOF", io.ErrUnexpectedEOF, true},
		{"connection reset", &net.OpError{Err: syscall.ECONNRESET}, true},
		{"connection refused", &net.OpError{Err: syscall.ECONNREFUSED}, true},
		{"broken pipe", fmt.Errorf("writing: %w", syscall.EPIPE), true},
		// An error the API server answered with is a decision, not a blip.
		{"api error", errors.New(`secrets "x" is forbidden: User cannot get resource`), false},
		// Context errors arrive wrapped in *url.Error, which satisfies net.Error —
		// they must not be mistaken for something worth retrying.
		{"canceled", &url.Error{Op: "Get", Err: context.Canceled}, false},
		{"deadline exceeded", &url.Error{Op: "Get", Err: context.DeadlineExceeded}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Transient(tc.err); got != tc.want {
				t.Errorf("Transient(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestDoRetriesTransientUntilSuccess(t *testing.T) {
	calls := 0
	err := Do(context.Background(), time.Second, time.Millisecond, func() error {
		calls++
		if calls < 3 {
			return &url.Error{Op: "Get", Err: io.EOF}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if calls != 3 {
		t.Errorf("calls = %d, want 3", calls)
	}
}

func TestDoDoesNotRetryDefinitiveErrors(t *testing.T) {
	// A misconfiguration must surface at once, not after the caller's whole
	// timeout — that is the reason Transient exists rather than retrying blindly.
	sentinel := errors.New("forbidden")
	calls := 0
	err := Do(context.Background(), time.Minute, time.Millisecond, func() error {
		calls++
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want %v", err, sentinel)
	}
	if calls != 1 {
		t.Errorf("calls = %d, want 1 (no retry)", calls)
	}
}

func TestDoReturnsLastTransientErrorOnTimeout(t *testing.T) {
	sentinel := &url.Error{Op: "Get", Err: io.EOF}
	err := Do(context.Background(), 10*time.Millisecond, time.Millisecond, func() error {
		return sentinel
	})
	// The caller still learns why it never recovered.
	if !errors.Is(err, io.EOF) {
		t.Fatalf("err = %v, want it to wrap io.EOF", err)
	}
}

func TestDoAttemptsOnceWithZeroTimeout(t *testing.T) {
	calls := 0
	if err := Do(context.Background(), 0, time.Millisecond, func() error {
		calls++
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Errorf("calls = %d, want 1", calls)
	}
}

func TestDoStopsOnContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	err := Do(ctx, time.Minute, 10*time.Millisecond, func() error {
		calls++
		cancel()
		return io.EOF
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if calls != 1 {
		t.Errorf("calls = %d, want 1", calls)
	}
}
