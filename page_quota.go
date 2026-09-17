package main

// themePalette is the light/dark palette shared by both of the plugin's pages.
// Light is the default and dark is an override, because the panel can be
// showing either one.
const themePalette = ` :root{
   --bg:#f6f8fa; --panel:#fff; --panel-2:#f0f2f5; --line:#d8dee4;
   --text:#1f2328; --muted:#656d76; --accent:#3b6ef0;
   --ok:#1a7f37; --warn:#9a6700; --danger:#cf222e;
   --track:#e6e9ee; --bar:rgba(246,248,250,.88); --hover:#afb8c1;
   --shadow:0 1px 2px rgba(31,35,40,.06),0 3px 10px rgba(31,35,40,.05);
 }
 [data-theme="dark"]{
   --bg:#0d1117; --panel:#161b22; --panel-2:#1c2128; --line:#30363d;
   --text:#e6edf3; --muted:#8b949e; --accent:#5b8cff;
   --ok:#3fb950; --warn:#d29922; --danger:#f85149;
   --track:#262c34; --bar:rgba(13,17,23,.86); --hover:#4a525b;
   --shadow:0 1px 2px rgba(0,0,0,.35),0 4px 14px rgba(0,0,0,.3);
 }`

// themeBootScript resolves the theme before the first paint.
//
// It runs in the document head, not with the rest of the page script: the
// panel may be dark, and deferring this to the end of the body paints a light
// page first and then repaints it.
const themeBootScript = `// The panel is same-origin and persists the theme it resolved, so ask it which
// one is on screen instead of guessing from the operating system: the panel can
// be showing light while the OS is dark. Falls back to the OS preference when
// the panel has not stored one.
function panelTheme() {
  try {
    var raw = localStorage.getItem('cli-proxy-theme');
    if (raw) {
      var parsed = JSON.parse(raw);
      var resolved = parsed && parsed.state && parsed.state.resolvedTheme;
      if (resolved === 'dark' || resolved === 'light') return resolved;
    }
  } catch (e) {}
  try {
    if (window.matchMedia && window.matchMedia('(prefers-color-scheme: dark)').matches) return 'dark';
  } catch (e) {}
  return 'light';
}

try { document.documentElement.setAttribute('data-theme', panelTheme()); } catch (e) {}`

