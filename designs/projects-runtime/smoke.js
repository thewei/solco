// Smoke test for exploration.html. Run in the page context, e.g.
//   agent-browser open file://$PWD/exploration.html
//   agent-browser eval "$(cat smoke.js)"
// Prints "PASS n checks" or the first failing check. No dependencies.
(() => {
  const $ = (s) => document.querySelector(s);
  const $$ = (s) => [...document.querySelectorAll(s)];
  const checks = [];
  const ok = (name, cond) => { if (!cond) throw new Error('FAIL: ' + name); checks.push(name); };
  try {
    for (const v of ['A', 'B', 'C', 'M']) {
      $(`[data-v=${v}]`).click();
      const scenes = $$('[data-scene]').map((e) => e.dataset.scene);
      ok(`variant ${v} lists scenes`, scenes.length >= 4);
      for (const sc of scenes) {
        $(`[data-scene=${sc}]`).click();
        const html = $('#root').innerHTML;
        ok(`${v}/${sc} renders`, html.length > 1500);
        ok(`${v}/${sc} has no undefined/NaN`, !/undefined|NaN/.test($('#root').textContent));
      }
    }
    $('[data-v=A]').click(); $('[data-scene=home]').click();
    ok('A home has tasks tab', !!$('#root [data-tab=tasks]'));
    $('#root [data-tab=library]').click();
    ok('library tab opens', $('#root').textContent.includes('Always read'));
    $('#root [data-tab=tasks]').click();
    $('#root .trw[data-id=SLC-14]').click();
    ok('task click opens run view', $('#root').textContent.includes('Question for you') && !!$('#root .strip'));
    for (const k of ['run', 'ctx', 'chg', 'rev', 'set']) { $(`#root [data-k=${k}]`).click(); ok(`inspector ${k}`, $('#root .aside .abody').textContent.length > 40); }
    $('#root [data-act=menu]').click();
    ok('task menu opens', !!$('#root .menu'));
    $('[data-scene=launch]').click();
    ok('launch sheet', $('#root').textContent.includes('What the agent will get'));
    $('#root [data-val="Plan first"]').click();
    ok('seg toggles brief preview', $('#root').textContent.includes('plan first, you approve'));
    console.log('PASS ' + checks.length + ' checks');
    return 'PASS ' + checks.length + ' checks';
  } catch (e) { console.log(String(e)); return String(e); }
})();
