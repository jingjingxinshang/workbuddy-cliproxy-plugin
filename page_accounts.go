package main

// themePalette is the light/dark palette used by the plugin's page.
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

// accountsPageHTML renders every WorkBuddy account: identity, credential state,
// quota and the daily check-in.
//
// The host management panel renders quota only for its six built-in providers
// (QuotaProviderType is a closed union), and it has no per-account view for a
// plugin provider at all, so this page is where an operator manages WorkBuddy:
// it runs on the plugin's own resource route and calls the plugin's own
// management routes with the management key they supply.
//
// Everything is inlined on purpose: a resource page runs same-origin with the
// management center, so loading a third-party script here would hand it the
// management key.
func accountsPageHTML() string {
	return `<!doctype html>
<html lang="zh-CN" data-theme="light">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<meta name="color-scheme" content="light dark">
<title>WorkBuddy 账号</title>
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
 input{width:240px;padding:8px 11px;border-radius:8px;border:1px solid var(--line);
   background:var(--panel);color:var(--text);font-size:13px;outline:none}
 input:focus{border-color:var(--accent);box-shadow:0 0 0 3px rgba(59,110,240,.15)}
 button{padding:8px 14px;border-radius:8px;border:1px solid transparent;background:var(--accent);
   color:#fff;font-size:13px;font-weight:500;cursor:pointer;transition:filter .15s}
 button:hover{filter:brightness(1.08)}
 button:disabled{background:var(--panel-2);color:var(--muted);border-color:var(--line);cursor:not-allowed}
 button.ghost{background:var(--panel);color:var(--text);border-color:var(--line)}
 button.ghost:hover{filter:none;border-color:var(--hover)}
 main{padding:22px 24px 48px;max-width:1180px;margin:0 auto}
 .summary{display:flex;gap:8px;flex-wrap:wrap;align-items:center;margin-bottom:18px}
 .toast{padding:10px 14px;border-radius:10px;background:var(--panel);border:1px solid var(--line);
   border-left:3px solid var(--accent);color:var(--muted);box-shadow:var(--shadow);margin-bottom:14px}
 .toast.ok{border-left-color:var(--ok)}
 .toast.err{border-left-color:var(--danger)}
 .acct{background:var(--panel);border:1px solid var(--line);border-radius:14px;
   box-shadow:var(--shadow);margin-bottom:18px;overflow:hidden}
 .acct-head{display:flex;flex-wrap:wrap;gap:12px;align-items:center;justify-content:space-between;
   padding:14px 18px;border-bottom:1px solid var(--line);background:var(--panel-2)}
 .acct-title{display:flex;align-items:baseline;gap:10px;min-width:0}
 .acct-name{font-size:15px;font-weight:650;letter-spacing:.1px;word-break:break-all}
 .acct-file{color:var(--muted);font-size:12px;word-break:break-all}
 .acct-chips{display:flex;gap:8px;flex-wrap:wrap;flex:1 1 340px}
 .chip{padding:4px 10px;border-radius:999px;background:var(--panel);border:1px solid var(--line);
   font-size:12px;color:var(--muted);white-space:nowrap}
 .chip b{color:var(--text);font-weight:600}
 .chip.ok{border-color:var(--ok)} .chip.ok b{color:var(--ok)}
 .chip.warn{border-color:var(--warn)} .chip.warn b{color:var(--warn)}
 .chip.err{border-color:var(--danger)} .chip.err b{color:var(--danger)}
 .acct-quota{padding:18px}
 .hero{display:flex;gap:26px;align-items:center;flex-wrap:wrap;
   background:var(--panel);border:1px solid var(--line);
   border-radius:16px;padding:22px 26px;margin-bottom:18px}
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
 .chips .chip b{color:var(--text);font-weight:600}
 h2{font-size:14px;font-weight:600;margin:0 0 12px;display:flex;align-items:center;gap:8px}
 h2 span{color:var(--muted);font-weight:400;font-size:12px}
 .grid{display:grid;gap:12px;grid-template-columns:repeat(auto-fill,minmax(268px,1fr))}
 .pkg{position:relative;background:var(--panel);border:1px solid var(--line);border-radius:12px;
   padding:14px 16px;transition:border-color .15s}
 .pkg:hover{border-color:var(--hover)}
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
   border-radius:12px;padding:16px 18px;color:var(--muted)}
 .state.err{border-left-color:var(--danger)}
 .state .title{color:var(--text);font-weight:600;margin-bottom:6px}
 .loading{display:flex;align-items:center;gap:10px;color:var(--muted)}
 .spinner{width:15px;height:15px;border-radius:50%;border:2px solid var(--track);border-top-color:var(--accent);
   animation:spin .8s linear infinite}
 @keyframes spin{to{transform:rotate(360deg)}}
 .foot{margin-top:26px;color:var(--muted);font-size:12px;text-align:center;line-height:1.8}
 .sr-only{position:absolute;width:1px;height:1px;margin:-1px;padding:0;overflow:hidden;
   clip:rect(0 0 0 0);white-space:nowrap;border:0}
</style>
<script>` + themeBootScript + `</script>
</head>
<body>
<header>
  <div class="brand"><i></i>WorkBuddy 账号 <small>provider workbuddy</small></div>
  <div class="actions">
    <label class="sr-only" for="key">管理密钥</label>
    <input id="key" type="password" placeholder="管理密钥" autocomplete="off" spellcheck="false">
    <button id="checkin-all" class="ghost" type="button">全部签到</button>
    <button id="refresh" type="button">刷新</button>
  </div>
</header>
<main>
  <div id="summary" class="summary"></div>
  <div id="toast" class="toast" style="display:none"></div>
  <div id="out" aria-live="polite"></div>
  <div class="foot">
    密钥仅保存在本机 localStorage，不会发送给插件本身。<br>
    签到状态为「未知」表示上游当前没有签到活动，其状态字段整块归零；点「签到」由领取接口给出权威结论，重复领取是安全的。
  </div>
</main>
<script>
var out = document.getElementById('out');
var summaryBox = document.getElementById('summary');
var toastBox = document.getElementById('toast');
var keyInput = document.getElementById('key');
var refreshButton = document.getElementById('refresh');
var checkinAllButton = document.getElementById('checkin-all');

var state = { accounts: [], quota: {}, runs: {} };

try { keyInput.value = localStorage.getItem('wbaw_mgmt_key') || ''; } catch (e) {}

function esc(text) {
  return String(text == null ? '' : text)
    .replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;');
}

function headers() {
  return { 'Authorization': 'Bearer ' + keyInput.value.trim() };
}

function toast(text, kind) {
  toastBox.style.display = 'block';
  toastBox.className = 'toast' + (kind ? ' ' + kind : '');
  toastBox.textContent = text;
}

function tone(percent) {
  if (percent <= 15) return 'var(--danger)';
  if (percent <= 40) return 'var(--warn)';
  return 'var(--ok)';
}

function fmtNumber(value) {
  if (value === null || value === undefined || value === '' || isNaN(value)) return '-';
  return Number(value).toLocaleString('en-US', { maximumFractionDigits: 2 });
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

function fmtStamp(ms) {
  if (!ms) return '-';
  var at = new Date(Number(ms));
  if (isNaN(at.getTime())) return '-';
  function pad(value) { return value < 10 ? '0' + value : String(value); }
  return pad(at.getMonth() + 1) + '-' + pad(at.getDate()) + ' ' + pad(at.getHours()) + ':' + pad(at.getMinutes());
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
  var left = at - Date.now();
  if (left <= 0) return '';
  var minutes = Math.floor(left / 60000);
  if (minutes < 60) return minutes + ' 分钟后';
  var hours = Math.floor(minutes / 60);
  if (hours < 24) return hours + ' 小时后';
  return Math.floor(hours / 24) + ' 天后';
}

function fractionOf(group) {
  var bucket = (group && group.buckets && group.buckets[0]) || {};
  return typeof bucket.remainingFraction === 'number' ? bucket.remainingFraction : 1;
}

function metricOf(summary, key) {
  for (var i = 0; i < (summary || []).length; i += 1) {
    if (summary[i] && summary[i].key === key) return summary[i];
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

// quotaBlock renders one account's quota: the same hero and package cards the
// plugin used before accounts existed.
function quotaBlock(quota) {
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

  var plan = (quota.subscription && quota.subscription.plan) ? quota.subscription.plan : 'WorkBuddy';

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

function quotaLoading() {
  return '<div class="state"><div class="loading"><span class="spinner"></span>正在读取额度…</div></div>';
}

function quotaError(message) {
  return '<div class="state err"><div class="title">额度读取失败</div>' +
    '<div>' + esc(message || '未知错误') + '</div></div>';
}

// checkinView turns a verdict into the chip and button state.
//
// "未知" is the normal answer when no check-in activity is running: the status
// endpoint zeroes today_checked_in in that case, so it cannot prove anything.
// Only the claim endpoint settles it, which is why the button stays available.
function checkinView(checkin) {
  if (!checkin) return { cls: '', text: '签到状态未知', detail: '', done: false };
  if (checkin.state === 'claimed') {
    var gain = checkin.freshly_claimed && typeof checkin.credit === 'number'
      ? ' +' + fmtNumber(checkin.credit) : '';
    var streak = checkin.streak_days ? '连续 ' + checkin.streak_days + ' 天' : '';
    return { cls: 'ok', text: '今日已签到' + gain, detail: streak, done: true };
  }
  if (checkin.state === 'unclaimed') {
    return { cls: 'warn', text: '今日未签到', detail: '', done: false };
  }
  return { cls: '', text: '签到状态未知', detail: checkin.error || '', done: false };
}

function accountShell(account, index) {
  var name = account.nickname || account.label || account.name || 'WorkBuddy';
  var chips = [];
  if (account.uid) chips.push('<span class="chip">UID <b>' + esc(account.uid) + '</b></span>');
  if (account.enterprise_id) chips.push('<span class="chip">企业 <b>' + esc(account.enterprise_id) + '</b></span>');
  if (account.region) chips.push('<span class="chip">区域 <b>' + esc(account.region) + '</b></span>');
  if (account.token_state === 'expired') {
    chips.push('<span class="chip err">凭据 <b>已过期</b></span>');
  } else if (account.token_state === 'missing') {
    chips.push('<span class="chip err">凭据 <b>缺少 token</b></span>');
  } else if (account.token_state) {
    chips.push('<span class="chip ok">凭据 <b>有效</b></span>');
  }
  if (account.expires_at) {
    chips.push('<span class="chip">到期 <b>' + esc(fmtStamp(account.expires_at)) + '</b></span>');
  }
  var view = checkinView(account.checkin);
  chips.push('<span class="chip ' + view.cls + '" id="ci-' + index + '">签到 <b>' + esc(view.text) + '</b></span>');
  if (view.detail) chips.push('<span class="chip">' + esc(view.detail) + '</span>');

  return '<section class="acct">' +
    '<div class="acct-head">' +
      '<div class="acct-title"><strong class="acct-name">' + esc(name) + '</strong>' +
        '<span class="acct-file">' + esc(account.name || '') + '</span></div>' +
      '<div class="acct-chips">' + chips.join('') + '</div>' +
      '<button class="ghost" type="button" id="cb-' + index + '" data-index="' + index + '"' +
        (view.done ? ' disabled' : '') + '>' + (view.done ? '已签到' : '签到') + '</button>' +
    '</div>' +
    '<div class="acct-quota" id="cq-' + index + '">' + quotaLoading() + '</div>' +
  '</section>';
}

function paintSummary() {
  if (!state.accounts.length) { summaryBox.innerHTML = ''; return; }
  var remain = 0;
  var total = 0;
  var claimed = 0;
  var known = 0;
  state.accounts.forEach(function (account) {
    var quota = state.quota[account.auth_index];
    if (quota) {
      var remainMetric = metricOf(quota.summary, 'remain');
      var totalMetric = metricOf(quota.summary, 'total');
      if (remainMetric) remain += Number(remainMetric.value) || 0;
      if (totalMetric) total += Number(totalMetric.value) || 0;
    }
    var checkin = account.checkin;
    if (checkin && checkin.state === 'claimed') claimed += 1;
    if (checkin && checkin.state && checkin.state !== 'unknown') known += 1;
  });
  var chips = ['<span class="chip">账号 <b>' + state.accounts.length + '</b></span>'];
  if (total > 0) {
    chips.push('<span class="chip">剩余 <b>' + esc(fmtNumber(remain)) + '</b> / ' +
      esc(fmtNumber(total)) + ' credits</span>');
  }
  chips.push('<span class="chip' + (claimed ? ' ok' : '') + '">已签到 <b>' + claimed + '</b></span>');
  if (known < state.accounts.length) {
    chips.push('<span class="chip">状态未知 <b>' + (state.accounts.length - known) + '</b></span>');
  }
  summaryBox.innerHTML = chips.join('');
}

function paintCheckin(index, checkin) {
  var account = state.accounts[index];
  if (!account) return;
  account.checkin = checkin;
  var chip = document.getElementById('ci-' + index);
  var button = document.getElementById('cb-' + index);
  var view = checkinView(checkin);
  if (chip) {
    chip.className = 'chip ' + view.cls;
    chip.innerHTML = '签到 <b>' + esc(view.text) + '</b>' +
      (view.detail ? ' · ' + esc(view.detail) : '');
  }
  if (button) {
    button.disabled = view.done;
    button.textContent = view.done ? '已签到' : '签到';
  }
  paintSummary();
}

function setRunning(key, running) {
  state.runs[key] = running;
  var busy = Object.keys(state.runs).some(function (item) { return state.runs[item]; });
  checkinAllButton.disabled = busy;
  refreshButton.disabled = busy;
}

function loadQuota(index) {
  var account = state.accounts[index];
  if (!account) return Promise.resolve();
  var box = document.getElementById('cq-' + index);
  if (box) box.innerHTML = quotaLoading();
  return fetch('/v0/management/workbuddy/quota?auth_index=' + encodeURIComponent(account.auth_index), { headers: headers() })
    .then(function (resp) {
      return resp.json().then(function (body) { return { ok: resp.ok, status: resp.status, body: body }; });
    })
    .then(function (result) {
      if (result.ok && result.body && !result.body.error) {
        state.quota[account.auth_index] = result.body;
        if (box) box.innerHTML = quotaBlock(result.body);
      } else {
        var message = (result.body && result.body.error) || ('HTTP ' + result.status);
        if (box) box.innerHTML = quotaError(message);
      }
    })
    .catch(function (err) {
      if (box) box.innerHTML = quotaError(String(err));
    })
    .then(function () { paintSummary(); });
}

function claim(index) {
  var account = state.accounts[index];
  if (!account || state.runs[index]) return Promise.resolve();
  setRunning(index, true);
  var button = document.getElementById('cb-' + index);
  if (button) button.disabled = true;
  return fetch('/v0/management/workbuddy/checkin?auth_index=' + encodeURIComponent(account.auth_index), {
    method: 'POST',
    headers: headers()
  })
    .then(function (resp) { return resp.json(); })
    .then(function (body) {
      var result = body && body.results && body.results[0];
      if (!result) { toast('签到失败：响应缺少结果', 'err'); return; }
      paintCheckin(index, result);
      if (result.state === 'claimed' && result.freshly_claimed) {
        toast((account.nickname || account.name) + ' 签到成功' +
          (typeof result.credit === 'number' ? '，获得 ' + fmtNumber(result.credit) + ' credits' : ''), 'ok');
        // A fresh claim changes the balance, so show the new number.
        return loadQuota(index);
      }
      if (result.state === 'claimed') toast((account.nickname || account.name) + ' 今天已经签过到了。', 'ok');
      else toast('签到未完成：' + (result.error || result.state), 'err');
    })
    .catch(function (err) { toast('签到请求失败：' + err, 'err'); })
    .then(function () {
      setRunning(index, false);
      // Re-derive the button from the verdict that is now stored, so a failed
      // attempt becomes clickable again and a successful one stays disabled.
      var current = state.accounts[index] && state.accounts[index].checkin;
      var done = Boolean(current && current.state === 'claimed');
      var refreshed = document.getElementById('cb-' + index);
      if (refreshed) {
        refreshed.disabled = done;
        refreshed.textContent = done ? '已签到' : '签到';
      }
    });
}

function claimAll(automatic) {
  if (!state.accounts.length) return Promise.resolve();
  setRunning('all', true);
  return fetch('/v0/management/workbuddy/checkin', { method: 'POST', headers: headers() })
    .then(function (resp) { return resp.json(); })
    .then(function (body) {
      var results = (body && body.results) || [];
      if (!results.length) { toast('签到失败：响应缺少结果', 'err'); return; }
      var fresh = 0;
      var claimed = 0;
      var failed = 0;
      var gains = 0;
      var quotaRefresh = [];
      results.forEach(function (result) {
        for (var i = 0; i < state.accounts.length; i += 1) {
          if (state.accounts[i].auth_index !== result.auth_index) continue;
          paintCheckin(i, result);
          if (result.state === 'claimed') claimed += 1;
          if (result.freshly_claimed) {
            fresh += 1;
            gains += Number(result.credit) || 0;
            quotaRefresh.push(i);
          }
          if (result.state === 'unknown') failed += 1;
          break;
        }
      });
      var head = automatic ? '自动签到：' : '签到：';
      if (fresh) {
        toast(head + fresh + ' 个账号领取成功' + (gains ? '，共 ' + fmtNumber(gains) + ' credits' : ''), 'ok');
      } else if (claimed === results.length) {
        toast(head + '今天已经全部签过到。', 'ok');
      } else {
        toast(head + claimed + ' 个已签到，' + failed + ' 个状态未知。', '');
      }
      return Promise.all(quotaRefresh.map(loadQuota));
    })
    .catch(function (err) { toast('签到请求失败：' + err, 'err'); })
    .then(function () { setRunning('all', false); });
}

function load() {
  var key = keyInput.value.trim();
  if (!key) { toast('请先填写管理密钥。', 'err'); return Promise.resolve(); }
  try { localStorage.setItem('wbaw_mgmt_key', key); } catch (e) {}
  setRunning('load', true);
  return fetch('/v0/management/workbuddy/accounts', { headers: headers() })
    .then(function (resp) {
      if (resp.status === 401 || resp.status === 403) throw new Error('管理密钥无效或权限不足（HTTP ' + resp.status + '）');
      if (!resp.ok) throw new Error('读取账号列表失败（HTTP ' + resp.status + '）');
      return resp.json();
    })
    .then(function (body) {
      state.accounts = (body && body.accounts) || [];
      state.quota = {};
      if (!state.accounts.length) {
        summaryBox.innerHTML = '';
        toastBox.style.display = 'none';
        out.innerHTML = '<div class="state"><div class="title">还没有 WorkBuddy 凭据</div>' +
          '<div>到面板左侧「OAuth 登录」页点击 WorkBuddy 卡片完成一次登录，再回到这里。</div></div>';
        return null;
      }
      out.innerHTML = state.accounts.map(accountShell).join('');
      paintSummary();
      return Promise.all(state.accounts.map(function (account, index) { return loadQuota(index); }));
    })
    .catch(function (err) {
      out.innerHTML = '<div class="state err"><div class="title">读取失败</div>' +
        '<div>' + esc(err && err.message ? err.message : String(err)) + '</div></div>';
    })
    .then(function () { setRunning('load', false); });
}

// The page claims on open because the plugin has no scheduler of its own: a
// token refresh also claims (see refreshAuth), but that only happens when a
// token is near expiry.
function boot() {
  if (!keyInput.value.trim()) return;
  load().then(function () { return claimAll(true); });
}

out.addEventListener('click', function (event) {
  var button = event.target.closest ? event.target.closest('button[data-index]') : null;
  if (!button) return;
  claim(Number(button.getAttribute('data-index')));
});

refreshButton.onclick = function () { load().then(function () { return claimAll(true); }); };
checkinAllButton.onclick = function () { claimAll(false); };
keyInput.addEventListener('keydown', function (event) { if (event.key === 'Enter') boot(); });

boot();
</script>
</body>
</html>`
}