// quotaPageHTML renders WorkBuddy quota in the plugin's own resource page.
//
// The host management panel renders quota only for its six built-in providers
// (QuotaProviderType is a closed union), so a plugin provider has no place in
// that UI: refreshing resolves no adapter and the panel falls back to its
// generic "unknown error" text. This page is the supported alternative — it
// runs on the plugin's own resource route and calls the plugin's own
// management route with the management key the operator supplies.
//
// Everything is inlined on purpose: the docs call out that a resource page can
// read the management center's storage, so loading third-party scripts here
// would hand them the management key.
func quotaPageHTML() string {
	return `<!doctype html>
<html lang="zh-CN" data-theme="light">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<meta name="color-scheme" content="light dark">
<title>WorkBuddy 额度</title>
<style>
 *,*::before,*::after{box-sizing:border-box}` + themePalette + `
 body{margin:0;background:var(--bg);color:var(--text);
   font:14px/1.55 -apple-system,BlinkMacSystemFont,"Segoe UI","Noto Sans SC",sans-serif;
   -webkit-font-smoothing:antialiased}
 header{position:sticky;top:0;z-index:5;display:flex;flex-wrap:wrap;gap:12px;
   align-items:center;justify-content:space-between;padding:16px 24px;
   background:var(--bar);backdrop-filter:blur(10px);border-bottom:1px solid var(--line)}
 .brand{display:flex;align-items:center;gap:10px;font-size:16px;font-weight:600;letter-spacing:.2px}
 .brand i{width:9px;height:9px;border-radius:50%;background:var(--accent);
   box-shadow:0 0 0 4px rgba(59,110,240,.14)}
 .brand small{color:var(--muted);font-weight:400;font-size:12px}
 .actions{display:flex;gap:8px;align-items:center}
 input{width:250px;padding:8px 11px;border-radius:8px;border:1px solid var(--line);
   background:var(--panel);color:var(--text);font-size:13px;outline:none}
 input:focus{border-color:var(--accent);box-shadow:0 0 0 3px rgba(59,110,240,.15)}
 button{padding:8px 14px;border-radius:8px;border:1px solid transparent;background:var(--accent);
   color:#fff;font-size:13px;font-weight:500;cursor:pointer;transition:filter .15s}
 button:hover{filter:brightness(1.08)}
 button:disabled{background:var(--panel-2);color:var(--muted);border-color:var(--line);cursor:not-allowed}
 main{padding:22px 24px 48px;max-width:1180px;margin:0 auto}
 .hero{display:flex;gap:26px;align-items:center;flex-wrap:wrap;
   background:var(--panel);box-shadow:var(--shadow);border:1px solid var(--line);
   border-radius:16px;padding:22px 26px;margin-bottom:22px}
 .ring{--pct:0;width:132px;height:132px;border-radius:50%;flex:0 0 auto;position:relative;
   background:conic-gradient(var(--ring-color,var(--ok)) calc(var(--pct)*1%), var(--track) 0)}
 .ring::after{content:"";position:absolute;inset:11px;border-radius:50%;background:var(--panel)}
 .ring-inner{position:absolute;inset:0;display:flex;flex-direction:column;align-items:center;
   justify-content:center;z-index:1}
 .ring-inner strong{font-size:26px;font-weight:650;letter-spacing:-.5px;font-variant-numeric:tabular-nums}
 .ring-inner span{font-size:12px;color:var(--muted);margin-top:2px}
 .hero-body{flex:1 1 320px;min-width:260px}
 .plan{font-size:13px;color:var(--muted);margin-bottom:6px;word-break:break-all}
 .amounts{display:flex;align-items:baseline;gap:10px;margin-bottom:12px}
 .amounts strong{font-size:38px;font-weight:650;letter-spacing:-1px;line-height:1;
   font-variant-numeric:tabular-nums}
 .amounts span{color:var(--muted);font-size:15px}
 .chips{display:flex;gap:8px;flex-wrap:wrap}
 .chip{padding:4px 10px;border-radius:999px;background:var(--panel-2);border:1px solid var(--line);
   font-size:12px;color:var(--muted)}
 .chips .chip b{color:var(--text);font-weight:600}
 h2{font-size:14px;font-weight:600;margin:0 0 12px;display:flex;align-items:center;gap:8px}
 h2 span{color:var(--muted);font-weight:400;font-size:12px}
 .grid{display:grid;gap:12px;grid-template-columns:repeat(auto-fill,minmax(268px,1fr))}
 .pkg{position:relative;background:var(--panel);border:1px solid var(--line);border-radius:12px;
   padding:14px 16px;box-shadow:var(--shadow);transition:border-color .15s,transform .15s}
 .pkg:hover{border-color:var(--hover);transform:translateY(-1px)}
 .pkg.urgent{border-color:var(--warn)}
 .badge{position:absolute;top:-8px;right:12px;padding:1px 8px;border-radius:999px;
   background:var(--warn);color:#fff;font-size:11px;font-weight:600}
 .pkg-name{font-size:13px;font-weight:600;margin-bottom:10px;word-break:break-all;
   display:-webkit-box;-webkit-line-clamp:2;-webkit-box-orient:vertical;overflow:hidden}
 .pkg-nums{display:flex;align-items:baseline;justify-content:space-between;gap:8px}
 .pkg-nums strong{font-size:20px;font-weight:650;font-variant-numeric:tabular-nums}
 .pkg-nums em{font-style:normal;color:var(--muted);font-size:13px}
 .bar{height:6px;border-radius:999px;background:var(--track);overflow:hidden;margin:10px 0 8px}
 .bar i{display:block;height:100%;border-radius:999px;transition:width .35s ease}
 .pkg-foot{display:flex;justify-content:space-between;gap:8px;font-size:12px;color:var(--muted)}
 .reset{display:flex;align-items:center;gap:5px}
 .soon{color:var(--warn);font-weight:600}
 .state{background:var(--panel);border:1px solid var(--line);border-left:3px solid var(--accent);
   border-radius:12px;padding:16px 18px;color:var(--muted);box-shadow:var(--shadow)}
 .state.err{border-left-color:var(--danger)}
 .state .title{color:var(--text);font-weight:600;margin-bottom:6px}
 .loading{display:flex;align-items:center;gap:10px;color:var(--muted)}
 .spinner{width:15px;height:15px;border-radius:50%;border:2px solid var(--track);border-top-color:var(--accent);
   animation:spin .8s linear infinite}
 @keyframes spin{to{transform:rotate(360deg)}}
 .foot{margin-top:26px;color:var(--muted);font-size:12px;text-align:center}
 .sr-only{position:absolute;width:1px;height:1px;margin:-1px;padding:0;overflow:hidden;
   clip:rect(0 0 0 0);white-space:nowrap;border:0}
 a{color:var(--accent)}
 .link{margin-top:12px;display:inline-block;font-size:13px}
</style>
<script>` + themeBootScript + `</script>
</head>
<body>
<header>
  <div class="brand"><i></i>WorkBuddy 额度 <small>provider workbuddy</small></div>
  <div class="actions">
    <label class="sr-only" for="key">管理密钥</label>
    <input id="key" type="password" placeholder="管理密钥" autocomplete="off" spellcheck="false">
    <button id="refresh" type="button">刷新额度</button>
  </div>
</header>
<main>
  <div id="out" aria-live="polite"></div>
  <div class="foot">密钥仅保存在本机 localStorage，不会发送给插件本身。</div>
</main>
<script>
var out = document.getElementById('out');
var keyInput = document.getElementById('key');
var refreshButton = document.getElementById('refresh');

try { keyInput.value = localStorage.getItem('wbaw_mgmt_key') || ''; } catch (e) {}

function esc(text) {
  return String(text == null ? '' : text)
    .replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;');
}

function tone(percent) {
  if (percent <= 15) return 'var(--danger)';
  if (percent <= 40) return 'var(--warn)';
  return 'var(--ok)';
}

function splitAmount(description) {
  var parts = String(description || '').split('/');
  if (parts.length !== 2) return null;
  var remain = parseFloat(parts[0].trim());
  var total = parseFloat(parts[1].trim());
  if (isNaN(remain) || isNaN(total)) return null;
  return { remain: remain, total: total };
}

function fmtDate(value) {
  var text = String(value || '').trim();
  if (!text) return '-';
  var match = /^(\d{4})-(\d{2})-(\d{2})[ T](\d{2}):(\d{2})/.exec(text);
  if (!match) return text;
  return match[2] + '-' + match[3] + ' ' + match[4] + ':' + match[5];
}

// Upstream sends "YYYY-MM-DD HH:mm:ss" with no zone, read as local time — the
// same reading the panel uses. Only the countdown depends on it, and the
// absolute date stays on screen next to it.
function resetMs(value) {
  var match = /^(\d{4})-(\d{2})-(\d{2})[ T](\d{2}):(\d{2})(?::(\d{2}))?/.exec(String(value || '').trim());
  if (!match) return null;
  var at = new Date(+match[1], +match[2] - 1, +match[3], +match[4], +match[5], +(match[6] || 0)).getTime();
  return isNaN(at) ? null : at;
}

function countdown(value) {
  var at = resetMs(value);
  if (at === null) return '';
  var diff = at - Date.now();
  if (diff <= 0) return '已重置';
  var days = Math.floor(diff / 86400000);
  var hours = Math.floor((diff % 86400000) / 3600000);
  if (days > 0) return days + ' 天后重置';
  var minutes = Math.floor((diff % 3600000) / 60000);
  if (hours > 0) return hours + ' 小时后重置';
  return (minutes > 0 ? minutes : 1) + ' 分钟后重置';
}

function fmtNumber(value) {
  if (typeof value !== 'number' || !isFinite(value)) return '-';
  return (Math.round(value * 100) / 100).toLocaleString('zh-CN');
}

function fractionOf(group) {
  var bucket = (group && group.buckets && group.buckets[0]) || {};
  return typeof bucket.remainingFraction === 'number' ? bucket.remainingFraction : 1;
}

function metricOf(summary, key) {
  for (var index = 0; index < summary.length; index++) {
    if (summary[index] && summary[index].key === key) return summary[index];
  }
  return null;
}

function pkgCard(group, urgent) {
  var bucket = (group.buckets || [])[0] || {};
  var parsed = splitAmount(bucket.description);
  var fraction = typeof bucket.remainingFraction === 'number' ? bucket.remainingFraction : null;
  var percent = fraction === null ? 0 : Math.max(0, Math.min(100, fraction * 100));
  var remain = parsed ? parsed.remain : null;
  var total = parsed ? parsed.total : null;
  var reset = bucket.resetTime || bucket.window || '';
  var left = countdown(reset);
  return '<article class="pkg' + (urgent ? ' urgent' : '') + '">' +
    (urgent ? '<span class="badge">剩余最少</span>' : '') +
    '<div class="pkg-name" title="' + esc(group.displayName) + '">' +
      esc(group.displayName || 'WorkBuddy') + '</div>' +
    '<div class="pkg-nums"><strong style="color:' + tone(percent) + '">' +
      (remain === null ? '-' : esc(fmtNumber(remain))) + '</strong>' +
      '<em>' + (total === null ? '' : '/ ' + esc(fmtNumber(total))) + '</em></div>' +
    '<div class="bar"><i style="width:' + percent.toFixed(1) + '%;background:' + tone(percent) + '"></i></div>' +
    '<div class="pkg-foot"><span>剩余 ' + percent.toFixed(1) + '%</span>' +
      '<span class="reset" title="' + esc(reset) + '"><span>' + esc(fmtDate(reset)) + '</span>' +
      (left ? '<span class="soon">· ' + esc(left) + '</span>' : '') + '</span></div>' +
    '</article>';
}

function renderCard(file, quota) {
  var summary = quota.summary || [];
  var remain = metricOf(summary, 'remain');
  var total = metricOf(summary, 'total');
  var usedMetric = metricOf(summary, 'used_percent');
  var remainValue = remain ? remain.value : null;
  var totalValue = total ? total.value : null;
  var unit = (remain && remain.unit) ? remain.unit : '';
  var percent = (typeof totalValue === 'number' && totalValue > 0 && typeof remainValue === 'number')
    ? Math.max(0, Math.min(100, (remainValue / totalValue) * 100)) : 0;
  // The plugin reports used_percent itself; compute only when it is absent.
  var used = (usedMetric && typeof usedMetric.value === 'number')
    ? usedMetric.value : (totalValue > 0 ? 100 - percent : 0);

  // Most depleted first: the package closest to running out is the one the
  // reader came for.
  var groups = (quota.groups || []).slice().sort(function (a, b) {
    return fractionOf(a) - fractionOf(b);
  });

  var soonestAt = null;
  var soonest = '';
  groups.forEach(function (group) {
    var bucket = (group.buckets || [])[0] || {};
    var at = resetMs(bucket.resetTime || bucket.window);
    if (at === null || at <= Date.now()) return;
    if (soonestAt !== null && at >= soonestAt) return;
    soonestAt = at;
    soonest = countdown(bucket.resetTime || bucket.window);
  });

  var plan = (quota.subscription && quota.subscription.plan)
    ? quota.subscription.plan : 'WorkBuddy';

  var head = '<section class="hero">' +
    '<div class="ring" style="--pct:' + percent.toFixed(1) + ';--ring-color:' + tone(percent) + '">' +
      '<div class="ring-inner"><strong>' + percent.toFixed(1) + '%</strong><span>剩余</span></div>' +
    '</div>' +
    '<div class="hero-body">' +
      '<div class="plan" title="' + esc(plan) + '">' + esc(plan) + '</div>' +
      '<div class="amounts"><strong>' + esc(fmtNumber(remainValue)) + '</strong>' +
        '<span>/ ' + esc(fmtNumber(totalValue)) + (unit ? ' ' + esc(unit) : '') + '</span></div>' +
      '<div class="chips">' +
        '<span class="chip">已用 <b>' + used.toFixed(1) + '%</b></span>' +
        '<span class="chip">套餐 <b>' + groups.length + '</b></span>' +
        '<span class="chip">凭据 <b>' + esc(file.label || file.name || '-') + '</b></span>' +
        (soonest ? '<span class="chip">最近重置 <b class="soon">' + esc(soonest) + '</b></span>' : '') +
      '</div>' +
    '</div>' +
    '</section>';

  var body = groups.length
    ? '<h2>套餐明细 <span>' + groups.length + ' 项</span></h2><div class="grid">' +
      groups.map(function (group, index) {
        return pkgCard(group, groups.length > 1 && index === 0);
      }).join('') + '</div>'
    : '';

  return head + body;
}

function renderNotice(title, detail, isError) {
  out.innerHTML = '<div class="state' + (isError ? ' err' : '') + '">' +
    '<div class="title">' + esc(title) + '</div>' +
    (detail ? '<div>' + esc(detail) + '</div>' : '') + '</div>';
}

function renderLoading() {
  out.innerHTML = '<div class="state"><div class="loading"><span class="spinner"></span>正在读取额度…</div></div>';
}

function renderErrorCard(file, message) {
  var name = file && (file.label || file.name) ? (file.label || file.name) : 'WorkBuddy';
  return '<div class="state err"><div class="title">' + esc(name) + '</div>' +
    '<div>' + esc(message || '未知错误') + '</div></div>';
}

// Sibling resource page: .../plugins/<id>/quota -> .../plugins/<id>/
function loginPageURL() {
  return location.pathname.replace(/\/quota\/?$/, '/');
}

function renderNoCredential() {
  out.innerHTML = '<div class="state">' +
    '<div class="title">还没有 WorkBuddy 凭据</div>' +
    '<div>先到「WorkBuddy 登录」菜单完成一次登录，再回到这里查看额度。</div>' +
    '<a class="link" href="' + esc(loginPageURL()) + '">前往 WorkBuddy 登录 →</a>' +
    '</div>';
}

function load() {
  var key = keyInput.value.trim();
  if (!key) { renderNotice('请先填写管理密钥', '密钥来自 CPA 的 remote-management.secret-key。', true); return; }
  try { localStorage.setItem('wbaw_mgmt_key', key); } catch (e) {}
  var headers = { 'Authorization': 'Bearer ' + key };
  refreshButton.disabled = true;
  renderLoading();

  fetch('/v0/management/auth-files', { headers: headers })
    .then(function (resp) {
      if (resp.status === 401 || resp.status === 403) throw new Error('管理密钥无效或权限不足（HTTP ' + resp.status + '）');
      if (!resp.ok) throw new Error('读取凭据列表失败（HTTP ' + resp.status + '）');
      return resp.json();
    })
    .then(function (body) {
      var files = (body.files || []).filter(function (file) {
        return String(file.provider || file.type || '').toLowerCase() === 'workbuddy';
      });
      if (!files.length) { renderNoCredential(); return []; }
      return Promise.all(files.map(function (file) {
        var index = file.auth_index || file.authIndex || '';
        var url = '/v0/management/workbuddy/quota' + (index ? '?auth_index=' + encodeURIComponent(index) : '');
        return fetch(url, { headers: headers })
          .then(function (resp) {
            return resp.json().then(function (data) { return { file: file, ok: resp.ok, data: data }; });
          })
          .catch(function (err) { return { file: file, ok: false, data: { error: String(err) } }; });
      }));
    })
    .then(function (results) {
      if (!results.length) return;
      var html = '';
      var allFailed = true;
      results.forEach(function (item) {
        if (item.ok) { allFailed = false; html += renderCard(item.file || {}, item.data); }
        else { html += renderErrorCard(item.file, item.data && item.data.error); }
      });
      if (allFailed && results.length === 1) {
        renderNotice('未能读取额度', results[0].data && results[0].data.error, true);
        return;
      }
      out.innerHTML = html;
    })
    .catch(function (err) {
      renderNotice('读取失败', err && err.message ? err.message : String(err), true);
    })
    .then(function () { refreshButton.disabled = false; });
}

refreshButton.onclick = load;
keyInput.addEventListener('keydown', function (event) { if (event.key === 'Enter') load(); });

if (keyInput.value) { load(); }
</script>
</body>
</html>`
}
