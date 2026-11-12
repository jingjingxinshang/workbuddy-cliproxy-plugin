package main

// Growth task centre automation.
//
// The task centre exposes a task list plus one accept call and one claim call.
// Claiming is what actually turns progress into credits, and a task that is
// finished but never claimed stays unclaimed forever, so this engine does two
// things per credential: accept whatever is waiting to be accepted, and claim
// whatever already reached its target.
//
// It deliberately does not synthesise the behaviour chains a few tasks require
// (opening a page, "summoning an expert", theme switching and so on). Those
// emit client-shaped activity events that are easy to get subtly wrong, and a
// wrong one is a risk to the account rather than a reward. The one behaviour
// this plugin can perform honestly is a chat activity report, which it already
// sends for the daily reward, so chat tasks are topped up with it.
//
// Two hard upstream limits shape the guards below:
//   - the growth system only exists for personal credentials: an enterprise
//     credential answers 403 "growth system is only available for personal
//     users";
//   - the international realm is read-only for tasks.
//
// Claiming is idempotent (a repeat answers already_claimed with 0 credits), so a
// failed claim is logged and retried on the next run instead of aborting the
// sweep.

import (
	"encoding/json"
	"fmt"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// tasksListPath and tasksAcceptPath live on the CLI host, next to the
	// heatmap and activity report the daily reward already uses.
	tasksListPath   = "/v2/activity/growth/tasks"
	tasksAcceptPath = "/v2/activity/growth/tasks/accept"
	// claimTaskPath is the web growth-centre shape: the task code is part of the
	// path and the request carries no body.
	claimTaskPath = "/activity/growth/tasks/%s/claim"
	// chatTaskCode is the task the plugin can advance by itself: it counts chat
	// activity reports, which is what sendGrowthActivity already emits.
	chatTaskCode = "chat_5"
	// autoTaskTimeout bounds one upstream call in this engine.
	autoTaskTimeout = 15 * time.Second
	// autoTaskAccountGap spaces credentials apart so a sweep does not look like a
	// burst of traffic from one client.
	autoTaskAccountGap = 3 * time.Second
)

// growthTask is one entry of the task list. Only the fields the engine acts on
// are read.
type growthTask struct {
	TaskCode     string `json:"task_code"`
	Title        string `json:"title"`
	Locked       bool   `json:"locked"`
	Claimed      bool   `json:"claimed"`
	AcceptStatus string `json:"accept_status"`
	Status       string `json:"status"`
	Target       int64  `json:"target"`
	Current      int64  `json:"current"`
}

// taskProgressReached reports whether the task's own progress numbers say it is
// finished. A task without a target cannot be judged this way.
func taskProgressReached(task growthTask) bool {
	if task.Target > 0 && task.Current >= task.Target {
		return true
	}
	status := strings.ToLower(strings.TrimSpace(task.Status))
	return status == "complete" || status == "completed"
}

// taskAcceptsPending reports whether the task is waiting to be accepted.
func taskAcceptsPending(task growthTask) bool {
	if task.Locked || task.Claimed {
		return false
	}
	if taskProgressReached(task) {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(task.AcceptStatus)) {
	case "accepted", "completed":
		return false
	}
	return true
}

// taskClaimable reports whether the task is finished and still unpaid. Progress
// is the judge, not the accept status: the accept status can briefly read as
// completed and then be rolled back upstream, and claiming on that reading
// claims nothing.
func taskClaimable(task growthTask) bool {
	return !task.Locked && !task.Claimed && taskProgressReached(task)
}

// listGrowthTasks reads the task list for one credential.
func listGrowthTasks(auth workbuddyAuth) ([]growthTask, error) {
	data, errCall := growthCall(auth, http.MethodGet, tasksListPath, nil)
	if errCall != nil {
		return nil, errCall
	}
	var payload struct {
		Tasks []growthTask `json:"tasks"`
	}
	if errUnmarshal := json.Unmarshal(data, &payload); errUnmarshal != nil {
		return nil, fmt.Errorf("task list decode: %w", errUnmarshal)
	}
	return payload.Tasks, nil
}

// acceptTasks accepts the given task codes in one call.
func acceptTasks(auth workbuddyAuth, codes []string) error {
	if len(codes) == 0 {
		return nil
	}
	body, errMarshal := json.Marshal(map[string]any{"task_codes": codes})
	if errMarshal != nil {
		return errMarshal
	}
	_, errCall := growthCall(auth, http.MethodPost, tasksAcceptPath, body)
	return errCall
}

