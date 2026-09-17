package main

/*
#include <stdint.h>
#include <stdlib.h>

typedef struct { void* ptr; size_t len; } cliproxy_buffer;
typedef struct { uint32_t abi_version; void* host_ctx; void* call; void* free_buffer; } cliproxy_host_api;
typedef int (*cliproxy_plugin_call_fn)(char*, uint8_t*, size_t, cliproxy_buffer*);
typedef void (*cliproxy_plugin_free_fn)(void*, size_t);
typedef void (*cliproxy_plugin_shutdown_fn)(void);
typedef struct { uint32_t abi_version; cliproxy_plugin_call_fn call; cliproxy_plugin_free_fn free_buffer; cliproxy_plugin_shutdown_fn shutdown; } cliproxy_plugin_api;
extern int cliproxyPluginCall(char*, uint8_t*, size_t, cliproxy_buffer*);
extern void cliproxyPluginFree(void*, size_t);
extern void cliproxyPluginShutdown(void);
*/
import "C"

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
	"unsafe"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

const (
	providerID  = "workbuddy"
	pluginName  = "WorkBuddy"
	pluginVer   = "0.1.0"
	loginTTL    = 5 * time.Minute
	pollTimeout = 20 * time.Second
)

type regionProfile struct {
	baseURL       string
	origin        string
	userAgent     string
	loginPlatform string
}

var profiles = map[string]regionProfile{
	"cn":   {baseURL: "https://copilot.tencent.com", origin: "https://www.codebuddy.cn", userAgent: "CLI/2.63.2 CodeBuddy/2.63.2", loginPlatform: "CLI"},
	"intl": {baseURL: "https://www.codebuddy.ai", origin: "https://www.workbuddy.ai", userAgent: "WorkBuddy/1.0", loginPlatform: "workbuddy-ai"},
}

type workbuddyAuth struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresAt    int64  `json:"expires_at"`
	UID          string `json:"uid,omitempty"`
	Nickname     string `json:"nickname,omitempty"`
	EnterpriseID string `json:"enterprise_id,omitempty"`
	Domain       string `json:"domain,omitempty"`
	Region       string `json:"region,omitempty"`
	UserAgent    string `json:"user_agent,omitempty"`
}

type loginState struct {
	State     string    `json:"state"`
	Region    string    `json:"region"`
	ExpiresAt time.Time `json:"expires_at"`
}

type envelope struct {
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *envelopeError  `json:"error,omitempty"`
}

type envelopeError struct {
	Code       string `json:"code"`
	Message    string `json:"message"`
	HTTPStatus int    `json:"http_status,omitempty"`
}

type registration struct {
	SchemaVersion uint32                 `json:"schema_version"`
	Metadata      pluginapi.Metadata     `json:"metadata"`
	Capabilities  registrationCapability `json:"capabilities"`
}

type registrationCapability struct {
	ModelProvider         bool                         `json:"model_provider"`
	AuthProvider          bool                         `json:"auth_provider"`
	Executor              bool                         `json:"executor"`
	ExecutorModelScope    pluginapi.ExecutorModelScope `json:"executor_model_scope"`
	ExecutorInputFormats  []string                     `json:"executor_input_formats,omitempty"`
	ExecutorOutputFormats []string                     `json:"executor_output_formats,omitempty"`
	CommandLinePlugin     bool                         `json:"command_line_plugin"`
	ManagementAPI         bool                         `json:"management_api"`
	QuotaProvider         bool                         `json:"quota_provider"`
}

type identifierResponse struct {
	Identifier string `json:"identifier"`
}
type streamResponse struct {
	Headers http.Header                     `json:"headers,omitempty"`
	Chunks  []pluginapi.ExecutorStreamChunk `json:"chunks,omitempty"`
}
type managementRegistration struct {
	Routes    []pluginapi.ManagementRoute `json:"routes,omitempty"`
	Resources []pluginapi.ResourceRoute   `json:"resources,omitempty"`
}

type serverEnvelope struct {
	Code int             `json:"code"`
	Msg  string          `json:"msg"`
	Data json.RawMessage `json:"data"`
}

