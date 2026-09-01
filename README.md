# twixtui

TwixT in the terminal: play Alex Randolph's connection game against a bot, against
someone at the same keyboard, or against someone on another machine, without leaving
your shell.

TwixT is a two-player connection game. The board is a square grid of holes; each turn
you place one peg and join your own pegs with links a chess knight's move apart. A link
blocks any other link whose line it crosses — including your own — so the game becomes a
running argument about which crossings you can afford to give away. One player connects
top to bottom, the other left to right, and the first to complete an unbroken chain
between their own two edges wins. Randolph devised it on paper in Vienna in 1957; 3M put
it in a box in 1962.

A terminal is a good place for it. The board is a grid of discrete positions joined by
short straight lines, which is what a character cell grid is already good at drawing,
and nothing here wants a mouse: the whole game is "move the cursor, place a peg, decide
about links". So it plays over SSH, in a pane beside your work, on a machine with no
display server, out of a single static binary with nothing to install alongside it.

The rules `twixtui` implements, the handful of points where historical editions
genuinely disagree, and where every rule comes from are written up in
[docs/rules.md](docs/rules.md), with the source audit trail in
[docs/RULES-PROVENANCE.md](docs/RULES-PROVENANCE.md).

## The board

A 24×24 game in progress, drawn at the compact scale. The renderer switches to a
larger scale, which draws every link with three-cell strokes, when the terminal is
big enough for it.

