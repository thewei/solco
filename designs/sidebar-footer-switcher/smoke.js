// Smoke test for exploration.html (in-page, no dependencies).
// Run:  agent-browser open "file://$PWD/exploration.html" && agent-browser eval "$(cat smoke.js)"
// It starts in the background (~90 s); poll:  agent-browser eval "window.__smokeText"  until it is non-null.
// Expect: all lines PASS and "JS errors: []" (81 assertions across variants A/B/C + many/lost/flows).
window.__smokeText = null; (async () => {
  const errs = [];
  window.addEventListener('error', (e) => errs.push('ERR ' + e.message));
  const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
  const $ = (s) => document.querySelector(s);
  const S = () => window.__solcoProto.state();
  const out = [];
  const check = (name, cond, extra) => out.push((cond ? 'PASS ' : 'FAIL ') + name + (extra !== undefined ? ' :: ' + JSON.stringify(extra) : ''));
  const click = (s) => { const el = $(s); if (!el) throw new Error('missing ' + s); el.click(); };
  const cur = () => S().current.kind + ':' + S().current.id;
  const openPop = async (anchor) => { if (!document.querySelector('.pop')) { document.querySelector(anchor).click(); await sleep(70); } };
  const variants = [['A', 0, '#a-main'], ['B', 1, '#a-device'], ['C', 2, '#a-devices']];

  for (const [V, idx, anchor] of variants) {
   try {
    document.querySelectorAll('.ide-variant-card')[idx].click();
    click('[data-sc=typical]'); click('[data-reset]'); await sleep(80);
    const P = '[V=' + V + '] ';

    // open popover
    await openPop(anchor);
    check(P + 'popover opens', !!$('.pop'));

    // success switch
    click('[data-act=switch][data-id=workbox]'); await sleep(250);
    check(P + 'banner while connecting', !!$('.banner'), cur());
    check(P + 'current stays local while connecting', cur() === 'local:default', cur());
    await sleep(1500);
    check(P + 'committed to workbox', cur() === 'remote:workbox', cur());
    check(P + 'banner gone', !$('.banner'));

    // failure switch (unreachable, 2.7s)
    await openPop(anchor);
    click('[data-act=switch][data-id=staging]'); await sleep(2900);
    check(P + 'failed switch stays on workbox', cur() === 'remote:workbox', cur());
    check(P + 'failure toast shown', !!$('.toast'));
    check(P + 'no global offline (conn ok)', S().conn === 'ok');

    // cancel switch
    await openPop(anchor);
    click('[data-act=switch][data-id=build-server]'); await sleep(200);
    click('[data-act=cancel-switch]'); await sleep(1800);
    check(P + 'cancel keeps workbox & no late commit', cur() === 'remote:workbox' && !S().switching, cur());

    // back to This Mac
    await openPop(anchor);
    click('.pop [data-act=switch][data-kind=local]'); await sleep(120);
    check(P + 'switch to This Mac', cur() === 'local:default', cur());

    // forget current device -> returns to Local
    await openPop(anchor);
    click('[data-act=switch][data-id=workbox]'); await sleep(1900);
    check(P + 'on workbox again', cur() === 'remote:workbox', cur());
    await openPop(anchor);
    click('[data-act=row-menu][data-id=workbox]'); await sleep(60);
    click('[data-op=forget]'); await sleep(60);
    check(P + 'forget confirm sheet', !!$('.sheet') && /Forget/.test($('.sheet').textContent));
    click('[data-act=sheet-ok]'); await sleep(120);
    check(P + 'forget current returns to local', cur() === 'local:default' && !S().devices.some((d) => d.id === 'workbox'), cur());

    // enable disabled device, then disable it while viewing
    await openPop(anchor);
    click('[data-act=row-menu][data-id=gpu-box]'); await sleep(60);
    click('[data-op=enable]'); await sleep(1300);
    check(P + 'enable -> online', S().devices.find((d) => d.id === 'gpu-box').status === 'online');
    await openPop(anchor);
    click('[data-act=switch][data-id=gpu-box]'); await sleep(1900);
    check(P + 'viewing gpu-box', cur() === 'remote:gpu-box', cur());
    await openPop(anchor);
    click('[data-act=row-menu][data-id=gpu-box]'); await sleep(60);
    click('[data-op=disable]'); await sleep(150);
    check(P + 'disable current returns to local', cur() === 'local:default', cur());

    // add: SSH fails -> error step -> retry
    document.querySelector('[data-cfg=addOutcome][data-val=fail]').click();
    await openPop(anchor);
    click('[data-act=add-open]'); await sleep(60);
    $('#addTarget').value = 'ops@bad.example.com'; $('#addTarget').dispatchEvent(new Event('input', { bubbles: true }));
    click('[data-act=add-go]'); await sleep(2600);
    check(P + 'add fail -> error sheet', S().sheet && S().sheet.step === 'error', S().sheet && S().sheet.step);
    click('[data-act=add-retry]'); await sleep(60);
    check(P + 'retry returns to form w/ values', S().sheet.step === 'form' && S().sheet.target === 'ops@bad.example.com');
    click('[data-act=sheet-cancel]'); await sleep(60);

    // add: needs approval -> y -> success, selection unchanged
    document.querySelector('[data-cfg=addOutcome][data-val=approve]').click();
    await openPop(anchor);
    click('[data-act=add-open]'); await sleep(60);
    $('#addTarget').value = 'ops@newbox.example.com'; $('#addTarget').dispatchEvent(new Event('input', { bubbles: true }));
    click('[data-act=add-go]'); await sleep(1800);
    check(P + 'approval prompt visible', !!$('.term .ask'));
    click('[data-act=term-ans][data-v=y]'); await sleep(3600);
    check(P + 'device added after approval', S().devices.some((d) => d.label === 'newbox.example.com'), S().devices.map((d) => d.label));
    check(P + 'add does not change selection', cur() === 'local:default', cur());

    // duplicate label guard
    document.querySelector('[data-cfg=addOutcome][data-val=ok]').click();
    await openPop(anchor);
    click('[data-act=add-open]'); await sleep(60);
    $('#addTarget').value = 'workbox'; $('#addTarget').dispatchEvent(new Event('input', { bubbles: true }));
    $('#addLabel').value = 'staging'; $('#addLabel').dispatchEvent(new Event('input', { bubbles: true }));
    click('[data-act=add-go]'); await sleep(80);
    check(P + 'duplicate label blocked', S().sheet && S().sheet.step === 'form' && /already exists/.test(S().sheet.error), S().sheet && S().sheet.error);
    click('[data-act=sheet-cancel]'); await sleep(60);

    // recover via menu
    await openPop(anchor);
    click('[data-act=row-menu][data-id=studio-mac]'); await sleep(60);
    click('[data-op=reconnect]'); await sleep(1300);
    check(P + 'reconnect terminal asks passphrase', !!$('.term .ask'));
    click('[data-act=term-ans][data-v=pw]'); await sleep(2800);
    check(P + 'studio-mac online after reconnect', S().devices.find((d) => d.id === 'studio-mac').status === 'online');
    click('[data-act=toast-act]'); await sleep(1900);
    check(P + 'toast Switch action switches', cur() === 'remote:studio-mac', cur());
   } catch (e) { out.push('EXC [' + V + '] ' + e.message + ' | pop=' + S().pop + ' sheet=' + JSON.stringify(S().sheet && S().sheet.step) + ' cur=' + cur()); }
  }

  // many scenario: search (A) + rail overflow (C)
  document.querySelectorAll('.ide-variant-card')[0].click();
  click('[data-sc=many]'); await sleep(80);
  click('#a-main'); await sleep(80);
  check('[A many] search box present', !!$('#popQ'));
  const q = $('#popQ'); q.value = 'ml-'; q.dispatchEvent(new Event('input', { bubbles: true })); await sleep(60);
  check('[A many] search filters rows', document.querySelectorAll('.pop .row.two').length === 2, document.querySelectorAll('.pop .row.two').length);
  document.querySelectorAll('.ide-variant-card')[2].click(); await sleep(80);
  check('[C many] +N overflow tile', /\+\d+/.test($('#a-devices').textContent), $('#a-devices').textContent);

  // lost scenario
  document.querySelectorAll('.ide-variant-card')[0].click();
  click('[data-sc=lost]'); await sleep(80);
  check('[lost] lostbar + dim', !!$('.lostbar') && !!$('.main.lost'));
  click('[data-act=lost-local]'); await sleep(80);
  check('[lost] Switch to This Mac', cur() === 'local:default', cur());

  // flows page doesn't throw and Esc handling
  document.querySelectorAll('.ide-variant-card')[3].click(); await sleep(80);
  check('[F] flows visible', !!$('.ide-artboard[data-variant=F].active .fl'));

  window.__smoke = { out, errs };
  return out.join('\n') + '\nJS errors: ' + JSON.stringify(errs);
})().then((r) => { window.__smokeText = r; }, (e) => { window.__smokeText = 'FATAL ' + e.message; }); 'started'