type stateResponse struct {
	State   string `json:"state"`
	AuthURL string `json:"authUrl"`
}
type tokenResponse struct {
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken"`
	ExpiresIn    int64  `json:"expiresIn"`
	Domain       string `json:"domain"`
}
type identityResponse struct {
	UID          string `json:"uid"`
	Nickname     string `json:"nickname"`
	EnterpriseID string `json:"enterpriseId"`
	Enterprise   struct {
		ID string `json:"id"`
	} `json:"enterprise"`
}
type catalogResponse struct {
	Agents []struct {
		Name   string   `json:"name"`
		Models []string `json:"models"`
	} `json:"agents"`
	Models []struct {
		ID                string   `json:"id"`
		Name              string   `json:"name"`
		DescriptionEN     string   `json:"descriptionEn"`
		DescriptionZH     string   `json:"descriptionZh"`
		MaxInputTokens    int64    `json:"maxInputTokens"`
		MaxOutputTokens   int64    `json:"maxOutputTokens"`
		SupportsToolCall  bool     `json:"supportsToolCall"`
		SupportsImages    bool     `json:"supportsImages"`
		SupportsReasoning bool     `json:"supportsReasoning"`
		OnlyReasoning     bool     `json:"onlyReasoning"`
		Tags              []string `json:"tags"`
		Reasoning         struct {
			DefaultEffort      string   `json:"defaultEffort"`
			SupportedEfforts   []string `json:"supportedEfforts"`
			CanDisableThinking bool     `json:"canDisableThinking"`
		} `json:"reasoning"`
	} `json:"models"`
}

var (
	hostAPI *C.cliproxy_host_api
	loginMu sync.Mutex
	logins  = map[string]loginState{}
)

func main() {}

//export cliproxy_plugin_init
func cliproxy_plugin_init(host *C.cliproxy_host_api, plugin *C.cliproxy_plugin_api) C.int {
	if plugin == nil {
		return 1
	}
	hostAPI = host
	plugin.abi_version = C.uint32_t(pluginabi.ABIVersion)
	plugin.call = C.cliproxy_plugin_call_fn(C.cliproxyPluginCall)
	plugin.free_buffer = C.cliproxy_plugin_free_fn(C.cliproxyPluginFree)
	plugin.shutdown = C.cliproxy_plugin_shutdown_fn(C.cliproxyPluginShutdown)
	return 0
}

//export cliproxyPluginCall
func cliproxyPluginCall(method *C.char, request *C.uint8_t, requestLen C.size_t, response *C.cliproxy_buffer) C.int {
	if response != nil {
		response.ptr = nil
		response.len = 0
	}
	if method == nil {
		writeResponse(response, errorEnvelope("invalid_method", "method is required", http.StatusBadRequest))
		return 1
	}
	var raw []byte
	if request != nil && requestLen > 0 {
		raw = C.GoBytes(unsafe.Pointer(request), C.int(requestLen))
	}
	result, err := handleMethod(C.GoString(method), raw)
	if err != nil {
		writeResponse(response, errorEnvelope("plugin_error", err.Error(), statusOf(err)))
		return 1
	}
	writeResponse(response, result)
	return 0
}

//export cliproxyPluginFree
func cliproxyPluginFree(ptr unsafe.Pointer, length C.size_t) {
	if ptr != nil {
		C.free(ptr)
	}
	_ = length
}

//export cliproxyPluginShutdown
func cliproxyPluginShutdown() { hostAPI = nil }