```
    A B C D E F G H I J K L M N O P Q R S T U V W X
 1    · · · · · · · · · · ● · · · · · · · · · · ·
 2  · · · · · · · · · · ·╱· · · · · · · · · · · · ·
 3  · · · · · · · · · · ●⎼· · · · · · · · · · · · ·
 4  · · · · · · · · · · · ·⎻● · · · · · · · · · · ·
 5  · · · · · · · · · · · ·╱· · · · · · · · · · · ·
 6  · · · · · · · · · · · ●⎼· · · · · · · · · · · ·
 7  · · · · · · · · · · · · ·⎻● · · · · · · · · · ·
 8  · · · · · · · · · · · · ·╱· · · · · · · · · · ·
 9  · · · · · · · · · · · · ●⎼· · · · · · · · · · ·
10  · · · · · · · · · · · · · ·⎻● · · · · · · · · ·
11  · ·⎼○⎼· · ·⎼○⎼· · ·⎼○⎼· · ·╱○⎼· · ·⎼○ · · · · ·
12  ○⎻· · ·⎻○⎻· · ·⎻○⎻· · ·⎻○ ●⎼· ·⎻○⎻· · · · · · ·
13  · · · · · · · · · · · · · · ·⎻● · · · · · · · ·
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

`●` is the vertical player, who connects the top border row to the bottom one;
`○` is the horizontal player, connecting left to right. `·` is an empty hole,
the strokes between pegs are links, and `[·]` is the cursor.

Vertical has an unbroken chain from L1 all the way down to O15. Horizontal's runs
from the left edge at A12 as far as M12 and then stops: the link it wants from M12
to O11 is blocked, because vertical's link from O10 to N12 crosses it. That is the
whole game in one picture — every link you build is also a wall.

## Install

### With Go

```
go install github.com/BAKocska/twixtui/cmd/twixtui@latest
```

Go 1.26 or newer. The binary lands in `$(go env GOPATH)/bin`.

### Download a binary

Every release publishes static, dependency-free binaries for four platforms. Take the
archive for yours from the
[releases page](https://github.com/BAKocska/twixtui/releases/latest):

| Platform | Archive |
| --- | --- |
| macOS, Apple silicon | `twixtui_<version>_darwin_arm64.tar.gz` |
| macOS, Intel | `twixtui_<version>_darwin_amd64.tar.gz` |
| Linux, arm64 | `twixtui_<version>_linux_arm64.tar.gz` |
| Linux, x86-64 | `twixtui_<version>_linux_amd64.tar.gz` |

Unpack it and put `twixtui` somewhere on your `PATH`:

```
tar xzf twixtui_<version>_darwin_arm64.tar.gz
sudo install -m 755 twixtui /usr/local/bin/twixtui
twixtui version
```

Every release also ships `checksums.txt`, so you can check what you downloaded:

```
shasum -a 256 -c checksums.txt --ignore-missing
```

**On macOS**, a file downloaded through a browser carries a quarantine attribute and
Gatekeeper refuses to run it — "cannot be opened because the developer cannot be
verified". These binaries are not signed or notarised, so clear the attribute yourself:

```
xattr -d com.apple.quarantine ./twixtui
```

Downloading with `curl -LO` instead of a browser avoids the attribute altogether, since
`curl` does not set it.

## Quick start

```
twixtui                                  interactive: asks for a profile, then a menu
twixtui play bot --tier intermediate     one game against the bot
twixtui play local                       two players, one keyboard
twixtui learn                            the interactive tutorial
twixtui help                             every command, with what it is for
```

## Commands

| Command | What it is for |
| --- | --- |
| `twixtui` | Interactive mode: asks which profile you are, then offers a menu. No flags to remember. |
| `twixtui play bot` | Play the built-in bot at one of three strengths. |
| `twixtui play local` | Hotseat: two players taking turns at the same terminal. |
| `twixtui play host` | Offer a live game to a remote opponent. |
| `twixtui play join` | Accept a remote opponent's live game. |
| `twixtui play correspondence` | Play by exchanging move codes, with no live connection at all. |
| `twixtui learn` | Interactive lessons: the rules, then the ideas behind them. |
| `twixtui profile` | `list`, `create`, `use`, `rename`, `delete`, `whoami` — the local usernames games are recorded against. |
| `twixtui leaderboard` | `show`, `reset` — standings and per-player history. |
| `twixtui game` | `list`, `show`, `replay`, `export`, `import` — saved games: browse them, step through them, move them between machines. |
| `twixtui rules show` | Print the rules in force, or one topic of them, for any ruleset. |
| `twixtui serve` | Run the relay that pairs two remote players. |
| `twixtui theme` | `list`, `set`, `show` — colour schemes. |
| `twixtui completion` | Emit a completion script for `bash`, `zsh`, `fish` or `powershell`. |
| `twixtui version` | Version, commit and build date of this binary. |

Every command accepts these:

| Flag | Effect |
| --- | --- |
| `--profile NAME` | Use this profile instead of asking. |
| `--config DIR` | Read and write state under `DIR`. Also settable as `TWIXTUI_CONFIG_DIR`. |
| `--theme NAME` | Theme for this run only; does not change the saved choice. |
| `--no-color` | No colour. `NO_COLOR` in the environment does the same. |

## Playing a bot

```
twixtui play bot --tier beginner
twixtui play bot --tier intermediate
twixtui play bot --tier pro
```

| Tier | What it does |
| --- | --- |
| `beginner` | One move ahead on peg count alone, answered at once: takes a win and blocks one, but has no plan. |
| `intermediate` | Three moves ahead with the full evaluation, still near-instant: punishes a loose chain. |
| `pro` | Thinks for up to three seconds, five to seven moves ahead, extending forced lines: the strongest play on offer. |

One alpha-beta search backs all three. They differ in how deep they may go, how many
candidate moves they will look at, and how much of the evaluation they are allowed to
see: the beginner works from peg counts alone and spreads its choice over its six
candidates, so it plays the second- or third-best move often; the pro adds a
transposition table and extends forced lines. The beginner and intermediate tiers are
capped by depth rather than by clock and answer instantly — only the pro tier actually
takes time to think.

The gaps are measured rather than assumed. Over 60 games each on a 10×10 board, colour
balanced and with swap off, `intermediate` beat `beginner` 58–2 and `pro` beat
`intermediate` 43–13 with 4 draws.

| Flag | Effect |
| --- | --- |
| `--tier beginner\|intermediate\|pro` | How hard the bot tries. |
| `--side vertical\|horizontal\|random` | Which connection you take. |
| `--ruleset std\|pp\|classic` | Which ruleset to play under. |
| `--size N` | Board side length, 6 to 48. |
| `--seed N` | Fix the bot's random seed. |
| `--hints` | Make hints available on your own turns. |

You pick your side before the first move. `--side random` is there for players who
would rather not choose and would rather not be asked either.

`--seed N` makes a bot game reproducible: the same seed, ruleset and moves produce the
same bot replies every time. That is how the bot's own tests pin its behaviour, and it
is useful for replaying a position that went wrong.

With `--hints`, asking for a hint runs that same search at full strength and gives you
the move it would play, a line on why, and the holes that reason is about, marked on
the board.

## Hotseat

```
twixtui play local --ruleset std --size 24
```

Two players, one terminal, alternating turns.

## Remote play

Two people each run `twixtui` on their own machine. There are three ways to connect, in
descending order of directness, and all three carry the same game protocol.

**Direct.** One player listens, the other dials in. Nothing but the binary is involved.
The listening side has to be reachable: the same LAN, a tailnet or WireGuard address, or
a forwarded port.

```
# on the host's machine, listening on the default port 4270
twixtui play host --side vertical