// claimTask claims one task's reward.
//
// The code travels in the path and there is no body. The web origin is tried
// after the CLI host: the CLI host serves the rest of the growth endpoints, but
// the claim endpoint is a web growth-centre route and an unfamiliar host must
// not stop the claim.
func claimTask(auth workbuddyAuth, code string) error {
	profile := profiles[regionOf(auth)]
	path := fmt.Sprintf(claimTaskPath, code)
	targets := []string{profile.baseURL, profile.origin}
	var lastErr error
	for _, target := range targets {
		if strings.TrimSpace(target) == "" {
			continue
		}
		status, body, errCall := upstreamWithin(autoTaskTimeout, http.MethodPost, target+path, profile.origin, billingUserAgent, billingHeaders(auth), nil, false)
		if errCall != nil {
			lastErr = errCall
			continue
		}
		if status >= 400 {
			lastErr = fmt.Errorf("http %d from %s: %s", status, target, truncate(string(body), 160))
			continue
		}
		return nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no host to claim on")
	}
	return lastErr
}

// autoTaskCredential is one credential the engine may act on.
type autoTaskCredential struct {
	AuthIndex string
	Auth      workbuddyAuth
}

// autoTaskCredentials lists the credentials the engine may act on. Enterprise
// credentials have no personal growth system, the international realm is
// read-only, and a credential the operator disabled is left alone.
func autoTaskCredentials() []autoTaskCredential {
	entries, errList := authListSource()
	if errList != nil {
		return nil
	}
	var out []autoTaskCredential
	for _, entry := range entries {
		index := strings.TrimSpace(entry.AuthIndex)
		if index == "" {
			continue
		}
		if entry.Disabled {
			continue
		}
		raw := credentialForSweep(index)
		if len(raw) == 0 {
			continue
		}
		var auth workbuddyAuth
		if json.Unmarshal(raw, &auth) != nil || auth.AccessToken == "" {
			continue
		}
		if strings.TrimSpace(auth.EnterpriseID) != "" {
			continue
		}
		if regionOf(auth) != "cn" {
			continue
		}
		out = append(out, autoTaskCredential{AuthIndex: index, Auth: auth})
	}
	return out
}

// autoTaskResult is what one credential's sweep achieved.
type autoTaskResult struct {
	AuthIndex string `json:"auth_index"`
	Accepted  int    `json:"accepted"`
	Claimed   int    `json:"claimed"`
	Topped    int    `json:"chat_reports"`
	Error     string `json:"error,omitempty"`
}

// runAutoTasksOne sweeps one credential: top up the chat task when it is short,
// accept what is waiting, then claim what is finished.
func runAutoTasksOne(credential autoTaskCredential) autoTaskResult {
	result := autoTaskResult{AuthIndex: credential.AuthIndex}
	tasks, errList := listGrowthTasks(credential.Auth)
	if errList != nil {
		result.Error = errList.Error()
		return result
	}

	// Top up the one task the plugin can honestly advance: it counts chat
	// activity reports, and the reward engine already knows how to send one.
	for _, task := range tasks {
		if task.TaskCode != chatTaskCode || task.Locked || task.Claimed {
			continue
		}
		missing := task.Target - task.Current
		if task.Target <= 0 || missing <= 0 {
			continue
		}
		if missing > int64(autoTaskChatReportLimit) {
			missing = int64(autoTaskChatReportLimit)
		}
		for sent := int64(0); sent < missing; sent++ {
			if errSend := sendGrowthActivity(credential.Auth); errSend != nil {
				result.Error = errSend.Error()
				break
			}
			result.Topped++
		}
	}

	var pending []string
	for _, task := range tasks {
		if taskAcceptsPending(task) {
			pending = append(pending, task.TaskCode)
		}
	}
	if errAccept := acceptTasks(credential.Auth, pending); errAccept != nil {
		if result.Error == "" {
			result.Error = errAccept.Error()
		}
	} else {
		result.Accepted = len(pending)
	}

	// Re-read so a task that just reached its target is claimed in this run.
	tasks, errList = listGrowthTasks(credential.Auth)
	if errList != nil {
		if result.Error == "" {
			result.Error = errList.Error()
		}
		return result
	}
	for _, task := range tasks {
		if !taskClaimable(task) {
			continue
		}
		if errClaim := claimTask(credential.Auth, task.TaskCode); errClaim != nil {
			// Claiming is idempotent, so a failure is retried next run rather than
			// failing the whole sweep.
			warn("task claim failed for %s %s: %v", credential.AuthIndex, task.TaskCode, errClaim)
			continue
		}
		result.Claimed++
	}
	return result
}