func handleMethod(method string, raw []byte) ([]byte, error) {
	switch method {
	case pluginabi.MethodPluginRegister, pluginabi.MethodPluginReconfigure:
		return okEnvelope(registrationData())
	case pluginabi.MethodAuthIdentifier, pluginabi.MethodExecutorIdentifier, pluginabi.MethodQuotaIdentifier:
		return okEnvelope(identifierResponse{Identifier: providerID})
	case pluginabi.MethodAuthParse:
		return okEnvelope(parseAuth(raw))
	case pluginabi.MethodAuthLoginStart:
		return okEnvelope(startLogin(raw))
	case pluginabi.MethodAuthLoginPoll:
		return okEnvelope(pollLogin(raw))
	case pluginabi.MethodAuthRefresh:
		return okEnvelope(refreshAuth(raw))
	case pluginabi.MethodModelStatic, pluginabi.MethodModelRegister:
		return okEnvelope(pluginapi.ModelRegistrationResponse{Provider: providerID, Models: nil})
	case pluginabi.MethodModelForAuth:
		return okEnvelope(discoverModels(raw))
	case pluginabi.MethodExecutorExecute:
		return execute(raw, false)
	case pluginabi.MethodExecutorExecuteStream:
		return execute(raw, true)
	case pluginabi.MethodExecutorCountTokens:
		return okEnvelope(pluginapi.ExecutorResponse{Payload: []byte(`{"total_tokens":0}`)})
	case pluginabi.MethodExecutorHTTPRequest:
		return okEnvelope(pluginapi.ExecutorHTTPResponse{StatusCode: http.StatusNotImplemented, Body: []byte(`{"error":"not implemented"}`)})
	case pluginabi.MethodCommandLineRegister:
		return okEnvelope(pluginapi.CommandLineRegistrationResponse{Flags: []pluginapi.CommandLineFlag{{Name: "workbuddy-login", Usage: "Start WorkBuddy QR login", Type: "bool"}}})
	case pluginabi.MethodCommandLineExecute:
		return okEnvelope(pluginapi.CommandLineExecutionResponse{Stdout: []byte("Use the CPA Management API to start WorkBuddy login.\n")})
	case pluginabi.MethodManagementRegister:
		return okEnvelope(managementRegistration{Resources: []pluginapi.ResourceRoute{{Path: "/status", Menu: "WorkBuddy", Description: "WorkBuddy provider status"}}})
	case pluginabi.MethodManagementHandle:
		return okEnvelope(pluginapi.ManagementResponse{StatusCode: http.StatusOK, Headers: http.Header{"Content-Type": []string{"application/json"}}, Body: []byte(`{"provider":"workbuddy","status":"ready"}`)})
	case pluginabi.MethodQuotaDescribe:
		return okEnvelope(pluginapi.QuotaDescribeResponse{SupportedProviders: []string{providerID}, DisplayName: pluginName})
	case pluginabi.MethodQuotaFetch:
		return okEnvelope(pluginapi.QuotaFetchResponse{})
	default:
		return errorEnvelope("unknown_method", "unknown method: "+method, http.StatusNotImplemented), nil
	}
}

func registrationData() registration {
	return registration{SchemaVersion: pluginabi.SchemaVersion, Metadata: pluginapi.Metadata{Name: pluginName, Version: pluginVer, Author: "WorkBuddy CPA Plugin", GitHubRepository: "https://github.com/jingjingxinshang/workbuddy-cliproxy-plugin", ConfigFields: []pluginapi.ConfigField{{Name: "default_region", Type: pluginapi.ConfigFieldTypeEnum, EnumValues: []string{"cn", "intl"}, Description: "Default WorkBuddy cluster used for login."}}}, Capabilities: registrationCapability{ModelProvider: true, AuthProvider: true, Executor: true, ExecutorModelScope: pluginapi.ExecutorModelScopeOAuth, ExecutorInputFormats: []string{"chat-completions"}, ExecutorOutputFormats: []string{"chat-completions"}, CommandLinePlugin: true, ManagementAPI: true, QuotaProvider: true}}
}

func parseAuth(raw []byte) pluginapi.AuthParseResponse {
	var req pluginapi.AuthParseRequest
	if json.Unmarshal(raw, &req) != nil {
		return pluginapi.AuthParseResponse{}
	}
	if req.Provider != providerID && !strings.Contains(strings.ToLower(req.FileName), "workbuddy") {
		return pluginapi.AuthParseResponse{}
	}
	var auth workbuddyAuth
	if json.Unmarshal(req.RawJSON, &auth) != nil || auth.AccessToken == "" {
		return pluginapi.AuthParseResponse{}
	}
	return pluginapi.AuthParseResponse{Handled: true, Auth: authData(auth, req.FileName)}
}

func authData(auth workbuddyAuth, fileName string) pluginapi.AuthData {
	if fileName == "" {
		fileName = providerID + ".json"
	}
	body, _ := json.Marshal(auth)
	id := auth.UID
	if id == "" {
		id = shortID(auth.AccessToken)
	}
	label := auth.Nickname
	if label == "" {
		label = "WorkBuddy " + id
	}
	return pluginapi.AuthData{Provider: providerID, ID: id, FileName: fileName, Label: label, StorageJSON: body, Metadata: map[string]any{"region": regionOf(auth)}}
}

