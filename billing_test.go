package main

import (
	"testing"
	"time"
)

// billingRead must never let more than billingConcurrency reads overlap. The
// endpoint answers 500 under concurrent load, so this cap is what keeps a panel
// load from looking like an outage.
func TestBillingReadCapsConcurrency(t *testing.T) {
	if cap(billingSlots) != billingConcurrency {
		t.Fatalf("limiter capacity = %d, want %d", cap(billingSlots), billingConcurrency)
	}

	// Fill every slot, then confirm a further acquire blocks rather than entering.
	released := make(chan struct{})
	held := 0
	for held < billingConcurrency {
		select {
		case billingSlots <- struct{}{}:
			held++
		default:
			t.Fatalf("limiter rejected slot %d of %d", held+1, billingConcurrency)
		}
	}
	go func() {
		billingSlots <- struct{}{}
		close(released)
	}()
	select {
	case <-released:
		t.Fatalf("a %dth read entered while %d were in flight", billingConcurrency+1, billingConcurrency)
	case <-time.After(100 * time.Millisecond):
	}
	for i := 0; i < held; i++ {
		<-billingSlots
	}
	select {
	case <-released:
	case <-time.After(2 * time.Second):
		t.Fatalf("the waiting read was not admitted after a slot freed")
	}
	<-billingSlots
}

// Only 5xx is worth repeating. A 4xx is a decision about this request, and a 200
// carrying a business error is an answer, not a failure.
func TestOnlyServerErrorsAreRetried(t *testing.T) {
	for _, status := range []int{500, 502, 503, 504} {
		if !isTransientBillingStatus(status) {
			t.Fatalf("status %d should be transient", status)
		}
	}
	for _, status := range []int{0, 200, 400, 401, 403, 404, 429} {
		if isTransientBillingStatus(status) {
			t.Fatalf("status %d should not be retried", status)
		}
	}
}

// The attempt count is fixed policy, asserted so a change is deliberate: three
// attempts is two retries, which covers a transient blip without holding a
// limiter slot through a long outage.
func TestBillingAttemptPolicy(t *testing.T) {
	if billingAttempts != 3 {
		t.Fatalf("billingAttempts = %d, want 3", billingAttempts)
	}
	if billingTimeout <= 0 || billingTimeout > 2*time.Minute {
		t.Fatalf("billingTimeout = %s, want a bounded per-attempt budget", billingTimeout)
	}
}
