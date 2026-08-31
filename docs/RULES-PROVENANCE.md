# Rules provenance

This is the audit trail behind `docs/rules.md` (R29, R30, R31): where every
rule came from, which independent sources agree, which genuinely disagree,
and — where no source settles the question — which judgement call `twixtui`
made and why.

## Claim tiers

Carried over unchanged from the research reports this document summarises:

- **VERIFIED** — a research agent personally read the primary source (rules
  text, source code, academic paper) and cites it directly.
- **REPORTED** — a secondary source (rules-site transcription, wiki,
  community authority) states it, without independent verification of the
  underlying primary text.
- **INFERRED** — a research agent's own reasoning, not stated by any source
  read.
- **UNREACHABLE** — a source that was sought and could not be read (paywalled,
  gated, 403, dead link, JS-rendered with no server-side content).

Every claim below traces to one of four research reports
(`.work/research/rules-canon.md`, `.work/research/impl-openspiel-ludii.md`,
`.work/research/impl-ccaesar26.md`, `.work/research/impl-t1j-bots.md`) or to
`twixtui`'s own source (`internal/game/ruleset.go`, `geometry.go`, `game.go`,
`notation.go`, `crosscheck_test.go`). A `REPORTED` claim in a research report
stays `REPORTED` here — it is not upgraded by repetition.

Ludii (`Ludeme/Ludii`) was investigated as a possible sixth comparator but is
excluded from the table below: the research pass found, by exhaustive
filename and full-content search of the entire `.lud` corpus, that Ludii has
no TwixT game definition at all `[VERIFIED, impl-openspiel-ludii.md §2, C9]`.
There is nothing to compare it against.

## RD1–RD16 by source

Six sources: the **canonical/documentary** reconstruction (the convergence of
BoardSpace.net, Little Golem's rules pages, ultraboardgames.com's rulebook
transcription, gambiter.com, hexwiki.net, and the David Bush/SGF material —
see `rules-canon.md` §2 for the full reachability list); **OpenSpiel**
(`google-deepmind/open_spiel`, `open_spiel/games/twixt/`); **ccaesar26**
(`ccaesar26/Twixt-Game`, C++); **T1j** (`eeichinger/twixt_t1j`, Java);
**Little Golem** (the online venue itself, `littlegolem.net` +
`docs.littlegolem.net`); and **twixtui** (`internal/game`).