# on the opponent's machine
twixtui play join host.example:4270
```

**Through a relay.** When neither side can accept an inbound connection — CGNAT, hotel,
campus or mobile networks — both sides dial out to a relay instead, and the relay pairs
them. The host prints a 22-character pairing code in three dashed groups; the opponent
needs all of it, not just the first group.

The relay is the same binary in another mode, and it pumps bytes without ever parsing
the game. Only the first group of the pairing code is sent to it; both ends derive a key
from the rest, which the relay is never told, and authenticate every frame with it. So a
relay cannot alter, inject, replay or drop a move without being caught. It does read
everything it carries, in plain text: both names, the ruleset and every move. Use a
relay you or your opponent runs, or one you are content to be read by.

```
# whoever has a reachable machine; default port 4271
twixtui serve --addr :4271

# the host, which prints a pairing code to pass on
twixtui play host --relay relay.example:4271

# the opponent, with the code they were given
twixtui play join K7MDPQ-3FHJ8TWZ-Q2XVNR5B --relay relay.example:4271
```

**Correspondence.** No live connection at all, and no network requirement whatsoever.
Each move produces a short checksummed code beginning `TWX-`, which you send to your
opponent over any channel you like — chat, email, read out over the phone — and they
paste it into their own copy. Games live in your config directory, and you can have
several running at once.

```
# start a game and print an invitation to send
twixtui play correspondence --new

# accept an invitation
twixtui play correspondence --join TWXI-...

# open the game it is your turn in
twixtui play correspondence

