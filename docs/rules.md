# TwixT — the rules, as twixtui plays them

TwixT is a two-player abstract connection game designed by Alex Randolph, first
played on paper in 1957 and sold in a box by 3M from 1962. Two players build
networks of pegs and diagonal links across a square board, each racing to
connect their own pair of opposite edges before the other player connects
theirs.

This page describes the rules exactly as `twixtui`'s engine enforces them,
under the default `std` ruleset, with worked examples using real board
coordinates. The last section covers the alternative rulesets and the handful
of options that change between historical editions and online venues. For the
sourcing behind every claim on this page — which rule comes from which
edition, and where sources disagree — see `docs/RULES-PROVENANCE.md`.

## The board

The board is a square grid of holes, 24×24 by default (`twixtui` supports
anywhere from 6×6 to 48×48). The four corner holes do not exist — you can
never place a peg on a corner, and no link ever needs to consider one.

Holes are named by column letter and row number, row 1 at the top: `A1` is the
(nonexistent) top-left corner, `D4` is the hole four columns in and four rows
down. Columns run `A`–`Z`, then continue `AA`, `AB`, … on boards wider than
26 — the same continuation Excel uses for its columns.

The outermost row of holes along each edge is a *border row*. The top and
bottom rows belong to one side; the left and right columns belong to the
other. A side may place a peg anywhere in its own border rows (and must,
eventually, to win) but never in the opponent's.

## The two sides

`twixtui` names the sides by which pair of edges they connect, not by colour:

- **Vertical** connects the top border row to the bottom border row, and
  always moves first.
- **Horizontal** connects the left border column to the right border column,
  and moves second.

Which physical colour you play is a separate choice you make when the game
starts; the axis you're trying to connect is what these rules are about.
Historical rule texts instead name the sides "Red"/"White" moves first — the
identity is the same game, the label differs by source (see
`RULES-PROVENANCE.md`, RD11).

## A turn

Each turn, in order:

1. **Optionally remove** any of your own links or pegs placed on an earlier
   turn (governed by the ruleset — see "Removing links and pegs" below).
2. **Place exactly one peg** of your own colour on any empty, non-corner hole
   that is not in your opponent's border row. This step is mandatory: there
   is no way to pass.
3. **Review the links** `twixtui` proposes for the peg you just placed, and
   adjust them if the ruleset lets you — see "Links" below.

## Links: the knight's move

Two of your own pegs may be joined by a link exactly when they stand a
chess knight's move apart: the column and row each differ by (1, 2) or (2, 1),
in any combination of directions. From any peg there are exactly eight holes a
link could reach.

Worked example: a peg at `D4` and a peg at `E6` are one column and two rows
apart — a legal knight's move — so they can be linked. A peg at `D4` and one
at `F5` are two columns and one row apart — also legal. A peg at `D4` and one
at `D6` (two rows, zero columns) cannot be linked at all; that offset is not
one of the eight.

A link is a straight line drawn between its two peg holes; think of the two
pegs as sitting at opposite corners of an otherwise-empty 2×3 rectangle of
holes.

### How `twixtui` proposes links

When you place a peg, `twixtui` immediately works out every legal link from
that peg to your other pegs — every knight's-move neighbour that isn't
blocked by the crossing rule (below) — and proposes all of them at once. You
don't build a network one link at a time from nothing.

Under the default `std` ruleset (and `classic`), you're then free to:

- **decline** any link `twixtui` offered — it simply won't be made, and
  nothing else on the board is affected;
- **add** any other legal link between two of your own pegs, including pegs
  placed on earlier turns.

Under the `pp` ruleset, linking is fully automatic: every legal link is
created, and none of them can be declined, added by hand, or removed later.

## The crossing rule

A link occupies the straight line between its two peg holes. A new link
cannot be created — offered, added, or auto-linked — if that line crosses the
line of any link already on the board. `twixtui` computes this crossing test
exactly, with integer arithmetic, so there's never a borderline case.

Under the default `std` ruleset this includes **your own** links: if you
already have a link on the board, you cannot add a second link of your own
that crosses it, even though it's your own colour blocking you. Worked
example: suppose Vertical has linked `D4` to `E6`. Horizontal then places
pegs at `D6` and `E4` — a legal knight's move apart from each other — but the
`D6`–`E4` link would cross `D4`–`E6` diagonally through the same six holes,
so it is blocked, even though the two links belong to different sides. The
same block would apply if both links belonged to the same side.

Under the `pp` ruleset, only an *opponent's* links block you: your own links
may freely cross each other. (Two links that merely cross are still not
connected to one another at the crossing point — only a shared peg endpoint
joins two links.)

A link that could have been made but wasn't — because you declined it, or
under `std` because it would have crossed one of your own links — is not a
barrier to anyone. The gap it leaves through is simply open board.

## Removing links and pegs

You can always undo something from **this same turn** — decline a link
`twixtui` just offered, or take back a link you just added by hand — with no
extra permission needed.

Taking off a link placed on an **earlier** turn needs the ruleset's link
removal option, which is on by default under `std` and `classic` and off
under `pp`. When it's on, you may remove any number of your own links, on any
of your own turns, with no count limit; you can never touch an opponent's
link.

Removing a whole peg (and every link attached to it) is a further, separate
option — `PegRemoval` — that is off in every built-in preset. Turn it on and
you may lift one of your own previously placed pegs off the board as part of
your turn, taking its links with it. (This option exists because exactly one
transcription of the printed rules describes it; see
`RULES-PROVENANCE.md`.)

## Winning

You win the instant your turn ends with an unbroken chain of your own linked
pegs joining a peg in one of your border rows to a peg in your *other* border
row. `twixtui` checks this after every committed turn, the moment the mover's
own move is complete — so it's always the player who just moved who wins,
never a move that accidentally helps the opponent connect.

