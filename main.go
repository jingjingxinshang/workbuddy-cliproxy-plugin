package main

/*
#include <stdint.h>
#include <stdlib.h>

typedef struct { void* ptr; size_t len; } cliproxy_buffer;
typedef void (*cliproxy_host_free_fn)(void*, size_t);
typedef int (*cliproxy_host_call_fn)(void*, const char*, const uint8_t*, size_t, cliproxy_buffer*);
typedef struct {
	uint32_t abi_version;
	void* host_ctx;
	cliproxy_host_call_fn call;
	cliproxy_host_free_fn free_buffer;
} cliproxy_host_api;
typedef int (*cliproxy_plugin_call_fn)(char*, uint8_t*, size_t, cliproxy_buffer*);
typedef void (*cliproxy_plugin_free_fn)(void*, size_t);
typedef void (*cliproxy_plugin_shutdown_fn)(void);
typedef struct { uint32_t abi_version; cliproxy_plugin_call_fn call; cliproxy_plugin_free_fn free_buffer; cliproxy_plugin_shutdown_fn shutdown; } cliproxy_plugin_api;
extern int cliproxyPluginCall(char*, uint8_t*, size_t, cliproxy_buffer*);
extern void cliproxyPluginFree(void*, size_t);
extern void cliproxyPluginShutdown(void);

static const cliproxy_host_api* stored_host;

static void store_host_api(const cliproxy_host_api* host) { stored_host = host; }

static int call_host_api(const char* method, const uint8_t* request, size_t request_len, cliproxy_buffer* response) {
	if (stored_host == NULL || stored_host->call == NULL) {
		return 1;
	}
	return stored_host->call(stored_host->host_ctx, method, request, request_len, response);
}

static void free_host_buffer(void* ptr, size_t len) {
	if (stored_host != NULL && stored_host->free_buffer != NULL && ptr != NULL) {
		stored_host->free_buffer(ptr, len);
	}
}
*/
import "C"

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
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
	pluginVer   = "0.2.1"
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
	C.store_host_api(host)
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
		return okEnvelope(managementRegistration{
			Resources: []pluginapi.ResourceRoute{
				{
					Path:        "/",
					Menu:        "WorkBuddy 登录",
					Description: "WorkBuddy 登录页面：选择区域、发起登录、查看状态",
				},
				{
					Path:        "/quota",
					Menu:        "WorkBuddy 额度",
					Description: "WorkBuddy 账号额度：剩余额度、套餐与重置时间",
				},
			},
			Routes: []pluginapi.ManagementRoute{
				{
					Method:      "GET",
					Path:        "/workbuddy/quota",
					Description: "读取指定凭据的 WorkBuddy 额度",
				},
			},
		})
	case pluginabi.MethodManagementHandle:
		return okEnvelope(handleManagement(raw))
	case pluginabi.MethodQuotaDescribe:
		return okEnvelope(pluginapi.QuotaDescribeResponse{SupportedProviders: []string{providerID}, DisplayName: pluginName})
	case pluginabi.MethodQuotaFetch:
		return quotaEnvelope(raw)
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
	return pluginapi.AuthParseResponse{Handled: true, Auth: authData(auth, authFileSource(req))}
}

// authData builds the credential record CPA stores.
//
// ID deliberately mirrors the file name. CPA names plugin-backed runtime
// credentials by ID, and its management endpoints address them by that name —
// `/auth-files/download` reads <auth-dir>/<name> verbatim. Returning the
// WorkBuddy uid here made the panel display (and try to download) a file that
// never existed, because the credential on disk is named after FileName.
func authData(auth workbuddyAuth, fileName string) pluginapi.AuthData {
	if fileName == "" {
		fileName = providerID + ".json"
	}
	body, _ := json.Marshal(auth)
	id := strings.TrimSuffix(fileName, ".json")
	if id == "" {
		id = providerID
	}
	label := auth.Nickname
	if label == "" {
		label = "WorkBuddy"
	}
	metadata := map[string]any{"region": regionOf(auth)}
	if auth.UID != "" {
		metadata["uid"] = auth.UID
	}
	if auth.Nickname != "" {
		metadata["nickname"] = auth.Nickname
	}
	if auth.EnterpriseID != "" {
		metadata["enterprise_id"] = auth.EnterpriseID
	}
	return pluginapi.AuthData{Provider: providerID, ID: id, FileName: fileName, Label: label, StorageJSON: body, Metadata: metadata}
}

