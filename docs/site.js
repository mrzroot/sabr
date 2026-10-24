(function () {
  'use strict';
  var $ = function (s, r) { return (r || document).querySelector(s); };
  var $$ = function (s, r) { return Array.prototype.slice.call((r || document).querySelectorAll(s)); };
  var root = document.documentElement;
  var FA = {
    'skip': 'رفتن به دمو', 'nav.demo': 'دمو', 'nav.install': 'نصب', 'nav.how': 'چطور', 'nav.proof': 'اثبات',
    'hero.kicker': 'برای اینترنت کند، محدودشده و ناپایدار',
    'hero.title': 'دانلودی که<br><span>هیچ‌وقت از صفر شروع نمی‌شود.</span>',
    'hero.lede': 'git clone، docker pull و دانلودهای بزرگ همه‌یا‌هیچ‌اند: کافی است اتصال در ۹۷٪ یک بار قطع شود تا همه‌چیز از صفر شروع شود. sabr کار را به قدم‌های کوچک و قابل‌ادامه تقسیم می‌کند، پس هر قطعی فقط چند ثانیه هزینه دارد نه کل دانلود را. حتی بعد از ری‌استارت هم ادامه می‌دهد.',
    'copy': 'کپی', 'copied': 'کپی شد ✓',
    'install.fine': 'خود نصب‌کننده هم قابل‌ادامه است و SHA-256 را بررسی می‌کند. یک فایل اجرایی تکی که فقط برای clone به git نیاز دارد.',
    'race.title': 'مسابقه روی یک لینک خراب',
    'race.sub': 'شبیه‌سازی تعاملی رفتار هر ابزار روی اتصالی که مدام قطع می‌شود. اجرای واقعی با اعداد واقعی پایین‌تر آمده است.',
    'race.badge': 'شبیه‌سازی', 'race.what': 'چه چیزی', 'race.drop': 'قطع اتصال هر', 'race.speed': 'سرعت', 'race.go': 'شروع', 'race.again': 'دوباره',
    'race.o1': 'مخزن cli/cli (حدود ۸۰ مگابایت)', 'race.o2': 'مخزن react (حدود ۳۵۰ مگابایت)', 'race.o3': 'لایهٔ داکر ۶۰۰ مگابایتی', 'race.o4': 'کرنل لینوکس (حدود ۵ گیگابایت)',
    'race.note': 'زمان سریع‌تر از واقعیت نمایش داده می‌شود و رفتار ابزارها ساده شده است: git بعد از هر قطعی بستهٔ pack را از صفر شروع می‌کند، اما sabr فقط همان قدمی را که در جریان بود از دست می‌دهد، با تأخیر فزاینده دوباره تلاش می‌کند و قدم را نصف می‌کند.',
    'st.run': 'در حال اجرا', 'st.fail': 'شکست', 'st.done': 'تمام', 'st.retry': 'تلاش دوباره',
    'info.attempt': 'تلاش {a} · {w} هدر رفته', 'info.gave': '{a} تلاش · {w} هدر رفته', 'info.sabr': '{d} قطعی · {s} قدم', 'info.time': 'زمان شبیه‌سازی‌شده {t}',
    'how.title': 'sabr clone چطور کار می‌کند',
    'how.s1t': 'آخرین کامیت', 'how.s1': 'یک fetch بدون blob با عمق ۱: فقط کامیت‌ها و پوشه‌ها، چند صد کیلوبایت.',
    'how.s2t': 'تاریخچه، قدم‌به‌قدم', 'how.s2': 'تاریخچه را قدم‌به‌قدم عمیق‌تر می‌کند. اندازهٔ قدم بعد از موفقیت دو برابر و بعد از قطعی نصف می‌شود.',
    'how.s3t': 'فایل‌ها، دسته‌دسته', 'how.s3': 'blobهای جامانده را فهرست می‌کند و در دسته‌های تطبیقی می‌گیرد. فایلی که مدام شکست بخورد، تکه‌تکه دانلود و هش آن بررسی می‌شود.',
    'how.s4t': 'یک مخزن معمولی', 'how.s4': 'بعد checkout می‌کند و upstream و origin/HEAD را تنظیم می‌کند؛ نتیجه همان git clone است. پیشرفت در .git/sabr.json ذخیره می‌شود، پس بعد از kill یا ری‌استارت فقط ادامه می‌دهد.',
    'how.docker': 'مستقیم با رجیستری حرف می‌زند، هر لایه را از همان بایتی که قطع شده ادامه می‌دهد و کش مشترک لایه‌ها دارد. خروجی‌اش آرشیو docker load است و با --load مستقیم در Docker یا Podman بارگذاری می‌شود. نیازی به تغییر تنظیمات daemon نیست.',
    'how.get': 'بین اجراها با Range ادامه می‌دهد. اگر فایل روی سرور تغییر کرده باشد از نو شروع می‌کند، نگهبان گیرکردن دارد و SHA-256 را بررسی می‌کند.',
    'how.chaos': 'یک لینک خرابِ آماده: پروکسی محلی که هر اتصال را بعد از N بایت قطع یا منجمد می‌کند. ابزارهای خودتان را هم با آن آزمایش کنید.',
    'proof.title': 'اجرای واقعی، نه شبیه‌سازی',
    'proof.sub': 'مخزن cli/cli گیت‌هاب (۱۲٬۳۵۷ کامیت) از پشت پروکسی‌ای که هر اتصال را بعد از حدود ۲ مگابایت قطع می‌کند. همین آزمایش در هر push در CI اجرا می‌شود.',
    'cmp.drop': 'تحمل قطعی', 'cmp.kill': 'تحمل kill/ری‌استارت', 'cmp.auto': 'خودکار', 'cmp.git': 'تاریخچهٔ git', 'cmp.docker': 'ایمیج داکر', 'cmp.file': 'فایل تکی',
    'cmp.old': 'فقط blobهای قدیمی', 'cmp.needs': 'اول یک clone کامل بدون blob لازم دارد', 'cmp.files': 'فقط فایل‌ها', 'cmp.oneshot': 'متادیتا هنوز یک‌جا', 'cmp.daemon': '✓ با تغییر تنظیمات daemon', 'cmp.any': '✓ روی هر سیستمی',
    'v.1': 'CI با گیت‌هاب واقعی: git معمولی از پشت پروکسی ۲ مگابایتی شکست می‌خورد؛ نتیجهٔ sabr با ls-remote یکی است و بیش از ۵٬۰۰۰ کامیت و تگ‌ها را دارد.',
    'v.2': 'CI با Docker Hub واقعی: python:3.12-slim از پشت همان پروکسی، بعد docker load و docker run.',
    'v.3': 'تست e2e: وسط clone با kill -9 متوقفش کنید، همان فرمان را دوباره بزنید، با درخت تمیز تمام می‌شود.',
    'v.4': 'تست روی لینوکس، مک و ویندوز؛ نصب‌کننده‌ها در HOME تازه و روی PowerShell 5.1 و 7 آزمایش شده‌اند و فایل دست‌کاری‌شده رد می‌شود.'
  };
  var EN = {};
  $$('[data-i18n]').forEach(function (el) { EN[el.dataset.i18n] = el.textContent; });
  $$('[data-i18n-html]').forEach(function (el) { EN[el.dataset.i18nHtml] = el.innerHTML; });
  Object.assign(EN, { copied: 'Copied ✓', 'race.again': 'Again', 'st.run': 'running', 'st.fail': 'failed', 'st.done': 'done', 'st.retry': 'retrying',
    'info.attempt': 'attempt {a} · {w} wasted', 'info.gave': '{a} attempts · {w} wasted', 'info.sabr': '{d} drops survived · {s} steps', 'info.time': 'simulated time {t}' });
  function lang() { return root.lang === 'fa' ? 'fa' : 'en'; }
  function t(k) { var d = lang() === 'fa' ? FA : EN; return d[k] != null ? d[k] : (EN[k] || k); }
  function applyLang() {
    $$('[data-i18n]').forEach(function (el) { el.textContent = t(el.dataset.i18n); });
    $$('[data-i18n-html]').forEach(function (el) { el.innerHTML = t(el.dataset.i18nHtml); });
    document.title = lang() === 'fa' ? 'صبر (sabr) · دانلودی که هیچ‌وقت از صفر شروع نمی‌شود' : 'sabr · downloads that never start over';
    labels(); paint();
  }
  $('#lang').onclick = function () { var fa = lang() !== 'fa'; root.lang = fa ? 'fa' : 'en'; root.dir = fa ? 'rtl' : 'ltr'; localStorage.setItem('sabr-lang', root.lang); applyLang(); };

  $$('.tabs button').forEach(function (b) { b.onclick = function () { $$('.tabs button').forEach(function (x) { x.setAttribute('aria-selected', x === b); }); $('#cmd').textContent = b.dataset.cmd; }; });
  if (/Win/.test(navigator.platform || navigator.userAgent)) $$('.tabs button')[1].click();
  $$('[data-copy]').forEach(function (b) {
    b.onclick = function () {
      var txt = $(b.dataset.copy).textContent, done = function () { b.textContent = t('copied'); setTimeout(function () { b.textContent = t('copy'); }, 1400); };
      if (navigator.clipboard) navigator.clipboard.writeText(txt).then(done, done); else done();
    };
  });

  // ---- race simulation ----
  function mb(x) { return x >= 1000 ? (x / 1000).toFixed(2) + ' GB' : x >= 10 ? Math.round(x) + ' MB' : x.toFixed(1) + ' MB'; }
  function dur(s) { s = Math.round(s); if (s < 60) return s + 's'; var m = Math.floor(s / 60); if (m < 60) return m + 'm ' + (s % 60) + 's'; return Math.floor(m / 60) + 'h ' + (m % 60) + 'm'; }
  function labels() {
    $('#dropv').textContent = $('#drop').value + ' MB';
    $('#speedv').textContent = $('#speed').value + ' Mbit/s';
  }
  ['drop', 'speed'].forEach(function (id) { $('#' + id).addEventListener('input', labels); });
  labels();

  var sim = null, raf = 0;
  function log(lane, cls, text) {
    var el = $('#' + lane + '-log'), line = document.createElement('span');
    if (cls) line.className = cls; line.textContent = text + '\n'; el.appendChild(line);
    while (el.childNodes.length > 80) el.removeChild(el.firstChild);
    el.scrollTop = el.scrollHeight;
  }
  function distance(D) { return D * (0.55 + Math.random() * 0.9); }
  function start() {
    cancelAnimationFrame(raf);
    var S = +$('#what').value, D = +$('#drop').value, v = +$('#speed').value / 8; // MB per simulated second
    $('#git-log').textContent = ''; $('#sabr-log').textContent = ''; $('#sabr-ticks').innerHTML = '';
    // aim for sabr to finish in ~14 s of real time
    var est = S / v * 1.8 + (S / D) * 2;
    sim = { S: S, D: D, v: v, k: Math.max(1, est / 14), t: 0,
      git: { p: 0, left: distance(D), attempt: 1, wasted: 0, wait: 0, state: 'run', max: 5 },
      sabr: { done: 0, inStep: 0, step: Math.min(1, S), left: distance(D), drops: 0, steps: 0, wait: 0, backoff: 1, state: 'run', lat: 0.3 } };
    log('git', '', '$ git clone https://example.com/big.git');
    log('git', '', 'Receiving objects…');
    log('sabr', '', '$ sabr clone https://example.com/big.git');
    log('sabr', '', '==> fetching in resumable steps');
    $('#go').textContent = t('race.again');
    var last = performance.now();
    (function frame(now) {
      var dt = Math.min(0.1, (now - last) / 1000); last = now;
      step(dt * sim.k); paint();
      if (sim.git.state !== 'run' && sim.git.state !== 'retry' && sim.sabr.state === 'done') return;
      raf = requestAnimationFrame(frame);
    })(last);
  }
  function step(dt) {
    var s = sim; s.t += dt;
    var g = s.git;
    if (g.state === 'retry') { g.wait -= dt; if (g.wait <= 0) { g.state = 'run'; g.left = distance(s.D); log('git', '', '$ git clone …   (attempt ' + g.attempt + ')'); } }
    else if (g.state === 'run') {
      var got = s.v * dt;
      if (got >= g.left && g.p + g.left < s.S) {
        g.p += g.left; g.wasted += g.p;
        log('git', 'e', 'fatal: early EOF at ' + mb(g.p) + ' (' + Math.round(g.p / s.S * 100) + '%)');
        log('git', 'e', 'fatal: fetch-pack: invalid index-pack output');
        g.p = 0;
        if (g.attempt >= g.max) { g.state = 'fail'; log('git', 'e', '✗ gave up after ' + g.attempt + ' attempts, ' + mb(g.wasted) + ' thrown away'); }
        else { g.attempt++; g.state = 'retry'; g.wait = 3; }
      } else {
        g.left -= got; g.p += got;
        if (g.p >= s.S) { g.p = s.S; g.state = 'done'; g.time = s.t; log('git', 'o', '✓ done in ' + dur(s.t)); }
      }
    }
    var b = s.sabr;
    if (b.state === 'retry') { b.wait -= dt; if (b.wait <= 0) { b.state = 'run'; b.lat = 0.3; b.left = distance(s.D); } }
    else if (b.state === 'run') {
      if (b.lat > 0) { b.lat -= dt; return; }
      var need = Math.min(b.step, s.S - b.done) - b.inStep, got2 = s.v * dt;
      if (got2 >= b.left && b.left < need) {
        b.drops++;
        log('sabr', 'w', '  ! step of ' + mb(b.step) + ' dropped at ' + mb(b.done + b.inStep + b.left) + '; next ' + mb(Math.max(0.25, b.step / 2)) + ' in ' + b.backoff + 's');
        b.inStep = 0; b.step = Math.max(0.25, b.step / 2); b.state = 'retry'; b.wait = b.backoff; b.backoff = Math.min(8, b.backoff * 2);
      } else if (got2 >= need) {
        var stepMB = need + b.inStep; b.done = Math.min(s.S, b.done + stepMB); b.inStep = 0; b.steps++;
        b.left -= need; b.backoff = 1;
        var tick = document.createElement('i'); tick.style.left = (b.done / s.S * 100) + '%'; if (b.steps % 2 === 0 || s.S < 200) $('#sabr-ticks').appendChild(tick);
        if (b.steps <= 3 || b.steps % 6 === 0) log("sabr", "", "    +" + mb(stepMB) + ' (' + mb(b.done) + ' so far)');
        if (b.done >= s.S) { b.state = 'done'; log('sabr', 'o', '  ✓ done in ' + dur(s.t) + ', ' + b.steps + ' steps, ' + b.drops + ' drops survived'); return; }
        b.step = Math.min(64, b.step * 2); b.lat = 0.3; b.left = distance(s.D);
      } else { b.inStep += got2; b.left -= got2; }
    }
  }
  function paint() {
    if (!sim) { $('#git-state').textContent = ''; $('#sabr-state').textContent = ''; return; }
    var s = sim, g = s.git, b = s.sabr;
    var gp = g.p / s.S * 100, bp = (b.done + b.inStep) / s.S * 100;
    $('#git-fill').style.width = gp + '%'; $('#sabr-fill').style.width = bp + '%';
    $('#git-ghost').style.width = Math.max(parseFloat($('#git-ghost').style.width) || 0, gp) + '%';
    $('#git-pct').textContent = Math.floor(gp) + '%'; $('#sabr-pct').textContent = Math.floor(bp) + '%';
    var gs = $('#git-state'); gs.className = 'state ' + (g.state === 'retry' ? 'run' : g.state); gs.textContent = t('st.' + g.state);
    var bs = $('#sabr-state'); bs.className = 'state ' + (b.state === 'retry' ? 'run' : b.state); bs.textContent = t('st.' + b.state);
    $('#git-info').textContent = (g.state === 'fail' ? t('info.gave') : t('info.attempt')).replace('{a}', g.attempt).replace('{w}', mb(g.wasted));
    $('#sabr-info').textContent = t('info.sabr').replace('{d}', b.drops).replace('{s}', b.steps) + ' · ' + t('info.time').replace('{t}', dur(s.t));
  }
  $('#go').onclick = start;
  $('#what').onchange = start; $('#drop').onchange = start; $('#speed').onchange = start;
  var io = 'IntersectionObserver' in window ? new IntersectionObserver(function (e) { if (e[0].isIntersecting && !sim) { start(); io.disconnect(); } }, { threshold: .3 }) : null;
  if (io) io.observe($('#race')); else start();
  if (lang() === 'fa') applyLang();
})();
