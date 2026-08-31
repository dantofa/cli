// Package retry classifies and rides out transport-level failures talking to a
// cluster. A long-lived connection to a managed API server (DOKS behind its load
// balancer) is dropped now and then; a poll loop or a teardown read that treats
// the first such drop as fatal turns a recoverable blip into a failed CI gate —
// or, on the delete path, a leaked cluster. Framework-free and stdlib-only, so it
// stays usable from every core package.
package retry

import (
	"context"
	"errors"
	"io"
	"net"
	"syscall"
	"time"
)

// Transient reports whether an error is a connection-level failure worth
// retrying, as opposed to a definitive answer from the API server.
//
// The distinction matters in both directions: retrying nothing makes one dropped
// connection fatal, and retrying everything makes a real misconfiguration (bad
// kubeconfig, missing RBAC) cost the caller's whole timeout before it is
// reported. Only transport failures qualify — an API error that arrived
// successfully is an answer, and answers are not retried.
func Transient(err error) bool {
	if err == nil {
		return false
	}
	// A cancelled or expired context is a decision, not a blip — and it arrives
	// wrapped in a *url.Error, which would otherwise match net.Error below.
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	// The server closed the connection without answering. client-go surfaces this
	// as a *url.Error wrapping io.EOF — the exact shape seen from CI runners.
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	if errors.Is(err, syscall.ECONNRESET) ||
		errors.Is(err, syscall.ECONNREFUSED) ||
		errors.Is(err, syscall.ECONNABORTED) ||
		errors.Is(err, syscall.EPIPE) {
		return true
	}
	// Anything the net package considers an error is a transport failure: an HTTP
	// response carrying an API error is not one of these.
	var nerr net.Error
	return errors.As(err, &nerr)
}

// Do calls fn until it succeeds, until a non-transient error is returned, or
// until the deadline passes — whichever comes first. A transient failure is
// retried after delay; the last one is returned if the deadline arrives before fn
// ever succeeds, so the caller still sees why.
//
// fn is always attempted at least once, even with a timeout already elapsed, so a
// zero timeout means "no retries" rather than "no attempt".
func Do(ctx context.Context, timeout, delay time.Duration, fn func() error) error {
	deadline := time.Now().Add(timeout)
	for {
		err := fn()
		if err == nil || !Transient(err) {
			return err
		}
		if !time.Now().Before(deadline) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
	}
}
