// Historical twixtui storyboard: the earlier three-tier interface before max
// and placement-only hint labels. Kept with the published illustrative movie,
// not a statement of the current UI. See docs/MANUAL.md for current behavior.
// Frames are authored recreations, not terminal captures.
const { useComposition, CompositionStage, Shot, Captions, Easing, animate, clamp } = window;

// ---- theme palettes (internal/theme/theme.go, hex for hex) ----
const THEMES = {
  classic: { VerticalPeg:'#e05252', VerticalLink:'#a83b3b', HorizontalPeg:'#7d8cc4', HorizontalLink:'#57639b', Grid:'#7a7a85', BorderRow:'#9a9aa5', Cursor:'#f0c040', Highlight:'#5fd38d', LastMove:'#e0e0e6', Text:'', Dim:'#8a8a95', Warning:'#e0952a' },
  slate:   { VerticalPeg:'#5fa8d3', VerticalLink:'#3d7fa3', HorizontalPeg:'#e0a458', HorizontalLink:'#b07c3c', Grid:'#5c6670', BorderRow:'#7a848e', Cursor:'#f2f2f2', Highlight:'#8ed081', LastMove:'#c9d1d9', Text:'#d7dde3', Dim:'#7d8994', Warning:'#e5c07b' },
  paper:   { VerticalPeg:'#9b2226', VerticalLink:'#bb4a4a', HorizontalPeg:'#1d3557', HorizontalLink:'#456990', Grid:'#9a9a92', BorderRow:'#77776f', Cursor:'#0a6e4a', Highlight:'#8a4a00', LastMove:'#3d3d38', Text:'#22221e', Dim:'#6b6b64', Warning:'#8a5a00' },
  mono:    { VerticalPeg:'', VerticalLink:'', HorizontalPeg:'', HorizontalLink:'', Grid:'', BorderRow:'', Cursor:'', Highlight:'', LastMove:'', Text:'', Dim:'', Warning:'' },
};
const FG = '#d6d6dc'; // the terminal's own foreground (Text "" inherits it)
const T_ = THEMES.classic;
const styles = (th) => ({
  hole: { c: th.Grid }, pegV: { c: th.VerticalPeg, b: 1 }, pegH: { c: th.HorizontalPeg, b: 1 },
  linkV: { c: th.VerticalLink }, linkH: { c: th.HorizontalLink }, cursor: { c: th.Cursor, b: 1 },
  hl: { c: th.Highlight }, last: { c: th.LastMove, b: 1 }, label: { c: th.BorderRow },
  labelV: { c: th.VerticalPeg }, labelH: { c: th.HorizontalPeg }, title: { c: th.Text, b: 1 },
  text: { c: th.Text }, status: { c: th.Dim }, msg: { c: th.Warning },
});
const ST = styles(T_);

// ---- segment rows ----
const S = (t, s) => ({ t, c: (s && s.c) || '', b: !!(s && s.b), bg: (s && s.bg) || '' });
const rowW = (r) => r.reduce((n, s) => n + [...s.t].length, 0);
const padRow = (r, w) => { const n = rowW(r); return n < w ? r.concat([S(' '.repeat(w - n))]) : r; };
const pad = (s, w) => s + ' '.repeat(Math.max(0, w - [...s].length));
const wrap = (text, w) => { const out = []; let line = ''; for (const word of text.split(' ')) { if (line && [...line].length + 1 + [...word].length > w) { out.push(line); line = word; } else line = line ? line + ' ' + word : word; } if (line) out.push(line); return out; };

// ---- chooser panel (menu.go listPanel) ----
function listPanel(title, opts, sel, help, width, height, preview) {
  const out = [[S(title, ST.title)], []];
  const helpLines = help ? wrap(help, width).slice(0, 2) : [];
  const rows = opts.map((o, i) => i === sel ? [S('> ', ST.cursor), S(o, ST.text)] : [S('  '), S(o, ST.text)]);
  out.push(...rows);
  if (preview && preview.length) out.push([], ...preview);
  while (out.length < height - helpLines.length) out.push([]);
  helpLines.forEach((l) => out.push([S(l, ST.text)]));
  return out.slice(0, height);
}
const FRONT = [
  ['Play', 'A new game: against the computer, at this keyboard, or over the network.'],
  ['Continue a saved game', 'Pick up an unfinished game exactly where it was left.'],
  ['Watch a finished game', 'Step through a finished or imported game move by move.'],
  ['Learn to play', 'The tutorial, the written rules, and the introduction again.'],
  ['Leaderboard', 'Ratings and results of every game recorded on this machine.'],
  ['Settings', 'Colours, what a new game starts from, hints, and who is playing.'],
  ['Quit', 'Leave twixtui.'],
];
const WHO = [
  ['the computer', 'Three engine tiers, beginner to pro. The default game is enter all the way.'],
  ['someone at this keyboard', 'Two players taking turns on one machine, on one board.'],
  ['someone over the network', 'A direct connection, a relay, or a correspondence game played by exchanging codes.'],
];
const TIERS = [
  ['beginner', 'one move ahead, counting only how many pegs each side still needs, answered at once: takes a win and blocks one, but has no plan'],
  ['intermediate', 'three moves ahead with the full evaluation, still near-instant: punishes a loose chain'],
  ['pro', 'thinks for up to three seconds, five to seven moves ahead, extending forced lines: the strongest play on offer'],
];
const SIDES = [
  ['vertical', 'You join the top and bottom borders. Vertical always moves first.'],
  ['horizontal', 'You join the left and right borders, and move second.'],
  ['random', 'Let twixtui pick one of the two for you.'],
];
const SETTINGS = [
  ['Colours — classic', 'The colour scheme the board and the panels are drawn in.'],
  ['Rules — std', "What a new game's rules question starts at. Any game can still answer differently."],
  ['Board — 24x24', "What a new game's board question starts at."],
  ['Hints — offered', 'Whether a game against the computer offers advice on your turn.'],
  ['Profile — Bálint', 'Play as somebody else on this machine.'],
];
const COLOURS = [
  ['classic', 'red and indigo, after the printed board game, for a dark terminal'],
  ['mono', 'no colour, distinguishes players by shape alone'],
  ['paper', 'dark ink, for a light terminal'],
  ['slate', 'muted blue and amber, for a dark terminal'],
];
const chooser = (title, opts, sel, w, h, preview) => listPanel(title, opts.map((o) => o[0]), sel, opts[sel][1], w, h, preview);
const HINT_FRONT = '↑/↓ k/j move · enter choose · q quit · ctrl+p/ctrl+n move';
const HINT_FORM = '↑/↓ k/j move · enter choose · esc back · q quit · ctrl+p/ctrl+n move';

