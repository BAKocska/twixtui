# Changelog

All notable changes to twixtui are recorded here.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this
project uses [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

Nothing has been released yet; this section records what is in the repository.

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

[Unreleased]: https://github.com/BAKocska/twixtui/commits/main
