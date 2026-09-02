<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/banner-dark.svg">
    <img src="assets/banner-light.svg" alt="twixtui — TwixT in the terminal" width="100%">
  </picture>
</p>

# twixtui

TwixT in the terminal: play Alex Randolph's connection game against a bot, against
someone at the same keyboard, or against someone on another machine, without leaving
your shell.

<p align="center">
  <a href="docs/MANUAL.md"><img src="https://img.shields.io/badge/FULL%20DOCUMENTATION-docs%2FMANUAL.md-2b6cb0?style=for-the-badge" alt="Full documentation: docs/MANUAL.md"></a>
</p>

<p align="center">
  <a href="#quick-start">Quick start</a> · <a href="docs/rules.md">Rules</a> · <a href="docs/MANUAL.md">Manual</a> · <a href="CHANGELOG.md">Changelog</a> · <a href="https://github.com/BAKocska/twixtui/releases/latest">Releases</a>
</p>

<p align="center">
  <a href="https://github.com/BAKocska/twixtui/actions/workflows/ci.yml"><img src="https://github.com/BAKocska/twixtui/actions/workflows/ci.yml/badge.svg" alt="CI"></a> <a href="https://github.com/BAKocska/twixtui/releases/latest"><img src="https://img.shields.io/github/v/release/BAKocska/twixtui?sort=semver" alt="Latest release"></a>
  <img src="https://img.shields.io/badge/go-1.26%2B-00ADD8?logo=go&logoColor=white" alt="Go 1.26 or newer"> <a href="LICENSE"><img src="https://img.shields.io/badge/licence-MIT-blue" alt="MIT licence"></a> <img src="https://img.shields.io/badge/platforms-macOS%20%7C%20Linux-lightgrey" alt="macOS and Linux, arm64 and x86-64">
</p>

> [!NOTE]
> One static binary, built without cgo, nothing to install alongside it — so it plays over SSH, in a pane beside your work, or on a machine with no display server.

TwixT is a two-player connection game. The board is a square grid of holes; each turn you
place one peg and join your own pegs with links a chess knight's move apart. A link blocks
any other link whose line it crosses — including your own — so the game becomes a running
argument about which crossings you can afford to give away. One player connects top to
bottom, the other left to right. Randolph devised it on paper in Vienna in 1957; 3M put it
in a box in 1962. A terminal is a good place for it: the board is a grid of discrete
positions joined by short straight lines, which is what a character cell grid is already
good at drawing, and nothing here wants a mouse.

## The board

```
    A B C D E F G H I J K L M N O P Q R S T U V W X
 1    · · · · · · · · · · ● · · · · · · · · · · ·
 2  · · · · · · · · · · ·╱· · · · · · · · · · · · ·
 3  · · · · · · · · · · ●──╮· · · · · · · · · · · ·
 4  · · · · · · · · · · · ·╰● · · · · · · · · · · ·
 5  · · · · · · · · · · · ·╱· · · · · · · · · · · ·
 6  · · · · · · · · · · · ●──╮· · · · · · · · · · ·
 7  · · · · · · · · · · · · ·╰● · · · · · · · · · ·
 8  · · · · · · · · · · · · ·╱· · · · · · · · · · ·
 9  · · · · · · · · · · · · ●──╮· · · · · · · · · ·
10  · · · · · · · · · · · · · ·╰● · · · · · · · · ·
11  · ·╭○──╮· ·╭○──╮· ·╭○──╮· ·╱○──╮· ·╭◎ · · · · ·
12  ○──╯· ·╰○──╯· ·╰○──╯· ·╰○ ●──╮·╰○──╯· · · · · ·
13  · · · · · · · · · · · · · · ·╰● · · · · · · · ·
14  · · · · · · · · · · · · · · ·╱· · · · · · · · ·
15  · · · · · · · · · · · · · · ● · · · · · · · · ·
16  · · · · · · · · · · · · · · · · · · · · · · · ·
17  · · · · · · · · · · · · ·[·]· · · · · · · · · ·
18  · · · · · · · · · · · · · · · · · · · · · · · ·
19  · · · · · · · · · · · · · · · · · · · · · · · ·
20  · · · · · · · · · · · · · · · · · · · · · · · ·
21  · · · · · · · · · · · · · · · · · · · · · · · ·
22  · · · · · · · · · · · · · · · · · · · · · · · ·
23  · · · · · · · · · · · · · · · · · · · · · · · ·
24    · · · · · · · · · · · · · · · · · · · · · ·
```

A 24×24 game in progress. `●` connects the top border row to the bottom one, `○`
connects left to right, `·` is an empty hole, `[·]` is the cursor, and the strokes
between pegs are links. Vertical has an unbroken chain from L1 down to O15; horizontal's
runs from the left edge at A12 as far as M12 and then stops, because vertical's link
from O10 to N12 crosses the one it wants to O11. Every link you build is also a wall.
Every glyph, scale and overlay is in [the manual](docs/MANUAL.md#the-board).

## Install

```
go install github.com/BAKocska/twixtui/cmd/twixtui@latest
```

Go 1.26 or newer. Or take a prebuilt binary — macOS or Linux, arm64 or x86-64, with
`checksums.txt` beside it — from the
[releases page](https://github.com/BAKocska/twixtui/releases/latest). On macOS a binary
downloaded through a browser needs its quarantine attribute cleared first;
[the manual](docs/MANUAL.md#download-a-binary) has that and the checksum command.

## Quick start

```
twixtui                                               # the menu, and a name the first time
twixtui play bot --tier intermediate --side vertical  # one game against the bot
twixtui play local                                    # two players, one keyboard
twixtui learn                                         # the interactive tutorial
twixtui help                                          # every command, with what it is for
```

## What's in the box

| | |
| --- | --- |
| [Play a bot](docs/MANUAL.md#playing-a-bot) | Three measured tiers: `beginner`, `intermediate`, and a clock-bounded `pro`. Hints run the same search at full strength. |
| [Hotseat](docs/MANUAL.md#hotseat) | Two players, one terminal, alternating turns. |
| [Remote play](docs/MANUAL.md#remote-play) | Direct, through a relay you or your opponent runs, or by correspondence with no live connection at all. |
| [Learn it](docs/MANUAL.md#the-tutorial) | Seven lessons taught by playing them, and a five-step introduction on [first run](docs/MANUAL.md#the-first-run). |
| [Profiles & standings](docs/MANUAL.md#profiles) | Local names with no passwords, a [leaderboard](docs/MANUAL.md#the-leaderboard), and saved games you can move between machines. |
| [Honest rulesets](docs/MANUAL.md#rulesets) | `std`, `pp` and `classic` make each historical disagreement an explicit setting; boards from 6×6 to 48×48. |
| [Themes & cover art](docs/MANUAL.md#themes) | Four colour schemes, and the [1962 box lid](docs/MANUAL.md#the-cover) drawn beside the menu in character cells. |
| [Shell completion](docs/MANUAL.md#shell-completion) | `bash`, `zsh`, `fish` and `powershell`, each value carrying its own one-line explanation. |

The tiers are measured rather than assumed: over 60 games on a 10×10 board,
`intermediate` beat `beginner` 58–2. The protocol, and how much of an edge `pro` does
and does not establish, are in [the manual](docs/MANUAL.md#playing-a-bot).

## Licence

MIT. See [LICENSE](LICENSE).

TwixT was designed by Alex Randolph. This is an independent implementation, not
affiliated with, endorsed by, or licensed from any publisher of the board game.