// ---- homage cover (internal/cover/homage.go at 60x31, colour) ----
const H = { skyHigh:'#a897b2', sky:'#9d8ba8', skyDusk:'#a185a3', far:'#756386', ink:'#221b26', cream:'#eae0c9', board:'#d3a24b', hole:'#6e5628', red:'#aa2d28', black:'#2b241f' };
const LET_T = ['█████', '▀▀█▀▀', '  █  ', '  █  ', ' ▄█▄ '];
const LETTERS = [LET_T, ['█     █', '█  ▄  █', '█  █  █', '█ █ █ █', '▝█▘ ▝█▘'], ['█', ' ', '█', '█', '█'], ['█▖ ▗█', ' █ █ ', '  █  ', ' █ █ ', '█▘ ▝█'], LET_T];
const WORDMARK = [0, 1, 2, 3, 4].map((r) => LETTERS.map((l) => l[r]).join(' '));
const WIDE = { '█':'██', '▀':'▀▀', '▄':'▄▄', '▝':' ▀', '▘':'▀ ', '▗':' ▄', '▖':'▄ ', ' ':'  ' };
function pegSprite(ph) {
  const r = [];
  if (ph < 9) { r.push('███', '▝█▘'); for (let i = 0; i < ph - 4; i++) r.push(' █ '); r.push('▗█▖', '███'); }
  else if (ph < 14) { r.push('█████', '▝███▘', ' ▝█▘ '); for (let i = 0; i < ph - 6; i++) r.push('  █  '); r.push(' ▗█▖ ', '▗███▖', '█████'); }
  else { r.push('███████', '▝█████▘', ' ▝███▘ '); for (let i = 0; i < ph - 6; i++) r.push('  ▐█▌  '); r.push(' ▗███▖ ', '▗█████▖', '███████'); }
  return r;
}
let COVER_CACHE = null;
function coverCells() {
  if (COVER_CACHE) return COVER_CACHE;
  const w = 60, h = 31;
  const cv = Array.from({ length: h }, (_, y) => Array.from({ length: w }, () => ({ ch: ' ', fg: H.ink, bg: y < 10 ? H.skyHigh : y < 20 ? H.sky : H.skyDusk })));
  const set = (x, y, ch, fg, bg) => { if (x >= 0 && x < w && y >= 0 && y < h) { const c = cv[y][x]; c.ch = ch; if (fg) c.fg = fg; if (bg) c.bg = bg; } };
  const sprite = (x0, y0, rows, fg) => rows.forEach((r, dy) => [...r].forEach((ch, dx) => { if (ch !== ' ') set(x0 + dx, y0 + dy, ch, fg); }));
  const text = (x0, y, s, fg) => [...s].forEach((ch, i) => set(x0 + i, y, ch, fg));
  const wm = WORDMARK.map((r) => [...pad(r, 27)].map((ch) => WIDE[ch] || ch + ch).join(''));
  sprite(3, 1, wm, H.ink);
  text(17, 7, 'A GAME OF BARRIERS FOR TWO', H.cream);
  const boardTop = 22;
  for (let x = 0; x < w; x++) set(x, boardTop, '▄', H.board);
  for (let y = boardTop + 1; y < h; y++) for (let x = 0; x < w; x++) set(x, y, ' ', H.ink, H.board);
  for (let y = boardTop + 1; y < h; y++) for (let x = 1 + ((y - boardTop) % 2) * 2; x < w; x += 5) set(x, y, '•', H.hole);
  const link = (x0, y0, x1, y1, fg) => { const dx = x1 - x0, dy = y1 - y0; const yAt = (x) => y0 + Math.trunc((2 * dy * (x - x0) + dx) / (2 * dx)); for (let x = x0; x <= x1; x++) { const y = yAt(x); let r = '─'; if (x < x1) { const nx = yAt(x + 1); if (nx > y) r = '╲'; else if (nx < y) r = '╱'; } set(x, y, r, fg); } };
  const chain = [{ x: 15, top: 15, bottom: 26 }, { x: 30, top: 16, bottom: 23 }, { x: 44, top: 15, bottom: 23 }];
  for (let i = 0; i < chain.length - 1; i++) link(chain[i].x + 2, chain[i].top, chain[i + 1].x - 1, chain[i + 1].top, H.ink);
  link(5, 18, 8, 18, H.red);
  const peg = (x, bottom, ph, fg) => { const sp = pegSprite(ph); sprite(x - Math.floor([...sp[0]].length / 2), bottom - ph + 1, sp, fg); };
  chain.forEach((p) => peg(p.x, p.bottom, p.bottom - p.top + 1, H.black));
  peg(10, 29, 12, H.red);
  peg(2, 30, 13, H.red);
  peg(57, 30, 13, H.red);
  text(13, 29, ' WIT AGAINST WIT, WALL AGAINST WALL ', H.ink);
  COVER_CACHE = cv.map((row) => { const segs = []; for (const c of row) { const l = segs[segs.length - 1]; if (l && l.c === c.fg && l.bg === c.bg) l.t += c.ch; else segs.push({ t: c.ch, c: c.fg, bg: c.bg, b: false }); } return segs; });
  return COVER_CACHE;
}