// credentialFileName is the stable on-disk name for one WorkBuddy account. The
// uid is preferred over the token because access tokens rotate on refresh while
// the credential file must keep its name.
func credentialFileName(auth workbuddyAuth) string {
	suffix := firstNonEmpty(auth.UID, shortID(auth.AccessToken))
	if suffix == "" {
		suffix = "default"
	}
	return providerID + "-" + suffix + ".json"
}

// authFileSource resolves the file name of an existing credential from the
// parse request, so a re-parsed credential keeps pointing at its own file.
func authFileSource(req pluginapi.AuthParseRequest) string {
	source := firstNonEmpty(req.FileName, req.Path)
	if source == "" {
		return providerID + ".json"
	}
	base := filepath.Base(source)
	if !strings.HasSuffix(strings.ToLower(base), ".json") {
		base += ".json"
	}
	return base
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
	return pluginapi.AuthLoginPollResponse{Status: pluginapi.AuthLoginStatusSuccess, Message: "WorkBuddy login complete", Auth: authData(auth, credentialFileName(auth))}
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

// truncate keeps error messages short; it is only used for upstream text that
// is already free of credentials.
func truncate(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit] + "…"
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

// handleManagement answers the plugin's own Management API resource requests.
//
// The host serves registered resources under /v0/resource/plugins/workbuddy/
// and forwards the matching Management API routes to management.handle. The
// page below is deliberately dependency-free: it runs in the browser served by
// CPA itself, so it can call the host's own /v0/management endpoints
// (<provider>-auth-url and get-auth-status) with a management key the operator
// types in. That keeps the login flow on the host's supported path, so the
// saved credential ends up in CPA's auth store instead of inside the plugin.
// billingUserAgent is required by the WorkBuddy billing endpoint: it answers
// 401 to the CLI user agent that chat requests use, so the two must stay apart.
const billingUserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/153.0.0.0 Safari/537.36"

type billingEnvelope struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
	Data struct {
		Response struct {
			Data struct {
				Accounts []struct {
					AccountID           int     `json:"AccountId"`
					PackageName         string  `json:"PackageName"`
					PackageCode         string  `json:"PackageCode"`
					CapacityRemain      float64 `json:"CapacityRemain"`
					CapacitySize        float64 `json:"CapacitySize"`
					CycleCapacityRemain float64 `json:"CycleCapacityRemain"`
					CycleCapacitySize   float64 `json:"CycleCapacitySize"`
					CycleEndTime        string  `json:"CycleEndTime"`
					Status              int     `json:"Status"`
				} `json:"Accounts"`
			} `json:"Data"`
		} `json:"Response"`
	} `json:"Data"`
}

// fetchQuota reports WorkBuddy quota to CPA's management UI.
//
// Empty response on any failure is deliberate: the management panel renders a
// blank quota instead of a broken page when the upstream is unreachable or the
// credential expired.
// hostCall invokes a host callback over the C ABI and returns the RPC result
// payload. The host answers with an envelope ({ok, result} / {ok, error}).
func hostCall(method string, payload any) (json.RawMessage, error) {
	if hostAPI == nil {
		return nil, errors.New("host api unavailable")
	}
	var body []byte
	if payload != nil {
		encoded, errMarshal := json.Marshal(payload)
		if errMarshal != nil {
			return nil, errMarshal
		}
		body = encoded
	}
	cMethod := C.CString(method)
	defer C.free(unsafe.Pointer(cMethod))
	var request *C.uint8_t
	if len(body) > 0 {
		request = (*C.uint8_t)(C.CBytes(body))
		defer C.free(unsafe.Pointer(request))
	}
	var response C.cliproxy_buffer
	if C.call_host_api(cMethod, request, C.size_t(len(body)), &response) != 0 {
		return nil, fmt.Errorf("host call %s failed", method)
	}
	if response.ptr == nil || response.len == 0 {
		return nil, fmt.Errorf("host call %s returned no payload", method)
	}
	raw := C.GoBytes(response.ptr, C.int(response.len))
	C.free_host_buffer(response.ptr, response.len)
	var env struct {
		OK     bool            `json:"ok"`
		Result json.RawMessage `json:"result,omitempty"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error,omitempty"`
	}
	if json.Unmarshal(raw, &env) != nil {
		return nil, fmt.Errorf("host call %s returned invalid envelope", method)
	}
	if !env.OK {
		message := "unknown host error"
		if env.Error != nil && env.Error.Message != "" {
			message = env.Error.Message
		}
		return nil, fmt.Errorf("host call %s failed: %s", method, message)
	}
	return env.Result, nil
}

