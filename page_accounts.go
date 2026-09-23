package main

// themePalette is the light/dark palette used by the plugin's page.
//
// The light values follow the management center's credential list, which this
// page is a per-provider version of: near-white canvas, white cards with a hair
// border, one saturated accent for actions and a green reserved for "healthy".
// Dark is an override because the panel can be showing either theme.
const themePalette = ` :root{
   --bg:#f5f6f8; --panel:#fff; --panel-2:#fafbfc; --line:#e6e8ec;
   --text:#1f2328; --muted:#6b7280; --faint:#9ca3af;
   --accent:#2563eb; --accent-ink:#fff;
   --ok:#16a34a; --ok-bg:#e9f7ef; --warn:#d97706; --warn-bg:#fef3e2;
   --danger:#dc2626; --danger-bg:#fdeaea; --info-bg:#eef2ff;
   --track:#eceef2; --bar:rgba(245,246,248,.86); --hover:#cbd2dc;
   --shadow:0 1px 2px rgba(16,24,40,.04),0 2px 8px rgba(16,24,40,.05);
   --shadow-lg:0 8px 28px rgba(16,24,40,.10);
 }
 [data-theme="dark"]{
   --bg:#0d1117; --panel:#161b22; --panel-2:#1b2028; --line:#2a3038;
   --text:#e6edf3; --muted:#8b949e; --faint:#6e7681;
   --accent:#4b83f0; --accent-ink:#fff;
   --ok:#3fb950; --ok-bg:#14301f; --warn:#d29922; --warn-bg:#33280f;
   --danger:#f85149; --danger-bg:#3a1d1d; --info-bg:#1b2337;
   --track:#242a33; --bar:rgba(13,17,23,.86); --hover:#3d444d;
   --shadow:0 1px 2px rgba(0,0,0,.4),0 3px 12px rgba(0,0,0,.28);
   --shadow-lg:0 10px 30px rgba(0,0,0,.45);
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
// It is laid out as a filtered card wall rather than a table, because quota is
// the thing being read: one bar per package, with what is left and when it
// resets, is legible at a glance where a numeric column is not.
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
 svg{display:block}
 .ico{width:15px;height:15px;stroke:currentColor;stroke-width:1.7;fill:none;
   stroke-linecap:round;stroke-linejoin:round;flex:0 0 auto}

 /* ── top bar ──────────────────────────────────────────────────────────── */
 header{position:sticky;top:0;z-index:6;display:flex;flex-wrap:wrap;gap:14px;
   align-items:center;justify-content:space-between;padding:14px 22px;
   background:var(--bar);backdrop-filter:blur(10px);border-bottom:1px solid var(--line)}
 .brand{display:flex;align-items:center;gap:12px;min-width:0}
 .logo{width:36px;height:36px;border-radius:10px;background:linear-gradient(135deg,#2563eb,#16a34a);
   display:flex;align-items:center;justify-content:center;color:#fff;font-weight:700;font-size:15px;
   letter-spacing:.5px;flex:0 0 auto}
 .brand h1{margin:0;font-size:15px;font-weight:650;letter-spacing:.2px}
 .brand p{margin:1px 0 0;font-size:12px;color:var(--muted)}
 .actions{display:flex;gap:8px;align-items:center;flex-wrap:wrap}
 button{display:inline-flex;align-items:center;gap:6px;padding:8px 13px;border-radius:9px;
   border:1px solid transparent;background:var(--accent);color:var(--accent-ink);font-size:13px;
   font-weight:500;cursor:pointer;transition:filter .15s,border-color .15s;font-family:inherit}
 button:hover{filter:brightness(1.07)}
 button:disabled{background:var(--panel-2);color:var(--faint);border-color:var(--line);cursor:not-allowed;filter:none}
 button.ghost{background:var(--panel);color:var(--text);border-color:var(--line)}
 button.ghost:hover{filter:none;border-color:var(--hover);background:var(--panel-2)}
 button.icon{padding:8px;border-radius:9px;background:var(--panel);color:var(--muted);border-color:var(--line)}
 button.icon:hover{color:var(--text);border-color:var(--hover)}
 input[type=password],input[type=search],select{padding:8px 11px;border-radius:9px;
   border:1px solid var(--line);background:var(--panel);color:var(--text);font-size:13px;
   outline:none;font-family:inherit}
 input:focus,select:focus{border-color:var(--accent);box-shadow:0 0 0 3px rgba(37,99,235,.14)}
 input[type=password]{width:190px} input[type=search]{width:100%}
 select{cursor:pointer}

 main{padding:20px 22px 44px;max-width:1240px;margin:0 auto}

 /* ── stat cards ───────────────────────────────────────────────────────── */
 .stats{display:grid;gap:12px;grid-template-columns:repeat(auto-fit,minmax(178px,1fr));margin-bottom:14px}
 .stat{background:var(--panel);border:1px solid var(--line);border-radius:13px;padding:14px 15px;
   box-shadow:var(--shadow)}
 .stat-top{display:flex;align-items:center;gap:8px;color:var(--muted);font-size:12.5px}
 .stat-num{font-size:25px;font-weight:650;letter-spacing:-.5px;margin:6px 0 2px;
   font-variant-numeric:tabular-nums}
 .stat-sub{font-size:11.5px;color:var(--faint);line-height:1.35}
 .dot{width:22px;height:22px;border-radius:7px;display:flex;align-items:center;justify-content:center;flex:0 0 auto}
 .dot .ico{width:13px;height:13px}
 .dot.ok{background:var(--ok-bg);color:var(--ok)}
 .dot.warn{background:var(--warn-bg);color:var(--warn)}
 .dot.err{background:var(--danger-bg);color:var(--danger)}
 .dot.info{background:var(--info-bg);color:var(--accent)}
 .dot.plain{background:var(--panel-2);color:var(--muted)}

 /* ── filters ──────────────────────────────────────────────────────────── */
 .filters{background:var(--panel);border:1px solid var(--line);border-radius:13px;
   padding:12px 14px;box-shadow:var(--shadow);margin-bottom:14px}
 .chips{display:flex;gap:8px;flex-wrap:wrap;margin-bottom:10px}
 .chip{display:inline-flex;align-items:center;gap:6px;padding:5px 11px;border-radius:999px;
   border:1px solid var(--line);background:var(--panel);color:var(--muted);font-size:12.5px;
   cursor:pointer;user-select:none;transition:border-color .15s,color .15s,background .15s;white-space:nowrap}
 .chip:hover{border-color:var(--hover);color:var(--text)}
 .chip b{font-weight:650;color:var(--text)}
 .chip[aria-pressed=true]{background:var(--ok);border-color:var(--ok);color:#fff}
 .chip[aria-pressed=true] b{color:#fff}
 .filters-row{display:flex;gap:8px;align-items:center;flex-wrap:wrap}
 .filters-row .grow{flex:1 1 260px;min-width:200px}
 .listhead{display:flex;align-items:center;justify-content:space-between;gap:12px;
   flex-wrap:wrap;margin:0 2px 12px;color:var(--muted);font-size:12.5px}
 .listhead b{color:var(--text)}

 /* ── account cards ────────────────────────────────────────────────────── */
 .grid{display:grid;gap:14px;grid-template-columns:repeat(auto-fill,minmax(330px,1fr))}
 /* A state block is a page-level message, not a card: let it span the wall
    instead of sitting in the first column. */
 .grid>.state{grid-column:1/-1}
 .card{background:var(--panel);border:1px solid var(--line);border-radius:14px;
   box-shadow:var(--shadow);display:flex;flex-direction:column;overflow:hidden;
   transition:border-color .15s,box-shadow .15s}
 .card:hover{border-color:var(--hover);box-shadow:var(--shadow-lg)}
 .card-head{display:flex;gap:12px;align-items:flex-start;padding:15px 16px 12px}
 .avatar{width:40px;height:40px;border-radius:12px;flex:0 0 auto;display:flex;align-items:center;
   justify-content:center;color:#fff;font-weight:650;font-size:16px;letter-spacing:.5px}
 .who{flex:1 1 auto;min-width:0}
 .who strong{display:block;font-size:14.5px;font-weight:650;line-height:1.3;
   overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
 .who span{display:block;font-size:11.5px;color:var(--faint);margin-top:2px;
   overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
 .pill{display:inline-flex;align-items:center;gap:5px;padding:3px 9px;border-radius:999px;
   font-size:11.5px;font-weight:600;white-space:nowrap;flex:0 0 auto}
 .pill i{width:6px;height:6px;border-radius:50%;background:currentColor;display:block}
 .pill.ok{background:var(--ok-bg);color:var(--ok)}
 .pill.warn{background:var(--warn-bg);color:var(--warn)}
 .pill.err{background:var(--danger-bg);color:var(--danger)}
 .pill.plain{background:var(--panel-2);color:var(--muted)}


 .quota{padding:13px 16px 4px}
 .quota h4{margin:0 0 10px;font-size:12px;font-weight:600;color:var(--muted);
   display:flex;align-items:center;justify-content:space-between;gap:8px}
 .quota h4 span{color:var(--faint);font-weight:400}
 .pkg{margin-bottom:13px}
 .pkg:last-child{margin-bottom:6px}
 .pkg-top{display:flex;align-items:baseline;justify-content:space-between;gap:8px;margin-bottom:6px}
 .pkg-name{font-size:12.5px;font-weight:600;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
 .pkg-pct{font-size:12px;color:var(--muted);font-variant-numeric:tabular-nums;flex:0 0 auto}
 .pkg-pct.warn{color:var(--warn);font-weight:600} .pkg-pct.err{color:var(--danger);font-weight:600}
 .bar{height:9px;border-radius:999px;background:var(--track);overflow:hidden}
 .bar i{display:block;height:100%;border-radius:999px;transition:width .4s ease}
 .pkg-foot{display:flex;align-items:center;justify-content:space-between;gap:8px;margin-top:5px;
   font-size:11.5px;color:var(--faint)}
 .pkg-foot .soon{color:var(--warn);font-weight:600}

 .quota-total{display:flex;align-items:baseline;gap:6px;font-size:12.5px;color:var(--muted);
   margin-bottom:11px}
 .quota-total b{color:var(--text);font-weight:650;font-variant-numeric:tabular-nums}
 .foot-note{font-size:11.5px;color:var(--faint);white-space:nowrap}
 .card-foot{display:flex;align-items:center;gap:8px;padding:11px 16px;margin-top:auto;
   border-top:1px solid var(--line);background:var(--panel-2)}
 .card-foot .spacer{flex:1 1 auto}
 .state{background:var(--panel);border:1px solid var(--line);border-left:3px solid var(--accent);
   border-radius:12px;padding:15px 17px;color:var(--muted);box-shadow:var(--shadow)}
 .state.err{border-left-color:var(--danger)}
 .state .title{color:var(--text);font-weight:600;margin-bottom:5px}
 .inline-state{border:none;border-radius:0;box-shadow:none;background:transparent;
   padding:6px 0 14px;border-left:none;font-size:12.5px;color:var(--muted)}
 .inline-state.err{color:var(--danger)}
 .loading{display:flex;align-items:center;gap:9px;color:var(--muted);font-size:12.5px;padding:6px 0 14px}
 .spinner{width:14px;height:14px;border-radius:50%;border:2px solid var(--track);
   border-top-color:var(--accent);animation:spin .8s linear infinite;flex:0 0 auto}
 @keyframes spin{to{transform:rotate(360deg)}}
 .toast{padding:10px 14px;border-radius:11px;background:var(--panel);border:1px solid var(--line);
   border-left:3px solid var(--accent);color:var(--muted);box-shadow:var(--shadow);margin-bottom:14px}
 .toast.ok{border-left-color:var(--ok)} .toast.err{border-left-color:var(--danger)}
 .foot{margin-top:26px;color:var(--faint);font-size:12px;text-align:center;line-height:1.85}
 .sr-only{position:absolute;width:1px;height:1px;margin:-1px;padding:0;overflow:hidden;
   clip:rect(0 0 0 0);white-space:nowrap;border:0}
 @media (max-width:640px){
   input[type=password]{width:130px}
 }
</style>
<script>` + themeBootScript + `</script>
</head>
<body>
<header>
  <div class="brand">
    <div class="logo">WB</div>
    <div>
      <h1>WorkBuddy 账号</h1>
      <p>额度与每日签到</p>
    </div>
  </div>
  <div class="actions">
    <label class="sr-only" for="key">管理密钥</label>
    <input id="key" type="password" placeholder="管理密钥" autocomplete="off" spellcheck="false">
    <button id="checkin-all" class="ghost" type="button"><svg class="ico" viewBox="0 0 24 24"><path d="M20 6 9 17l-5-5"/></svg>全部签到</button>
    <button id="refresh" type="button"><svg class="ico" viewBox="0 0 24 24"><path d="M21 12a9 9 0 1 1-2.6-6.4M21 3v6h-6"/></svg>刷新</button>
  </div>
</header>
<main>
  <div id="stats" class="stats"></div>

  <section class="filters" id="filters" style="display:none">
    <div id="chips" class="chips"></div>
    <div class="filters-row">
      <div class="grow">
        <label class="sr-only" for="query">搜索账号</label>
        <input id="query" type="search" placeholder="搜索账号 / UID / 文件名 / 套餐" autocomplete="off">
      </div>
      <label class="sr-only" for="sort">排序</label>
      <select id="sort">
        <option value="risk">额度最紧张优先</option>
        <option value="name">按账号名称</option>
        <option value="checkin">未签到优先</option>
      </select>
    </div>
  </section>

  <div id="toast" class="toast" style="display:none"></div>
  <div id="listhead" class="listhead"></div>
  <div id="out" class="grid" aria-live="polite"></div>

  <div class="foot">
    密钥仅保存在本机 localStorage，不会发送给插件本身。<br>
    签到状态为「未知」表示上游当前没有签到活动，其状态字段整块归零；点「签到」由领取接口给出权威结论，重复领取是安全的。
  </div>
</main>
<script>
var out = document.getElementById('out');
var statsBox = document.getElementById('stats');
var chipsBox = document.getElementById('chips');
var listheadBox = document.getElementById('listhead');
var toastBox = document.getElementById('toast');
var keyInput = document.getElementById('key');
var queryInput = document.getElementById('query');
var sortSelect = document.getElementById('sort');
var refreshButton = document.getElementById('refresh');
var checkinAllButton = document.getElementById('checkin-all');

// quota holds what came back per credential, errors holds the ones that failed,
// and pending tracks in-flight reads so a card can show a spinner without the
// whole list re-rendering.
var state = { accounts: [], quota: {}, errors: {}, pending: {}, runs: {}, filter: 'all', query: '', sort: 'risk' };

// Summary and filter chrome cost attention, so they only appear once there are
// enough accounts that the wall cannot be read at a glance. With a single
// account a card saying "总账号 1", a filter row whose every entry reads "1" and a
// sort control that cannot reorder anything are furniture.
var SUMMARY_MIN_ACCOUNTS = 4;
var FILTER_MIN_ACCOUNTS = 6;

try { keyInput.value = localStorage.getItem('wbaw_mgmt_key') || ''; } catch (e) {}

var ICONS = {
  user: '<path d="M20 21v-2a4 4 0 0 0-4-4H8a4 4 0 0 0-4 4v2"/><circle cx="12" cy="7" r="4"/>',
  check: '<path d="M20 6 9 17l-5-5"/>',
  alert: '<path d="M12 9v4M12 17h.01"/><path d="M10.3 3.9 1.8 18a2 2 0 0 0 1.7 3h17a2 2 0 0 0 1.7-3L13.7 3.9a2 2 0 0 0-3.4 0Z"/>',
  clock: '<circle cx="12" cy="12" r="9"/><path d="M12 7v5l3 2"/>',
  wallet: '<path d="M19 7V5a2 2 0 0 0-2-2H5a2 2 0 0 0 0 4h14a1 1 0 0 1 1 1v9a1 1 0 0 1-1 1H5a2 2 0 0 1-2-2V5"/><path d="M16 12h.01"/>',
  chart: '<path d="M3 3v18h18"/><path d="M7 14l3-4 3 3 4-6"/>',
  box: '<path d="M21 8 12 3 3 8l9 5 9-5Z"/><path d="M3 8v8l9 5 9-5V8"/>',
  refresh: '<path d="M21 12a9 9 0 1 1-2.6-6.4M21 3v6h-6"/>',
  shield: '<path d="M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10Z"/>'
};

function svg(name) {
  return '<svg class="ico" viewBox="0 0 24 24">' + (ICONS[name] || '') + '</svg>';
}

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
  if (percent <= 15) return 'err';
  if (percent <= 40) return 'warn';
  return 'ok';
}

function toneColor(percent) {
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

// remainingPercent is one account's headline number: what is left across every
// package, as a percentage. Returns null when nothing is known yet, so the card
// can say so instead of rendering 0%.
function remainingPercent(quota) {
  if (!quota) return null;
  var remain = metricOf(quota.summary, 'remain');
  var total = metricOf(quota.summary, 'total');
  if (!remain || !total || !(Number(total.value) > 0)) return null;
  return Math.max(0, Math.min(100, (Number(remain.value) / Number(total.value)) * 100));
}

// avatarTint gives each account a stable colour, so two accounts are told apart
// by the same visual cue every time the page is opened.
var TINTS = ['#2563eb', '#16a34a', '#d97706', '#db2777', '#7c3aed', '#0891b2', '#dc2626', '#4b5563'];
function avatarTint(key) {
  var sum = 0;
  var text = String(key || '');
  for (var i = 0; i < text.length; i += 1) sum = (sum * 31 + text.charCodeAt(i)) % 9973;
  return TINTS[sum % TINTS.length];
}

function checkinView(checkin) {
  if (!checkin) return { cls: 'plain', text: '状态未知', detail: '', done: false, claimed: false };
  if (checkin.state === 'claimed') {
    var gain = checkin.freshly_claimed && typeof checkin.credit === 'number'
      ? ' +' + fmtNumber(checkin.credit) : '';
    var streak = checkin.streak_days ? '连续 ' + checkin.streak_days + ' 天' : '';
    return { cls: 'ok', text: '今日已签到' + gain, detail: streak, done: true, claimed: true };
  }
  if (checkin.state === 'unclaimed') {
    return { cls: 'warn', text: '今日未签到', detail: '', done: false, claimed: false };
  }
  return { cls: 'plain', text: '状态未知', detail: checkin.error || '', done: false, claimed: false };
}

// credentialView describes the stored token, which decides whether the account
// can be used at all.
function credentialView(account) {
  if (account.token_state === 'expired') return { cls: 'err', text: '凭据已过期' };
  if (account.token_state === 'missing') return { cls: 'err', text: '缺少 token' };
  if (account.token_state) return { cls: 'ok', text: '可用' };
  return { cls: 'plain', text: '状态未知' };
}

function accountLabel(account) {
  return account.nickname || account.label || account.name || 'WorkBuddy';
}

// riskScore orders the wall: the account closest to running out comes first,
// because that is what the reader came for. Unknown quota sorts after known.
function riskScore(account) {
  var percent = remainingPercent(state.quota[account.auth_index]);
  return percent === null ? 101 : percent;
}

function matchesQuery(account) {
  if (!state.query) return true;
  var quota = state.quota[account.auth_index];
  var names = (quota && quota.groups ? quota.groups.map(function (group) { return group.displayName || ''; }) : []).join(' ');
  var haystack = [accountLabel(account), account.name, account.uid, account.enterprise_id, account.region, names]
    .join(' ').toLowerCase();
  return haystack.indexOf(state.query) !== -1;
}

function matchesFilter(account) {
  if (state.filter === 'all') return true;
  var credential = credentialView(account);
  var checkin = checkinView(account.checkin);
  var percent = remainingPercent(state.quota[account.auth_index]);
  if (state.filter === 'usable') return credential.cls === 'ok';
  if (state.filter === 'attention') return credential.cls === 'err';
  if (state.filter === 'risk') return percent !== null && percent <= 40;
  if (state.filter === 'claimed') return checkin.claimed;
  return true;
}

function visibleAccounts() {
  var list = state.accounts.filter(function (account) {
    return matchesFilter(account) && matchesQuery(account);
  });
  if (state.sort === 'risk') {
    list.sort(function (a, b) { return riskScore(a) - riskScore(b); });
  } else if (state.sort === 'checkin') {
    list.sort(function (a, b) { return Number(checkinView(a.checkin).claimed) - Number(checkinView(b.checkin).claimed); });
  } else {
    list.sort(function (a, b) { return accountLabel(a).localeCompare(accountLabel(b), 'zh-CN'); });
  }
  return list;
}

// pkgRows renders one bar per package: name, what is left, and when it resets.
// Package figures come from the description the plugin normalized ("remain /
// total"), so the bar and the numbers can never disagree.
function pkgRows(quota) {
  var groups = (quota.groups || []).slice().sort(function (a, b) {
    return fractionOf(a) - fractionOf(b);
  });
  if (!groups.length) return '';
  return groups.map(function (group) {
    var bucket = (group.buckets || [])[0] || {};
    var parsed = splitAmount(bucket.description);
    var fraction = typeof bucket.remainingFraction === 'number' ? bucket.remainingFraction : null;
    var percent = fraction === null ? 0 : Math.max(0, Math.min(100, fraction * 100));
    var reset = bucket.resetTime || bucket.window || '';
    var left = countdown(reset);
    var name = group.displayName || 'WorkBuddy';
    return '<div class="pkg">' +
      '<div class="pkg-top">' +
        '<span class="pkg-name" title="' + esc(name) + '">' + esc(name) + '</span>' +
        '<span class="pkg-pct ' + tone(percent) + '">剩余 ' + percent.toFixed(percent % 1 ? 1 : 0) + '%</span>' +
      '</div>' +
      '<div class="bar" title="' + esc(parsed ? fmtNumber(parsed.remain) + ' / ' + fmtNumber(parsed.total) : '') + '">' +
        '<i style="width:' + percent.toFixed(1) + '%;background:' + toneColor(percent) + '"></i>' +
      '</div>' +
      '<div class="pkg-foot">' +
        '<span>' + (parsed ? esc(fmtNumber(parsed.remain)) + ' / ' + esc(fmtNumber(parsed.total)) : '额度未知') + '</span>' +
        '<span title="' + esc(reset) + '">' + esc(fmtDate(reset)) +
          (left ? ' <span class="soon">· ' + esc(left) + '</span>' : '') + '</span>' +
      '</div>' +
    '</div>';
  }).join('');
}

function quotaBody(account) {
  var key = account.auth_index;
  if (state.pending[key]) {
    return '<div class="quota"><div class="loading"><span class="spinner"></span>正在读取额度…</div></div>';
  }
  var quota = state.quota[key];
  if (!quota) {
    var message = state.errors[key] || '额度未知';
    return '<div class="quota"><div class="inline-state err">' + esc(message) +
      ' <button class="ghost" type="button" data-quota="' + esc(key) + '">重试</button></div></div>';
  }
  var groups = quota.groups || [];
  if (!groups.length) {
    return '<div class="quota"><div class="inline-state">上游没有返回套餐明细。</div></div>';
  }
  var totalLine = '';
  if (groups.length > 1) {
    var remainMetric = metricOf(quota.summary, 'remain');
    var totalMetric = metricOf(quota.summary, 'total');
    var unit = (remainMetric && remainMetric.unit) ? ' ' + remainMetric.unit : '';
    totalLine = '<div class="quota-total">合计 <b>' +
      esc(fmtNumber(remainMetric ? remainMetric.value : null)) + '</b> / ' +
      esc(fmtNumber(totalMetric ? totalMetric.value : null)) + esc(unit) + '</div>';
  }
  return '<div class="quota">' + totalLine + pkgRows(quota) + '</div>';
}

function accountCard(account) {
  var key = account.auth_index;
  var credential = credentialView(account);
  var view = checkinView(account.checkin);
  var label = accountLabel(account);
  var initial = String(label).trim().charAt(0).toUpperCase() || 'W';
  // The expiry is the one fact worth a line under the name: a credential that
  // stops refreshing shows up here before it shows up as a failed request. It
  // replaced the credential filename, which was an opaque id that told the
  // reader nothing about which account they were looking at.
  var sub = account.expires_at ? '到期 ' + fmtStamp(account.expires_at) : '';
  // The check-in state is stated once, by the button, and the credential state
  // once, by the pill. The tag row that used to repeat both (区域/UID/企业/签到)
  // was removed: the region is a setting, not a per-account fact, and a uid tells
  // the reader less than the name above it.
  return '<article class="card" data-key="' + esc(key) + '">' +
    '<div class="card-head">' +
      '<div class="avatar" style="background:' + avatarTint(key) + '">' + esc(initial) + '</div>' +
      '<div class="who"><strong title="' + esc(label) + '">' + esc(label) + '</strong>' +
        (sub ? '<span title="' + esc(sub) + '">' + esc(sub) + '</span>' : '') + '</div>' +
      '<span class="pill ' + credential.cls + '"><i></i>' + esc(credential.text) + '</span>' +
    '</div>' +
    quotaBody(account) +
    '<div class="card-foot">' +
      '<button class="icon" type="button" title="刷新额度" aria-label="刷新额度" data-quota="' + esc(key) + '">' +
        svg('refresh') + '</button>' +
      (view.detail ? '<span class="foot-note">' + esc(view.detail) + '</span>' : '') +
      '<span class="spacer"></span>' +
      '<button class="' + (view.done ? 'ghost' : '') + '" type="button" data-checkin="' + esc(key) + '"' +
        (view.done ? ' disabled' : '') + '>' + svg('check') + (view.done ? '已签到' : '签到') + '</button>' +
    '</div>' +
  '</article>';
}

// paintStats is the summary strip. "额度告警" counts the accounts at or under
// 40% remaining, which is the same threshold the bars change colour at, so the
// number and the wall agree.
// paintStats is the summary strip.
//
// Only counters that can change a decision are shown. A check-in state of
// "unknown" is deliberately not one of them: it is the normal answer whenever no
// check-in activity is running (the footer says as much), so a card for it reads
// like a fault while meaning "nothing to do". The alarm counters are shown only
// when they are non-zero, because a permanent 0 is a card that never says
// anything; their absence is the all-clear.
function paintStats() {
  if (!state.accounts.length) { statsBox.innerHTML = ''; listheadBox.innerHTML = ''; return; }
  var usable = 0;
  var attention = 0;
  var risk = 0;
  var claimed = 0;
  var remain = 0;
  var total = 0;
  state.accounts.forEach(function (account) {
    var credential = credentialView(account);
    var view = checkinView(account.checkin);
    var percent = remainingPercent(state.quota[account.auth_index]);
    var quota = state.quota[account.auth_index];
    if (credential.cls === 'ok') usable += 1;
    if (credential.cls === 'err') attention += 1;
    if (percent !== null && percent <= 40) risk += 1;
    if (view.claimed) claimed += 1;
    if (quota) {
      var remainMetric = metricOf(quota.summary, 'remain');
      var totalMetric = metricOf(quota.summary, 'total');
      if (remainMetric) remain += Number(remainMetric.value) || 0;
      if (totalMetric) total += Number(totalMetric.value) || 0;
    }
  });
  var balance = total > 0
    ? '<div class="stat-sub">合计 ' + fmtNumber(remain) + ' / ' + fmtNumber(total) + ' credits</div>'
    : '<div class="stat-sub">等待额度读取</div>';
  function card(tone, icon, label, value, sub) {
    return '<div class="stat"><div class="stat-top"><span class="dot ' + tone + '">' + svg(icon) +
      '</span>' + label + '</div><div class="stat-num">' + value + '</div>' +
      '<div class="stat-sub">' + sub + '</div></div>';
  }
  var cards = [];
  if (state.accounts.length >= SUMMARY_MIN_ACCOUNTS) {
    cards.push(card('plain', 'user', '总账号', state.accounts.length, balance.replace(/^<div class="stat-sub">|<\/div>$/g, '')));
    cards.push(card('ok', 'check', '可用', usable, '凭据有效、可直接调用'));
    if (attention) cards.push(card('err', 'alert', '需处理', attention, '凭据过期或缺少 token'));
    if (risk) cards.push(card('warn', 'chart', '额度告警', risk, '剩余 40% 及以下'));
    cards.push(card('ok', 'clock', '今日已签到', claimed,
      (state.accounts.length - claimed) + ' 个账号尚未签到'));
  }
  statsBox.innerHTML = cards.join('');

  // The same rule for the filters: a category with nothing in it is not a
  // filter, it is a button that can only ever produce an empty list, and a whole
  // filter row is not worth its space until the list is long enough to search.
  var counts = { all: state.accounts.length, usable: usable, attention: attention, risk: risk, claimed: claimed };
  var labels = [
    ['all', '全部'], ['usable', '可用'], ['attention', '需处理'],
    ['risk', '额度告警'], ['claimed', '已签到']
  ];
  var filterable = state.accounts.length >= FILTER_MIN_ACCOUNTS;
  document.getElementById('filters').style.display = filterable ? '' : 'none';
  if (!filterable) {
    // Clearing the inputs matters as much as hiding them: a query left over from
    // a larger fleet would keep filtering a list the reader can no longer see the
    // control for.
    chipsBox.innerHTML = '';
    state.filter = 'all';
    state.query = '';
    queryInput.value = '';
    return;
  }
  var active = labels.filter(function (pair) {
    return pair[0] === 'all' || pair[0] === 'usable' || pair[0] === 'claimed' || counts[pair[0]] > 0;
  });
  // A filter whose category emptied out must not stay selected, or the wall
  // would keep rendering an empty list with no way back except a reload.
  if (!active.some(function (pair) { return pair[0] === state.filter; })) state.filter = 'all';
  chipsBox.innerHTML = active.map(function (pair) {
    return '<button class="chip" type="button" aria-pressed="' + (state.filter === pair[0]) +
      '" data-filter="' + pair[0] + '">' + pair[1] + ' <b>' + counts[pair[0]] + '</b></button>';
  }).join('');
}

function renderList() {
  var list = visibleAccounts();
  if (!state.accounts.length) { out.innerHTML = ''; return; }
  if (!list.length) {
    out.innerHTML = '<div class="state"><div class="title">没有符合筛选条件的账号</div>' +
      '<div>换个筛选条件或清空搜索框再试。</div></div>';
  } else {
    out.innerHTML = list.map(function (account) {
      return accountCard(account);
    }).join('');
  }
  // A count of what is on screen only means something when the screen is not
  // showing everything, so it appears with a filter or a search and not before.
  var narrowed = state.filter !== 'all' || Boolean(state.query);
  listheadBox.innerHTML = narrowed
    ? '<div>显示 <b>' + list.length + '</b> / ' + state.accounts.length + ' 个账号</div>'
    : '';
}

// render repaints everything the list depends on. Cards are rebuilt from state
// rather than patched in place, so filtering, sorting and a quota refresh can
// never leave the wall showing a number that no longer matches the data.
function render() {
  paintStats();
  renderList();
}

function paintCheckin(authIndex, checkin) {
  for (var i = 0; i < state.accounts.length; i += 1) {
    if (state.accounts[i].auth_index !== authIndex) continue;
    state.accounts[i].checkin = checkin;
    break;
  }
  render();
}

function setRunning(key, running) {
  state.runs[key] = running;
  var busy = Object.keys(state.runs).some(function (item) { return state.runs[item]; });
  checkinAllButton.disabled = busy;
  refreshButton.disabled = busy;
}

function accountByAuthIndex(authIndex) {
  for (var i = 0; i < state.accounts.length; i += 1) {
    if (state.accounts[i].auth_index === authIndex) return state.accounts[i];
  }
  return null;
}

function loadQuota(authIndex, quiet) {
  var account = accountByAuthIndex(authIndex);
  if (!account) return Promise.resolve();
  state.pending[authIndex] = true;
  delete state.errors[authIndex];
  if (!quiet) render();
  return fetch('/v0/management/workbuddy/quota?auth_index=' + encodeURIComponent(authIndex), { headers: headers() })
    .then(function (resp) {
      return resp.json().then(function (body) { return { ok: resp.ok, status: resp.status, body: body }; });
    })
    .then(function (result) {
      if (result.ok && result.body && !result.body.error) {
        state.quota[authIndex] = result.body;
      } else {
        state.errors[authIndex] = (result.body && result.body.error) || ('HTTP ' + result.status);
      }
    })
    .catch(function (err) {
      state.errors[authIndex] = String(err);
    })
    .then(function () {
      delete state.pending[authIndex];
      render();
    });
}

function claim(authIndex) {
  var account = accountByAuthIndex(authIndex);
  if (!account || state.runs[authIndex]) return Promise.resolve();
  setRunning(authIndex, true);
  return fetch('/v0/management/workbuddy/checkin?auth_index=' + encodeURIComponent(authIndex), {
    method: 'POST',
    headers: headers()
  })
    .then(function (resp) { return resp.json(); })
    .then(function (body) {
      var result = body && body.results && body.results[0];
      if (!result) { toast('签到失败：响应缺少结果', 'err'); return; }
      paintCheckin(authIndex, result);
      if (result.state === 'claimed' && result.freshly_claimed) {
        toast(accountLabel(account) + ' 签到成功' +
          (typeof result.credit === 'number' ? '，获得 ' + fmtNumber(result.credit) + ' credits' : ''), 'ok');
        // A fresh claim changes the balance, so show the new number.
        return loadQuota(authIndex, true);
      }
      if (result.state === 'claimed') toast(accountLabel(account) + ' 今天已经签过到了。', 'ok');
      else toast('签到未完成：' + (result.error || result.state), 'err');
    })
    .catch(function (err) { toast('签到请求失败：' + err, 'err'); })
    .then(function () { setRunning(authIndex, false); });
}

function claimAll() {
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
          state.accounts[i].checkin = result;
          if (result.state === 'claimed') claimed += 1;
          if (result.freshly_claimed) {
            fresh += 1;
            gains += Number(result.credit) || 0;
            quotaRefresh.push(result.auth_index);
          }
          if (result.state === 'unknown') failed += 1;
          break;
        }
      });
      render();
      var head = '签到：';
      if (fresh) {
        toast(head + fresh + ' 个账号领取成功' + (gains ? '，共 ' + fmtNumber(gains) + ' credits' : ''), 'ok');
      } else if (claimed === results.length) {
        toast(head + '今天已经全部签过到。', 'ok');
      } else {
        toast(head + claimed + ' 个已签到，' + failed + ' 个状态未知。', '');
      }
      return Promise.all(quotaRefresh.map(function (key) { return loadQuota(key, true); }));
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
      state.errors = {};
      state.pending = {};
      if (!state.accounts.length) {
        paintStats();
        out.innerHTML = '';
        toastBox.style.display = 'none';
        listheadBox.innerHTML = '';
        out.innerHTML = '<div class="state"><div class="title">还没有 WorkBuddy 凭据</div>' +
          '<div>到面板左侧「OAuth 登录」页点击 WorkBuddy 卡片完成一次登录，再回到这里。</div></div>';
        return null;
      }
      render();
      return Promise.all(state.accounts.map(function (account) { return loadQuota(account.auth_index, true); }));
    })
    .catch(function (err) {
      out.innerHTML = '<div class="state err"><div class="title">读取失败</div>' +
        '<div>' + esc(err && err.message ? err.message : String(err)) + '</div></div>';
    })
    .then(function () { setRunning('load', false); });
}

// The page does not claim on open. Nothing upstream requires the bonus to be
// taken the moment the page loads, and a page that is labelled as a view should
// not mutate an account just because it was opened. Claiming is the two buttons.
func boot() {
  if (!keyInput.value.trim()) return;
  load();
}

out.addEventListener('click', function (event) {
  if (!event.target.closest) return;
  var quotaButton = event.target.closest('button[data-quota]');
  if (quotaButton) {
    loadQuota(quotaButton.getAttribute('data-quota'));
    return;
  }
  var checkinButton = event.target.closest('button[data-checkin]');
  if (checkinButton) claim(checkinButton.getAttribute('data-checkin'));
});

chipsBox.addEventListener('click', function (event) {
  if (!event.target.closest) return;
  var chip = event.target.closest('button[data-filter]');
  if (!chip) return;
  state.filter = chip.getAttribute('data-filter');
  render();
});

queryInput.addEventListener('input', function () {
  state.query = queryInput.value.trim().toLowerCase();
  renderList();
});

sortSelect.addEventListener('change', function () {
  state.sort = sortSelect.value;
  renderList();
});

// Refresh reloads only. It used to also claim every account's daily bonus, which
// made a button labelled "刷新" a mutating action; claiming has its own button.
refreshButton.onclick = function () { load(); };
checkinAllButton.onclick = function () { claimAll(); };
keyInput.addEventListener('keydown', function (event) { if (event.key === 'Enter') boot(); });

boot();
</script>
</body>
</html>`
}