// ---- board renderer (internal/ui/board.go, compact scale) ----
function renderBoard(n, pegs, links, opt, st) {
  st = st || ST;
  const w = 2 * (n - 1) + 3;
  const g = Array.from({ length: n }, () => Array.from({ length: w }, () => null));
  const set = (x, y, ch, s) => { if (y >= 0 && y < n && x >= 0 && x < w) g[y][x] = { ch, s }; };
  for (let r = 0; r < n; r++) for (let c = 0; c < n; c++) { const corner = (r === 0 || r === n - 1) && (c === 0 || c === n - 1); if (!corner) set(1 + 2 * c, r, '·', st.hole); }
  for (const [c1, r1, c2, r2, p] of links) {
    const s = p === 'V' ? st.linkV : st.linkH;
    const a = c1 <= c2 ? { c: c1, r: r1 } : { c: c2, r: r2 }, b = c1 <= c2 ? { c: c2, r: r2 } : { c: c1, r: r1 };
    if (Math.abs(c1 - c2) === 1) { set(1 + 2 * a.c + 1, (r1 + r2) / 2, b.r > a.r ? '╲' : '╱', s); }
    else { const xa = 1 + 2 * a.c; set(xa + 1, a.r, '─', s); set(xa + 2, a.r, '─', s); set(xa + 3, a.r, b.r > a.r ? '╮' : '╯', s); set(xa + 3, b.r, b.r > a.r ? '╰' : '╭', s); }
  }
  for (const p of pegs) { const last = opt.last && opt.last[0] === p.c && opt.last[1] === p.r; set(1 + 2 * p.c, p.r, last ? (p.p === 'V' ? '◉' : '◎') : (p.p === 'V' ? '●' : '○'), last ? st.last : (p.p === 'V' ? st.pegV : st.pegH)); }
  for (const [c, r] of (opt.highlights || [])) { set(2 * c, r, '(', st.hl); set(2 * c + 2, r, ')', st.hl); }
  if (opt.cursor) { const [c, r] = opt.cursor; set(2 * c, r, '[', st.cursor); set(2 * c + 2, r, ']', st.cursor); }
  const gw = String(n).length;
  const rows = [];
  const letters = [S(' '.repeat(gw + 3))];
  for (let c = 0; c < n; c++) letters.push(S(String.fromCharCode(65 + c), c === 0 || c === n - 1 ? st.labelH : st.label), S(' '));
  rows.push(letters);
  for (let r = 0; r < n; r++) {
    const row = [S(' '), S(String(r + 1).padStart(gw), r === 0 || r === n - 1 ? st.labelV : st.label), S(' ')];
    for (const cell of g[r]) { if (!cell) { const l = row[row.length - 1]; if (!l.c && !l.b) l.t += ' '; else row.push(S(' ')); } else row.push(S(cell.ch, cell.s)); }
    rows.push(row);
  }
  return rows;
}
// README position, 24x24 (assets/board.svg glyph for glyph)
const cols = 'ABCDEFGHIJKLMNOPQRSTUVWX';
const P = (s) => [cols.indexOf(s[0]), parseInt(s.slice(1), 10) - 1];
const V_CHAIN = ['L1', 'K3', 'M4', 'L6', 'N7', 'M9', 'O10', 'N12', 'P13', 'O15'];
const H_CHAIN = ['A12', 'C11', 'E12', 'G11', 'I12', 'K11', 'M12', 'O11', 'Q12', 'S11'];
function position(withBotMove) {
  const hs = withBotMove ? H_CHAIN : H_CHAIN.slice(0, -1);
  const pegs = [...V_CHAIN.map((s) => { const [c, r] = P(s); return { c, r, p: 'V' }; }), ...hs.map((s) => { const [c, r] = P(s); return { c, r, p: 'H' }; })];
  const links = [];
  for (let i = 0; i < V_CHAIN.length - 1; i++) links.push([...P(V_CHAIN[i]), ...P(V_CHAIN[i + 1]), 'V']);
  for (let i = 0; i < hs.length - 1; i++) { if (hs[i] === 'M12') continue; links.push([...P(hs[i]), ...P(hs[i + 1]), 'H']); } // M12–O11 is refused: O10–N12 crosses it
  return { pegs, links };
}
const KEYHELP = [['h/←', 'move left'], ['j/↓', 'move down'], ['k/↑', 'move up'], ['l/→', 'move right'], ['H', 'jump left'], ['J', 'jump down'], ['K', 'jump up'], ['L', 'jump right'], ['g', 'top edge'], ['G', 'bottom edge'], ['0', 'left edge'], ['$', 'right edge'], ['space', 'place peg'], ['enter', 'place / commit turn'], ['x', 'link mode on/off'], ['a', 'abort turn'], ['q', 'leave the game'], ['ctrl+c', 'leave and end the program'], ['?', 'hint: what to play and why'], ['d', 'offer or accept a draw'], ['r', 'resign']];
// The hint panel in the program is the engine's own writing: internal/app/
// hint.go lays out Headline, Detail and the legend exactly as bot.Hint
// returned them for the position that was searched, which is why it can talk
// in pegs and routes. Nothing here ran a search, so this tour labels the panel
// for what it is and says what the engine does instead of quoting counts no
// search produced.
const HINT_LABEL = 'hint · illustrative';
const HINT_ILLUSTRATIVE = [
  "Play M13 to cut horizontal's cheapest route.",
  'In the program the engine writes this text itself, for the position it has just searched.',
  'marked on the board: M13 is the move, the rest are the holes the explanation refers to',
];
function gamePanel(o) {
  const w = 36, L = [];
  const add = (segs) => L.push(segs);
  add(o.headline);
  if (o.hint) { add([]); add([S(o.hintLabel || HINT_LABEL, ST.text)]); o.hint.forEach((t) => wrap(t, w).forEach((l) => add([S(l, ST.text)]))); }
  add([]);
  const seat = (side, glyph, sty, name, axis, turn) => add([S(turn ? '> ' : '  ', ST.text), S(glyph, sty), S(' ' + side + ' ' + name + ' · ' + axis, ST.text)]);
  seat('vertical', '●', ST.pegV, o.vName, 'top-bottom', o.turn === 'V');
  seat('horizontal', '○', ST.pegH, o.hName, 'left-right', o.turn === 'H');
  add([]); add([S('move ' + o.move, ST.text)]); if (o.last) add([S('last ' + o.last, ST.text)]);
  add([]); add([S('twixtui · ' + o.kind, ST.title)]); add([S(o.rules, ST.text)]);
  add([]);
  KEYHELP.forEach(([k, h]) => add([S(pad(k, 7), ST.label), S(h, ST.text)]));
  return L;
}
function gameFrame(board, panel) {
  const out = [];
  for (let i = 0; i < 31; i++) { const b = board[i] || []; const p = panel[i]; out.push(p ? padRow(b, 54).concat(p) : b); }
  return out;
}
const GAME_STATUS = 'space place · enter commit · x links · a abort · q leave · ? hint · d draw · r resign';