// credentialJSONForQuota resolves the stored credential for a quota request.
//
// Quota fetches do NOT carry StorageJSON (only model discovery does), so the
// credential has to be read back from the host through the auth callback. The
// host returns the merged credential payload, which is the same JSON this
// plugin originally returned from auth.login.poll / auth.parse.
func credentialJSONForQuota(req pluginapi.QuotaFetchRequest) []byte {
	if len(req.StorageJSON) > 0 {
		return req.StorageJSON
	}
	authIndex := strings.TrimSpace(req.AuthIndex)
	if authIndex == "" {
		return nil
	}
	result, errCall := hostCall(pluginabi.MethodHostAuthGet, map[string]string{"auth_index": authIndex})
	if errCall != nil {
		return nil
	}
	var payload struct {
		JSON json.RawMessage `json:"json"`
	}
	if json.Unmarshal(result, &payload) != nil {
		return nil
	}
	return payload.JSON
}

// quotaEnvelope answers a quota fetch.
//
// Failures are reported as an error envelope instead of an empty result: the
// management panel surfaces the message, which is the only way to tell a
// missing credential apart from an upstream rejection or a network failure.
// Messages never contain tokens — only status codes and upstream error codes.
func quotaEnvelope(raw []byte) ([]byte, error) {
	resp, errFetch := fetchQuota(raw)
	if errFetch == nil {
		return okEnvelope(resp)
	}
	envelope, errMarshal := pluginabi.NewErrorEnvelope("quota_unavailable", errFetch.Error(), http.StatusBadGateway)
	if errMarshal != nil {
		return errorEnvelope("quota_unavailable", errFetch.Error(), http.StatusBadGateway), nil
	}
	return envelope, nil
}