| RD | Dimension | Canonical/documentary | OpenSpiel | ccaesar26/Twixt-Game | T1j | Little Golem | twixtui |
|---|---|---|---|---|---|---|---|
| RD1 | Board size | 24×24 default; 12×12/30×30/36×36/48×48 documented variants `[REPORTED]` | Configurable 5–24, default 8 `[VERIFIED]` | Configurable, default 24, capped at 40 `[VERIFIED]` | Configurable 12–36, default 24 `[VERIFIED]` | 24×24, 30×30, 48×48 offered `[VERIFIED]` | Configurable 6–48 (`MinSize`/`MaxSize`), default 24 in every preset `[VERIFIED, ruleset.go]` |
| RD2 | Corner holes | Do not exist, never playable `[REPORTED]` | Explicit off-board check including the 4 corners `[VERIFIED]` | Explicit exception thrown for all 4 corner coordinates `[VERIFIED]` | No explicit check; unplayable only as an emergent side effect of border-row exclusion overlapping at corners `[VERIFIED]` | Corners forbidden, stated explicitly `[VERIFIED]` | Explicit `IsCorner`/`ErrCornerHole` check `[VERIFIED, game.go]` |
| RD3 | Opponent border row | Never allowed; own border rows always allowed `[REPORTED, unanimous]` | Border cell exclusively legal for its owner `[VERIFIED]` | Colour-specific row/column checks, own axis unrestricted `[VERIFIED]` | `pinAllowed()` checks explicitly `[VERIFIED]` | Explicit on both rules pages `[VERIFIED]` | `CanPlace` checks `IsBorderRow(pl.Opponent(), p)` `[VERIFIED, game.go]` |
| RD4 | Link geometry | Knight's move, (±1,±2)/(±2,±1), 8 offsets `[REPORTED, unanimous]` | `kLinkDescriptorTable`, 8 compass directions `[VERIFIED]` | Manhattan-distance-3 check plus explicit 8-offset table `[VERIFIED]` | 8 offsets tested in `setPin()` `[VERIFIED]` | "6-hole rectangle... like a knight's move" `[VERIFIED]` | `dirOffsets`, 8 entries `[VERIFIED, geometry.go]` |
| RD5 | Link creation | STD/box: deliberate, per-link player choice — an unmade link is no barrier. PP and nearly all software: automatic. `[REPORTED (deliberate) + VERIFIED (auto, arXiv:1403.6518 §3)]` | Fully automatic, no separate action, no possible conflict between candidate links `[VERIFIED]` | **Fully manual** — every link needs a separate `CreateLink` call; no auto-linking of any kind, even on the `pp`-adjacent reading `[VERIFIED]` | Automatic — every one of the 8 offsets checked and linked on placement `[VERIFIED]` | Automatic: "the server will automatically add all legal links to that peg" `[VERIFIED]` | Hybrid: every legal link is proposed automatically on placement (`autoLink`), then under `DeliberateLinking=true` (`std`/`classic`) the player may decline any of them or add others by hand; under `DeliberateLinking=false` (`pp`) none can be touched `[VERIFIED, ruleset.go `DeliberateLinking`; game.go `PlacePeg`/`AddLink`/`RemoveLink`]` |
| RD6 | Crossing predicate | Literal 2-D segment intersection; own links block under STD; PP relaxes only the own-link case. No source gives a discrete offset table. `[REPORTED (rule) + INFERRED (algebraic form)]` | Precomputed blocker table (`kLinkDescriptorTable[dir].blocking_links`), colour-agnostic `[VERIFIED]` | Strict cross-product segment intersection (`DoSegmentsIntersect`), colour-blind `[VERIFIED]` | "9 crossing bridges" precomputed table per link slot, colour-blind (matches STD) `[VERIFIED]` | Legacy page: opponent-only. Current docs page: silent on own links — see disagreement D2. `[REPORTED, contradictory]` | `segmentsProperlyCross` — exact integer cross-product test; `blockerTable` derived from it, not transcribed; colour-blind unless `OwnLinksMayCross` (`pp`) ignores same-owner blockers. Cross-checked against OpenSpiel's `kLinkDescriptorTable` NNE entry and found to agree exactly. `[VERIFIED, geometry.go, game.go, crosscheck_test.go]` |
| RD7 | Link removal | STD/box: freely removable, own links, any own turn, no count limit — but the *original 1962 print text is reported to be internally inconsistent about this between print runs* (see below). PP: never removable. `[REPORTED, unreliable for the 1962 primary text]` | **No removal mechanism exists** — links are permanent once created `[VERIFIED]` | Freely removable any time via `RemoveLink`, no turn-timing restriction `[VERIFIED]` | A removal primitive exists (`removePin`/`removeBridge`) but is never wired to any player-facing move `[VERIFIED, absence for player-facing UI]` | **Self-contradictory**: the legacy rules page says PP forbids removal; the current docs page says removal is explicitly allowed. Same platform, two pages, opposite claims. `[VERIFIED, both pages read directly]` | `LinkRemoval` flag: true under `std`/`classic` (own links from earlier turns freely removable, any own turn, no count limit), false under `pp`. `twixtui` sided with the SGF/legacy-LG reading for `pp` (no removal) — see disagreement D3. `[VERIFIED, ruleset.go; game.go `RemoveLink`]` |
| RD8 | Win condition | Unbroken chain of own linked pegs from a peg in one own border row to a peg in the other own border row `[REPORTED, unanimous]` | Transitive flood-fill reachability; one peg simultaneously linked to both of a player's border flags `[VERIFIED]` | DFS chain check border-to-border by row/column match; any piece on the chain touching each target border counts `[VERIFIED]` | `Races.java` checks connectivity between border-row sets; exact algorithm not read line-by-line `[VERIFIED existence only]` | Same chain-to-both-borders description `[VERIFIED]` | Union-find with 4 virtual border nodes, checked immediately after the mover's own placement each turn `[VERIFIED, game.go `connected`/`finishTurn`]` |
| RD9 | Draw trigger | "If neither side can [complete a connection], the game is a draw" — open-ended, no formal detection/declaration procedure documented for physical play `[REPORTED (possibility) + INFERRED (no procedure)]` | Narrow, mechanical: draw fires the instant the player about to move has zero legal actions, exercised by a dedicated CI test `[VERIFIED]` | Player-declared only (`EndInDraw()`); no board-full or repetition auto-detection; the shipped console's draw-offer UI looks non-functional (calls a read-only query instead of the mutator) `[VERIFIED]` | Mutual agreement only ("Draw by agreement" UI string) `[VERIFIED existence of the message]` | Open-ended "neither side can connect" reading on the rules pages, plus the SGF venue convention of `propose-draw`/`accept-draw` `[VERIFIED]` | Two independent mechanisms: (a) mechanical — draw fires the instant the side to move has no legal placement anywhere on the board (`Reason: NoMovesLeft`), same narrow-trigger family as OpenSpiel; (b) mutual agreement via `OfferDraw`/`AcceptDraw` (`Reason: Agreement`), matching the SGF `propose-draw`/`accept-draw` convention. Mechanism (a) is a **judgement call** — see below. `[VERIFIED, game.go `finishTurn`, `OfferDraw`, `AcceptDraw`]` |
| RD10 | Swap/pie rule | Absent from the original 1962 3M/Avalon Hill text; added later by Randolph, present from the Schmidt Spiele edition onward `[VERIFIED (absence, red-bean.com/sgf/twixt.html) + REPORTED (later addition)]` | Mandatory mechanic: play on the exact same cell as the first move to trigger it; replayed at a 90°-rotated mirror coordinate `[VERIFIED]` | **Absent entirely** — no swap code anywhere in the codebase `[VERIFIED, absence]` | A third, distinct mechanic: swaps player identity/colour only, never repositions the peg `[VERIFIED]` | Mirrors the first peg across the board's main diagonal and recolours it; worked example given verbatim: White's `B4` becomes Black's `D2` `[VERIFIED, worked example read directly]` | `Ruleset.Swap` flag: on under `std`/`pp`, off under `classic` (matching the historical 1962 absence). Mechanic: mirror the first peg across the main diagonal and reassign it to the swapping side, available exactly on the second ply, once only. Matches Little Golem's documented convention exactly, including the `B4`→`D2` example. `[VERIFIED, ruleset.go `Swap`; game.go `Swap()`; `TestSwapReflectsAcrossDiagonal`]` |
| RD11 | Turn order / axis binding | Split naming convention: "3M lineage" (Red moves first, Red = top/bottom) vs. "Little-Golem lineage" (White moves first, White = top/bottom) — same underlying rule, different colour label. First mover always owns top/bottom in every source. `[REPORTED]` | Player 0 = red = `x`, first, connects top/bottom; Player 1 = blue = `o`, connects left/right `[VERIFIED]` | Red always first, connects rows 0/(size-1); Black connects columns `[VERIFIED]` | Y-axis (top/bottom) player starts by default (`mdYstarts=true`) `[VERIFIED]` | White moves first, connects top/bottom `[VERIFIED]` | Sides are **not named by colour** at all: `Vertical` connects top/bottom and always moves first; `Horizontal` connects left/right. Colour is a separate, cosmetic choice made at game setup (R7), decoupled from engine identity — this sidesteps the naming split rather than resolving it. `[VERIFIED, game.go `Player` type and doc comment]` |
| RD12 | Opening restrictions | None beyond border-row placement and the one-shot swap window `[REPORTED, absence across all sources]` | None beyond the swap structural rule `[VERIFIED, absence]` | None found `[VERIFIED, absence]` | Not addressed as a distinct rule | None documented | None beyond `CanPlace` and the swap window; no move-number special-casing in the placement path `[VERIFIED, absence, game.go]` |
| RD13 | Notation | Column letter + row number (`B4`); SGF link-centre notation (`C2*`, `B*3`) for link-only "long moves" `[VERIFIED, red-bean.com/sgf/twixt.html]` | Internal integer action *and* a human-readable `xc5`-style string (row counted from the top, `x`/`o` prefix) `[VERIFIED]` | Raw 0-indexed `(row, col)` integer pairs; no algebraic notation in the engine `[VERIFIED]` | Letter + number, matching the community standard `[VERIFIED]` | Letter + number; link-centre apostrophe notation for link-only moves (SGF spec) `[VERIFIED]` | Letter(s) + number, row counted from 1 at the top (`ColumnName`/`ParsePoint`); links written as two hole names joined by `:` rather than a link-centre/slope encoding; move syntax adds `~`/`+`/`-`/`x` prefixes and the whole-word tokens `swap`/`resign`/`draw?`/`draw!` — twixtui's own scheme, not a transcription of the SGF spec, but covering the same move vocabulary. `[VERIFIED, notation.go]` |
| RD14 | Own links may cross | STD/box: forbidden (same predicate as RD6). PP: permitted — "your own links may cross each other... may result in a winning path which loops across itself." `[REPORTED]` | Forbidden — the game never reaches a state with two crossing same-colour links, by construction `[VERIFIED]` | Forbidden (same colour-blind check as RD6) `[VERIFIED]` | Forbidden `[VERIFIED]` | Legacy page permits it (PP); current docs page silent/contradictory — see disagreement D2 | `OwnLinksMayCross` flag: false under `std`/`classic` (forbidden), true under `pp` (permitted) — matches the SGF/legacy-LG PP description exactly; exercised by `TestOwnLinksMayCrossUnderPP`. `[VERIFIED, ruleset.go, game.go]` |
| RD15 | Pass / resign / forfeit / draw offer | No pass exists (every turn places a peg). Resign/forfeit/draw-offer are tournament/online-venue conventions (SGF move types; Little Golem time-bank forfeiture), not base rules. `[VERIFIED (SGF types, LG timeout) + REPORTED (absence in base rules)]` | No pass/resign/forfeit action exists anywhere in the action space `[VERIFIED, absence]` | No resign/forfeit/pass; only the mutual-draw path exists as an early-ending mechanism `[VERIFIED, absence]` | Resign/forfeit/draw-agreement modelled in UI strings and the `.T1` save format `[VERIFIED]` | SGF defines `resign`/`forfeit`/`propose-draw`/`accept-draw`; Little Golem enforces timeout-driven forfeiture via a time bank plus vacation days `[VERIFIED]` | No pass (`ErrNoPegPlaced` if a turn doesn't place a peg). `Resign()`, `OfferDraw()`, `AcceptDraw()` are first-class engine operations, recorded in history as `ResignMove`/`DrawOfferMove`/`DrawAcceptMove`. No timeout-forfeiture mechanism — turn-clock enforcement is a network/UI concern outside the rules engine's scope. `[VERIFIED, game.go]` |
| RD16 | Notable extras | (i) Double TwixT 4-player "Privilege" variant is part of the physical box rules, not a fan addition. (ii) Row-handicapping is an official variant. (iii) TwixT PP is documented as the *original* 1957 ruleset, not a later simplification. (iv) The one transcription describing peg removal (ultraboardgames.com) is the sole attestation for that mechanic — see the judgement-call note below. `[REPORTED/VERIFIED as cited in rules-canon.md §3, rule 4a and §5]` | Observation tensor encodes a "has-blocked-neighbours" bit with no rules analogue; `MaxGameLength` budgets exactly one extra ply for the swap replay `[VERIFIED]` | Finite, configurable peg/link supply (default 50 each) — a resource constraint not found in any base-rules source `[VERIFIED]` | Save format (`.T1`) is a simple versioned line-oriented text fixture, including a pie-rule-enabled flag and a starting-player flag `[VERIFIED]` | Board sizes 24/30/48 offered as distinct game types on the same platform `[VERIFIED]` | `PegRemoval` exists as an explicit opt-in option, off in every preset, because ultraboardgames.com's transcription is the *only* source (of every source read across all four reports) that describes lifting a peg, not merely a link. `[VERIFIED, ruleset.go; see rules-canon.md rule 4a]` |

## Disagreements, mapped to twixtui options

Every dimension above where the sources genuinely diverge (not merely
differ in naming or coverage) is exposed as an explicit `Ruleset` field
rather than hard-coded — R31.

- **D1 — Deliberate vs. automatic linking (RD5).** The fullest box-rules
  transcription describes deliberate, per-link choice; nearly every software
  implementation and the PP ruleset instead auto-link. twixtui's
  `DeliberateLinking` field: `true` under `std`/`classic` (auto-proposed, but
  declinable/extendable), `false` under `pp` (fully automatic, no control).
- **D2 — Own links crossing (RD6/RD14).** STD forbids it; PP (per the SGF
  spec and Little Golem's legacy rules page) permits it; Little Golem's
  *current* docs page is silent on the point, in direct tension with its own
  legacy page. twixtui's `OwnLinksMayCross` field: `false` under
  `std`/`classic`, `true` under `pp`.
- **D3 — Link removal (RD7).** STD/box: freely removable. PP, per the SGF
  spec and Little Golem's legacy page: never removable. Little Golem's
  *current* docs page says the opposite of its own legacy page. twixtui's
  `LinkRemoval` field: `true` under `std`/`classic`, `false` under `pp` —
  siding with the SGF spec and the legacy LG page, since the SGF
  specification is the single most implementation-neutral cross-venue source
  found (see `impl-t1j-bots.md` §1d), while flagging that Little Golem's own
  current documentation disagrees with itself on this point.
- **D4 — Draw trigger (RD9).** Not exposed as a ruleset toggle: no source
  offers two competing *mechanical* triggers to choose between, only an
  open-ended, undecidable description versus a hand-picked mechanical one.
  See the judgement-call note below.
- **D5 — Swap mechanic and presence (RD10).** Presence is a genuine
  divergence (absent from 1962, present later) and is the `Swap` field:
  `true` under `std`/`pp`, `false` under `classic`. The *mechanic itself*
  (three distinct implementations found: Little Golem's diagonal-mirror,
  OpenSpiel's same-cell-then-rotate, T1j's colour-only swap) is not exposed
  as an option — twixtui fixes it to the Little Golem/SGF diagonal-mirror
  convention regardless of preset, because that is the convention actually
  used by the dominant online venue and documented by name in the SGF
  specification.
- **D6 — Colour/turn-order naming (RD11).** Not a rules divergence but a
  labelling one — the underlying invariant (first mover owns top/bottom) is
  universal. twixtui resolves it architecturally rather than as a ruleset
  option: sides are named `Vertical`/`Horizontal` by the axis they connect,
  and colour is chosen separately by the player at setup.
- **D7 — Peg removal (RD16).** Only one transcription attests it at all.
  twixtui's `PegRemoval` field defaults `false` in every named preset and
  must be explicitly turned on.
- **D8 — Board size (RD1).** Sources range from a hardcoded 24×24
  (ccaesar26's original repo, glTwixt) through configurable 5–48 depending on
  implementation. twixtui exposes `Size` as a free field, `MinSize=6`,
  `MaxSize=48`, default 24 regardless of preset.

## Known unresolved points

- **Little Golem's own documentation contradicts itself on link removal.**
  The legacy rules page (`littlegolem.net/jsp/games/gamedetail.jsp`) states
  PP forbids removing links; the current docs page
  (`docs.littlegolem.net/games/twixt/`) states the opposite — that removal is
  explicitly allowed. Both were read directly the same research session; the
  contradiction is not a citation error. `[VERIFIED, impl-t1j-bots.md, C7/C8]`
  This is left unresolved in the historical record. twixtui's `pp` preset
  sides with the no-removal reading (matching the legacy LG page and the SGF
  specification's `PP` value), and that choice is recorded here rather than
  silently picked.
- **The printed 1962 rules are reported to be internally inconsistent about
  link removal, between print runs, and the primary text could not be read.**
  David Bush's SGF specification — the community's most-cited documentary
  authority — states, of the `3M` ruleset value: *"depending on which
  production run you have, the description of link removal may be confusing
  or nonexistent. There are no recorded games, as far as David knows."*
  `[VERIFIED, red-bean.com/sgf/twixt.html, quoted directly in
  rules-canon.md §5]` The BGG-hosted transcription of the original box text
  ("what 3M left out") is itself unreachable — gated behind a login, and
  every other direct fetch of BGG's pages for it returned HTTP 403.
  `[UNREACHABLE, rules-canon.md §2]` twixtui's `classic` preset therefore sets
  `LinkRemoval: true` on the strength of the *reconstructed* box-rules
  reading (ultraboardgames.com's transcription and its corroborators), not
  against a personally-read primary artefact — this is a `REPORTED`-tier
  claim, not `VERIFIED`, and is recorded as such here.
- **The Red/White-first naming split is not resolved by any source** — it is
  two labels for one underlying invariant (first mover owns top/bottom), and
  no source explains why the labelling differs. twixtui sidesteps rather than
  resolves it (D6, above).
- **T1j's AI-search internals were not read line-by-line** (`MoveGenerator.java`,
  `FindMove.java`, `Evaluation.java`, `Races.java`); RD7's "not wired to any
  player-facing move" and RD8's win-check description are both scoped
  explicitly to what was read, not to the whole codebase.
  `[VERIFIED (scoped), impl-t1j-bots.md §5, C5]`

## Judgement calls twixtui made that no source settles

Per the assignment: these two are not sourced rules, and are recorded loudly
rather than passed off as documented behaviour.

1. **The mechanical draw trigger.** Every documentary source describes draws
   as occurring "if neither side can possibly complete a connection any
   more" — a condition about the reachable future of the game, not about the
   current position, and one no source gives a detection procedure for
   (`rules-canon.md` RD9, C13). twixtui instead declares a draw the instant
   the side to move has *no legal placement anywhere on the board* — a
   narrower, always-computable condition that happens to match the family of
   trigger OpenSpiel's independent implementation uses (though OpenSpiel is
   evidence that this reading is reasonable, not evidence that it is *the*
   documented rule — no source claims the two conditions are equivalent, and
   in principle a position could exist where placement is still legal but no
   connection is still achievable). This is `twixtui`'s own design decision,
   not a transcription of any source.
2. **Peg removal defaults off.** `PegRemoval` exists in the `Ruleset` struct
   and is fully implemented, but is off in every named preset, because
   exactly one source — ultraboardgames.com's rulebook transcription — 
   attests that the printed rules ever allowed lifting a whole peg (as
   opposed to just a link) off the board (`rules-canon.md`, rule 4a). No
   other implementation surveyed (OpenSpiel, ccaesar26, T1j) offers this at
   all, and no other documentary source mentions it. Exposing it as an
   opt-in option rather than omitting it keeps the rule available to anyone
   who wants to play the ultraboardgames.com reading, without asserting that
   reading is the historically dominant one.