func startLogin(raw []byte) pluginapi.AuthLoginStartResponse {
	var req pluginapi.AuthLoginStartRequest
	_ = json.Unmarshal(raw, &req)
	region := "cn"
	if v, ok := req.Metadata["region"].(string); ok && profiles[v].baseURL != "" {
		region = v
	}
	profile := profiles[region]
	url := profile.baseURL + "/v2/plugin/auth/state?platform=" + profile.loginPlatform
	status, body, err := upstream("POST", url, profile.origin, profile.userAgent, nil, []byte("{}"), false)
	if err != nil || status >= 400 {
		return pluginapi.AuthLoginStartResponse{Provider: providerID, ExpiresAt: time.Now().Add(loginTTL), Metadata: map[string]any{"error": fmt.Sprintf("WorkBuddy login start failed: %v", err)}}
	}
	var env serverEnvelope
	if json.Unmarshal(body, &env) != nil || env.Code != 0 {
		return pluginapi.AuthLoginStartResponse{Provider: providerID, ExpiresAt: time.Now().Add(loginTTL), Metadata: map[string]any{"error": "invalid WorkBuddy login response"}}
	}
	var data stateResponse
	_ = json.Unmarshal(env.Data, &data)
	loginMu.Lock()
	logins[data.State] = loginState{State: data.State, Region: region, ExpiresAt: time.Now().Add(loginTTL)}
	loginMu.Unlock()
	return pluginapi.AuthLoginStartResponse{Provider: providerID, URL: data.AuthURL, State: data.State, ExpiresAt: time.Now().Add(loginTTL), Metadata: map[string]any{"region": region}}
}

func pollLogin(raw []byte) pluginapi.AuthLoginPollResponse {
	var req pluginapi.AuthLoginPollRequest
	_ = json.Unmarshal(raw, &req)
	loginMu.Lock()
	pending, ok := logins[req.State]
	loginMu.Unlock()
	if !ok || time.Now().After(pending.ExpiresAt) {
		return pluginapi.AuthLoginPollResponse{Status: pluginapi.AuthLoginStatusError, Message: "login expired"}
	}
	profile := profiles[pending.Region]
	status, body, err := upstream("GET", profile.baseURL+"/v2/plugin/auth/token?state="+urlQuery(req.State), profile.origin, profile.userAgent, nil, nil, false)
	if err != nil || status >= 500 {
		return pluginapi.AuthLoginPollResponse{Status: pluginapi.AuthLoginStatusPending, Message: "waiting for WorkBuddy login"}
	}
	var token tokenResponse
	var env serverEnvelope
	if json.Unmarshal(body, &env) == nil && env.Code == 0 {
		_ = json.Unmarshal(env.Data, &token)
	} else {
		_ = json.Unmarshal(body, &token)
	}
	if token.AccessToken == "" {
		return pluginapi.AuthLoginPollResponse{Status: pluginapi.AuthLoginStatusPending, Message: "waiting for QR scan"}
	}
	identity := fetchIdentity(profile, token.AccessToken)
	auth := workbuddyAuth{AccessToken: token.AccessToken, RefreshToken: token.RefreshToken, ExpiresAt: time.Now().Add(time.Duration(token.ExpiresIn) * time.Second).UnixMilli(), UID: identity.UID, Nickname: identity.Nickname, EnterpriseID: identity.EnterpriseID, Domain: token.Domain, Region: pending.Region, UserAgent: profile.userAgent}
	loginMu.Lock()
	delete(logins, req.State)
	loginMu.Unlock()
	return pluginapi.AuthLoginPollResponse{Status: pluginapi.AuthLoginStatusSuccess, Message: "WorkBuddy login complete", Auth: authData(auth, "workbuddy-"+shortID(auth.AccessToken)+".json")}
}