func fetchQuota(raw []byte) (pluginapi.QuotaFetchResponse, error) {
	var req pluginapi.QuotaFetchRequest
	if errUnmarshal := json.Unmarshal(raw, &req); errUnmarshal != nil {
		return pluginapi.QuotaFetchResponse{}, fmt.Errorf("invalid quota request: %w", errUnmarshal)
	}
	credential := credentialJSONForQuota(req)
	if len(credential) == 0 {
		return pluginapi.QuotaFetchResponse{}, fmt.Errorf("credential unavailable (auth_index=%q): host did not return a stored credential", req.AuthIndex)
	}
	var auth workbuddyAuth
	if errUnmarshal := json.Unmarshal(credential, &auth); errUnmarshal != nil {
		return pluginapi.QuotaFetchResponse{}, fmt.Errorf("stored credential is not readable: %w", errUnmarshal)
	}
	if auth.AccessToken == "" {
		return pluginapi.QuotaFetchResponse{}, fmt.Errorf("stored credential has no access_token")
	}
	profile := profiles[regionOf(auth)]
	payload, errMarshal := json.Marshal(map[string]any{
		"PageNumber":      1,
		"PageSize":        200,
		"ProductCode":     "p_tcaca",
		"Status":          []int{0, 3},
		"OnlyValidPeriod": true,
	})
	if errMarshal != nil {
		return pluginapi.QuotaFetchResponse{}, fmt.Errorf("encode billing request: %w", errMarshal)
	}
	status, body, errCall := upstream("POST", profile.baseURL+"/billing/meter/get-user-resource", profile.origin, billingUserAgent, billingHeaders(auth), payload, false)
	if errCall != nil {
		return pluginapi.QuotaFetchResponse{}, fmt.Errorf("billing request failed: %w", errCall)
	}
	if status >= 400 {
		return pluginapi.QuotaFetchResponse{}, fmt.Errorf("billing endpoint returned HTTP %d: %s", status, truncate(string(body), 200))
	}
	var env billingEnvelope
	if errUnmarshal := json.Unmarshal(body, &env); errUnmarshal != nil {
		return pluginapi.QuotaFetchResponse{}, fmt.Errorf("billing response is not JSON: %s", truncate(string(body), 200))
	}
	if env.Code != 0 {
		return pluginapi.QuotaFetchResponse{}, fmt.Errorf("billing API error code=%d: %s", env.Code, truncate(env.Msg, 200))
	}
	accounts := env.Data.Response.Data.Accounts
	if len(accounts) == 0 {
		return pluginapi.QuotaFetchResponse{}, fmt.Errorf("billing response contains no accounts")
	}
	resp := pluginapi.QuotaFetchResponse{}
	var totalRemain, totalSize float64
	for _, account := range accounts {
		remain := account.CycleCapacityRemain
		if remain == 0 {
			remain = account.CapacityRemain
		}
		size := account.CycleCapacitySize
		if size == 0 {
			size = account.CapacitySize
		}
		totalRemain += remain
		totalSize += size
		name := firstNonEmpty(account.PackageName, account.PackageCode, "WorkBuddy")
		fraction := 0.0
		if size > 0 {
			fraction = remain / size
		}
		description := fmt.Sprintf("%.0f / %.0f", remain, size)
		resp.Groups = append(resp.Groups, pluginapi.QuotaGroup{
			DisplayName: name,
			Buckets: []pluginapi.QuotaBucket{{
				Window:            account.CycleEndTime,
				RemainingFraction: fraction,
				ResetTime:         account.CycleEndTime,
				Description:       description,
			}},
		})
	}
	resp.Summary = []pluginapi.QuotaMetric{
		{Key: "remain", Label: "剩余额度", Value: totalRemain, Format: "number", Unit: "credits"},
		{Key: "total", Label: "总额度", Value: totalSize, Format: "number", Unit: "credits"},
	}
	if totalSize > 0 {
		resp.Summary = append(resp.Summary, pluginapi.QuotaMetric{
			Key: "used_percent", Label: "已用比例", Value: (1 - totalRemain/totalSize) * 100, Format: "number", Unit: "%",
		})
	}
	plan := firstNonEmpty(accounts[0].PackageName, accounts[0].PackageCode)
	resp.Subscription = &pluginapi.QuotaSubscription{Plan: plan, TierName: plan}
	return resp, nil
}

func billingHeaders(auth workbuddyAuth) map[string]string {
	headers := map[string]string{
		"Authorization":     "Bearer " + auth.AccessToken,
		"X-Client-Platform": "web",
		"X-Product":         "SaaS",
	}
	if auth.UID != "" {
		headers["X-User-Id"] = auth.UID
	}
	if auth.EnterpriseID != "" {
		headers["X-Enterprise-Id"] = auth.EnterpriseID
	}
	if auth.RefreshToken != "" {
		headers["X-Refresh-Token"] = auth.RefreshToken
	}
	return headers
}