// theme chooser sample (menu.go newThemeSample: 6x6, C2● B4○ D4● D5○, cursor B2, last D5, highlight E3)
const sample = (th) => renderBoard(6, [{ c: 2, r: 1, p: 'V' }, { c: 1, r: 3, p: 'H' }, { c: 3, r: 3, p: 'V' }, { c: 3, r: 4, p: 'H' }], [[2, 1, 3, 3, 'V'], [1, 3, 3, 4, 'H']], { cursor: [1, 1], last: [3, 4], highlights: [[4, 2]] }, styles(th));

// standings (menu.go standingsLines)
const STANDINGS = (() => {
  const row = (rank, name, r, g, s) => [S(pad(rank, 4) + pad(name, 16) + ' ' + String(r).padStart(6) + ' ' + String(g).padStart(6) + ' ' + (s + '%').padStart(6), ST.text)];
  return [[S('Leaderboard', ST.title)], [S(pad('#', 4) + pad('player', 16) + ' ' + 'rating'.padStart(6) + ' ' + 'games'.padStart(6) + ' ' + 'score'.padStart(6), ST.text)],
    row('1', 'Sára', 1231, 9, 56), row('2', 'Bálint', 1204, 14, 46), [], [S("Bots are not ranked: a tier's rating is fixed, not earned.", ST.text)], [],
    row('', 'pro bot', 1800, 11, 64), row('', 'intermediate bot', 1400, 8, 38), row('', 'beginner bot', 1000, 4, 0)];
})();

// ---- motion helpers ----
const MOTION = {
  enter: (from, to, start, end) => animate({ from, to, start, end, ease: Easing.easeInOutCubic }),
  typed: (T, s, start, cps) => s.slice(0, clamp(Math.floor((T - start) * (cps || 22)), 0, s.length)),
  reveal: (T, start, dur) => clamp((T - start) / (dur || 0.5), 0, 1),
};
const stepAt = (T, steps, init) => { let v = init; for (const [t, val] of steps) if (T >= t) v = val; return v; };
const blink = (T) => Math.floor(T * 2) % 2 === 0;

