# Changelog

All notable changes to twixtui are recorded here.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this
project uses [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- A front screen worth the name. The menu was a flat list that had grown by
  accretion, with three separate ways to start a game and no route at all to a
  finished one. Entries are grouped by how often somebody does the thing rather
  than by the mechanism behind it: Play asks who is on the other side as the first
  question of a game instead of being three doors; Continue and Watch are what a
  returning player does with games that already exist, and Watch reaches the replay
  viewer the menu could not; Learn gathers the tutorial, the written rules and the
  introduction; Settings gathers colours, the default ruleset, the default board and
  whether hints are offered, stored per machine as the colour scheme already was.
  The lists answer `j` and `k` as well as the arrows, resolved from the keymap, so
  rebinding the board's movement moves the menu too.
- A first-run introduction, skippable at every step and never shown twice. Five
  steps on the real board and the real engine: what the game is, the board and
  whose borders are whose, a turn is one peg, links form a knight's move away, and
  links block links. Nothing gates advancing — the invitations are invitations —
  and two keys leave from any step, both counting as seen, because somebody who
  skipped does not want it again tomorrow. Where the tutorial lives is a note left
  on the menu rather than a step, since the step naming it is the one a skipper
  never reaches. The flag belongs to the profile, not the machine, so the second
  person on a shared machine is still a newcomer.
- The cover of the 1962 box, in a terminal, without needing kitty graphics. Two
  artworks ship because neither wins everywhere: a projection of the project's own
  flat reduction of the lid composition, and hand-composed character art that
  answers below the size at which a projection stops reading, and on a terminal
  with no colour. Quadrant blocks beat half blocks at every size and braille beat
  the luminance ramp, so the losers were dropped; the 256-colour quantiser is
  optimal against the xterm cube by exhaustion. The picture is nine kilobytes,
  decoded on first use rather than at startup, and regenerable from a committed
  source. `TWIXTUI_COVER_ART` and `TWIXTUI_COVER_IMAGE` override the choice and
  the picture.
- A rematch on the game-over screen, with the sides swapped: vertical moves first
  and the opening advantage is real enough that the swap rule exists to blunt it,
  so a second game on the same seats would hand the same player the same edge
  twice.
- The winning chain is marked when a game ends, recovered from the link graph, so
  the player is not left tracing it across a 24×24 board.

### Changed

- Continuous integration is five jobs rather than one, on Linux and macOS. The
  end-to-end layer drives a real terminal through tmux and skipped silently when
  tmux was absent: twenty-one of twenty-two tests skipped while the job exited
  zero. tmux is installed and a step now fails the build on any skip in that
  package, proven against a negative control. A race job covers the packages with
  concurrency and found a data race that predated it. The suite runs in 36 seconds
  rather than 77 for the same 542 tests.
- The bot's tier gap is measured across board sizes and the figures previously
  published are withdrawn: they were run under at least two protocols and read as
  one curve. On one stated protocol, pro scores 0.542 on 12×12 and 0.958 on 16×16
  at the shipped budgets, and under an equal thirty-second guard 0.458, 0.417,
  0.583 and 0.583 from 10×10 to 16×16, every one with a 95% floor below 0.5.

### Fixed

- The introduction described the paper-and-pencil linking rule while running on
  the default ruleset, where the player chooses their links. A newcomer was told
  that the first thing they would meet in a real game does not happen.
- Games are saved as they are played rather than only on a clean exit, so closing
  the terminal no longer loses one. A finished game is final and can no longer be
  resumed or overwritten, which used to destroy a recorded result.
- The cursor and the highlight no longer erase the links they sit on, and a link
  that has to cross between two pegs is carried through one of them rather than
  stopped by it.

## [0.1.1] - 2026-09-01

### Added

- The peg just played is marked on the board, `◉` for vertical and `◎` for horizontal,
  so an opponent's reply can be found on a 24×24 board without reading a coordinate off
  the panel and counting. Every theme already defined a colour for it and nothing used
  it; it is a glyph rather than only a colour because every distinction this board
  draws has to survive colour being off.
- A shallow link's horizontal run is carried through a peg it has to cross. The run
  crosses the column of holes between the link's two ends, and where both candidate
  holes hold pegs there was no free cell left and the run simply stopped, so the link
  came out broken. It is now drawn through the peg as `⊕` or `⊖`, which says the run
  passes through and still names the peg's owner. Only a straight run is carried
  through: a corner or a junction on a peg would say the line turns or branches there,
  which is a larger untruth than passing through it.

### Changed

- `--profile NAME` overrides the stored choice for one run without writing it back, so
  a scripted game can no longer retarget the next interactive one and the next session.
  It also resolves a name by exactly the rules `profile use` applies and passes that
  refusal on unchanged, rather than treating every name it could not resolve — an
  ambiguous one included — as a request to make a profile, which is how a typo split a
  player's history across two identities. The one profile the flag may still create is
  the first, on a machine that has none: there is no stored choice to retarget there
  and no other name the player could have meant.
- A game read in with `game import` is stored as what it is. The record format carries
  no names and no kind, so an imported game keeps the two players it names instead of
  being filed under the profile that read it in, and nobody on this machine is known to
  have played it: it can be shown and replayed, but it is not offered for resumption
  and it does not reach the leaderboard. An unfinished imported record had previously
  reached the list of games waiting for a move, where playing on meant taking a seat
  belonging to one of the two players named in it. Re-importing the same record no
  longer duplicates it.

### Fixed

- A shallow link is drawn as one connected line instead of detached dashes. A link of
  column ±2, row ±1 covers four screen columns for every row it descends, and it was
  drawn as a ramp of horizontal scan lines: at the compact scale the cell where the
  line crosses between two rows always belongs to a hole and was always skipped, so the
  link came out as two stubs with a hole between them, and even once that cell was
  filled, three scan-line heights read as a row of dashes rather than as a connection.
  Links are now assembled from the edges each cell's lines reach rather than from
  glyphs, which makes a shallow link a connected polyline of box-drawing pieces and
  gives two links leaving a peg on the same side one shared run meeting at a tee. Half
  of all link shapes were affected, at the scale a 24×24 board actually uses.
- The cursor and the highlight no longer erase the links they sit on. A bracket goes
  one cell either side of a hole, which at the compact scale is exactly where the first
  cell of a link leaving that peg lands and where a compact steep link's single stroke
  lands. The worst case was the ordinary turn rather than a rare cursor position: the
  staged peg is highlighted and the cursor is already on it, so placing a peg that
  formed a link east or west immediately detached that link from its own peg. A bracket
  now goes into a free cell or not at all, and an overlay left without one falls back to
  a mark on the hole itself, in three families so that the cursor, a highlight, and a
  highlighted hole under the cursor stay apart, each naming what the hole holds so that
  nothing is hidden.
- Links that meet nowhere are no longer drawn as joined. A junction says the lines in
  that cell are connected, so drawing one between two chains that share no peg asserts
  a connection the game does not have. Route selection now scores a whole candidate
  line against the connectivity accumulated so far instead of asking the drawn glyphs,
  which could not see a shallow link at all before every link had contributed; a cell
  reached by several links is judged against all of them rather than against the first
  arrival alone; and a crossing the rules permit is drawn as a crossing rather than as
  a pair of tees, which had read as two connections that are not legal moves.
- Games are saved as they are played, which is what the documentation already promised.
  Saving happened only when the player left the screen or the game ended, so a game in
  progress existed nowhere but in memory and closing the terminal window lost it. The
  write now happens wherever the recorded position moves on, which covers a move played
  here, a bot's reply, a move arriving over the network and a pasted correspondence
  code, without each of those having to remember to.
- A finished game stays finished. The saved-game list was built from the store when it
  opened, and the screen it opened was what changed that store, so a game that had just
  been resigned was still on offer: choosing it reopened the position from before the
  resignation with the resigning player back on the move, and leaving again wrote that
  over the finished record, after the game had been rated. Resuming now re-reads the
  record and refuses a game that is over, the store refuses to replace a finished game
  with an unfinished one, and a screen is told when it is revealed again so the menu
  re-reads the stores instead of trusting the panel it built.
- A host no longer keeps a session it is not playing. A host offers exactly one game,
  so one finished handshake is handed over and the rest must be closed, but the handover
  could not tell which had won: two guests finishing a moment apart both believed they
  had, and the loser was left holding a socket and a reader goroutine on the host while
  its own player sat looking at a board waiting for a move that would never arrive.
- The tutorial fits its board into the space the board actually gets. It chose a drawing
  scale against the whole terminal and then took eight rows off the board for the
  lesson, which at 100×30 clipped the very holes the lesson was pointing at when the
  smaller scale would have fitted the whole board.
- The notice saying the terminal is too small is no longer itself cut mid-word, at every
  width where it can appear. Each line now has a series of forms, widest first, and the
  widest that fits whole is used; a line with nothing short enough is dropped rather
  than cut, and the size it quotes is derived from the minimum rather than written out
  again, so it cannot go stale.
- The line telling a player their unfinished game was saved, and how to pick it up, is
  shown where the player lands. It worked while leaving a game ended the program; once a
  game opened from the menu returned to the menu, the notes queued instead and printed
  together at the end, after the program had gone.
- Messages that said something untrue. A code whose last character had been altered was
  reported as truncated, sending the player to look for text that was not missing; the
  two signals are now separate. Link mode offered a link that crossing always refuses,
  and now says it is blocked while keeping the digit that names what blocks it. The
  refusal for playing in the opponent's border said "row" about a column. A one-move
  game read "after 1 moves". The two ways out of a game shared one help string while
  behaving differently in a game opened from the menu. A correspondence host never
  learns who it is playing, so its game rendered as "Alice vs your opponent (remote)",
  which reads as a fault rather than as a fact about a game played by exchanging codes.
  Joining printed nothing at all while it waited, so it could not be told apart from a
  hang, and interrupting it surfaced a raw socket error; it now says which code it is
  using, which relay it is going through, and that ctrl+c gives up. A relay announced
  itself before binding, and announced itself even when the bind then failed.
- A player's own leaderboard history prints times in local time, like every other
  surface, rather than in UTC, so one game no longer appears at two different times
  depending on where it is read.
- An unknown `--theme` is rejected whether output is a terminal or a file. It errored on
  a terminal and was ignored when the output was redirected, so the same command behaved
  differently depending on where its output went.

## [0.1.0] - 2026-09-01

First release.

### Added

- Rules engine for TwixT: boards from 6×6 to 48×48 with the corner holes excluded,
  per-side border rows, knight's-move links, link crossing decided by actual segment
  geometry, and a transactional turn — place a peg, take or decline each offered link,
  add or remove links by hand, then commit or abort the whole turn as one unit.
- Win, resignation, draw offer and acceptance, draw when the side to move has nowhere
  legal left to play, the swap (pie) rule, and single-move undo.
- Three rulesets, `std`, `pp` and `classic`, covering the printed box rules, the
  paper-and-pencil rules played at online venues, and the original 1962 3M edition.
  Every rule that historical editions genuinely disagree about is an explicit option
  rather than a silent choice, and a ruleset has a canonical encoding and a short
  fingerprint so two networked players cannot disagree about which rules are in force.
- Text notation for holes, links, moves and whole games, including link declines,
  removals and peg lifts, so a game can be transcribed, replayed and checked move by
  move.
- Bot at three strengths. One alpha-beta search backs all three; they differ in search
  depth, candidate width and how much of the evaluation they may see. The beginner and
  intermediate tiers are capped by depth and answer instantly; only the pro tier spends
  a clock budget. Measured over 60 games on a 10×10 board, colour balanced with swap
  off, intermediate beat beginner 58–2. How much stronger pro is depends on the board
  size, and on a small board it may not be stronger at all: on twelve openings played
  from both sides it scores 0.542 on 12×12 and 0.958 on 16×16 at the shipped budgets,
  and given an equal thirty-second guard 0.458 on 10×10, 0.417 on 12×12 and 0.583 on
  both 14×14 and 16×16, every one with a 95% floor below 0.5. On request the bot explains the
  move it would play and marks the holes its reasoning is about.
- Terminal board renderer with two drawing scales, a viewport that scrolls to follow
  the cursor and survives a resize, and a layout engine that fits the board and the
  information panel to whatever size the terminal is.
- Data-driven keymap: vim-style cursor movement, jumps, edge jumps, peg placement, and
  a link mode where the eight knight's-move directions are numbered and toggled by the
  digit keys. Every binding is an unmodified printable key, a plain uppercase letter,
  or one of the basic special keys — the arrows, space, enter, escape and ctrl+c — so
  none of them depend on terminal-specific modifier encoding.
- Seven tutorial lessons — the board, links, blocking, double threats, winning, the
  swap rule, and a practice game — which set up real positions and ask the learner to
  play into them.
- Username profiles with no passwords: create, rename, delete, most-recently-used
  ordering, and a ranked fuzzy search that reports which characters matched, so a
  half-remembered name still finds its profile.
- Leaderboard: every finished game recorded with side, ruleset, move count, duration
  and outcome; standings with ratings and win rates; per-player history; reset.
- Remote play over three transports, all carrying the same protocol: a direct TCP
  connection, a relay shipped in the same binary for players who cannot accept an
  inbound connection, and offline correspondence codes exchanged over any chat channel.
  Protocol version and ruleset are compared during the handshake, so a mismatch is
  refused before the first move instead of desyncing mid-game, and a dropped live
  connection can be resumed by replaying the missing moves.
- Four colour themes — `classic`, `slate`, `paper` and `mono` — with the chosen theme
  persisted, and a monochrome theme that distinguishes the sides by shape alone.
- Subcommand-based command line with shell completion for bash, zsh, fish and
  PowerShell, carrying a one-line explanation per command and per enumerated flag
  value.
- End-to-end test harness that runs the binary inside a real terminal, so terminal
  behaviour is tested against a terminal rather than asserted.
- Player-facing rules documentation in `docs/rules.md`, and the source audit trail
  behind every rule decision in `docs/RULES-PROVENANCE.md`.
- Packaging: static, dependency-free binaries for macOS and Linux on arm64 and x86-64,
  published as tar.gz archives with a checksums file and a source archive, with the
  version, commit and build date stamped into the binary.
- Continuous integration on every push and pull request: build, vet, gofmt check and
  the full test suite. Releases are cut from a `v*` tag. The landing page in `web/` is
  published by its own workflow.

### Security

- Every frame of a relayed game is authenticated. A pairing code now carries a room
  name, which is all the relay is told, and key material both ends derive a frame key
  from; each message is covered by a truncated HMAC over its direction, its position in
  the conversation and the decoded message itself. A relay operator can therefore no
  longer forge, inject, replay, reflect or drop a move without being caught — a review
  had demonstrated a relay replacing a move with a resignation the victim's engine
  accepted. An operator can still read everything a relay carries, which the
  documentation now says plainly. Direct connections are unchanged.
- Text arriving from an opponent, or out of a pasted code, is stripped of the control
  bytes a terminal acts on. An opponent's name or a rejected move could previously
  retitle or repaint the player's window.
- A relay no longer holds a room or a connection slot after the socket that claimed it
  is gone, so joins from closed sockets can no longer exhaust it, and it reports what it
  is refusing instead of failing silently. Message text fields are bounded, a decoded
  invite's game identifier is validated where it is read rather than only by its caller,
  a silent connection can no longer occupy a direct host indefinitely, and a bad entry
  part way through a transcript block leaves the game untouched rather than half
  advanced.

### Fixed

- Correspondence play works. It was documented and reachable but broken at every step:
  the identifier a new game minted was refused by the store, the game screen rejected a
  remote seat with no live connection, and nothing could produce or apply a move code.
- Quitting with ctrl+c saved the game in progress. The shell answered the key itself and
  never let the screen finish, so leaving with `q` saved and leaving with ctrl+c did not.
- A profile chosen in the interface is remembered. It was not, so after picking a name
  and playing a game, the next subcommand still reported that nobody was playing.
- The default colour scheme is legible. Its darker player was near-black, invisible on a
  dark terminal, and its panel text near-white, invisible on a light one, so there was no
  terminal it was fully legible on.
- The information panel no longer cuts text mid-word, and drops the reminder of which
  edges a side joins before it shortens an opponent's name.
- The leaderboard ranks people rather than mixing them with the bot tiers' fixed
  ratings, which had put a player who lost their only game above the bot that beat them.
  A player's own history no longer inverts the games their opponent recorded.
- The rules print as text rather than as raw markdown, a saved game prints its whole
  board instead of clipping it, an unknown subcommand fails with a suggestion instead of
  succeeding silently, and the tutorial's prose is set to a readable measure on a wide
  terminal.

[Unreleased]: https://github.com/BAKocska/twixtui/compare/v0.1.1...HEAD
[0.1.1]: https://github.com/BAKocska/twixtui/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/BAKocska/twixtui/releases/tag/v0.1.0
