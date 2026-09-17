package main

// quotaPageHTML renders WorkBuddy quota in the plugin's own resource page.
//
// The host management panel renders quota only for its six built-in providers
// (QuotaProviderType is a closed union), so a plugin provider has no place in
// that UI: clicking the panel's refresh button resolves no adapter and the UI
// falls back to its generic "unknown error" text. This page is the supported
// alternative — it runs on the plugin's own resource route and calls the
// plugin's own management route with the management key the operator supplies.
//
// Everything is inlined on purpose: the docs call out that a resource page can
// read the management center's storage, so loading third-party scripts here
// would hand them the management key.
func quotaPageHTML() string {
	return `<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>WorkBuddy 额度</title>
<style>
 *,*::before,*::after{box-sizing:border-box}
 :root{
   --bg:#0d1117; --panel:#161b22; --panel-2:#1c2128; --line:#2d333b;
   --text:#e6edf3; --muted:#8b949e; --accent:#4f8cff;
   --ok:#3fb950; --warn:#d29922; --danger:#f85149;
 }
 body{margin:0;background:var(--bg);color:var(--text);
   font:14px/1.55 -apple-system,BlinkMacSystemFont,"Segoe UI","Noto Sans SC",sans-serif;
   -webkit-font-smoothing:antialiased}
 header{position:sticky;top:0;z-index:5;display:flex;flex-wrap:wrap;gap:12px;
   align-items:center;justify-content:space-between;padding:16px 24px;
   background:rgba(13,17,23,.86);backdrop-filter:blur(10px);border-bottom:1px solid var(--line)}
 .brand{display:flex;align-items:center;gap:10px;font-size:16px;font-weight:600;letter-spacing:.2px}
 .brand i{width:9px;height:9px;border-radius:50%;background:var(--accent);
   box-shadow:0 0 0 4px rgba(79,140,255,.16)}
 .brand small{color:var(--muted);font-weight:400;font-size:12px}
 .actions{display:flex;gap:8px;align-items:center}
 input{width:250px;padding:8px 11px;border-radius:8px;border:1px solid var(--line);
   background:var(--panel);color:var(--text);font-size:13px;outline:none}
 input:focus{border-color:var(--accent);box-shadow:0 0 0 3px rgba(79,140,255,.15)}
 button{padding:8px 14px;border-radius:8px;border:1px solid transparent;background:var(--accent);
   color:#fff;font-size:13px;font-weight:500;cursor:pointer;transition:filter .15s}
 button:hover{filter:brightness(1.08)}
 button:disabled{background:var(--panel-2);color:var(--muted);border-color:var(--line);cursor:not-allowed}
 main{padding:22px 24px 48px;max-width:1180px;margin:0 auto}
 .hero{display:flex;gap:26px;align-items:center;flex-wrap:wrap;
   background:linear-gradient(180deg,#171d26,#141a22);border:1px solid var(--line);
   border-radius:16px;padding:22px 26px;margin-bottom:22px}
 .ring{--pct:0;width:132px;height:132px;border-radius:50%;flex:0 0 auto;position:relative;
   background:conic-gradient(var(--ring-color,var(--ok)) calc(var(--pct)*1%), #262c34 0)}
 .ring::after{content:"";position:absolute;inset:11px;border-radius:50%;background:#141a22}
 .ring-inner{position:absolute;inset:0;display:flex;flex-direction:column;align-items:center;
   justify-content:center;z-index:1}
 .ring-inner strong{font-size:26px;font-weight:650;letter-spacing:-.5px}
 .ring-inner span{font-size:12px;color:var(--muted);margin-top:2px}
 .hero-body{flex:1 1 320px;min-width:260px}
 .plan{font-size:13px;color:var(--muted);margin-bottom:6px;word-break:break-all}
 .amounts{display:flex;align-items:baseline;gap:10px;margin-bottom:12px}
 .amounts strong{font-size:38px;font-weight:650;letter-spacing:-1px;line-height:1}
 .amounts span{color:var(--muted);font-size:15px}
 .chips{display:flex;gap:8px;flex-wrap:wrap}
 .chip{padding:4px 10px;border-radius:999px;background:var(--panel-2);border:1px solid var(--line);
   font-size:12px;color:var(--muted)}
 .chips .chip b{color:var(--text);font-weight:600}
 h2{font-size:14px;font-weight:600;margin:0 0 12px;display:flex;align-items:center;gap:8px}
 h2 span{color:var(--muted);font-weight:400;font-size:12px}
 .grid{display:grid;gap:12px;grid-template-columns:repeat(auto-fill,minmax(268px,1fr))}
 .pkg{background:var(--panel);border:1px solid var(--line);border-radius:12px;padding:14px 16px;
   transition:border-color .15s,transform .15s}
 .pkg:hover{border-color:#3d444d;transform:translateY(-1px)}
 .pkg-name{font-size:13px;font-weight:600;margin-bottom:10px;word-break:break-all;
   display:-webkit-box;-webkit-line-clamp:2;-webkit-box-orient:vertical;overflow:hidden}
 .pkg-nums{display:flex;align-items:baseline;justify-content:space-between;gap:8px}
 .pkg-nums strong{font-size:20px;font-weight:650}
 .pkg-nums em{font-style:normal;color:var(--muted);font-size:13px}
 .bar{height:6px;border-radius:999px;background:#262c34;overflow:hidden;margin:10px 0 8px}
 .bar i{display:block;height:100%;border-radius:999px;transition:width .35s ease}
 .pkg-foot{display:flex;justify-content:space-between;gap:8px;font-size:12px;color:var(--muted)}
 .state{background:var(--panel);border:1px solid var(--line);border-left:3px solid var(--accent);
   border-radius:12px;padding:16px 18px;color:var(--muted)}
 .state.err{border-left-color:var(--danger)}
 .state .title{color:var(--text);font-weight:600;margin-bottom:6px}
 .loading{display:flex;align-items:center;gap:10px;color:var(--muted)}
 .spinner{width:15px;height:15px;border-radius:50%;border:2px solid #30363d;border-top-color:var(--accent);
   animation:spin .8s linear infinite}
 @keyframes spin{to{transform:rotate(360deg)}}
 .foot{margin-top:26px;color:#6e7681;font-size:12px;text-align:center}
</style>
</head>
<body>
<header>
  <div class="brand"><i></i>WorkBuddy 额度 <small>provider workbuddy</small></div>
  <div class="actions">
    <input id="key" type="password" placeholder="管理密钥" autocomplete="off">
    <button id="refresh">刷新额度</button>
  </div>
</header>
<main>
  <div id="out"></div>
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
  if (isNaN(remain) || isNaN(total) || total <= 0) return null;
  return { remain: remain, total: total };
}

function fmtDate(value) {
  var text = String(value || '').trim();
  if (!text) return '-';
  var match = /^(\d{4})-(\d{2})-(\d{2})[ T](\d{2}):(\d{2})/.exec(text);
  if (!match) return text;
  return match[2] + '-' + match[3] + ' ' + match[4] + ':' + match[5];
}

function fmtNumber(value) {
  var number = Number(value);
  if (!isFinite(number)) return '-';
  return (Math.round(number * 100) / 100).toLocaleString('zh-CN');
}

function pkgCard(group) {
  var bucket = (group.buckets || [])[0] || {};
  var parsed = splitAmount(bucket.description);
  var fraction = typeof bucket.remainingFraction === 'number' ? bucket.remainingFraction : null;
  var percent = fraction === null ? 0 : Math.max(0, Math.min(100, fraction * 100));
  var remain = parsed ? parsed.remain : '-';
  var total = parsed ? parsed.total : '-';
  return '<article class="pkg">' +
    '<div class="pkg-name" title="' + esc(group.displayName) + '">' + esc(group.displayName || 'WorkBuddy') + '</div>' +
    '<div class="pkg-nums"><strong style="color:' + tone(percent) + '">' + esc(fmtNumber(remain)) + '</strong>' +
    '<em>/ ' + esc(fmtNumber(total)) + '</em></div>' +
    '<div class="bar"><i style="width:' + percent.toFixed(1) + '%;background:' + tone(percent) + '"></i></div>' +
    '<div class="pkg-foot"><span>剩余 ' + percent.toFixed(1) + '%</span>' +
    '<span title="' + esc(bucket.resetTime || '') + '">' + esc(fmtDate(bucket.resetTime || bucket.window)) + '</span></div>' +
    '</article>';
}

function renderCard(file, quota) {
  var summary = quota.summary || [];
  var remain = null, total = null;
  summary.forEach(function (metric) {
    if (metric.key === 'remain') remain = metric.value;
    if (metric.key === 'total') total = metric.value;
  });
  var percent = (typeof total === 'number' && total > 0 && typeof remain === 'number')
    ? Math.max(0, Math.min(100, (remain / total) * 100)) : 0;
  var groups = quota.groups || [];
  var plan = (quota.subscription && quota.subscription.plan) ? quota.subscription.plan : 'WorkBuddy';
  var used = total > 0 ? (100 - percent) : 0;

  var head = '<section class="hero">' +
    '<div class="ring" style="--pct:' + percent.toFixed(1) + ';--ring-color:' + tone(percent) + '">' +
      '<div class="ring-inner"><strong>' + percent.toFixed(1) + '%</strong><span>剩余</span></div>' +
    '</div>' +
    '<div class="hero-body">' +
      '<div class="plan">' + esc(plan) + '</div>' +
      '<div class="amounts"><strong>' + esc(fmtNumber(remain)) + '</strong><span>/ ' + esc(fmtNumber(total)) + ' credits</span></div>' +
      '<div class="chips">' +
        '<span class="chip">已用 <b>' + used.toFixed(2) + '%</b></span>' +
        '<span class="chip">套餐 <b>' + groups.length + '</b></span>' +
        '<span class="chip">凭据 <b>' + esc(file.label || file.name || '-') + '</b></span>' +
      '</div>' +
    '</div>' +
    '</section>';

  var body = groups.length
    ? '<h2>套餐明细 <span>' + groups.length + ' 项</span></h2><div class="grid">' +
      groups.map(pkgCard).join('') + '</div>'
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
      var targets = files.length ? files : [null];
      return Promise.all(targets.map(function (file) {
        var index = file ? (file.auth_index || file.authIndex || '') : '';
        var url = '/v0/management/workbuddy/quota' + (index ? '?auth_index=' + encodeURIComponent(index) : '');
        return fetch(url, { headers: headers })
          .then(function (resp) {
            return resp.json().then(function (data) { return { file: file, ok: resp.ok, data: data }; });
          })
          .catch(function (err) { return { file: file, ok: false, data: { error: String(err) } }; });
      }));
    })
    .then(function (results) {
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