// ---- terminal window ----
const CW = 7.8, LH = 18, COLS = 120, ROWS = 32, TERM_W = 1180, TERM_H = 640, PITCH = 1400;
// quadrant / half blocks painted as boxes (no font fallback): [top-left, top-right, bottom-left, bottom-right]
const BLOCKS = { '█':[1,1,1,1], '▀':[1,1,0,0], '▄':[0,0,1,1], '▌':[1,0,1,0], '▐':[0,1,0,1], '▘':[1,0,0,0], '▝':[0,1,0,0], '▖':[0,0,1,0], '▗':[0,0,0,1], '▚':[1,0,0,1], '▞':[0,1,1,0], '▛':[1,1,1,0], '▜':[1,1,0,1], '▙':[1,0,1,1], '▟':[0,1,1,1] };
function Cell({ ch, c }) {
  const q = BLOCKS[ch];
  if (!q) return <span style={{ display: 'inline-block', width: CW, textAlign: 'center' }}>{ch}</span>;
  const col = c || FG;
  return <span style={{ display: 'inline-block', width: CW, height: LH, verticalAlign: 'top', position: 'relative' }}>
    {q.map((on, i) => on ? <span key={i} style={{ position: 'absolute', left: i % 2 ? CW / 2 : 0, top: i < 2 ? 0 : LH / 2, width: CW / 2 + 0.3, height: LH / 2 + 0.3, background: col }} /> : null)}
  </span>;
}

function Term({ i, title, screen, T }) {
  const left = i * PITCH + 50, top = 6;
  const { rows, status, reveal, prompt, cursor } = screen;
  const shown = Math.floor(reveal * 31);
  const lines = [];
  if (prompt !== undefined) {
    lines.push([S('$ ', ST.status), S(prompt, { c: FG }), S(cursor ? '▌' : ' ', ST.cursor)]);
  } else {
    for (let r = 0; r < 31; r++) lines.push(r < shown ? (rows[r] || []) : []);
    lines.push(reveal >= 1 ? [S(status, ST.status)] : []);
  }
  return (
    <div style={{ position: 'absolute', left, top, width: TERM_W, height: TERM_H, background: '#14141b', borderRadius: 10, border: '1px solid #2a2a33', overflow: 'hidden', boxShadow: '0 30px 80px rgba(0,0,0,.6)' }}>
      <div style={{ height: 36, background: '#1c1c24', borderBottom: '1px solid #2a2a33', display: 'flex', alignItems: 'center', padding: '0 14px', gap: 10 }}>
        <span style={{ width: 12, height: 12, borderRadius: 6, background: '#3a3a46' }} /><span style={{ width: 12, height: 12, borderRadius: 6, background: '#3a3a46' }} /><span style={{ width: 12, height: 12, borderRadius: 6, background: '#3a3a46' }} />
        <span style={{ flex: 1, textAlign: 'center', color: '#8a8a95', fontSize: 13, marginRight: 56 }}>{title}</span>
      </div>
      <pre style={{ margin: 0, padding: '8px 16px', fontFamily: 'inherit', fontSize: 13, lineHeight: LH + 'px', color: FG, whiteSpace: 'pre', width: COLS * CW, height: ROWS * LH }}>
        {lines.map((segs, r) => <div key={r} style={{ height: LH }}>{segs.map((s, k) => <span key={k} style={{ display: 'inline-block', width: [...s.t].length * CW, height: LH + 0.6, marginBottom: -0.6, lineHeight: LH + 'px', verticalAlign: 'top', overflow: 'hidden', whiteSpace: 'pre', letterSpacing: 0, color: s.c || undefined, fontWeight: s.b ? 700 : 400, background: s.bg || undefined }}>{[...s.t].map((ch, j) => <Cell key={j} ch={ch} c={s.c} />)}</span>)}</div>)}
      </pre>
    </div>
  );
}

// ---- topic switch: a shell where the next topic is typed ----
function screenSwitch(T, start, title, sub) {
  const cmd = 'echo "' + title + '"';
  const typed = MOTION.typed(T, cmd, start + 0.3, 30);
  const done = typed.length === cmd.length && T > start + 0.3 + cmd.length / 30 + 0.3;
  const clr = MOTION.typed(T, 'clear', start + 4.2, 20);
  const cleared = T >= start + 4.2 + 5 / 20 + 0.25;
  const cur = blink(T) ? '▌' : ' ';
  if (cleared) return { rows: [[S('$ ', ST.status), S(cur, ST.cursor)]], status: '', reveal: 1 };
  const rows = [[S('$ ', ST.status), S(typed, { c: FG }), S(done ? '' : cur, ST.cursor)]];
  if (done) rows.push([S(title, { c: FG, b: 1 })], [S(sub, ST.status)], [], [S('$ ', ST.status), S(clr, { c: FG }), S(cur, ST.cursor)]);
  return { rows, status: '', reveal: 1 };
}