func handleManagement(raw []byte) pluginapi.ManagementResponse {
	path := strings.TrimRight(managementRequestPath(raw), "/")

	// Plugin-owned route: quota for one credential.
	//
	// The management panel renders quota only for its six built-in providers
	// (QuotaProviderType is a closed union), so a plugin provider has no place
	// there. This route exists for the plugin's own resource page, which is the
	// supported way for a plugin to draw its own data.
	if strings.HasSuffix(path, "/workbuddy/quota") {
		return quotaRouteResponse(raw)
	}

	if strings.HasSuffix(path, "/status") {
		body, errMarshal := json.Marshal(map[string]any{
			"provider": providerID,
			"name":     pluginName,
			"version":  pluginVer,
			"regions":  []string{"cn", "intl"},
			"login": map[string]string{
				"start":  "/v0/management/" + providerID + "-auth-url",
				"status": "/v0/management/get-auth-status",
			},
			"quota": "/v0/management/workbuddy/quota",
		})
		if errMarshal != nil {
			body = []byte(`{"provider":"workbuddy"}`)
		}
		return jsonManagementResponse(body)
	}

	if strings.HasSuffix(path, "/quota") {
		return htmlManagementResponse(quotaPageHTML())
	}
	return htmlManagementResponse(loginPageHTML())
}

func jsonManagementResponse(body []byte) pluginapi.ManagementResponse {
	return pluginapi.ManagementResponse{
		StatusCode: http.StatusOK,
		Headers:    http.Header{"Content-Type": []string{"application/json; charset=utf-8"}},
		Body:       body,
	}
}

func errorManagementResponse(status int, message string) pluginapi.ManagementResponse {
	body, errMarshal := json.Marshal(map[string]string{"error": message})
	if errMarshal != nil {
		body = []byte(`{"error":"unknown error"}`)
	}
	return pluginapi.ManagementResponse{
		StatusCode: status,
		Headers:    http.Header{"Content-Type": []string{"application/json; charset=utf-8"}},
		Body:       body,
	}
}

func htmlManagementResponse(body string) pluginapi.ManagementResponse {
	return pluginapi.ManagementResponse{
		StatusCode: http.StatusOK,
		Headers:    http.Header{"Content-Type": []string{"text/html; charset=utf-8"}},
		Body:       []byte(body),
	}
}

// hostAuthFileEntry is the subset of a host credential record this plugin needs.
type hostAuthFileEntry struct {
	AuthIndex string `json:"auth_index"`
	Name      string `json:"name"`
	Provider  string `json:"provider"`
	Label     string `json:"label"`
}

// resolveAuthIndex picks the credential to query.
//
// The auth_index is optional on purpose: the resource page may run before any
// credential field is known, and requiring a query parameter only made the page
// fail with "auth_index is required" instead of showing quota. When it is
// missing, the plugin asks the host for its credential list and uses the first
// WorkBuddy entry.
func resolveAuthIndex(raw []byte) (string, error) {
	explicit := managementQueryValue(raw, "auth_index")
	if explicit == "" {
		explicit = managementQueryValue(raw, "authIndex")
	}
	if explicit != "" {
		return explicit, nil
	}
	result, errCall := hostCall(pluginabi.MethodHostAuthList, map[string]any{})
	if errCall != nil {
		return "", fmt.Errorf("cannot list credentials: %w", errCall)
	}
	var payload struct {
		Files []hostAuthFileEntry `json:"files"`
	}
	if errUnmarshal := json.Unmarshal(result, &payload); errUnmarshal != nil {
		return "", fmt.Errorf("credential list is not readable: %w", errUnmarshal)
	}
	wanted := strings.ToLower(strings.TrimSpace(managementQueryValue(raw, "name")))
	for _, file := range payload.Files {
		if !strings.EqualFold(strings.TrimSpace(file.Provider), providerID) {
			continue
		}
		if wanted != "" && strings.ToLower(strings.TrimSpace(file.Name)) != wanted {
			continue
		}
		if strings.TrimSpace(file.AuthIndex) != "" {
			return strings.TrimSpace(file.AuthIndex), nil
		}
	}
	return "", fmt.Errorf("no WorkBuddy credential found (provider=%s)", providerID)
}

