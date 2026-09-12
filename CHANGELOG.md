# Changelog

All notable changes to twixtui are recorded here.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this
project uses [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- Native Windows support on x64 and ARM64: cross-process `LockFileEx` locking,
  long paths, read-only store access, and rejection of DOS-device game identifiers.
  On filesystems supporting POSIX rename, replacement preserves existing
  delete-sharing readers while new opens see the new data. Refused writes leave
  the previous file intact; Windows does not claim Unix directory-fsync durability.
- The end-to-end suite runs natively on Windows in a ConPTY pseudoconsole it
  owns, covering the scenarios the tmux backend covers plus PowerShell
  completion and an execution-identity check that refuses a cross-compiled or
  emulated pass. CI is configured to run it from source and against a
  checksummed snapshot archive on both architectures, and release packaging
  adds `windows_amd64` and `windows_arm64` ZIPs under the existing
  `checksums.txt`. Windows downloads are not published yet.

### Fixed

- Windows bracketed paste no longer gains NUL bytes before uppercase letters.
  The filter removes standalone modifier records, not genuine NUL input, and
  leaves pending UTF-16 pairs intact.
  It is pinned in `go.work` while
  [the upstream fix](https://github.com/charmbracelet/ultraviolet/pull/184) is
  reviewed; Windows builds currently require the checkout or packaged ZIP.

## [0.4.0] - 2026-09-09

### Added

- Saved-game replay has a numbered entry list, `:` entry-number input, and
  final-position connection-chain highlights. Draw offers count as entries,
  not plies; invalid numbers leave the input open for correction, and short
  terminals keep errors visible.
- Reproducible replay construction, traversal and long-seek benchmarks with
  checked 12×12/24-entry and 48×48/400-entry records.

### Changed

- Replay keeps one mutable board rather than cloning the growing history at
  every entry. On the fixed 48×48/400-entry benchmark, construction allocated
  about 243 KB instead of 67 MB. Five single-worker Go 1.26.5 samples on an
  Apple M5 Pro measured construction only, not rendering or whole-game speed.
  Seeking now performs undo/replay work instead of selecting a cached snapshot;
  the same fixture's full backward/forward traversal took about 0.19 ms.
- Long backward seeks rebuild the requested prefix rather than repeatedly
  rescanning draw-offer history. Ordinary one- and five-entry steps use undo.

### Fixed

- First-peg hints describe starting a route, not continuing a nonexistent chain,
  while preserving tactical priorities and the placement-only policy. Search
  evaluation, effort tiers and move ordering are unchanged.
- Undo restores a draw offer that survived a swap, allowing a later acceptance
  to be replayed correctly. The new replay cursor exercises this case.
- Replays reset the viewport before the first peg and preserve whitespace
  between notation tokens when drawing imported entry labels.
- The PP hint test verifies its ruleset capabilities, and correspondence e2e
  waits for Escape to dismiss the exchange before sending further keys.

## [0.3.3] - 2026-09-08

### Fixed

- Hints now state their placement-only search policy: offered links are kept,
  and swaps and deliberate link edits are not searched. A missing route in the
  evaluation is no longer presented as a drawn game or an exhaustive result;
  winning-move claims require the rules engine's actual result.
- The recommended coordinate and policy remain readable in short side/bottom
  panels and at the supported 20-column minimum, including during the swap
  offer. Interrupted defence enumeration does not publish a partial count.
- The manual now correctly describes saved-network reconnection and the
  CLI-only player-history view. The README, website and rules text explain
  hint limits; the older illustrative tour is identified as historical.
- Release archives include the full manual and its board illustration, plus
  the README's SVG banners.

## [0.3.2] - 2026-09-08

### Fixed

- Finished and disconnected game boards now show the actions available in that
  state instead of advertising turn edits or advice. Inspection and Enter-to-leave
  remain available; finished bot/hotseat games offer rematches, and correspondence
  keeps its final exchange accessible. A peer's result or disconnection also
  dismisses any pending local confirmation.

## [0.3.1] - 2026-09-08

### Added

- A killer-move ordering lever in the bot search, off in every tier. Each ply
  remembers the last two holes that produced a beta cutoff there and tries them
  first at the next node it reaches at that ply; a remembered hole the node
  cannot play is ignored rather than added, so the moves searched stay the
  node's own. The effort benchmark gains a `pvs-killers` contender so the lever
  can be measured against its absence, and the artifact records the lever.

  It ships off because the measurement did not support turning it on. At full
  width, where the move set is fixed, the search returns identical values and
  enters 29.8% fewer nodes with neither principal-variation search nor the
  transposition table and 12.0% fewer with both, over twelve frozen midgames
  at depth four — but no tier searches at full width. At pro's shipped width
  the picture is depth-dependent and the values are not identical: over 40
  positions on 10×10 and 16×16 with opening seeds 1–12, total nodes were 6.8%
  worse at depth five, 2.9% better at depth six and 6.8% better at depth
  seven, the median position was within a tenth of a percent at all three, and six to eight
  positions per depth returned a different score. At an equal 8,000-node
  budget the paired match was 12–11–1 on 10×10 and 9–13–2 on 16×16 for the
  killer side, both inconclusive under the conservative bound. What the tiers
  play is therefore unchanged from 0.3.0.
- An aspiration-window lever at the search root, measured and left off. When
  enabled it searches the root's first move inside a two-peg band around the
  previous iteration's score and widens the failing side to the whole scale, so
  a root score is never a bound. It is off in every tier because the harness
  says it costs work rather than saving it: at a fixed depth of six over the
  40 frozen positions of 10×10 and 16×16 seeds 1–12 it spent 3.4% more nodes
  than the whole window, and 13.6% more at depth eight on 10×10 with the
  shipped two-peg band (13.0% at four and eight pegs); of twenty band
  settings between one and forty-eight pegs, the best saved 0.02%, which is
  noise, and every other cost nodes. Twenty-four games per board at 8,000
  nodes were 11–11–2 on each, with every opening pair level, which
  establishes no strength difference in either direction. The effort
  benchmark gains a `pvs-aspiration` candidate and records the lever in every
  artifact so the measurement can be repeated.
- A corpus of proved edge templates for the bot's evaluation, together with the
  exhaustive prover that certifies it. A template says that a peg two, three or
  four rows from its own border reaches that border in a fixed number of
  placements whatever peg the opponent places, or none, provided a named set
  of holes — the cells it may use and the guard holes that close it under the
  crossing rule — is empty. The opponent's replies are peg placements with
  every offered link taken, which is what the bot plays; declining or removing
  links under `std`, and the first-move swap, are outside the claim. Nothing is
  called a template until the prover has demonstrated it on the real engine
  with the opponent free to place anywhere on the board, and the proofs run in
  the ordinary test suite, so a claim added without one fails the build. The
  evaluation can use a matching template to stop counting a step of a cheapest
  chain as a bottleneck, which is behind a lever; the peg counts are untouched
  either way.
- An effort-experiment pair, `pvs` and `pvs-templates`, which is the same
  search with the corpus out of and in the evaluation.

### Changed

- No bot tier switches the edge templates on. A paired match of the two
  candidates above — 12 openings from both sides, 30,000 nodes a move, `std`
  rules — changed no move at all on 16×16 and changed the result of one opening
  in twelve on 10×10, against the corpus; both board sizes are inconclusive
  under the conservative paired bound. The corpus, the prover and the lever stay
  in the tree to be measured again when the corpus covers more; the shipped
  evaluation is unchanged.

### Fixed

- A relayed game could refuse a valid opponent. Each relay handler wrote the
  greeting to its own client after the pair was made and then pumped its
  client's bytes to the other; a client sends its first frame the moment it
  reads the greeting, so the second arrival's frame could reach the waiting
  client before that client's own greeting had been written, and it read a
  protocol frame where a greeting line should have been. The handler that
  completes a pair now greets both clients before either pump exists. Seen on
  loaded machines; never on a fast one.
- A host's winning session could be closed under it. The handshake's
  cancellation watcher was told to stand down without waiting for it to hear,
  so a cancellation arriving in the same instant — which is exactly when a host
  stops listening because its opponent has just connected — could close the
  connection the session had already been built on. Standing the watcher down
  now waits for it, and a watcher that has been stood down never closes.
- Tests: oversize record fixtures are sized by fixed literals rather than by
  the bound they test, so a raised bound fails the test instead of allocating
  the new size; the terminal harness reads a program's exit status only once
  tmux has published it, reports a signal death as a shell would, and reads a
  zombie's status from the kernel when the Ubuntu runner's tmux never reaps
  the pane's process.

## [0.3.0] - 2026-09-07

### Added

- A `max` bot tier, selectable from the command line, menu, completion and
  saved-game resume: a ten-second guard, depth-24 ceiling, wider shortlists
  and eight forced-line extension plies. Its nominal rating anchor matches
  pro until separately calibrated. Effort is explicit; stronger play on
  every board is not promised.
- `bot.NewWithLimits` and `bot.StatsOf` expose node/depth/time ceilings and
  actual nodes, all position analyses, completed depth and stop reason.
- An opt-in paired bot benchmark with plain/PVS/MCTS alternatives, explicit
  terminal setup slots, per-move receipts and conservative uncertainty.
  Errors abort; truncated games are not draws, and incomplete pairs cannot
  establish a strength direction. The manual records the protocol and results.

### Changed

- docs: replace the repository banner with a box-lid illustration beside a shell
  prompt, and introduce the game with an animated feature tour. The tour keeps
  its editable source and a frame-by-frame export command; the full board
  explanation remains in the manual.
- Cached immutable knight geometry and border lists reduced warm evaluator
  cost while preserving all scores on 30 frozen positions. Five-sample medians
  on an M5 Pro dropped 25.2% at 10×10 and 34.2% at 24×24, with zero warm-load allocations.
- Pro and max use principal-variation search with extension-aware
  transposition entries. Hints use max's search policy under a two-second
  guard, not its ten-second game budget.
- docs: the README is a front page rather than the whole manual. It had grown to
  something over six hundred lines and covered everything at full depth, which
  meant the answer to "what is this and how do I start" sat in the same
  undifferentiated column as the bot's confidence intervals and the PowerShell
  completion line. The depth moved, essentially unedited, to docs/MANUAL.md,
  which has a table of contents and a stable anchor per section; the README keeps
  the pitch, an introductory tour, installing, starting, and a table of what the program
  does with a link per row into the manual. Nothing was dropped: every line of
  the old README between the opening and the licence is in the manual, and every
  measured strength figure moved across unchanged. The one sentence that did not
  move verbatim is the beginner tier's, which said the tier works from peg counts
  alone; it counts how many pegs each side still needs, which is a different
  thing, and it now says so. A banner SVG serves the front page, while the coloured
  board-position SVG illustrates the manual. Both live under assets/, with their
  provenance and the code they repeat recorded in assets/README.md.

### Added

- `play host --bind` chooses which of this machine's addresses a direct game
  listens on, so a game can be kept to the loopback address or to one
  interface. `--port` is unchanged, and the default still listens everywhere.

### Fixed

- An interrupted network game was saved, and the notice said it could be
  resumed, but nothing resumed it: the Continue list called it unavailable and
  told the player to host or join again, which started a new game and left the
  saved one where it was. The transport had carried resumption all along and no
  caller ever asked for it. `play host --resume <id>`, `play join --resume <id>`
  and the Continue list's own reconnection route now continue the stored game:
  the two copies reconcile the moves one side missed, refuse transcripts that
  disagree, and keep the saved game's identifier rather than adding a second row.
  Connection results stay bound to the attempt and saved game that requested
  them, so a late result from a cancelled attempt cannot replace another game.
  Replayed entries are checked against their own positions rather than the
  session's later state, including a queued move followed by a draw offer.
- A record file holding more than one record imported the last of them and
  stored the file's bytes as they arrived, so content no digest covered survived
  a round trip. A record with a repeated field is now refused by name, imports
  are bounded at a megabyte rather than read whole, every diagnostic quotes at
  most a fragment of its input, and what is stored is the checked canonical
  record. Its canonical encoding must also fit the size limit before a store
  or export accepts it.
- `game export` wrote the stored record without the check `game show` and
  `game replay` make, so a corrupt saved game was handed out as a record this
  same build refuses on import. It is validated and replayed first, and a
  validation failure leaves an existing `--out` file untouched.
- `game --help` claimed an edited saved file is refused. Only the record itself
  carries that check; the labels around it are this machine's own notes. The
  help now says which is which. The stored record's result also prevents
  reopening a finished game even if its local finished label has been cleared,
  and a finished game now refuses a different finished record too. Both checks
  are made under a per-game lock held across the read and the write, so two
  windows that finish the same game keep the first result: the store used to
  check and then write as two steps, and the second finish could pass the
  check, overwrite the first result, and be rated a second time. A game is now
  rated only after the store has taken its result.
- `play host --help` said a relay never sees the game, while the relay's own
  documentation says the operator reads both names, the ruleset and every move
  in plain text. The help now says what the pairing code does and does not
  buy — integrity, not secrecy — and that a direct game sends its invitation
  before either end has proved anything. `serve --help` no longer claims a
  relay cannot drop a move: it cannot alter, inject or replay one.
- An explicitly empty flag value silently meant the flag was absent, so
  `--config ""` wrote to the default configuration directory and `--profile ""`
  played as somebody else. Empty values are now refused, including a
  present-but-blank `TWIXTUI_CONFIG_DIR`, while omitted flags keep their
  defaults. `--limit` refuses a negative count; zero still means all.
- A profile's recorded games survive the profile, but `leaderboard show
  --player` resolved names against the profile store alone, so a deleted
  player's history was unreachable while the standings still ranked them. It
  now resolves against the result log as well, keeping a local and a remote
  player of the same name apart.
- Boards wider than 26 columns ran their two-letter coordinate labels together
  into one unreadable line. The header now takes a row per letter, so every
  column is named above its own holes at both drawing scales.
- Column alignment counted characters rather than the cells a terminal draws
  them in, so a name with a fullwidth or combining character pushed a table out
  of line. The listings, the standings and the profile picker measure display
  width instead.
- The query field's own editing was rune-based and its scrolling kept the wrong
  end of the line: with the cursor left of the overflow the caret was the first
  thing dropped, a fullwidth character straddling the cut could make the field
  one cell too wide, and backspace could strip an accent off its letter. The
  field now windows around the caret and steps whole characters.
- Smaller corrections: `--side` refuses a value while naming all three it
  takes; an invite code's refusal no longer calls itself a move code; a pairing
  code is checked before the waiting banner is printed; the panel no longer
  says it is connected after the opponent has left; `rules show --provenance`
  suggests and completes only topics that document has; `theme show` paints its
  colours on a terminal and stays plain when redirected, and names the theme
  actually asked for; `learn Blocking` finds the lesson; every command's help
  says what it is for and a missing argument names the usage; the completion
  command lists the shells it knows; profile completion tells profiles created
  in the same second apart; `leaderboard reset` says it deletes the result log
  and not the saved games; and browsing profiles, standings or saved games no
  longer needs a writable configuration directory.
- Read-only profile and standings snapshots take their revision stamp from the
  same opened file as their contents. A concurrent atomic replacement can no
  longer pair old contents with the new stamp and leave a reader stale.
- Root score bounds no longer masquerade as exact ties and displace a better
  move. Sampled choices receive fully searched candidate values.
- Interrupted iterations preserve the last completed results; exact node
  ceilings admit their final leaf, and recursion caps evaluate the position
  rather than inventing a window score.
- Interrupted tactical enumeration no longer produces false “only defence”
  hints. Searches reject uncommitted turn edits rather than undoing them.

- ci: a tag push matched no trigger of the test workflow, so releases were
  built and published from refs the checks had never run on. The release
  workflow now calls the CI workflow and publishes only after it passes; the
  first tag after this change validates the wiring live.

## [0.2.1] - 2026-09-01

### Fixed

- The introduction's first screen taught the wrong linking rule. It said pegs a
  knight's move apart link up as the peg goes down, which is how the other
  linking convention works; the introduction runs on the default rules, where
  each link is offered and may be declined, and its own fourth screen said so.
  A new player was told two different rules four screens apart and would have
  acted on the first. The step now says pegs can be linked, and the test that
  holds the introduction to the rules it runs on reads every step rather than
  only the step named after the topic, which is how this survived an earlier fix
  to the other screen.

## [0.2.0] - 2026-09-01

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

[Unreleased]: https://github.com/BAKocska/twixtui/compare/v0.4.0...HEAD
[0.4.0]: https://github.com/BAKocska/twixtui/compare/v0.3.3...v0.4.0
[0.3.3]: https://github.com/BAKocska/twixtui/compare/v0.3.2...v0.3.3
[0.3.2]: https://github.com/BAKocska/twixtui/compare/v0.3.1...v0.3.2
[0.3.1]: https://github.com/BAKocska/twixtui/compare/v0.3.0...v0.3.1
[0.3.0]: https://github.com/BAKocska/twixtui/compare/v0.2.1...v0.3.0
[0.2.1]: https://github.com/BAKocska/twixtui/compare/v0.2.0...v0.2.1
[0.2.0]: https://github.com/BAKocska/twixtui/compare/v0.1.1...v0.2.0
[0.1.1]: https://github.com/BAKocska/twixtui/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/BAKocska/twixtui/releases/tag/v0.1.0
