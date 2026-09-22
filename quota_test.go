package main

import (
	"encoding/json"
	"testing"
)

// A spent package must report zero remaining.
//
// The reported case: a 500-credit package was used up, and the quota page still
// said 500 remaining. The cycle capacity had reached 0, and 0 was read as "the
// field is absent" -- so the figure fell through to the package capacity, which
// still held the purchased total. Absent and zero have to be told apart.
func TestQuotaReportsSpentPackageAsZero(t *testing.T) {
	// Cycle figures present and spent; package figures still hold the purchase.
	body := billingBody(t, `{"Accounts":[{
		"AccountId": 1,
		"PackageName": "500 积分包",
		"PackageCode": "p_tcaca",
		"CapacityRemain": 500,
		"CapacitySize": 500,
		"CycleCapacityRemain": 0,
		"CycleCapacitySize": 500,
		"CycleEndTime": "2026-10-01 00:00:00",
		"Status": 0
	}]}`)

	resp, err := quotaFromBilling(body)
	if err != nil {
		t.Fatalf("quotaFromBilling: %v", err)
	}
	summary := map[string]float64{}
	for _, metric := range resp.Summary {
		summary[metric.Key] = metric.Value
	}
	if summary["remain"] != 0 {
		t.Fatalf("remain = %v, want 0: a spent cycle must not fall through to the package figure", summary["remain"])
	}
	if summary["total"] != 500 {
		t.Fatalf("total = %v, want 500", summary["total"])
	}
	if summary["used_percent"] != 100 {
		t.Fatalf("used_percent = %v, want 100", summary["used_percent"])
	}
	if len(resp.Groups) != 1 {
		t.Fatalf("groups = %d, want 1", len(resp.Groups))
	}
	if fraction := resp.Groups[0].Buckets[0].RemainingFraction; fraction != 0 {
		t.Fatalf("remaining fraction = %v, want 0", fraction)
	}
}

// An account that carries only the package figures still reports them.
func TestQuotaFallsBackToPackageCapacity(t *testing.T) {
	body := billingBody(t, `{"Accounts":[{
		"AccountId": 2,
		"PackageName": "Lifetime",
		"CapacityRemain": 120,
		"CapacitySize": 500,
		"CycleEndTime": "2026-10-01 00:00:00",
		"Status": 3
	}]}`)

	resp, err := quotaFromBilling(body)
	if err != nil {
		t.Fatalf("quotaFromBilling: %v", err)
	}
	summary := map[string]float64{}
	for _, metric := range resp.Summary {
		summary[metric.Key] = metric.Value
	}
	if summary["remain"] != 120 || summary["total"] != 500 {
		t.Fatalf("remain/total = %v/%v, want 120/500", summary["remain"], summary["total"])
	}
}

// Multiple packages are summed, and an empty answer stays an error rather than
// rendering as a zero.
func TestQuotaSumsPackages(t *testing.T) {
	body := billingBody(t, `{"Accounts":[
		{"AccountId":1,"PackageName":"a","CycleCapacityRemain":0,"CycleCapacitySize":500},
		{"AccountId":2,"PackageName":"b","CycleCapacityRemain":50,"CycleCapacitySize":100}
	]}`)
	resp, err := quotaFromBilling(body)
	if err != nil {
		t.Fatalf("quotaFromBilling: %v", err)
	}
	summary := map[string]float64{}
	for _, metric := range resp.Summary {
		summary[metric.Key] = metric.Value
	}
	if summary["remain"] != 50 || summary["total"] != 600 {
		t.Fatalf("remain/total = %v/%v, want 50/600", summary["remain"], summary["total"])
	}

	if _, errEmpty := quotaFromBilling(billingBody(t, `{"Accounts":[]}`)); errEmpty == nil {
		t.Fatal("an empty account list must be an error, not a zero quota")
	}
}

// billingBody wraps account JSON in the envelope the billing endpoint returns.
func billingBody(t *testing.T, accounts string) []byte {
	t.Helper()
	wrapper := map[string]any{
		"code": 0,
		"msg":  "ok",
		"data": map[string]any{
			"response": map[string]any{
				"data": json.RawMessage(accounts),
			},
		},
	}
	body, err := json.Marshal(wrapper)
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	return body
}