// quotaRouteResponse answers GET /v0/management/workbuddy/quota[?auth_index=...]
func quotaRouteResponse(raw []byte) pluginapi.ManagementResponse {
	authIndex, errResolve := resolveAuthIndex(raw)
	if errResolve != nil {
		return errorManagementResponse(http.StatusBadGateway, errResolve.Error())
	}
	request, errMarshal := json.Marshal(pluginapi.QuotaFetchRequest{AuthIndex: authIndex, Provider: providerID})
	if errMarshal != nil {
		return pluginapi.ManagementResponse{
			StatusCode: http.StatusInternalServerError,
			Headers:    http.Header{"Content-Type": []string{"application/json; charset=utf-8"}},
			Body:       []byte(`{"error":"failed to encode quota request"}`),
		}
	}
	quota, errFetch := fetchQuota(request)
	if errFetch != nil {
		body, _ := json.Marshal(map[string]any{"error": errFetch.Error()})
		return pluginapi.ManagementResponse{
			StatusCode: http.StatusBadGateway,
			Headers:    http.Header{"Content-Type": []string{"application/json; charset=utf-8"}},
			Body:       body,
		}
	}
	body, errMarshal := json.Marshal(quota)
	if errMarshal != nil {
		return pluginapi.ManagementResponse{
			StatusCode: http.StatusInternalServerError,
			Headers:    http.Header{"Content-Type": []string{"application/json; charset=utf-8"}},
			Body:       []byte(`{"error":"failed to encode quota response"}`),
		}
	}
	return jsonManagementResponse(body)
}

// managementQueryValue reads one query parameter case-insensitively. Values may
// arrive as a string or an array of strings depending on how the host encoded
// them.
func managementQueryValue(raw []byte, key string) string {
	var payload map[string]any
	if json.Unmarshal(raw, &payload) != nil {
		return ""
	}
	var query any
	for name, value := range payload {
		if strings.EqualFold(name, "query") {
			query = value
			break
		}
	}
	values, okValues := query.(map[string]any)
	if !okValues {
		return ""
	}
	for name, value := range values {
		if !strings.EqualFold(name, key) {
			continue
		}
		switch typed := value.(type) {
		case string:
			return strings.TrimSpace(typed)
		case []any:
			if len(typed) == 0 {
				return ""
			}
			if text, okText := typed[0].(string); okText {
				return strings.TrimSpace(text)
			}
		}
	}
	return ""
}

// managementRequestPath reads the request path case-insensitively, because the
// host serializes its own request struct and the JSON key casing is not part of
// the plugin contract.
func managementRequestPath(raw []byte) string {
	if len(raw) == 0 {
		return ""
	}
	var payload map[string]any
	if json.Unmarshal(raw, &payload) != nil {
		return ""
	}
	for key, value := range payload {
		if !strings.EqualFold(key, "path") {
			continue
		}
		if text, ok := value.(string); ok {
			return text
		}
	}
	return ""
}

