package main

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
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

// An enterprise account is not in the personal resource-pack system: the
// personal endpoint answers with no accounts at all, so the page showed a
// permanent zero. Its own endpoint reports the other way round -- credit is what
// has been spent and limitNum is the allocation -- so remaining has to be
// derived.
func TestQuotaForEnterpriseDerivesRemaining(t *testing.T) {
	// Live sample: 812.45 used of 2000 allocated.
	body := []byte(`{"code":0,"msg":"OK","data":{"credit":812.45,"limitNum":2000,` +
		`"cycleStartTime":"2026-09-27 00:00:00","cycleEndTime":"2026-10-26 23:59:59",` +
		`"cycleResetTime":"2026-10-27 00:00:00"}}`)

	resp, err := quotaFromEnterpriseUsage(body)
	if err != nil {
		t.Fatalf("quotaFromEnterpriseUsage: %v", err)
	}
	remain, ok := metricValue(resp, "remain")
	if !ok {
		t.Fatal("no remain metric")
	}
	if remain < 1187.5 || remain > 1187.6 {
		t.Fatalf("remain = %v, want 2000-812.45", remain)
	}
	total, ok := metricValue(resp, "total")
	if !ok || total != 2000 {
		t.Fatalf("total = %v (present=%v), want 2000", total, ok)
	}
	if len(resp.Groups) != 1 || len(resp.Groups[0].Buckets) != 1 {
		t.Fatalf("groups = %+v, want one bucket", resp.Groups)
	}
	bucket := resp.Groups[0].Buckets[0]
	if bucket.ResetTime != "2026-10-27 00:00:00" {
		t.Fatalf("reset time = %q, want the cycle reset", bucket.ResetTime)
	}
	if bucket.RemainingFraction < 0.59 || bucket.RemainingFraction > 0.60 {
		t.Fatalf("fraction = %v, want about 0.594", bucket.RemainingFraction)
	}
}

// An allocation of -1 means the account is not metered. There is no total to take
// a percentage of, so the card must report only what has been spent rather than
// drawing a bar against a number that does not exist.
func TestQuotaForUnmeteredEnterpriseReportsOnlySpend(t *testing.T) {
	resp, err := quotaFromEnterpriseUsage([]byte(`{"code":0,"msg":"OK","data":{"credit":42.5,"limitNum":-1,"cycleResetTime":"2026-10-27 00:00:00"}}`))
	if err != nil {
		t.Fatalf("quotaFromEnterpriseUsage: %v", err)
	}
	if _, ok := metricValue(resp, "total"); ok {
		t.Fatal("an unmetered account must not report a total")
	}
	if _, ok := metricValue(resp, "remain"); ok {
		t.Fatal("an unmetered account must not report a remaining figure")
	}
	used, ok := metricValue(resp, "used")
	if !ok || used != 42.5 {
		t.Fatalf("used = %v (present=%v), want 42.5", used, ok)
	}
	if len(resp.Groups) != 1 || len(resp.Groups[0].Buckets) != 1 {
		t.Fatalf("groups = %+v, want one bucket", resp.Groups)
	}
	if resp.Groups[0].Buckets[0].RemainingFraction != 1 {
		t.Fatalf("fraction = %v, want a full bar", resp.Groups[0].Buckets[0].RemainingFraction)
	}
}

// Spending past the allocation is clamped rather than reported as negative.
func TestQuotaForEnterpriseClampsOverspend(t *testing.T) {
	resp, err := quotaFromEnterpriseUsage([]byte(`{"code":0,"data":{"credit":50,"limitNum":2000}}`))
	if err != nil {
		t.Fatalf("quotaFromEnterpriseUsage: %v", err)
	}
	if remain, _ := metricValue(resp, "remain"); remain != 1950 {
		t.Fatalf("remain = %v, want 1950", remain)
	}

	resp, err = quotaFromEnterpriseUsage([]byte(`{"code":0,"data":{"credit":2500,"limitNum":2000}}`))
	if err != nil {
		t.Fatalf("quotaFromEnterpriseUsage: %v", err)
	}
	if remain, _ := metricValue(resp, "remain"); remain != 0 {
		t.Fatalf("remain = %v, want 0 rather than a negative figure", remain)
	}
}

