# WorkBuddy CPA Plugin

独立的 CLIProxyAPI 动态库插件，将 WorkBuddy 登录、模型发现和 Chat Completions 接入 CPA Plugin ABI。

## 当前能力

- CPA Plugin ABI v1 / schema v6 注册
- WorkBuddy CN 和 INTL 登录状态创建与轮询
- access token / refresh token 持久化到 CPA auth 记录
- 自动刷新 token
- `/v3/config` 模型发现
- `/v2/chat/completions` 普通和 SSE 请求
- WorkBuddy 所需的 Origin、Referer、User-Agent、账号身份请求头
- CPA Management API 资源入口
- CPA Plugin Store Registry 发布结构

## 本地构建

```bash
go mod tidy
go build -buildmode=c-shared -o /tmp/workbuddy.dylib .
```

目标 Oracle Linux 服务器上构建：

```bash
GOOS=linux GOARCH=amd64 CGO_ENABLED=1 \
  go build -buildmode=c-shared -o workbuddy-linux-amd64.so .

# ARM64 服务器
GOOS=linux GOARCH=arm64 CGO_ENABLED=1 \
  go build -buildmode=c-shared -o workbuddy-linux-arm64.so .
```

由于 `-buildmode=c-shared` 依赖 CGO，Linux 产物建议在目标 Oracle 服务器或同架构 Linux CI runner 上构建。

## CPA 插件商店发布

项目包含：

- `registry.json`：CPA 可查询的 Registry 示例
- `.github/workflows/release.yml`：构建 Linux amd64/arm64、macOS amd64/arm64 并发布 GitHub Release

发布新版本时，创建一个不带 `v` 前缀的版本标签：

```bash
git tag 0.1.0
git push origin 0.1.0
```

Registry 使用 `github-release` 安装类型。CPA 会根据当前 `GOOS/GOARCH` 查询 Release 资产、下载、校验并放入配置的插件目录。

在 CPA 的 `config.yaml` 中配置自定义 Registry：

```yaml
plugins:
  enabled: true
  dir: "./plugins"
  store-sources:
    - "https://raw.githubusercontent.com/jingjingxinshang/workbuddy-cliproxy-plugin/main/registry.json"
```

然后在 CPA 管理页面中搜索 `WorkBuddy`，执行安装和启用。

### 关于 “需要认证”

不要把 `auth_required` 写进 `registry.json`。CPA 管理面板的安装按钮逻辑是：

```tsx
const missingAuth = entry.authRequired && !entry.authConfigured;
const actionDisabled = !connected || missingAuth || ...;
```

`auth_required: true` 表示“下载该插件需要先配置 `plugins.store-auth` 凭证”，只适用于私有仓库或需要鉴权的下载地址。公开仓库必须省略该字段，否则安装按钮会被禁用并显示“需要认证”。

WorkBuddy 自身的账号登录属于插件运行时行为，由插件在安装并启用后通过 Management API 触发，与插件商店的 `store-auth` 无关。

## 登录

插件实现的是 CPA 的 `AuthProvider`，登录走 CPA 的标准 OAuth 流程，凭据由 CPA 自己保存到 `auths/`：

```bash
# 1. 发起登录，返回 {url, state}
curl -H "Authorization: Bearer <MANAGEMENT_KEY>" \
  "http://127.0.0.1:8317/v0/management/workbuddy-auth-url?region=cn"

# 2. 打开返回的 url，用 WorkBuddy 客户端扫码/登录

# 3. 轮询状态；返回 {"status":"ok"} 即完成并已保存凭据
curl -H "Authorization: Bearer <MANAGEMENT_KEY>" \
  "http://127.0.0.1:8317/v0/management/get-auth-status?state=<state>"
```

`region` 取值 `cn`（中国大陆）或 `intl`（国际），决定使用哪个集群。

插件同时注册了一个管理页面，在 CPA 管理面板中打开 `WorkBuddy` 菜单即可使用图形化登录：

```text
/v0/resource/plugins/workbuddy/
```

该页面在本机浏览器中填写管理密钥后调用上述两个端点，密钥只保存在浏览器 localStorage，不会发给插件。

登录成功后，WorkBuddy 模型会出现在 CPA 的模型列表中。

WorkBuddy 原始接口属于第三方服务，登录、额度和模型接口的使用必须符合 WorkBuddy 服务条款。