func loginPageHTML() string {
	return `<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>WorkBuddy 登录</title>
<style>
 body{font:14px/1.6 -apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif;margin:0;padding:28px 22px;background:#111;color:#ddd}
 h1{font-size:19px;margin:0 0 4px}
 .sub{color:#999;margin-bottom:20px;font-size:13px}
 label{display:block;margin:14px 0 6px;color:#bbb;font-size:13px}
 select,input{width:100%;box-sizing:border-box;padding:9px 10px;border-radius:6px;border:1px solid #333;background:#1a1a1a;color:#eee;font-size:14px}
 button{margin-top:16px;padding:10px 16px;border-radius:6px;border:0;background:#2f6feb;color:#fff;font-size:14px;cursor:pointer}
 button:disabled{background:#333;color:#888;cursor:not-allowed}
 .row{display:flex;gap:10px}
 .row>div{flex:1}
 .box{margin-top:18px;padding:12px 14px;border-radius:8px;background:#1a1a1a;border-left:3px solid #2f6feb;word-break:break-all}
 .err{border-left-color:#e78a8a;color:#f0b0b0}
 .ok{border-left-color:#6ee7a8;color:#a8f0c8}
 a{color:#7cc4ff}
 code{background:#222;padding:1px 5px;border-radius:4px}
 ol{padding-left:20px;color:#bbb}
</style>
</head>
<body>
<h1>WorkBuddy</h1>
<div class="sub">CLIProxyAPI 插件 &middot; provider <code>workbuddy</code> &middot; v` + pluginVer + `</div>

<label for="region">集群区域</label>
<select id="region">
  <option value="cn">中国大陆 (cn)</option>
  <option value="intl">国际 (intl)</option>
</select>

<label for="key">管理密钥（仅保存在本机浏览器）</label>
<input id="key" type="password" placeholder="remote-management.secret-key" autocomplete="off">

<button id="start">开始登录</button>

<div id="out" class="box" style="display:none"></div>

<div class="box" style="border-left-color:#444">
<ol>
  <li>填写管理密钥，选择区域，点击「开始登录」。</li>
  <li>在浏览器打开返回的地址，用 WorkBuddy 客户端扫码或登录。</li>
  <li>本页会自动轮询，成功后凭据写入 CPA 的 <code>auths/</code> 目录。</li>
  <li>回到 CPA 的模型列表即可看到 WorkBuddy 模型。</li>
</ol>
</div>

<script>
var out = document.getElementById('out');
var keyInput = document.getElementById('key');
var regionSelect = document.getElementById('region');
var startButton = document.getElementById('start');
var savedKey = '';

try { savedKey = localStorage.getItem('wbaw_mgmt_key') || ''; } catch (e) { savedKey = ''; }
if (savedKey) { keyInput.value = savedKey; }

function show(text, kind) {
  out.style.display = 'block';
  out.className = 'box' + (kind ? ' ' + kind : '');
  out.innerHTML = text;
}

function authHeaders(key) {
  return { 'Authorization': 'Bearer ' + key };
}

startButton.onclick = function () {
  var key = keyInput.value.trim();
  if (!key) { show('请先填写管理密钥。', 'err'); return; }
  try { localStorage.setItem('wbaw_mgmt_key', key); } catch (e) {}
  var region = regionSelect.value;
  startButton.disabled = true;
  show('正在向 WorkBuddy 申请登录地址…');

  fetch('/v0/management/workbuddy-auth-url?region=' + encodeURIComponent(region), { headers: authHeaders(key) })
    .then(function (resp) { return resp.json().then(function (body) { return { status: resp.status, body: body }; }); })
    .then(function (result) {
      if (result.status !== 200) {
        show('请求失败：' + JSON.stringify(result.body), 'err');
        startButton.disabled = false;
        return;
      }
      var url = result.body.url || '';
      var state = result.body.state || '';
      if (!url || !state) {
        show('响应缺少 url 或 state：' + JSON.stringify(result.body), 'err');
        startButton.disabled = false;
        return;
      }
      show('请在浏览器打开以下地址完成登录：<br><a href="' + url + '" target="_blank" rel="noreferrer">' + url + '</a><br><br>状态：等待扫码…');
      poll(key, state);
    })
    .catch(function (err) { show('请求异常：' + err, 'err'); startButton.disabled = false; });
};

function poll(key, state) {
  var deadline = Date.now() + 300000;
  var timer = setInterval(function () {
    if (Date.now() > deadline) {
      clearInterval(timer);
      startButton.disabled = false;
      show('登录超时，请重新开始。', 'err');
      return;
    }
    fetch('/v0/management/get-auth-status?state=' + encodeURIComponent(state), { headers: authHeaders(key) })
      .then(function (resp) { return resp.json(); })
      .then(function (body) {
        if (body.status === 'ok') {
          clearInterval(timer);
          startButton.disabled = false;
          show('登录成功，凭据已保存。现在可以在 CPA 模型列表中看到 WorkBuddy 模型。', 'ok');
          return;
        }
        if (body.status === 'error') {
          clearInterval(timer);
          startButton.disabled = false;
          show('登录失败：' + (body.error || '未知错误'), 'err');
          return;
        }
        out.innerHTML = out.innerHTML.replace(/状态：[^<]*/, '状态：等待扫码…');
      })
      .catch(function (err) { show('轮询异常：' + err, 'err'); });
  }, 2000);
}
</script>
</body>
</html>`
}