// ---- screens over authored time ----
function screenT1(T, C) {
  // topic shell → prompt → menu → who → tier → side → game → hint
  if (T < C.Menu + 0.2) return screenSwitch(T, 0, '01 · Playing the bot', 'the menu · three tiers · a 24x24 game · a hint that explains itself');
  if (T < C.Menu + 1.05) return { prompt: MOTION.typed(T, 'twixtui', C.Menu + 0.3), cursor: blink(T), reveal: 1 };
  const menuStart = C.Menu + 1.05;
  const M = C.Menu + 1;
  const sel = stepAt(T, [[M + 1.0, 1], [M + 1.5, 2], [M + 2.0, 3], [M + 2.5, 4], [M + 3.0, 3], [M + 3.3, 2], [M + 3.6, 1], [M + 3.9, 0]], 0);
  if (T < C.Tiers) {
    const left = chooser('twixtui — Bálint', FRONT, sel, 58, 31);
    const art = coverCells();
    const rows = left.map((l, k) => padRow(l, 60).concat(art[k] || []));
    return { rows, status: HINT_FRONT, reveal: MOTION.reveal(T, menuStart, 0.6) };
  }
  if (T < C.Tiers + 1.0) return { rows: chooser('Who do you want to play?', WHO, 0, 120, 31), status: HINT_FORM, reveal: 1 };
  if (T < C.Tiers + 4.2) {
    const tsel = stepAt(T, [[C.Tiers + 1.9, 0], [C.Tiers + 2.5, 1], [C.Tiers + 3.1, 2]], 1);
    return { rows: chooser('How strong an opponent?', TIERS, tsel, 120, 31), status: HINT_FORM, reveal: 1 };
  }
  if (T < C.Game) return { rows: chooser('Which side do you play?', SIDES, 0, 120, 31), status: HINT_FORM, reveal: 1 };
  // game
  const botDone = T >= C.Game + 3.4;
  const pos = position(botDone);
  const thinking = T >= C.Game + 1.2 && !botDone;
  const spin = ['|', '/', '-', '\\'][Math.floor(T * 8) % 4];
  const cur = stepAt(T, [[C.Game + 4.2, [14, 16]], [C.Game + 4.5, [15, 16]], [C.Game + 4.8, [16, 16]], [C.Game + 5.2, [16, 15]], [C.Game + 5.6, [16, 14]]], [13, 16]);
  const asking = T >= C.Hint + 0.2 && T < C.Hint + 1.6;
  const shown = T >= C.Hint + 1.6;
  const hl = shown ? [P('M13'), P('K13'), P('O12'), P('P12'), P('R12')] : [];
  const board = renderBoard(24, pos.pegs, pos.links, { cursor: cur, last: botDone ? P('S11') : P('O15'), highlights: hl });
  const headline = thinking ? [S('pro bot is thinking ' + spin, ST.text)] : botDone ? [S('● ', ST.pegV), S('vertical to move: Bálint', ST.text)] : [S('● ', ST.pegV), S('vertical to move: Bálint', ST.text)];
  // While the engine is being asked, the panel says only that (hint.go's
  // hintSearching), so that beat needs no illustrative label.
  const hint = asking ? ['asking the engine'] : shown ? HINT_ILLUSTRATIVE : null;
  const panel = gamePanel({ headline, hint, hintLabel: asking ? 'hint' : HINT_LABEL, vName: 'Bálint', hName: 'pro bot', turn: botDone || !thinking && T < C.Game + 1.2 ? (botDone ? 'V' : 'H') : 'H', move: botDone ? 21 : 20, last: botDone ? 'S11' : 'O15', kind: 'bot', rules: 'std rules · 24x24' });
  return { rows: gameFrame(board, panel), status: GAME_STATUS, reveal: MOTION.reveal(T, C.Game - 0.3, 0.7) };
}
// A whole pairing code: six characters of room, then sixteen of key, in
// Crockford base32 and dashed groups (internal/netplay/auth.go). This is the
// documentation's own example code — synthetic, but the shape the program
// prints and accepts.
const PAIRING_CODE = 'ABCDEF-GHJKMNPQ-RSTVWXYZ';
const HOST_CMD = 'twixtui play host --relay relay.example';
const JOIN_CMD = 'twixtui play join --relay relay.example ' + PAIRING_CODE;