# or name one, when several are waiting
twixtui play correspondence --game a88srf81
```

Inside the game, committing a move prints the code to send on a line of its own, and
`c` opens a field to paste your opponent's code into. A code carries the game it
belongs to and the position it was made in, so one pasted into the wrong game, pasted
twice, or mangled on the way is refused — and tells you which of those happened —
rather than corrupting the game. Codes can be re-sent: the same one applied twice is
refused the second time, so a lost message costs nothing.

The host chooses the ruleset, the board size and its own side — `--ruleset`, `--size`,
`--side`, and `--port` for a port other than the default — and the joining copy takes
them from the handshake. Protocol version and ruleset are compared as part of that
handshake, so mismatched builds or mismatched rules are refused before the first move
rather than desyncing halfway through a game. If a live connection drops, reconnecting
replays the missing moves — and refuses to continue if the two transcripts disagree.

## The tutorial

```
twixtui learn                 the lesson list
twixtui learn blocking        straight to one lesson
```

Lessons put a real position on a real board and then ask you to play into it. The rules
are taught by playing them rather than by reciting them: the blocking lesson ends by
asking you to play the one peg whose link cuts your opponent's chain off from their
border for good, and tells you what went wrong when it does not.

The lessons are `board`, `links`, `blocking`, `double-threat`, `winning`, `swap` and
`practice`.

## Profiles

Games are recorded against a local username. There are no passwords and no accounts —
a profile is a name plus when it was created and last used.

```
twixtui profile list                 all profiles, most recently used first
twixtui profile create ada
twixtui profile use ada
twixtui profile whoami               which profile is in force
twixtui profile rename ada ada.l
twixtui profile delete ada.l
```

On launch, `twixtui` asks which profile you are. The prompt is both a fuzzy search over
the profiles you already have and a browsable list of them, so a half-remembered name or
a typo still finds the right one. `--profile NAME` skips the question.

## The leaderboard

Every finished game is recorded: who played, which side they took, the ruleset, how many
moves, how long it took and how it ended.

```
twixtui leaderboard show --limit 20      standings, best first
twixtui leaderboard reset                wipe the record
```

Saved games are kept too, and can be moved between machines:

```
twixtui game list --limit 20
twixtui game show 7
twixtui game replay 7
twixtui game export 7 --out ada-vs-pro.twixt
twixtui game import ada-vs-pro.twixt
```

## Rulesets

TwixT's editions and online venues genuinely disagree about a handful of rules.
`twixtui` makes each disagreement an explicit setting rather than a silent choice, and
ships three presets:

| Preset | Corresponds to | What it turns on |
| --- | --- | --- |
| `std` (default) | The printed box rules, as reconstructed for the Avalon Hill, Schmidt Spiele and Kosmos editions. | You choose which of the offered links to take; you may take your own links off on a later turn; no link may cross another, not even one of your own; swap offered. |
| `pp` | The paper-and-pencil ruleset, as played at online venues such as Little Golem. | Links are made automatically and are permanent; your own links may cross each other; swap offered. |
| `classic` | The original 1962 3M edition. | The box rules above, without swap — Randolph added swap in a later edition. |

Board size is chosen independently of the preset: 24×24 by default, anything from 6×6
to 48×48.

```
twixtui rules show --ruleset pp
twixtui rules show crossing
twixtui play bot --ruleset classic --size 12
```

Which sources support which reading, and what the primary 1962 text does and does not
settle, is in [docs/rules.md](docs/rules.md) and
[docs/RULES-PROVENANCE.md](docs/RULES-PROVENANCE.md).

## Themes

```
twixtui theme list                   the four built-in themes
twixtui theme show                   the one in force
twixtui theme set slate
```

| Theme | Description |
| --- | --- |
| `classic` (default) | Red and black, as the printed board game. |
| `slate` | Muted blue and amber, for dark terminals. |
| `paper` | Dark ink on a light background. |
| `mono` | No colour, distinguishes players by shape alone. |

`--theme NAME` overrides the saved choice for one run; `--no-color` and `NO_COLOR`
override both.

## Shell completion

Completions carry a one-line explanation per command, subcommand and enumerated flag
value, so tab does not merely finish a word, it tells you what the word does.
Descriptions appear in zsh, fish, and bash 4.4 or newer.

**bash**

```
twixtui completion bash > /etc/bash_completion.d/twixtui
```

On macOS with Homebrew's `bash-completion@2`:

```
twixtui completion bash > "$(brew --prefix)/etc/bash_completion.d/twixtui"
```

Or, for the current shell only:

```
source <(twixtui completion bash)
```

**zsh**

```
twixtui completion zsh > "${fpath[1]}/_twixtui"
```

Then start a new shell. If completion has never been enabled in your zsh, add
`autoload -U compinit && compinit` to `~/.zshrc` first.

**fish**

```
twixtui completion fish > ~/.config/fish/completions/twixtui.fish
```

**PowerShell**

```
twixtui completion powershell | Out-String | Invoke-Expression
```

Put that line in your PowerShell profile to make it permanent.

## Keybindings

The board is driven from the keyboard, vim-style. Every binding is an unmodified key
or a plain uppercase letter: modified arrows and protocol-dependent combinations are
not reliable inside a terminal multiplexer, so the keymap does not use any.

| Key | Action |
| --- | --- |
| `h` `j` `k` `l`, or the arrow keys | Move the cursor one hole. |
| `H` `J` `K` `L` | Jump three holes. |
| `g` / `G` | Jump to the top / bottom edge. |
| `0` / `$` | Jump to the left / right edge. |
| `space` | Place a peg in the hole under the cursor. |
| `enter` | Places the peg when none is staged yet, and commits the turn once one is. |
| `x` | Enter or leave link mode. |
| `1`-`8` | Only in link mode: toggle the link in that direction. |
| `esc` | Leave link mode. |
| `a` | Abort the turn: the board goes back to how it stood when your turn began. |
| `q`, `ctrl+c` | Quit. |

Placing a peg offers every link that peg can legally make, and link mode is where you
turn those on and off: the eight knight's-move directions around the cursor are
numbered, and the digit keys toggle them. Nothing is final until you commit the turn,
so a link you regret is one keystroke away from being undone.

## Building from source

```
git clone https://github.com/BAKocska/twixtui
cd twixtui
go build ./cmd/twixtui
go test ./...
```

The end-to-end suite drives a real terminal through `tmux`. Without `tmux` on the
`PATH` those tests skip rather than fail, so install it if you want the whole suite to
actually run.

## Licence

MIT. See [LICENSE](LICENSE).

TwixT was designed by Alex Randolph. This is an independent implementation, not
affiliated with, endorsed by, or licensed from any publisher of the board game.