func TestQuotaFromEnterpriseUsageRejectsErrors(t *testing.T) {
	if _, err := quotaFromEnterpriseUsage([]byte(`not json`)); err == nil {
		t.Fatal("a non-JSON body must be an error")
	}
	if _, err := quotaFromEnterpriseUsage([]byte(`{"code":403,"msg":"not_authorized","data":{}}`)); err == nil {
		t.Fatal("a non-zero business code must be an error")
	}
}

// metricValue reads one summary metric by key.
func metricValue(resp pluginapi.QuotaFetchResponse, key string) (float64, bool) {
	for _, metric := range resp.Summary {
		if metric.Key == key {
			return metric.Value, true
		}
	}
	return 0, false
}

// packageJSON is one package fixture.
func packageJSON(name, code string, remain, size float64, end string) string {
	return `{"AccountId":1,"PackageName":"` + name + `","PackageCode":"` + code + `",` +
		`"CycleCapacityRemain":` + json.Number(formatNumber(remain)).String() + `,` +
		`"CycleCapacitySize":` + json.Number(formatNumber(size)).String() + `,` +
		`"CycleEndTime":"` + end + `"}`
}

func formatNumber(value float64) string {
	if value == float64(int64(value)) {
		return strconv.FormatInt(int64(value), 10)
	}
	return strconv.FormatFloat(value, 'f', -1, 64)
}

// A WorkBuddy account earns a package a day, so a long-lived account has dozens
// and a card that lists every one is a wall of near-identical rows. The soonest
// expiring ones are listed and the rest folded into one row -- but the totals have
// to keep counting all of them, or the summary stops matching the list.
func TestQuotaFoldsThePackageTail(t *testing.T) {
	accounts := make([]string, 0, 12)
	// Twelve packages, one per day, so the last is the furthest away.
	for day := 1; day <= 12; day++ {
		accounts = append(accounts, packageJSON("日签包", "p", 100, 100,
			fmt.Sprintf("2026-10-%02d 00:00:00", day)))
	}
	resp, err := quotaFromBilling(billingBody(t, `{"Accounts":[`+strings.Join(accounts, ",")+`]}`))
	if err != nil {
		t.Fatalf("quotaFromBilling: %v", err)
	}

	// Five listed packages plus the folded row.
	if len(resp.Groups) != quotaDetailLimit+1 {
		t.Fatalf("groups = %d, want %d listed + 1 folded", len(resp.Groups), quotaDetailLimit)
	}
	if got := resp.Groups[quotaDetailLimit].DisplayName; got != "其余 7 个包裹" {
		t.Fatalf("folded row = %q, want the remaining seven", got)
	}
	if got := resp.Groups[quotaDetailLimit].Buckets[0].Description; got != "700 / 700" {
		t.Fatalf("folded row amount = %q, want the tail's sum", got)
	}

	// The listed ones are the soonest to expire, in order.
	for index, want := range []string{"2026-10-01", "2026-10-02", "2026-10-03", "2026-10-04", "2026-10-05"} {
		if got := resp.Groups[index].Buckets[0].ResetTime; got[:10] != want {
			t.Fatalf("row %d resets %q, want %s", index, got, want)
		}
	}
	// The folded row resets with the last of the tail.
	if got := resp.Groups[quotaDetailLimit].Buckets[0].ResetTime; got[:10] != "2026-10-12" {
		t.Fatalf("folded reset = %q, want the furthest expiry", got)
	}

	// Every package is still counted.
	if remain, _ := metricValue(resp, "remain"); remain != 1200 {
		t.Fatalf("summary remain = %v, want 1200 (all twelve packages)", remain)
	}
	if total, _ := metricValue(resp, "total"); total != 1200 {
		t.Fatalf("summary total = %v, want 1200", total)
	}
}

// A short list is not folded at all.
func TestQuotaListsEveryPackageWhenThereAreFew(t *testing.T) {
	body := billingBody(t, `{"Accounts":[`+
		packageJSON("体验版", "p1", 500, 500, "2026-10-31 23:59:59")+`,`+
		packageJSON("裂变包", "p2", 37, 100, "2026-10-14 11:27:00")+`]}`)
	resp, err := quotaFromBilling(body)
	if err != nil {
		t.Fatalf("quotaFromBilling: %v", err)
	}
	if len(resp.Groups) != 2 {
		t.Fatalf("groups = %d, want both packages listed and nothing folded", len(resp.Groups))
	}
	// Sorted soonest first, so the nearly-expired package leads.
	if got := resp.Groups[0].DisplayName; got != "裂变包" {
		t.Fatalf("first group = %q, want the soonest-expiring package", got)
	}
}
