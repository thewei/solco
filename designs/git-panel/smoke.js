// 原型自检：agent-browser eval "$(cat designs/git-panel/smoke.js)" ；结果在 window.__smokeText（轮询到非 null）。
// 全部为同步 DOM 断言（无定时依赖）：A / B / C 各跑一遍。
(() => {
  window.__smokeText = null;
  const out = [];
  const ok = (name, cond, extra = '') => out.push(`${cond ? 'PASS' : 'FAIL'}  ${name}${extra ? '  · ' + extra : ''}`);
  const errs = [];
  window.addEventListener('error', e => errs.push(e.message));
  const $ = (s, r = document) => r.querySelector(s);
  const $$ = (s, r = document) => [...r.querySelectorAll(s)];
  const ctl = (k, v) => $(`[data-ctl=${k}] [data-val="${v}"]`).click();
  const reset = () => { ctl('theme', 'dark'); ctl('winw', '1180'); ctl('sbw', '252'); ctl('dirty', '1'); ctl('list', 'cols'); ctl('view', 'changes'); S.hint = false; S.composer = false; S.branch = 'main'; S.sbSearch = false; S.sbQ = ''; S.toolSearch = false; S.toolQ = ''; render(); };
  const dbl = el => { el.dispatchEvent(new MouseEvent('click', { bubbles: true, detail: 1 })); el.dispatchEvent(new MouseEvent('click', { bubbles: true, detail: 2 })); el.dispatchEvent(new MouseEvent('dblclick', { bubbles: true, detail: 2 })); };
  const R = () => $('.ide-artboard.active .design-root');

  for (const v of ['A', 'B', 'C']) {
    setVariant(v); reset();
    const t = `[${v}] `;
    // ① 视图切换
    ok(t + 'view switch exists', v === 'A' ? !!$('.ptabs .ptab', R()) : v === 'B' ? !!$('.tool .ltab', R()) : !!$('.srcl .srow', R()));
    ok(t + 'switch has icons and a Changes count', $$('[data-act=view] svg', R()).length >= 2 && /4/.test($('[data-act=view]', R()).textContent));
    // ② 头部高度
    const hdrH = $('#m-hdr', R()).getBoundingClientRect().height;
    ok(t + 'header height', v === 'B' ? hdrH < 70 : hdrH <= 36, hdrH.toFixed(0) + 'px');
    // ③ 搜索默认不占位，点图标才展开
    ok(t + 'no permanent search field in the sidebar', !$('#in-sb', R()));
    $('[data-act=sb-search]', R()).click();
    ok(t + 'sidebar search expands on click', !!$('#in-sb', R()));
    $('#in-sb', R()).value = 'ed'; $('#in-sb', R()).dispatchEvent(new Event('input', { bubbles: true }));
    ok(t + 'sidebar search filters refs', $$('.tr[data-kind=branch]', R()).every(e => /editor/.test(e.dataset.name)) && $$('.tr[data-kind=branch]', R()).length === 1);
    $('[data-act=sb-search-close]', R()).click();
    ok(t + 'closing search restores the tree', $$('.tr[data-kind=branch]', R()).length === 5);
    // ④ 单击只选中，双击才切换
    let target = $('.tr[data-name="feat/editor"]', R());
    target.dispatchEvent(new MouseEvent('click', { bubbles: true, detail: 1 }));
    ok(t + 'single click selects, no dialog, no switch', !$('.dlg', R()) && S.branch === 'main' && target.isConnected && $('.tr[data-name="feat/editor"]', R()).classList.contains('sel'));
    dbl($('.tr[data-name="feat/editor"]', R()));
    ok(t + 'double-click on a dirty tree asks first', !!$('.dlg', R()) && S.branch === 'main');
    $('[data-act=dlg-cancel]', R()).click();
    ok(t + 'Cancel keeps the branch', !$('.dlg', R()) && S.branch === 'main');
    dbl($('.tr[data-name="feat/editor"]', R())); $('[data-act=dlg-switch]', R()).click();
    ok(t + 'Switch Branch switches', S.branch === 'feat/editor' && /feat\/editor/.test($('#m-hdr', R()).textContent));
    ctl('dirty', '0'); S.branch = 'main'; render();
    dbl($('.tr[data-name="feat/editor"]', R()));
    ok(t + 'clean tree switches without a dialog (as today)', S.branch === 'feat/editor' && !$('.dlg', R()));
    // 其它引用行：单击无副作用
    ctl('dirty', '1'); S.branch = 'main'; render();
    $('.tr[data-kind=worktree]', R()).dispatchEvent(new MouseEvent('click', { bubbles: true, detail: 1 }));
    $('.tr[data-kind=tag]', R()).dispatchEvent(new MouseEvent('click', { bubbles: true, detail: 1 }));
    ok(t + 'worktree/tag single click has no side effect (no toast about Finder/copy)', !/Finder|Copied/.test(($('.toast', R()) || {}).textContent || ''));
    // ⑤ Commits：图谱宽度自适应
    ctl('view', 'history');
    const gw = $('.crow .gsvg', R()).getBoundingClientRect().width;
    ok(t + 'graph column hugs the lanes (was a fixed 76)', gw <= 34, gw.toFixed(0) + 'px');
    const plain = $$('.crow', R())[1], dot = $('.gsvg circle', plain).getBoundingClientRect(), subj = $('.csubj', plain).getBoundingClientRect();
    ok(t + 'dot → subject gap is small (was ≈50 + 8)', subj.left - dot.right < 40, (subj.left - dot.right).toFixed(0) + 'px');
    ok(t + 'one subject per place: no metadata line / banner above the diff', !$('.review .fc .banner') && !$('.review .metaline'));
    ok(t + 'commit header carries the only copy of the subject', $$('.review .csub', R()).length === 1);
    ok(t + 'commit message is selectable', getComputedStyle($('.review .csub', R())).userSelect === 'text');
    ok(t + 'no Commit|Changes|File Tree segmented control', !$('.review [data-act=review-tab]'));
    // 行高
    ok(t + '1-line row is 24', Math.round($('.crow', R()).getBoundingClientRect().height) === 24);
    ctl('list', 'two');
    ok(t + '2-line row is 40', Math.round($('.crow', R()).getBoundingClientRect().height) === 40);
    ctl('list', 'cols');
    // 提交搜索
    $('[data-act=tool-search]', R()).click();
    $('#in-tool', R()).value = 'feat(projects)'; $('#in-tool', R()).dispatchEvent(new Event('input', { bubbles: true }));
    ok(t + 'commit search filters', $$('.crow', R()).length === 2 && /of 200/.test($('#m-tool', R()).textContent), $$('.crow', R()).length + ' rows');
    ok(t + 'graph wires hidden while filtering (dot only)', $$('.crow .gsvg line', R()).length === 0);
    // ⑥ Changes：复制
    ctl('view', 'changes');
    const cd = $$('.ln .cd', R())[0], no = $('.ln .no', R());
    ok(t + 'diff code is selectable', getComputedStyle(cd).userSelect === 'text');
    ok(t + 'line numbers are not selectable', getComputedStyle(no).userSelect === 'none');
    const rg = document.createRange(); rg.setStartBefore($$('.ln .cd', R())[1]); rg.setEndAfter($$('.ln .cd', R())[3]);
    const sel = getSelection(); sel.removeAllRanges(); sel.addRange(rg);
    ok(t + 'selection copies code only (no digits from the gutter)', !/^\s*\d+\s/m.test(sel.toString()) && sel.toString().split('\n').length >= 3, JSON.stringify(sel.toString().slice(0, 40)));
    sel.removeAllRanges();
    ok(t + 'composer is one line while idle', !$('#m-cmp', R()).classList.contains('open') && getComputedStyle($('.cmp .more', R())).display === 'none');
    $('#in-sub', R()).dispatchEvent(new FocusEvent('focusin', { bubbles: true }));
    ok(t + 'composer opens on focus', $('#m-cmp', R()).classList.contains('open'));
    ok(t + 'toolbar has no Stage All / Unstage All (they live in section headers)', !$$('.tool button', R()).some(b => /Stage All/.test(b.textContent)));
    ok(t + 'no duplicate "Local Changes" title row / Total footer in the file list', !/Total/.test($('.nav', R()).textContent) && !/Local Changes/.test($('.nav', R()).textContent));
    // 窄窗口：文件导航自动收起
    ctl('winw', '760'); ctl('sbw', '252');
    ok(t + 'file navigator auto-closes below 750 center width', $('.nav', R()).classList.contains('off'));
    ctl('winw', '1180');
    // 浅色
    ctl('theme', 'light'); ok(t + 'light theme renders', getComputedStyle($('.win', R())).backgroundColor !== '' && R().dataset.theme === 'light'); ctl('theme', 'dark');
  }
  ok('no JS errors', errs.length === 0, errs.join(' | '));
  setVariant('A'); reset();
  const fails = out.filter(l => l.startsWith('FAIL'));
  window.__smokeText = out.join('\n') + `\n\n${out.length - fails.length}/${out.length} PASS`;
})();