func refreshAuth(raw []byte) pluginapi.AuthRefreshResponse {
	var req pluginapi.AuthRefreshRequest
	_ = json.Unmarshal(raw, &req)
	var auth workbuddyAuth
	if json.Unmarshal(req.StorageJSON, &auth) != nil || auth.RefreshToken == "" {
		return pluginapi.AuthRefreshResponse{}
	}
	profile := profiles[regionOf(auth)]
	status, body, err := upstream("POST", profile.baseURL+"/v2/plugin/auth/token/refresh", profile.origin, profile.userAgent, map[string]string{"X-Refresh-Token": auth.RefreshToken}, []byte("{}"), false)
	if err != nil || status >= 400 {
		return pluginapi.AuthRefreshResponse{}
	}
	var env serverEnvelope
	var token tokenResponse
	if json.Unmarshal(body, &env) == nil && env.Code == 0 {
		_ = json.Unmarshal(env.Data, &token)
	} else {
		_ = json.Unmarshal(body, &token)
	}
	if token.AccessToken == "" {
		return pluginapi.AuthRefreshResponse{}
	}
	auth.AccessToken = token.AccessToken
	if token.RefreshToken != "" {
		auth.RefreshToken = token.RefreshToken
	}
	auth.ExpiresAt = time.Now().Add(time.Duration(maxInt64(token.ExpiresIn, 3600)) * time.Second).UnixMilli()
	return pluginapi.AuthRefreshResponse{Auth: authData(auth, req.AuthID+".json"), NextRefreshAfter: time.UnixMilli(auth.ExpiresAt).Add(-5 * time.Minute)}
}

func discoverModels(raw []byte) pluginapi.ModelResponse {
	var req pluginapi.AuthModelRequest
	_ = json.Unmarshal(raw, &req)
	var auth workbuddyAuth
	if json.Unmarshal(req.StorageJSON, &auth) != nil {
		return pluginapi.ModelResponse{Provider: providerID}
	}
	profile := profiles[regionOf(auth)]
	headers := map[string]string{"Authorization": "Bearer " + auth.AccessToken, "X-Product": "SaaS"}
	status, body, err := upstream("GET", profile.baseURL+"/v3/config", profile.origin, authUserAgent(auth, profile), headers, nil, false)
	if err != nil || status >= 400 {
		return pluginapi.ModelResponse{Provider: providerID}
	}
	var env serverEnvelope
	var catalog catalogResponse
	if json.Unmarshal(body, &env) == nil && env.Data != nil {
		if env.Code != 0 {
			return pluginapi.ModelResponse{Provider: providerID}
		}
		_ = json.Unmarshal(env.Data, &catalog)
	} else {
		_ = json.Unmarshal(body, &catalog)
	}
	models := make([]pluginapi.ModelInfo, 0, len(catalog.Models))
	for _, m := range catalog.Models {
		if hasTag(m.Tags, "lite") || hasTag(m.Tags, "text-to-image") || hasTag(m.Tags, "image-to-image") {
			continue
		}
		input := []string{"text"}
		if m.SupportsImages {
			input = append(input, "image")
		}
		context := m.MaxInputTokens
		if context == 0 {
			context = 200000
		}
		output := m.MaxOutputTokens
		if output == 0 {
			output = 8192
		}
		display := m.Name
		if display == "" {
			display = m.ID
		}
		levels := m.Reasoning.SupportedEfforts
		var thinking *pluginapi.ThinkingSupport
		if m.SupportsReasoning {
			thinking = &pluginapi.ThinkingSupport{Levels: levels, ZeroAllowed: m.Reasoning.CanDisableThinking, DynamicAllowed: true}
		}
		models = append(models, pluginapi.ModelInfo{ID: m.ID, Name: m.ID, Object: "model", OwnedBy: providerID, DisplayName: display, Description: firstNonEmpty(m.DescriptionEN, m.DescriptionZH), ContextLength: context, MaxCompletionTokens: output, SupportedGenerationMethods: []string{"chat"}, SupportedInputModalities: input, SupportedOutputModalities: []string{"text"}, SupportedParameters: []string{"tools", "stream"}, Thinking: thinking})
	}
	return pluginapi.ModelResponse{Provider: providerID, Models: models, AuthUpdate: *reqAuthUpdate(req, auth)}
}

func execute(raw []byte, stream bool) ([]byte, error) {
	var req pluginapi.ExecutorRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, err
	}
	var auth workbuddyAuth
	if err := json.Unmarshal(req.StorageJSON, &auth); err != nil || auth.AccessToken == "" {
		return errorEnvelope("authentication_error", "WorkBuddy auth is missing", http.StatusUnauthorized), nil
	}
	profile := profiles[regionOf(auth)]
	headers := chatHeaders(auth, profile)
	status, body, err := upstream("POST", profile.baseURL+"/v2/chat/completions", profile.origin, authUserAgent(auth, profile), headers, req.Payload, stream)
	if err != nil {
		return errorEnvelope("upstream_error", err.Error(), http.StatusBadGateway), nil
	}
	if status >= 400 {
		return errorEnvelope("upstream_error", string(body), status), nil
	}
	if !stream {
		return okEnvelope(pluginapi.ExecutorResponse{Payload: body, Headers: http.Header{"Content-Type": []string{"application/json"}}})
	}
	chunks := splitSSE(body)
	return okEnvelope(streamResponse{Headers: http.Header{"Content-Type": []string{"text/event-stream"}}, Chunks: chunks})
}