function screenT2(T, C) {
  const s = C.Remote - 0.6;
  if (T < s) return screenSwitch(T, C.Topic2, '02 · Playing together', 'direct · through a relay you run · by correspondence');
  if (T < s + 1.9) return { prompt: MOTION.typed(T, HOST_CMD, s, 26), cursor: blink(T), reveal: 1 };
  if (T < C.Remote + 3.4) {
    // internal/cli/play.go, the --relay branch of `play host`: the code, the
    // line the other player runs, then what is being waited for. ctrl+c is
    // what gives up — the game's own key help is not on screen yet.
    const rows = [
      [S('$ ', ST.status), S(HOST_CMD, { c: FG })],
      [S('Pairing code: ' + PAIRING_CODE, ST.text)],
      [S('Your opponent runs: ' + JOIN_CMD, ST.text)],
      [],
      [S('Waiting for them to join. Press ctrl+c to give up.', ST.label)],
    ];
    return { rows, status: '', reveal: MOTION.reveal(T, s + 1.9, 0.4) };
  }
  const empty = renderBoard(24, [], [], { cursor: [11, 11] });
  const panel = gamePanel({ headline: [S('● ', ST.pegV), S('vertical to move: Bálint', ST.text)], vName: 'Bálint', hName: 'Sára (remote)', turn: 'V', move: 1, last: '', kind: 'remote', rules: 'std rules · 24x24' });
  return { rows: gameFrame(empty, panel), status: 'space place · enter commit · x links · a abort · q leave · d draw · r resign', reveal: MOTION.reveal(T, C.Remote + 3.4, 0.5) };
}
function screenT3(T, C) {
  const s = C.Remote + 2.35;
  // The guest's line is typed once the camera is on the guest — the shot
  // arrives at C.Remote + 2.35 and the line starts there, so the whole
  // pairing code is entered on camera instead of off it. It arrives at paste
  // speed, because a code is pasted rather than typed out, and that is what
  // leaves room for the guest's own waiting copy before the two terminals
  // cut to the game at C.Remote + 3.4.
  if (T < s + 0.72) return { prompt: MOTION.typed(T, JOIN_CMD, s, 96), cursor: blink(T), reveal: 1 };
  if (T < C.Remote + 3.4) {
    // internal/cli/play.go, the --relay branch of `play join`: the code is
    // echoed back because a mistyped one is the ordinary reason for a wait
    // that never ends.
    const rows = [
      [S('$ ', ST.status), S(JOIN_CMD, { c: FG })],
      [S('Pairing code: ' + PAIRING_CODE, ST.text)],
      [S('Joining through the relay at relay.example.', ST.text)],
      [],
      [S('Waiting for the host. Press ctrl+c to give up.', ST.label)],
    ];
    return { rows, status: '', reveal: MOTION.reveal(T, s + 0.72, 0.18) };
  }
  const empty = renderBoard(24, [], [], { cursor: [11, 11] });
  const panel = gamePanel({ headline: [S('● ', ST.pegV), S('vertical to move: Bálint', ST.text)], vName: 'Bálint (remote)', hName: 'Sára', turn: 'V', move: 1, last: '', kind: 'remote', rules: 'std rules · 24x24' });
  return { rows: gameFrame(empty, panel), status: 'space place · enter commit · x links · a abort · q leave · d draw · r resign', reveal: MOTION.reveal(T, C.Remote + 3.4, 0.5) };
}
function screenT4(T, C) {
  const s = C.Standings - 0.5;
  if (T < s) return screenSwitch(T, C.Topic3, '03 · Around the board', 'profiles · the leaderboard · four colour schemes');
  if (T < s + 0.9) return { prompt: MOTION.typed(T, 'twixtui', s), cursor: blink(T), reveal: 1 };
  const sel = stepAt(T, [[C.Standings + 0.5, 1], [C.Standings + 0.65, 2], [C.Standings + 0.8, 3], [C.Standings + 0.95, 4]], 0);
  if (T < C.Standings + 1.4) {
    const left = chooser('twixtui — Bálint', FRONT, sel, 58, 31);
    const art = coverCells();
    return { rows: left.map((l, k) => padRow(l, 60).concat(art[k] || [])), status: HINT_FRONT, reveal: MOTION.reveal(T, s + 0.9, 0.5) };
  }
  return { rows: STANDINGS, status: HINT_FORM, reveal: MOTION.reveal(T, C.Standings + 1.4, 0.4) };
}
function screenT5(T, C) {
  const s = C.Themes - 0.7;
  if (T < s + 0.9) return { prompt: MOTION.typed(T, 'twixtui', s), cursor: blink(T), reveal: 1 };
  if (T < C.Themes + 0.4) {
    const left = chooser('twixtui — Bálint', FRONT, 5, 58, 31);
    const art = coverCells();
    return { rows: left.map((l, k) => padRow(l, 60).concat(art[k] || [])), status: HINT_FRONT, reveal: MOTION.reveal(T, s + 0.9, 0.5) };
  }
  if (T < C.Themes + 1.0) return { rows: chooser('Settings', SETTINGS, 0, 120, 31), status: HINT_FORM, reveal: 1 };
  const sel = stepAt(T, [[C.Themes + 1.9, 1], [C.Themes + 2.7, 2], [C.Themes + 3.5, 3]], 0);
  const name = COLOURS[sel][0];
  return { rows: chooser('Colours', COLOURS, sel, 120, 31, sample(THEMES[name])), status: HINT_FORM, reveal: 1 };
}

// ---- camera ----
function camAt(T, C) {
  const cx = (i) => i * PITCH + 640;
  const K = [
    [0, cx(0), 326, 1],
    [C.Tiers + 0.8, cx(0), 326, 1], [C.Tiers + 1.6, cx(0) - 310, 190, 1.9],
    [C.Game - 0.6, cx(0) - 310, 190, 1.9], [C.Game, cx(0), 326, 1],
    [C.Game + 1.4, cx(0), 326, 1], [C.Game + 2.2, cx(0) - 230, 310, 1.55],
    [C.Hint - 0.6, cx(0) - 230, 310, 1.55], [C.Hint, cx(0), 326, 1],
    [C.Hint + 1.6, cx(0), 326, 1], [C.Hint + 2.4, cx(0) + 60, 180, 2.2],
    [C.Hint + 3.6, cx(0) + 60, 180, 2.2], [C.Hint + 4.4, cx(0) - 340, 290, 2.0],
    [C.Hint + 5.2, cx(0) - 340, 290, 2.0], [C.Topic2 - 0.6, cx(0), 326, 1],
    [C.Topic2 - 0.4, cx(0), 326, 1], [C.Topic2 + 0.4, cx(1), 326, 1],
    // The relay beat is the one place where the words are the content: the
    // pairing code, and the line the other player runs. One frame holding
    // both windows means 0.47 scale, which is where that text stops being
    // readable, and a tighter two-window frame would crop the 84-column
    // "Your opponent runs:" line. So the beat is staged as two shots — in on
    // the host while the code and its wait are up, then over to the guest
    // before its join line is typed, so the code is read once as the host
    // prints it and again as the guest enters it. The crossing pulls back to
    // 0.9 on the way so it reads as leaving one machine for the other rather
    // than as a whip. The old wide framing survives as the closer: once both
    // boards are up at C.Remote + 3.4 there is nothing left to read, and the
    // pull-back can carry the payoff — two terminals in one game.
    [C.Remote + 0.8, cx(1), 326, 1], [C.Remote + 1.2, cx(1) - 200, 215, 1.6],
    [C.Remote + 1.85, cx(1) - 200, 215, 1.6], [C.Remote + 2.1, cx(1) + PITCH / 2, 260, 0.9],
    [C.Remote + 2.35, cx(2) - 200, 215, 1.6], [C.Remote + 3.4, cx(2) - 200, 215, 1.6],
    [C.Remote + 3.9, cx(1) + PITCH / 2, 326, 0.47],
    [C.Topic3 - 0.2, cx(1) + PITCH / 2, 326, 0.47], [C.Topic3 + 0.2, cx(3), 326, 1],
    [C.Standings + 1.8, cx(3), 326, 1], [C.Standings + 2.6, cx(3) - 420, 160, 1.9],
    [C.Themes - 0.9, cx(3) - 420, 160, 1.9], [C.Themes - 0.1, cx(4), 326, 1],
    [C.Themes + 1.2, cx(4), 326, 1], [C.Themes + 2.0, cx(4) - 400, 210, 1.8],
    [C.Themes + 3.9, cx(4) - 400, 210, 1.8], [C.Themes + 4.7, cx(4), 326, 1],
  ];
  let a = K[0], b = K[0];
  for (let i = 0; i < K.length; i++) { if (K[i][0] <= T) { a = K[i]; b = K[Math.min(i + 1, K.length - 1)]; } }
  if (b[0] <= a[0]) return { x: a[1], y: a[2], s: a[3] };
  const p = Easing.easeInOutCubic(clamp((T - a[0]) / (b[0] - a[0]), 0, 1));
  return { x: a[1] + (b[1] - a[1]) * p, y: a[2] + (b[2] - a[2]) * p, s: a[3] + (b[3] - a[3]) * p };
}

