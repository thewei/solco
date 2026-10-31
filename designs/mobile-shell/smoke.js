// 冒烟:agent-browser open file://$PWD/exploration.html && agent-browser eval "$(cat smoke.js)"
// 在页面里驱动状态机,逐项断言;最后返回 PASS n checks 或第一批失败项。
(() => {
  const fails = []; let n = 0;
  const ok = (c, m) => { n++; if (!c) fails.push(m); };
  const wait = ms => new Promise(r => setTimeout(r, ms));
  const mount = () => document.getElementById('mount' + S.variant);
  const ph = () => mount().querySelector('.ph');
  const click = sel => { const el = mount().querySelector(sel); if (!el) { fails.push('missing ' + sel); n++; return; } el.click(); };
  const rows = () => { const t = ph().querySelector('.term'); return t ? +t.dataset.rows : 0; };

  return (async () => {
    // 1. 所有变体 × 所有场景都能渲染,且无空 .ph
    for (const v of ['A', 'B', 'C', 'D']) {
      for (const [id] of SCENES) {
        setVariant(v); setScene(id);
        const count = document.querySelectorAll('#mount' + v + ' .ph').length;
        ok(v === 'D' ? document.querySelectorAll('#mountD .ph').length === 3 : count === 1, `render ${v}/${id}`);
      }
    }
    // 2. 主题 / 终端底色 / 输入形态
    setVariant('A'); setScene('term');
    S.theme = 'light'; update(); ok(ph().classList.contains('light'), 'theme light');
    S.term = 'black'; update(); ok(ph().classList.contains('tb-black'), 'term black');
    S.term = 'match'; S.theme = 'dark'; S.inp = 'keys'; update(); ok(!ph().querySelector('.lin'), 'keys-only has no line input');
    S.inp = 'line'; update(); ok(!!ph().querySelector('.lin'), 'line input present');

    // 3. 首页:模式芯片、发送 → 会话 / shell
    setVariant('A'); setScene('home');
    ok(!!ph().querySelector('.ctx .cx'), 'home has project chip');
    click('[data-act="mode"][data-mode="term"]');
    ok(S.mode === 'term' && /Run a command/.test(mount().querySelector('.cmp-ta').placeholder), 'terminal mode placeholder');
    ok(!ph().querySelector('.ctx .cx:nth-child(2)'), 'terminal mode hides agent chip');
    click('[data-act="send"]'); ok(S.view === 'shell', 'send in terminal mode opens shell');
    setScene('home'); S.draft = '修一下 Tab 溢出'; lastKey = ''; update(); click('[data-act="send"]');
    ok(S.view === 'session' && S.sk === 'fresh', 'send in agent mode opens fresh session');
    ok(/修一下 Tab 溢出/.test(mount().querySelector('.chat .u').textContent), 'fresh session echoes the draft');

    // 4. 侧栏:开 / 关 / 行 → 会话
    setScene('chat'); click('[data-act="drawer"]'); ok(ph().classList.contains('drawer-open'), 'drawer opens');
    ok(mount().querySelectorAll('.dw .rw').length === 6, 'drawer lists 6 rows');
    ok(!!mount().querySelector('.dw .rw.sel'), 'drawer marks the current row');
    click('.dw .rw[data-view="shell"]'); ok(S.view === 'shell' && !S.drawer, 'drawer row opens shell and closes drawer');
    setScene('chat'); click('[data-act="drawer"]'); click('.scrim'); ok(!S.drawer, 'scrim closes drawer');

    // 5. A:分段头切 Chat ⇄ Terminal;键盘行数
    setVariant('A'); setScene('chat');
    click('.seg button:nth-child(2)'); ok(ph().classList.contains('page-term'), 'A segmented → terminal');
    click('.seg button:nth-child(1)'); ok(!ph().classList.contains('page-term'), 'A segmented → chat');
    setScene('term'); await wait(400); const aRows = rows();
    setScene('term-kb'); await wait(400); const aKb = rows();
    ok(aRows > aKb && aKb >= 16, `A rows shrink with keyboard (${aRows}→${aKb})`);

    // 6. B:终端页零顶栏、行数更多;拉手翻页
    setVariant('B'); setScene('term-kb'); await wait(400); const bKb = rows();
    ok(bKb > aKb, `B has more terminal rows than A with keyboard (${bKb} > ${aKb})`);
    ok(!mount().querySelector('.pg-term .hd'), 'B terminal page has no header');
    setScene('chat'); click('.pull'); ok(ph().classList.contains('page-term'), 'B pull-tab → terminal');
    click('.pg-term [data-act="page"][data-page="chat"]'); ok(!ph().classList.contains('page-term'), 'B ‹ Chat → chat');

    // 7. C:窥视条打开 / 收起终端层;ask 态窥视条为琥珀
    setVariant('C'); setScene('chat');
    ok(!!ph().querySelector('.ccmp .peek'), 'C composer carries a peek strip');
    click('.peek'); ok(ph().classList.contains('page-term'), 'C peek opens terminal layer');
    click('.tsheet [data-act="page"][data-page="chat"]'); ok(!ph().classList.contains('page-term'), 'C collapse returns to chat');
    setScene('ask'); ok(ph().querySelector('.peek').classList.contains('need'), 'C ask → amber peek');

    // 8. 提问卡:点选 → 回到 live 并出现回答行
    setVariant('A'); setScene('ask'); click('.op[data-n="1"]');
    ok(S.sk === 'live' && !!mount().querySelector('.ans'), 'answering the question resumes the session');

    // 9. 断线横幅;选择器弹层
    setScene('offline'); ok(getComputedStyle(mount().querySelector('.banner')).display !== 'none', 'offline banner visible');
    click('[data-act="retry"]'); ok(!S.offline, 'retry clears offline');
    setScene('home'); click('.cx'); ok(ph().classList.contains('sheet-open'), 'project chip opens sheet');
    click('.sheet [data-v="codex"]'); ok(S.agent === 'codex' && !S.sheet, 'picking an agent closes the sheet');

    // 10. 深链
    setVariant('C'); setScene('term'); ok(location.hash === '#C/term/dark/match/line', 'hash reflects state (' + location.hash + ')');

    return fails.length ? 'FAIL ' + fails.length + '/' + n + ': ' + fails.join(' | ') : 'PASS ' + n + ' checks';
  })();
})()