func upstream(method, target, origin, userAgent string, extra map[string]string, body []byte, stream bool) (int, []byte, error) {
	ctx, cancel := contextWithTimeout(pollTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, target, bytes.NewReader(body))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", origin)
	req.Header.Set("Referer", origin+"/")
	req.Header.Set("User-Agent", userAgent)
	if stream {
		req.Header.Set("Accept", "text/event-stream")
	}
	for k, v := range extra {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	return resp.StatusCode, data, err
}

func chatHeaders(auth workbuddyAuth, profile regionProfile) map[string]string {
	h := map[string]string{"Authorization": "Bearer " + auth.AccessToken, "X-Refresh-Token": auth.RefreshToken, "X-Product": "SaaS", "X-No-Department-Info": "1", "X-Requested-With": "XMLHttpRequest"}
	if auth.UID != "" {
		h["X-User-Id"] = auth.UID
	}
	if auth.EnterpriseID != "" {
		h["X-Enterprise-Id"] = auth.EnterpriseID
	}
	if auth.Domain != "" {
		h["X-Domain"] = auth.Domain
	}
	return h
}
func fetchIdentity(profile regionProfile, token string) identityResponse {
	_, body, err := upstream("GET", profile.baseURL+"/v2/plugin/login/account", profile.origin, profile.userAgent, map[string]string{"Authorization": "Bearer " + token}, nil, false)
	if err != nil {
		return identityResponse{}
	}
	var env serverEnvelope
	var out identityResponse
	if json.Unmarshal(body, &env) == nil && env.Data != nil {
		_ = json.Unmarshal(env.Data, &out)
	} else {
		_ = json.Unmarshal(body, &out)
	}
	if out.EnterpriseID == "" {
		out.EnterpriseID = out.Enterprise.ID
	}
	return out
}
func splitSSE(body []byte) []pluginapi.ExecutorStreamChunk {
	scanner := bufio.NewScanner(bytes.NewReader(body))
	scanner.Buffer(make([]byte, 4096), 4<<20)
	chunks := []pluginapi.ExecutorStreamChunk{}
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		chunks = append(chunks, pluginapi.ExecutorStreamChunk{Payload: append(append([]byte(nil), line...), '\n', '\n')})
	}
	if len(chunks) == 0 && len(body) > 0 {
		chunks = append(chunks, pluginapi.ExecutorStreamChunk{Payload: body})
	}
	return chunks
}
func okEnvelope(value any) ([]byte, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return json.Marshal(envelope{OK: true, Result: raw})
}
func errorEnvelope(code, message string, status int) []byte {
	raw, _ := json.Marshal(envelope{OK: false, Error: &envelopeError{Code: code, Message: message, HTTPStatus: status}})
	return raw
}
func writeResponse(response *C.cliproxy_buffer, raw []byte) {
	if response == nil || len(raw) == 0 {
		return
	}
	ptr := C.CBytes(raw)
	if ptr == nil {
		return
	}
	response.ptr = ptr
	response.len = C.size_t(len(raw))
}
func regionOf(auth workbuddyAuth) string {
	if profiles[auth.Region].baseURL != "" {
		return auth.Region
	}
	return "cn"
}
func authUserAgent(auth workbuddyAuth, profile regionProfile) string {
	if auth.UserAgent != "" {
		return auth.UserAgent
	}
	return profile.userAgent
}
func urlQuery(value string) string {
	return strings.NewReplacer("%", "%25", " ", "%20", "?", "%3F", "&", "%26", "=", "%3D").Replace(value)
}
func shortID(value string) string {
	if len(value) <= 12 {
		return value
	}
	return value[:12]
}
func hasTag(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
func statusOf(err error) int {
	if err == nil {
		return 0
	}
	return http.StatusInternalServerError
}
func contextWithTimeout(d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), d)
}
func reqAuthUpdate(req pluginapi.AuthModelRequest, auth workbuddyAuth) *pluginapi.AuthData {
	data := authData(auth, req.AuthID+".json")
	return &data
}