function Piece({ captions }) {
  const { T, CUES: C } = useComposition();
  const cam = camAt(T, C);
  const items = [
    { at: 0.4, text: 'twixtui — TwixT in the terminal, one static binary · an animated tour, not a recording', until: C.Menu - 0.6 },
    { at: C.Menu + 1.4, text: 'the menu, with the 1962 lid drawn in character cells beside it' },
    { at: C.Tiers + 1.2, text: 'three bot tiers: beginner, intermediate, pro' },
    { at: C.Game + 0.6, text: 'a 24×24 game against the pro bot — every link is also a wall' },
    { at: C.Hint + 0.4, text: '? asks the engine what it would play, and why', until: C.Topic2 - 0.6 },
    { at: C.Remote + 0.4, text: 'a host waits through a relay; the guest joins with the pairing code', until: C.Topic3 - 0.7 },
    { at: C.Standings + 0.4, text: 'profiles with no passwords, and a leaderboard' },
    { at: C.Themes + 0.6, text: 'four colour schemes, previewed before you pick' },
    { at: C.Themes + 4.0, text: 'go install github.com/BAKocska/twixtui/cmd/twixtui@latest' },
  ];
  return (
    <div data-screen-label={'t=' + Math.floor(T) + 's'} style={{ position: 'absolute', inset: 0, overflow: 'hidden', background: '#0b0b0e', fontFamily: "'JetBrains Mono', ui-monospace, Menlo, Consolas, 'DejaVu Sans Mono', monospace" }}>
      <div style={{ position: 'absolute', left: 0, right: 0, top: 52, bottom: 0, overflow: 'hidden' }}>
      <div style={{ position: 'absolute', left: 0, top: 0, transformOrigin: '0 0', transform: `translate(640px,326px) scale(${cam.s}) translate(${-cam.x}px,${-cam.y}px)` }}>
        <Term i={0} title="twixtui — 120×32" screen={screenT1(T, C)} T={T} />
        <Shot from={C.Topic2 - 1} to={C.Topic3 + 1}><Term i={1} title="twixtui — host" screen={screenT2(T, C)} T={T} /></Shot>
        <Shot from={C.Remote + 1.2} to={C.Topic3 + 1}><Term i={2} title="twixtui — join" screen={screenT3(T, C)} T={T} /></Shot>
        <Shot from={C.Topic3 - 1} to={C.Themes}><Term i={3} title="twixtui — 120×32" screen={screenT4(T, C)} T={T} /></Shot>
        <Shot from={C.Themes - 1} to={1e9}><Term i={4} title="twixtui — 120×32" screen={screenT5(T, C)} T={T} /></Shot>
      </div>
      </div>
      {captions !== false && (
        <div style={{ position: 'absolute', left: 50, right: 50, top: 14, height: 30, display: 'flex', alignItems: 'center', color: '#9a9aa5', fontSize: 15 }}>
          <span style={{ color: '#8a8a95', whiteSpace: 'pre', opacity: items.some((it) => T >= it.at && T < (it.until != null ? it.until : (items[items.indexOf(it) + 1] ? items[items.indexOf(it) + 1].at : 1e9))) ? 1 : 0 }}>{'$ '}</span>
          <div style={{ position: 'relative', flex: 1, height: 30 }}>
            <Captions items={items} style={{ position: 'absolute', left: 0, top: 0, right: 0, bottom: 'auto', textAlign: 'left', font: "400 15px/30px 'JetBrains Mono', ui-monospace, Menlo, monospace", color: '#d6d6dc', textShadow: 'none' }} />
          </div>
        </div>
      )}
    </div>
  );
}

function TwixtDemo(props) {
  const captions = String(props.captions) !== 'false';
  return (
    <CompositionStage width={1280} height={720} scenes={window.OM_SCENES} playback={window.OM_PLAYBACK} bg="#0b0b0e">
      <Piece captions={captions} />
    </CompositionStage>
  );
}
window.TwixtDemo = TwixtDemo;