// autoTaskChatReportLimit caps how many activity reports one run may send, so a
// misread target cannot turn into an unbounded burst.
const autoTaskChatReportLimit = 10

// runAutoTasks sweeps every eligible credential and returns the summary.
func runAutoTasks() []autoTaskResult {
	results := make([]autoTaskResult, 0)
	for position, credential := range autoTaskCredentials() {
		if position > 0 {
			time.Sleep(autoTaskAccountGap)
		}
		results = append(results, runAutoTasksOne(credential))
	}
	return results
}

// autoTasksEnabled reports whether the automation is switched on. Absent means
// off: this engine acts on the operator's own account, so it has to be asked
// for rather than come on by itself.
func autoTasksEnabled() bool {
	configMu.RLock()
	enabled := pluginCfg.AutoTasks
	configMu.RUnlock()
	return enabled != nil && *enabled
}

// autoTasksSummary is the payload the management route answers.
func autoTasksSummary(results []autoTaskResult) map[string]any {
	accepted, claimed, topped := 0, 0, 0
	errors := make([]map[string]string, 0)
	for _, result := range results {
		accepted += result.Accepted
		claimed += result.Claimed
		topped += result.Topped
		if result.Error != "" {
			errors = append(errors, map[string]string{"auth_index": result.AuthIndex, "error": result.Error})
		}
	}
	return map[string]any{
		"accounts":        len(results),
		"accepted":        accepted,
		"claimed":         claimed,
		"chat_reports":    topped,
		"errors":          errors,
		"account_details": results,
		"run_at":          strconv.FormatInt(time.Now().UnixMilli(), 10),
	}
}

// autoTasksHours are the local hours one unattended sweep may start in. The
// window is an hour wide so a tick that is late, or a boot mid-window, still
// runs; a run that already happened this hour is skipped by the guard below.
var autoTasksHours = []int{9, 15, 21}

// autoTasksTick is how often the loop looks at the clock.
const autoTasksTick = 10 * time.Minute

// shouldRunAutoTasksNow reports whether the current time falls in a sweep slot.
func shouldRunAutoTasksNow(now time.Time) bool {
	for _, hour := range autoTasksHours {
		start := time.Date(now.Year(), now.Month(), now.Day(), hour, 0, 0, 0, now.Location())
		if !now.Before(start) && now.Before(start.Add(time.Hour)) {
			return true
		}
	}
	return false
}

// lastAutoTasksRun is the wall clock of the last unattended sweep, so a slot
// cannot run twice just because the tick landed twice inside the same hour.
var (
	lastAutoTasksMu  sync.Mutex
	lastAutoTasksRun time.Time
)

// claimAutoTasksSlot reserves the current hour's run. It reports false when this
// hour already ran.
func claimAutoTasksSlot(now time.Time) bool {
	lastAutoTasksMu.Lock()
	defer lastAutoTasksMu.Unlock()
	slot := now.Truncate(time.Hour)
	if !lastAutoTasksRun.Before(slot) {
		return false
	}
	lastAutoTasksRun = slot
	return true
}

// runAutoTasksSafely runs a sweep without letting a panic escape. This goroutine
// is not entered through the host's RPC path, so nothing above it would recover
// one, and an escaping panic would take the host process down.
func runAutoTasksSafely() {
	defer func() {
		if recovered := recover(); recovered != nil {
			warn("task automation panicked: %v", recovered)
		}
	}()
	runAutoTasks()
}

// autoTasksLoop is the automation timer. Like the reward loop it is never
// stopped: the host unloads this library on exit, and touching Go runtime state
// from the shutdown callback is what produced SIGSEGVs in cgo. This goroutine
// holds nothing that outlives the process.
func autoTasksLoop() {
	ticker := time.NewTicker(autoTasksTick)
	defer ticker.Stop()
	for range ticker.C {
		if !autoTasksEnabled() || !shouldRunAutoTasksNow(time.Now()) {
			continue
		}
		if !claimAutoTasksSlot(time.Now()) {
			continue
		}
		runAutoTasksSafely()
	}
}

func init() { go autoTasksLoop() }

// autoTasksRouteResponse answers POST /v0/management/<pluginID>/autotasks.
//
// A manual run ignores the scheduled toggle, the same way the check-in button
// does: the toggle gates unattended runs, not an operator asking for one.
func autoTasksRouteResponse(raw []byte) pluginapi.ManagementResponse {
	_ = raw
	results := runAutoTasks()
	body, errMarshal := json.Marshal(autoTasksSummary(results))
	if errMarshal != nil {
		return errorManagementResponse(http.StatusInternalServerError, "cannot encode the result")
	}
	return jsonManagementResponse(body)
}
