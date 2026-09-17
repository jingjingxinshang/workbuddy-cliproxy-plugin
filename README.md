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
- QuotaProvider：`/billing/meter/get-user-resource` 额度查询
- 面板内账号页：多账号列表（昵称 / UID / 企业 / 区域 / 套餐 / 额度 / 凭据到期 / 签到状态）
- 每日签到：`/billing/meter/checkin-status` + `/billing/meter/daily-checkin`，页面打开与 token 刷新时自动领取，也可手动
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

发布新版本时：

1. 同步更新两处版本号：`main.go` 的 `pluginVer`（运行期注册信息，面板显示的就是它）与 `registry.json` 的 `version`（插件商店据此判断可安装版本）。两者必须一起改。
2. 提交后创建一个不带 `v` 前缀的版本标签并推送，工作流会自动构建 Linux amd64/arm64 并发布 GitHub Release：

```bash
# 标签号必须与上面两处版本号一致
git tag 0.3.0
git push origin 0.3.0
```

注意：注册表里宣告的版本必须在 Release 里真实存在，否则商店显示可更新却下载不到包。

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

登录不需要插件侧的任何配置，也不需要插件自己的页面。插件实现的是 CPA 的 `AuthProvider`，管理面板的「OAuth 登录」页会为声明了 OAuth 的插件自动渲染一张卡片，点「开始 WorkBuddy 登录」即可；凭据由 CPA 自己保存到 `auths/`：

```text
GET /v0/management/workbuddy-auth-url              -> {url, state}
GET /v0/management/get-auth-status?state=<state>   -> {"status":"ok"} 后凭据已写入
```

面板用的是它自己的连接会话，不需要在页面里再填一次管理密钥。同一流程也可以直接用管理密钥调接口：

```bash
# 1. 发起登录，返回 {url, state}
curl -H "Authorization: Bearer <MANAGEMENT_KEY>" \
  "http://127.0.0.1:8317/v0/management/workbuddy-auth-url"

# 2. 打开返回的 url，用 WorkBuddy 客户端扫码/登录

# 3. 轮询状态；返回 {"status":"ok"} 即完成并已保存凭据
curl -H "Authorization: Bearer <MANAGEMENT_KEY>" \
  "http://127.0.0.1:8317/v0/management/get-auth-status?state=<state>"
```

### 集群区域

面板的卡片不带参数，所以登录用哪个集群由插件的 `default_region` 决定，默认 `cn`。在面板「插件管理」页点 WorkBuddy 的「编辑配置」即可修改，也可以直接写 `config.yaml`：

```yaml
plugins:
  configs:
    workbuddy:
      enabled: true
      default_region: intl   # cn | intl
```

插件在 `plugin.register` / `plugin.reconfigure` 时从宿主下发的 `config_yaml` 中读取该字段，取值 `cn`（中国大陆）或 `intl`（国际）。

临时用另一个集群登录一次时不必改配置：宿主会把 auth URL 上的 query 参数全部作为登录元数据传给插件，显式带上 `region` 即可覆盖默认值。

```bash
curl -H "Authorization: Bearer <MANAGEMENT_KEY>" \
  "http://127.0.0.1:8317/v0/management/workbuddy-auth-url?region=intl"
```

## 账号页

插件只注册一个资源页面，在 CPA 管理面板中打开 `WorkBuddy 账号` 菜单即可进入：

```text
/v0/resource/plugins/workbuddy/quota     账号页
```

该页面在本机浏览器中填写管理密钥后调用 CPA 的管理接口，密钥只保存在浏览器 localStorage，不会发给插件。页面会读取面板持久化的主题（`cli-proxy-theme`），自动跟随面板的浅色/深色设置；读不到时回落到系统偏好。

页面调用的路由是：

```text
GET  /v0/management/workbuddy/accounts[?name=<凭据文件名>]     账号列表：身份 + 凭据状态 + 签到状态
GET  /v0/management/workbuddy/quota[?auth_index=...]         单个账号额度
POST /v0/management/workbuddy/checkin[?auth_index=|?name=]   签到（不带参数 = 全部账号）
```

每个账号一张卡片，展示昵称、UID、企业 ID、区域、凭据状态与到期时间、当前套餐、剩余额度与重置时间、签到状态与连续天数，并提供单独的「签到」按钮；顶部有「全部签到」。页面会先列出所有凭据再逐个查额度，不带 `auth_index` 时取第一个凭据。

账号页存在的必要性：面板只为六个内置 provider 渲染额度（`QuotaProviderType` 是封闭联合类型），插件 provider 在面板里既没有额度位置也没有账号视图，因此插件用自己的资源页展示账号、额度与签到。

## 每日签到

签到走与额度同一套请求（`/billing/meter/*`、浏览器 User-Agent、区域集群）：

```text
POST /billing/meter/checkin-status   读状态（只读，不领取）
POST /billing/meter/daily-checkin    领取每日奖励
```

触发方式有两种，都是幂等的：

- **打开账号页时自动领取**，并在有账号未签到时给出提示；也可点「全部签到」或单个账号的「签到」。
- **token 刷新时自动领取**（每个本地自然日最多一次）。这是插件唯一一个不需要有人打开页面就会执行的钩子；不刷新 token 就不会触发。

### 为什么「签到状态」经常是未知

`checkin-status` 返回的 `today_checked_in` 描述的是一个**签到活动**，不是每日奖励。没有活动时上游把整块字段归零（`active:false`、`today_checked_in:false`），把它读成「今天没签到」会误报已经领过的账号。因此没有活动时插件报 `unknown` 而不是 `unclaimed`。

领取接口才是权威来源：重复领取会返回 `code=10001`（HTTP 400，「今天已签到，请明天再来」），插件把这个结果算作已签到而不是失败。所以「签到」按钮始终可用，点一下就能得到确定结论。

登录成功后，WorkBuddy 模型会出现在 CPA 的模型列表中。

WorkBuddy 原始接口属于第三方服务，登录、额度和模型接口的使用必须符合 WorkBuddy 服务条款。
