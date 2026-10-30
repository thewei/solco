// Smoke test for exploration.html. Run in the page context, e.g.
//   agent-browser open file://$PWD/exploration.html
//   agent-browser eval "$(cat smoke.js)"
// Prints "PASS n checks" or the first failing check. No dependencies.
(() => {
  const $ = (s) => document.querySelector(s);
  const click = (s) => { const e = $(s); if (!e) throw new Error('missing ' + s); e.click(); };
  const S = () => window.__usageProto.state();
  const checks = [];
  const ok = (name, cond) => { if (!cond) throw new Error('FAIL: ' + name); checks.push(name); };
  try {
    for (const v of ['A', 'B', 'C']) {
      click(`[data-v=${v}]`);
      ok(`variant ${v} renders cards or detail`, $('#root').textContent.includes('Claude Code'));
    }
    click('[data-v=A]');
    for (const sc of ['full', 'mac', 'error', 'empty', 'refresh']) {
      click(`[data-scene=${sc}]`);
      ok(`scene ${sc} renders`, $('#root').innerHTML.length > 500);
    }
    click('[data-scene=full]');
    click('[data-ctl=winw] [data-val="760"]');
    ok('window narrows to 760', getComputedStyle($('.win')).width === '760px');
    click('[data-ctl=winw] [data-val="1180"]');
    click('[data-ctl=theme] [data-val=light]');
    ok('light theme applied', S().theme === 'light');
    click('[data-ctl=theme] [data-val=dark]');
    click('[data-state=menu]');
    ok('⋯ menu opens', !!$('.menu'));
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }));
    ok('no uncaught state error', true);
  } catch (e) {
    return String(e.message);
  }
  return 'PASS ' + checks.length + ' checks';
})();
