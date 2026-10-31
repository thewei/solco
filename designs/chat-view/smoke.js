// Smoke test for exploration.html. Run in the page context, e.g.
//   agent-browser open file://$PWD/exploration.html
//   agent-browser eval "$(cat smoke.js)"
// Prints "PASS n checks" or the first failing check. No dependencies.
(() => {
  const $ = (s) => document.querySelector(s);
  const $$ = (s) => [...document.querySelectorAll(s)];
  const P = window.__chatProto;
  const click = (s) => { const e = $(s); if (!e) throw new Error('missing ' + s); e.click(); };
  const checks = [];
  const ok = (name, cond) => { if (!cond) throw new Error('FAIL: ' + name); checks.push(name); };
  try {
    for (const v of ['A', 'B', 'C']) {
      click(`[data-variant=${v}]`); click('[data-scene=settled]');
      ok(`variant ${v} renders conversation`, $('#root').textContent.includes('parseTimestamp'));
      ok(`variant ${v} has composer`, !!$('#ta'));
      for (const sc of ['settled', 'live', 'ask', 'stopped', 'readonly']) {
        click(`[data-scene=${sc}]`);
        ok(`${v}/${sc} renders`, $('#root .log').innerHTML.length > 800);
        ok(`${v}/${sc} composer presence`, sc === 'readonly' ? !$('#ta') : !!$('#ta'));
      }
    }
    // approval dock only in ask
    click('[data-variant=A]'); click('[data-scene=ask]');
    ok('ask dock shows 3 options', $$('.ask .opt').length === 3);
    click('[data-scene=settled]');
    ok('no dock outside ask', !$('.ask'));
    // process disclosure toggles
    const before = $$('.runbody').length;
    click('.runbtn');
    ok('run opens', $$('.runbody').length === before + 1);
    click('.runbtn.open');
    ok('run closes', $$('.runbody').length === before);
    // B: ledger + changes card
    click('[data-variant=B]');
    ok('B shows changes card', $$('.changes').length === 2);
    ok('B work dividers collapsed', $$('.ledger').length === 0);
    click('.work'); ok('B ledger opens', $$('.ledger').length === 1);
    // C: sticky headers + turn map
    click('[data-variant=C]');
    ok('C has 2 turn blocks', $$('.blk').length === 2);
    ok('C turn map has 2 ticks', $$('[data-jump]').length === 2);
    // live: spinner + stop; typing swaps stop for queue
    click('[data-scene=live]');
    ok('live shows stop', !!$('#stopb'));
    const ta = $('#ta'); ta.value = 'follow up'; ta.dispatchEvent(new Event('input'));
    ok('typing marks composer typed', $('#cmp').classList.contains('typed'));
    ok('queue button enabled', !$('.send.queue').disabled);
    // type lab
    click('[data-ctl=theme] [data-val=light]');
    ok('light theme applied', getComputedStyle($('#root')).getPropertyValue('--bg').trim() === '#f5f5f7');
    click('[data-ctl=size] [data-val="16"]');
    ok('size override applied', getComputedStyle($('#root')).getPropertyValue('--fs').trim() === '16px');
    click('[data-ctl=ink] [data-val=high]');
    ok('contrast table lists 4 rows', $$('.ratio div').length === 4);
    ok('std/high ink pass 4.5 on i1..i3', !$$('.ratio .no').some(n => n.textContent.includes('<4.5')));
    click('[data-ctl=autospace] [data-val=off]');
    ok('autospace off class', $('#root').classList.contains('noauto'));
    click('[data-ctl=w] [data-val="560"]');
    ok('window narrows', getComputedStyle($('#root')).width === '560px');
    // replay finishes into settled
    click('[data-scene=replay]');
    ok('replay starts live', P.state().scene === 'replay');
    // Addendum 2: fresh agent / order / running
    click('[data-scene=fresh]');
    ok('fresh invites typing', $('.invite') && $('.invite').textContent.includes('Type a prompt below'));
    ok('fresh keeps the composer, no ring/model', !!$('#ta') && !$('.ring') && !$('.older'));
    click('[data-ctl=fresh] [data-val=F2]');
    ok('F2 is the quiet note', !!$('.quietnote') && !$('.invite'));
    click('[data-ctl=fresh] [data-val=F1]');
    click('[data-sugg]');
    ok('suggestion fills the composer', $('#ta').value.length > 5 && !$('#sendb').disabled);
    click('[data-ctl=ready] [data-val=starting]');
    $('#ta').value = 'hi'; $('#ta').dispatchEvent(new Event('input'));
    ok('starting agent: Send stays locked', $('#sendb').disabled);
    click('[data-ctl=ready] [data-val=ready]');
    $('#ta').value = 'hello agent'; $('#ta').dispatchEvent(new Event('input'));
    click('#sendb');
    ok('send echoes the user bubble at once', P.state().scene === 'freshsent' && $('.bub') && $('.bub').textContent === 'hello agent');
    ok('echo is followed by a Working line', !!$('.workline'));
    click('[data-scene=order]');
    const clumped = (() => { click('[data-ctl=order] [data-val=before]'); return $$('.log .prose').length; })();
    const ordered = (() => { click('[data-ctl=order] [data-val=after]'); return $$('.log .prose').length; })();
    ok('before merges replies, after keeps them apart', clumped === 2 && ordered === 4);
    const kinds = [...document.querySelectorAll('.log .turn > *')].map(n => n.classList.contains('prose') ? 'p' : n.classList.contains('run') ? 'r' : '-').join('');
    ok('after alternates reply / tools', /^r?p(rp)+/.test(kinds.replace(/-/g, '')));
    click('[data-scene=running]');
    ok('L1 shimmers the live verbs and spins', !!$('.runbtn .spin') && !!$('.runbtn .shim'));
    click('[data-ctl=load] [data-val=L2]');
    ok('L2 shows elapsed', !!$('.runbtn .el') && !!$('.trow .meta.live'));
    click('[data-ctl=load] [data-val=L3]');
    ok('L3 shows the indeterminate bar', !!$('.indet'));
  } catch (e) { return String(e.message); }
  return 'PASS ' + checks.length + ' checks';
})();