## Draws

`twixtui` declares a draw the instant the side about to move has nowhere at
all left to place a peg: every remaining hole is occupied, a corner, or in
their opponent's border row.

This is a deliberately mechanical, always-computable trigger. No printed or
online rules text specifies a formal procedure for the more commonly quoted
condition — "if neither side can possibly complete a connection any more" —
and that condition is not mechanically decidable in general, so `twixtui`
uses the narrower, unambiguous "no legal placement remains" test instead.
See `RULES-PROVENANCE.md` for why this is called out as a judgement call
rather than a sourced rule.

Either player may also offer a draw at any time; if the other player accepts,
the game ends immediately by agreement, independent of the position on the
board.

## The swap rule (pie rule)

Immediately after the very first peg of the game is placed, and only then,
the side to move may **swap** instead of playing a normal move: they take
over that first peg as if it were their own, reflected across the board's
main diagonal so that it lands somewhere legal for their own side.

Worked example: Vertical opens with a peg at `B4`. Horizontal, instead of
placing their own peg, swaps: the `B4` peg is removed and a new peg belonging
to Horizontal appears at `D2` — the row and column of the original hole
traded places. Play then continues with Vertical to move, exactly as if
Horizontal had used their first turn to answer normally.

The swap option exists to cancel out the first-move advantage: if you think
the opening peg is too strong to answer, take it instead of responding to
it. It is available exactly once, on the second ply of the game, and never
again. It is off under the `classic` ruleset, which reproduces the original
1962 edition that predates this rule.

## Resigning

Either side may resign at any point, ending the game immediately in the
opponent's favour.

## Notation

`twixtui` records and replays games in a compact text notation. An ordinary
move is just the hole the peg went into:

```
D4
```

which places a peg at `D4` and takes every link `twixtui` offers. Append
edits, space-separated, to change that:

| Prefix | Meaning | Example |
|---|---|---|
| *(none)* | the hole to place a peg in | `D4` |
| `~<hole>:<hole>` | decline an offered link | `E6 ~D4:E6` |
| `+<hole>:<hole>` | add a link by hand | `D4 +A6:B4` |
| `-<hole>:<hole>` | remove a link from an earlier turn | `D4 -A6:B4` |
| `x<hole>` | lift one of your own pegs off the board | `D4 xA6` |

A link is always written as its two holes joined by a colon (a hyphen also
parses). `~` and `-` end up doing the same thing to the board — the prefix is
purely there to tell a reader *why* a link came off: `~` means "I was offered
this and didn't want it", `-` means "I'm removing something from an earlier
turn". Removals and peg lifts are applied before the peg placement in the
same turn, matching the order the rules describe: you clear the board first,
then place.

Four further tokens stand alone as a whole move, matching the special move
types online venues and the SGF game-record format use:

- `swap` — exercise the swap option
- `resign` — resign the game
- `draw?` — offer a draw
- `draw!` — accept a standing draw offer

A full game record is these moves joined by `;`.

## Rulesets

`twixtui` ships three named rulesets. Board size is chosen independently of
which one you pick — the default is 24×24 under every preset.

| Preset | Corresponds to | Deliberate linking | Link removal | Own links may cross | Swap |
|---|---|---|---|---|---|
| `std` | The printed box rules (3M/Avalon Hill/Schmidt Spiele/Kosmos), as reconstructed by the community's fullest rulebook transcriptions, and BoardSpace.net's published rules | yes — you may decline offered links and add your own | yes | no | yes |
| `pp` | "Paper & Pencil" / "TwixT PP" — the ruleset used by Little Golem and documented by the SGF game-record specification; also the ruleset most other software (OpenSpiel, T1j's automatic linking) implements | no — every legal link is automatic and fixed | no | yes | yes |
| `classic` | The original 1962 3M edition, before Randolph added the swap rule in a later edition | yes | yes | no | **no** |

Each of these named presets is a bundle of five independent switches the
engine tracks separately (board size is a sixth, orthogonal knob):

- **Deliberate linking** — whether you control which offered links you keep,
  or every legal link is automatic and untouchable.
- **Link removal** — whether you may take your own older links off the
  board.
- **Peg removal** — whether you may lift your own pegs, off in every preset,
  opt-in only.
- **Own links may cross** — whether your own links block each other the same
  way an opponent's do, or only an opponent's links can block you.
- **Swap** — whether the second player gets the one-time swap option.

## Strategy primer

A few terms come up throughout `twixtui`'s bot hints and the tutorial, and
match the vocabulary players actually use:

- **Setup move** — a peg placed with no immediate link, laying groundwork for
  a connection a move or two later, usually near your own border row early
  in the game.
- **Gap** — the distance between two of your pegs that aren't yet linked but
  could be joined later, once you fill the hole between them (a knight's
  move is a gap of one; a longer unlinked run is described by its size, e.g.
  a "5-2 gap").
- **Tilt** (or **pivot**) — a peg placed off to one side of an existing chain
  that opens up a second direction to extend it, so the opponent can't block
  both directions with a single move.
- **Mesh** — a dense cluster of your own links built up in one area of the
  board, strong locally but slow to extend; the opposite of a thin, fast
  chain race toward your border.
- **Beam** — a long run of pegs and links pushing straight toward your
  target border row, prioritising speed over local strength.
- **Hammer attack** — an aggressive placement that threatens to block the
  opponent's chain and extend your own at the same time, forcing them to
  respond defensively.

A hint from `twixtui`'s bot always names the concrete move and holes it's
recommending, and where relevant explains itself using this same vocabulary.
