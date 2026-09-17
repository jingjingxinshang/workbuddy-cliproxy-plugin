package main

// quotaPageHTML renders WorkBuddy quota in the plugin's own resource page.
//
// The host management panel renders quota only for its six built-in providers
// (QuotaProviderType is a closed union), so a plugin provider has no place in
// that UI: clicking the panel's refresh button resolves no adapter and the UI
// falls back to its generic "unknown error" text. This page is the supported
// alternative — it runs on the plugin's own resource route and calls the
// plugin's own management route with the management key the operator supplies.
func quotaPageHTML() string {
	return `<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>WorkBuddy 额度</title>
<style>
 body{font:14px/1.6 -apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif;margin:0;padding:28px 22px;background:#111;color:#ddd}
 h1{font-size:19px;margin:0 0 4px}
 .sub{color:#999;margin-bottom:20px;font-size:13px}
 label{display:block;margin:14px 0 6px;color:#bbb;font-size:13px}
 input{width:100%;box-sizing:border-box;padding:9px 10px;border-radius:6px;border:1px solid #333;background:#1a1a1a;color:#eee;font-size:14px}
 button{margin-top:16px;padding:9px 15px;border-radius:6px;border:0;background:#2f6feb;color:#fff;font-size:13px;cursor:pointer}
 button:disabled{background:#333;color:#888;cursor:not-allowed}
 .card{margin-top:16px;padding:14px 16px;border-radius:8px;background:#1a1a1a;border-left:3px solid #2f6feb}
 .card.err{border-left-color:#e78a8a;color:#f0b0b0}
 .name{font-size:15px;font-weight:600;margin-bottom:8px}
 .big{font-size:22px;font-weight:600}
 .muted{color:#999;font-size:13px}
 .bar{height:8px;border-radius:4px;background:#333;margin:10px 0 6px;overflow:hidden}
 .bar>span{display:block;height:100%;background:#6ee7a8}
 table{width:100%;border-collapse:collapse;margin-top:10px;font-size:13px}
 th,td{text-align:left;padding:5px 6px;border-bottom:1px solid #262626;color:#ccc}
 th{color:#8a8a8a;font-weight:500}
 code{background:#222;padding:1px 5px;border-radius:4px}
</style>
</head>
<body>
<h1>WorkBuddy 额度</h1>
<div class="sub">插件资源页 &middot; provider <code>workbuddy</code></div>

<label for="key">管理密钥（仅保存在本机浏览器）</label>
<input id="key" type="password" placeholder="remote-management.secret-key" autocomplete="off">
<button id="refresh">刷新额度</button>

<div id="out"></div>

<script>
var out = document.getElementById('out');
var keyInput = document.getElementById('key');
var refreshButton = document.getElementById('refresh');
var savedKey = '';

try { savedKey = localStorage.getItem('wbaw_mgmt_key') || ''; } catch (e) { savedKey = ''; }
if (savedKey) { keyInput.value = savedKey; }

function headers() { return { 'Authorization': 'Bearer ' + keyInput.value.trim() }; }

function esc(text) {
  return String(text == null ? '' : text)
    .replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;');
}

function renderEmpty(message) {
  out.innerHTML = '<div class="card err">' + esc(message) + '</div>';
}

function renderCard(file, quota) {
  var summary = quota.summary || [];
  var remain = null;
  var total = null;
  summary.forEach(function (metric) {
    if (metric.key === 'remain') remain = metric.value;
    if (metric.key === 'total') total = metric.value;
  });
  var percent = 0;
  if (typeof total === 'number' && total > 0 && typeof remain === 'number') {
    percent = Math.max(0, Math.min(100, (remain / total) * 100));
  }

  var rows = '';
  (quota.groups || []).forEach(function (group) {
    (group.buckets || []).forEach(function (bucket) {
      var fraction = bucket.remainingFraction;
      var percentText = (typeof fraction === 'number') ? (fraction * 100).toFixed(1) + '%' : '-';
      rows += '<tr><td>' + esc(group.displayName || '') + '</td><td>' + esc(bucket.description || '') +
        '</td><td>' + percentText + '</td><td>' + esc(bucket.resetTime || bucket.window || '') + '</td></tr>';
    });
  });

  var plan = (quota.subscription && quota.subscription.plan) ? quota.subscription.plan : '';
  var title = file.label || file.name;

  return '<div class="card">' +
    '<div class="name">' + esc(title) + '</div>' +
    '<div class="big">' + esc(remain === null ? '-' : remain) + ' / ' + esc(total === null ? '-' : total) + '</div>' +
    '<div class="muted">' + (plan ? esc(plan) + ' &middot; ' : '') + '剩余 ' + percent.toFixed(1) + '%</div>' +
    '<div class="bar"><span style="width:' + percent.toFixed(1) + '%"></span></div>' +
    (rows ? '<table><tr><th>套餐</th><th>用量</th><th>剩余</th><th>重置时间</th></tr>' + rows + '</table>' : '') +
    '<div class="muted" style="margin-top:8px">凭据：' + esc(file.name) + '</div>' +
    '</div>';
}

refreshButton.onclick = function () {
  var key = keyInput.value.trim();
  if (!key) { renderEmpty('请先填写管理密钥。'); return; }
  try { localStorage.setItem('wbaw_mgmt_key', key); } catch (e) {}
  refreshButton.disabled = true;
  out.innerHTML = '<div class="card">正在读取…</div>';

  fetch('/v0/management/auth-files', { headers: headers() })
    .then(function (resp) {
      if (!resp.ok) throw new Error('读取凭据列表失败：HTTP ' + resp.status);
      return resp.json();
    })
    .then(function (body) {
      var files = (body.files || []).filter(function (file) {
        return String(file.provider || file.type || '').toLowerCase() === 'workbuddy';
      });
      if (files.length === 0) {
        renderEmpty('没有找到 WorkBuddy 凭据，请先到「WorkBuddy 登录」页面完成登录。');
        return null;
      }
      return Promise.all(files.map(function (file) {
        var index = file.auth_index || file.authIndex || '';
        return fetch('/v0/management/workbuddy/quota?auth_index=' + encodeURIComponent(index), { headers: headers() })
          .then(function (resp) {
            return resp.json().then(function (data) {
              return { file: file, ok: resp.ok, data: data };
            });
          })
          .catch(function (err) {
            return { file: file, ok: false, data: { error: String(err) } };
          });
      })).then(function (results) {
        var html = '';
        results.forEach(function (item) {
          if (item.ok) {
            html += renderCard(item.file, item.data);
          } else {
            html += '<div class="card err"><div class="name">' + esc(item.file.label || item.file.name) +
              '</div><div>' + esc(item.data && item.data.error ? item.data.error : '未知错误') + '</div></div>';
          }
        });
        out.innerHTML = html;
      });
    })
    .catch(function (err) { renderEmpty(String(err)); })
    .then(function () { refreshButton.disabled = false; });
};
</script>
</body>
</html>`
}
