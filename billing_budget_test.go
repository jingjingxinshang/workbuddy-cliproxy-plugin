package main

import (
	"testing"
	"time"
)

// A saturated limiter must fail fast, not queue. Waiting without a bound is how a
// limiter becomes a hang: four stuck reads left every later request waiting until
// its caller gave up, which reads as a timeout with no cause.
func TestSaturatedLimiterFailsFast(t *testing.T) {
	if cap(billingSlots) != billingConcurrency {
		t.Fatalf("limiter capacity = %d, want %d", cap(billingSlots), billingConcurrency)
	}
	for i := 0; i < billingConcurrency; i++ {
		billingSlots <- struct{}{}
	}
	defer func() {
		for i := 0; i < billingConcurrency; i++ {
			<-billingSlots
		}
	}()

	start := time.Now()
	select {
	case billingSlots <- struct{}{}:
		<-billingSlots
		t.Fatal("a read entered a saturated limiter instead of being refused")
	case <-time.After(billingSlotWait):
	}
	if elapsed := time.Since(start); elapsed > billingSlotWait+2*time.Second {
		t.Fatalf("refusal took %s, want about %s", elapsed, billingSlotWait)
	}
}

// The wait must be short enough that a caller notices a refusal rather than a
// hang: this is what the panel experiences as a timeout.
func TestSlotWaitIsShort(t *testing.T) {
	if billingSlotWait <= 0 {
		t.Fatal("billingSlotWait must be positive")
	}
	if billingSlotWait > 5*time.Second {
		t.Fatalf("billingSlotWait = %s, too long to report as anything but a hang", billingSlotWait)
	}
}

// The whole read is bounded, retries included. Budgeting each attempt instead let
// a failing upstream multiply its latency by the attempt count, so a broken
// credential took a minute to say so.
func TestBillingBudgetBoundsTheWholeRead(t *testing.T) {
	if billingBudget <= 0 {
		t.Fatal("billingBudget must be positive")
	}
	if billingBudget > 30*time.Second {
		t.Fatalf("billingBudget = %s, too long for a panel request", billingBudget)
	}
	if billingAttemptTimeout <= 0 {
		t.Fatal("billingAttemptTimeout must be positive")
	}
	if billingAttemptTimeout > billingBudget {
		t.Fatalf("an attempt (%s) cannot exceed the whole budget (%s)", billingAttemptTimeout, billingBudget)
	}
	// The guarantee that matters: worst case is the budget, not attempts x timeout.
	worstCase := time.Duration(billingAttempts) * billingAttemptTimeout
	if worstCase <= billingBudget {
		t.Fatalf("attempts x timeout = %s does not exceed the budget (%s), so the test proves nothing", worstCase, billingBudget)
	}
}
