# Changelog

All notable changes to twixtui are recorded here.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this
project uses [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

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
  a clock budget. Measured over 60 games each on a 10×10 board, colour balanced with
  swap off, intermediate beat beginner 58–2 and pro beat intermediate 43–13 with 4
  draws. On request the bot explains the move it would play and marks the holes its
  reasoning is about.
- Terminal board renderer with two drawing scales, a viewport that scrolls to follow
  the cursor and survives a resize, and a layout engine that fits the board and the
  information panel to whatever size the terminal is.
- Data-driven keymap: vim-style cursor movement, jumps, edge jumps, peg placement, and
  a link mode where the eight knight's-move directions are numbered and toggled by the
  digit keys. Every binding is an unmodified key or a plain uppercase letter, so none
  of them depend on terminal-specific modifier encoding.
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

[Unreleased]: https://github.com/BAKocska/twixtui/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/BAKocska/twixtui/releases/tag/v0.1.0
