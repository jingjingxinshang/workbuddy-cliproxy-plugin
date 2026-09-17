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
	"gopkg.in/yaml.v3"
)

const (
	providerID  = "workbuddy"
	pluginName  = "WorkBuddy"
	pluginVer   = "0.3.0"
	loginTTL    = 5 * time.Minute
	pollTimeout = 20 * time.Second

	// defaultRegion is the cluster used when neither the request metadata nor
	// the plugin configuration names a usable one.
	defaultRegion = "cn"
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
	// LastCheckinDay is the local date of the last successful daily-bonus
	// claim, so the token-refresh hook claims at most once a day.
	LastCheckinDay string `json:"last_checkin_day,omitempty"`
}

type loginState struct {
	State     string    `json:"state"`
	Region    string    `json:"region"`
	ExpiresAt time.Time `json:"expires_at"`
}

// pluginConfig is the slice of plugins.configs.<id> this plugin reads.
//
// The panel edits that object through the plugin management API and the host
// hands the whole section back as YAML on every register/reconfigure call, so
// there is no separate place an operator has to configure WorkBuddy login.
type pluginConfig struct {
	DefaultRegion string `yaml:"default_region"`
}

// lifecycleRequest is the plugin.register / plugin.reconfigure payload. The
// host serializes the config bytes as base64 inside JSON, which unmarshalling
// into []byte reverses.
type lifecycleRequest struct {
	ConfigYAML    []byte `json:"config_yaml"`
	SchemaVersion uint32 `json:"schema_version"`
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

	configMu  sync.RWMutex
	pluginCfg = pluginConfig{DefaultRegion: defaultRegion}
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
		if errConfigure := configure(raw); errConfigure != nil {
			return errorEnvelope("invalid_config", errConfigure.Error(), http.StatusBadRequest), nil
		}
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
			// One resource page: the accounts view. Login is not a plugin
			// page — the panel's OAuth page renders a card for every plugin
			// that advertises an auth provider and drives the host's generic
			// /v0/management/<provider>-auth-url flow, which needs no plugin
			// side configuration or management key entry.
			Resources: []pluginapi.ResourceRoute{
				{
					Path:        "/quota",
					Menu:        "WorkBuddy 账号",
					Description: "WorkBuddy 账号：额度、套餐、签到与重置时间",
				},
			},
			Routes: []pluginapi.ManagementRoute{
				{
					Method:      "GET",
					Path:        "/workbuddy/accounts",
					Description: "列出所有 WorkBuddy 账号及其身份、凭据状态与签到状态",
				},
				{
					Method:      "GET",
					Path:        "/workbuddy/quota",
					Description: "读取指定凭据的 WorkBuddy 额度",
				},
				{
					Method:      "POST",
					Path:        "/workbuddy/checkin",
					Description: "为指定账号或全部账号执行每日签到",
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
	return registration{SchemaVersion: pluginabi.SchemaVersion, Metadata: pluginapi.Metadata{Name: pluginName, Version: pluginVer, Author: "WorkBuddy CPA Plugin", GitHubRepository: "https://github.com/jingjingxinshang/workbuddy-cliproxy-plugin", ConfigFields: []pluginapi.ConfigField{{Name: "default_region", Type: pluginapi.ConfigFieldTypeEnum, EnumValues: []string{"cn", "intl"}, Description: "WorkBuddy cluster new logins default to. The panel's OAuth card starts a login without parameters, so this decides between the CN and INTL clusters."}}}, Capabilities: registrationCapability{ModelProvider: true, AuthProvider: true, Executor: true, ExecutorModelScope: pluginapi.ExecutorModelScopeOAuth, ExecutorInputFormats: []string{"chat-completions"}, ExecutorOutputFormats: []string{"chat-completions"}, CommandLinePlugin: true, ManagementAPI: true, QuotaProvider: true}}
}

// configure reads the configuration the host delivers on register and
// reconfigure.
//
// Nothing about WorkBuddy login is configured in the plugin's own UI: the panel
// starts the flow through the host's generic plugin OAuth route, and the host
// passes this plugin's plugins.configs.workbuddy section here as YAML. Only the
// default region is read from it, because the panel's OAuth card sends no
// parameters and therefore cannot pick a cluster.
func configure(raw []byte) error {
	cfg := pluginConfig{DefaultRegion: defaultRegion}
	if len(raw) > 0 {
		var req lifecycleRequest
		if errUnmarshal := json.Unmarshal(raw, &req); errUnmarshal != nil {
			return errUnmarshal
		}
		if len(req.ConfigYAML) > 0 {
			if errUnmarshal := yaml.Unmarshal(req.ConfigYAML, &cfg); errUnmarshal != nil {
				return errUnmarshal
			}
		}
	}
	cfg.DefaultRegion = normalizeRegion(cfg.DefaultRegion)
	configMu.Lock()
	pluginCfg = cfg
	configMu.Unlock()
	return nil
}

// normalizeRegion keeps only regions this plugin has a profile for, so an
// unusable setting degrades to the default instead of breaking login.
func normalizeRegion(region string) string {
	region = strings.ToLower(strings.TrimSpace(region))
	if profiles[region].baseURL != "" {
		return region
	}
	return defaultRegion
}

// configuredRegion is the region a login uses when the request names none.
func configuredRegion() string {
	configMu.RLock()
	region := pluginCfg.DefaultRegion
	configMu.RUnlock()
	return normalizeRegion(region)
}

// loginRegion resolves the cluster for one login. An explicit region wins over
// the configured default, because the host maps every query parameter of
// /v0/management/workbuddy-auth-url into the login metadata — that keeps
// `?region=intl` working for an operator who usually signs in to the other
// cluster.
func loginRegion(metadata map[string]any) string {
	if value, okValue := metadata["region"].(string); okValue && profiles[value].baseURL != "" {
		return value
	}
	return configuredRegion()
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
	region := loginRegion(req.Metadata)
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
	// A refresh is the only moment the plugin runs without someone opening its
	// page, so it is also when the daily bonus gets claimed. Best effort only.
	maybeCheckin(&auth)
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
	return upstreamWithin(pollTimeout, method, target, origin, userAgent, extra, body, stream)
}

// upstreamWithin is upstream with an explicit budget, so a caller on a latency
// sensitive path (the check-in refresh hook) can keep its worst case short.
func upstreamWithin(budget time.Duration, method, target, origin, userAgent string, extra map[string]string, body []byte, stream bool) (int, []byte, error) {
	ctx, cancel := contextWithTimeout(budget)
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
	return normalizeRegion(auth.Region)
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
	return credentialJSONForAuthIndex(req.AuthIndex)
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

// resourcePluginsPrefix is where the host mounts browser-navigable plugin
// resources. It is a fixed host constant, so the plugin can recognize its own
// pages without knowing the id the host derived from the library file name.
const resourcePluginsPrefix = "/v0/resource/plugins/"

// resourcePagePath reports the page path inside a plugin resource route.
//
// The plugin id segment is skipped rather than compared: the host derives it
// from the library file name, so it is not necessarily the provider id.
func resourcePagePath(path string) (string, bool) {
	index := strings.Index(path, resourcePluginsPrefix)
	if index < 0 {
		return "", false
	}
	rest := path[index+len(resourcePluginsPrefix):]
	if slash := strings.Index(rest, "/"); slash >= 0 {
		rest = rest[slash:]
	} else {
		rest = "/"
	}
	page := strings.TrimRight(rest, "/")
	if page == "" {
		page = "/"
	}
	return page, true
}

func handleManagement(raw []byte) pluginapi.ManagementResponse {
	path := strings.TrimRight(managementRequestPath(raw), "/")

	// Resource pages come first.
	//
	// The host forwards both path spaces to this one handler with the full
	// request path:
	//
	//	/v0/resource/plugins/workbuddy/quota   the page, which the panel loads in an iframe
	//	/v0/management/workbuddy/quota         the route that page's script calls
	//
	// They overlap by suffix — the resource path also ends in "/workbuddy/quota"
	// — so matching the route first answered the iframe navigation with the
	// quota JSON, and the menu showed the raw payload instead of the page.
	if page, okPage := resourcePagePath(path); okPage {
		if page == "/quota" {
			return htmlManagementResponse(accountsPageHTML())
		}
		// The plugin's own login page used to live at "/". It was removed
		// because the panel OAuth page already starts the same host flow with
		// its own credentials, so answering 404 keeps a stale bookmark
		// diagnosable instead of serving a page that no longer exists.
		return errorManagementResponse(http.StatusNotFound, "unknown plugin resource page")
	}

	// Plugin-owned routes. The host dispatches these by exact method and path,
	// so every branch here is also declared in the management registration.
	if strings.HasSuffix(path, "/"+providerID+"/accounts") {
		if method := managementRequestMethod(raw); method != "" && method != http.MethodGet {
			return errorManagementResponse(http.StatusMethodNotAllowed, "accounts is a GET route")
		}
		return accountsView(raw)
	}

	if strings.HasSuffix(path, "/"+providerID+"/checkin") {
		if method := managementRequestMethod(raw); method != "" && method != http.MethodPost {
			return errorManagementResponse(http.StatusMethodNotAllowed, "checkin is a POST route")
		}
		return checkinRouteResponse(raw)
	}

	// Quota for one credential.
	//
	// The management panel renders quota only for its six built-in providers
	// (QuotaProviderType is a closed union), so a plugin provider has no place
	// there. The accounts page is the supported way for a plugin to draw its own
	// data.
	if strings.HasSuffix(path, "/"+providerID+"/quota") {
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

	// Resource pages are answered above, so anything left is an unregistered
	// Management API path. Answering JSON keeps a typo'd route debuggable
	// instead of silently returning a page.
	return errorManagementResponse(http.StatusNotFound, "unknown plugin management path")
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
	explicit := strings.TrimSpace(firstNonEmpty(managementQueryValue(raw, "auth_index"), managementQueryValue(raw, "authIndex")))
	if explicit != "" {
		return explicit, nil
	}
	entries, errList := workbuddyAccounts()
	if errList != nil {
		return "", errList
	}
	wanted := strings.ToLower(strings.TrimSpace(managementQueryValue(raw, "name")))
	for _, file := range entries {
		if wanted != "" && strings.ToLower(strings.TrimSpace(file.Name)) != wanted {
			continue
		}
		return strings.TrimSpace(file.AuthIndex), nil
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

// managementRequestMethod reads the HTTP method case-insensitively for the same
// reason managementRequestPath does. An empty answer means the host did not send
// one, which callers treat as "no restriction".
func managementRequestMethod(raw []byte) string {
	if len(raw) == 0 {
		return ""
	}
	var payload map[string]any
	if json.Unmarshal(raw, &payload) != nil {
		return ""
	}
	for key, value := range payload {
		if !strings.EqualFold(key, "method") {
			continue
		}
		if text, ok := value.(string); ok {
			return strings.ToUpper(strings.TrimSpace(text))
		}
	}
	return ""
}
