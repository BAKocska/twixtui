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
display server, out of a single binary with nothing to install alongside it.

The rules `twixtui` implements, the handful of points where historical editions
genuinely disagree, and where every rule comes from are written up in
[docs/rules.md](docs/rules.md), with the source audit trail in
[docs/RULES-PROVENANCE.md](docs/RULES-PROVENANCE.md).

## The board

A 24×24 game in progress, drawn at the compact scale, which puts neighbouring holes
two columns and one row apart. The renderer switches to a larger scale, four
columns and two rows, when the terminal is big enough for it.

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

`●` is the vertical player, who connects the top border row to the bottom one;
`○` is the horizontal player, connecting left to right. `·` is an empty hole,
`[·]` is the cursor, and `◎` is the peg just played — `◉` when it is vertical's.

The strokes between pegs are links. A link one column across and two rows deep is
steep enough for a plain diagonal, `╱` or `╲`. One that goes two columns across and
only one row down covers four screen columns for every row it descends, far
shallower than any diagonal a cell can draw, so it is drawn as a connected run of
`─` with corners `╭ ╮ ╰ ╯`, and a tee or a cross — `├ ┤ ┬ ┴ ┼` — where two links
leave a peg on the same side and share a run. Where two links that cross reach the
same cell, that cell gets `╳`, because a tee there would say they meet. Two
crossing links need not reach the same cell — at the compact scale two crossing
steep links land one directly above the other — and then nothing is marked: the
only cell a mark could go in belongs to one of the two links, and taking it would
erase that link. A peg a run has to pass through is drawn as `⊕` or `⊖`, which says
both things at once and still names its owner.

The cursor and a highlight sit as brackets either side of a hole, `[ ]` and `( )`.
Where a link already owns those cells a bracket would erase it, so the mark goes on
the hole itself instead: `◇ ◆ ◈` for the cursor, `□ ■ ▣` for a highlight, `△ ▲ ▽`
for a highlighted hole with the cursor on it. Outline is an empty hole, solid is
vertical and the third form is horizontal, so a mark never hides what the hole
holds.

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

Every release publishes binaries for four platforms, built without cgo so they carry
no third-party dependencies. Take the
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
twixtui                                               # the menu, and a name the first time
twixtui play bot --tier intermediate --side vertical  # one game against the bot
twixtui play local                                    # two players, one keyboard
twixtui learn                                         # the interactive tutorial
twixtui help                                          # every command, with what it is for
```

Start with the first line. Playing a game needs a profile to record it against,
and the bare command takes a name when the machine has none; on a machine with no
profiles `--profile NAME` will make one too.

## The first run

The first time a profile opens the interactive mode it meets a short
introduction: five steps on a real board, showing what the game is, whose borders
are whose, that a turn is one peg, how links form, and that links block. Each
step invites a move rather than requiring one, and nothing in it refuses to
advance.

It is skippable at every step and the key that skips is named on every step. It
is shown once per profile and never again, and skipping counts as having seen it:
somebody who skipped does not want it again tomorrow. `Learn to play` on the menu
has an entry that replays it deliberately, next to the seven-lesson tutorial it
points at.

The flag belongs to the profile rather than to the machine, so a second person
with their own profile on a shared machine is still a newcomer.

## The menu

`twixtui` with no arguments opens the front screen, which is grouped by how often
somebody does the thing rather than by the mechanism behind it:

| Entry | What is behind it |
| --- | --- |
| `Play` | A new game. Who is on the other side is the first question — the computer, somebody at this keyboard, or somebody on another machine — and the questions after it depend on the answer. Escape walks back through them. |
| `Continue a saved game` | The games still waiting for a move. A network game that has lost its connection, and a game imported from elsewhere, are listed but cannot be played on, and the row says why. |
| `Watch a finished game` | Step through a finished game, including one imported from another machine. |
| `Learn to play` | The tutorial, the written rules, and the introduction again. |
| `Leaderboard` | The standings, and one player's history. |
| `Settings` | Colours, the default ruleset, the default board size, whether hints are offered, and which profile is playing. Set once and forgotten; kept per machine, as the colour scheme is. |
| `Quit` | Leave. `q` does the same from the front screen. |

Every level is leavable by escape and the way out is named on the screen. The
cover artwork appears beside the menu when the terminal is wide enough for both;
it never costs an entry, so at eighty columns there is simply no picture.

## Commands

| Command | What it is for |
| --- | --- |
| `twixtui` | Interactive mode: the menu, and a profile first if none has been chosen yet. No flags to remember. |
| `twixtui play bot` | Play the built-in bot at one of three strengths. |
| `twixtui play local` | Hotseat: two players taking turns at the same terminal. |
| `twixtui play host` | Offer a live game to a remote opponent. |
| `twixtui play join` | Accept a remote opponent's live game. |
| `twixtui play correspondence` | Play by exchanging move codes, with no live connection at all. |
| `twixtui learn` | Interactive lessons: the rules, then the ideas behind them. |
| `twixtui profile` | `list`, `create`, `use`, `rename`, `delete`, `whoami` — the local usernames games are recorded against. |
| `twixtui leaderboard` | `show`, `reset` — standings and per-player history. |
| `twixtui game` | `list`, `show`, `replay`, `export`, `import`, `delete` — saved games: browse them, step through them, move them between machines. |
| `twixtui rules show` | Print the rules, or one topic of them, and the sources behind them. |
| `twixtui serve` | Run the relay that pairs two remote players. |
| `twixtui theme` | `list`, `set`, `show` — colour schemes. |
| `twixtui completion` | Emit a completion script for `bash`, `zsh`, `fish` or `powershell`. |
| `twixtui version` | Version, commit and build date of this binary. |

Every command accepts these:

| Flag | Effect |
| --- | --- |
| `--profile NAME` | Play this one command as that profile, without changing the stored choice. |
| `--config DIR` | Read and write state under `DIR`. Also settable as `TWIXTUI_CONFIG_DIR`. |
| `--theme NAME` | Theme for this run only; does not change the saved choice. |
| `--no-color` | No colour. `NO_COLOR` in the environment does the same. |

## Playing a bot

```
twixtui play bot --tier beginner --side vertical
twixtui play bot --tier intermediate --side horizontal
twixtui play bot --tier pro --side random
```

| Tier | What it does |
| --- | --- |
| `beginner` | One move ahead on peg count alone, answered at once: takes a win and blocks one, but has no plan. |
| `intermediate` | Three moves ahead with the full evaluation, still near-instant: punishes a loose chain. |
| `pro` | Thinks for up to three seconds, extending forced lines, with a transposition table: the strongest play on offer. Its nominal depth limit is sixteen, but sixteen is theoretical — with the per-move budget lifted, a thirty-second search of a 16×16 position reached depth seven. |

One alpha-beta search backs all three. They differ in how deep they may go, how many
candidate moves they will look at, and how much of the evaluation they are allowed to
see: the beginner works from peg counts alone and spreads its choice over its six
candidates, so it plays the second- or third-best move often; the pro adds a
transposition table and extends forced lines. The beginner and intermediate tiers are
capped by depth rather than by clock and answer instantly — only the pro tier actually
takes time to think.

The gaps are measured rather than assumed, by a tournament the test suite runs:
every opening played twice, once from each side, with the swap option off. Over 60
games on a 10×10 board, `intermediate` beat `beginner` 58–2.

How much stronger `pro` is depends on the board, and on a small one it may not be
stronger at all. The measurement that runs on every build asks only that `pro` not
do materially worse than `intermediate` on 10×10 — a floor of 0.45 of the points
available — because that is all that holds there.

The size dependence is measured separately, on the same protocol: twelve unique
openings, each played from both sides, twenty-four games a size. At the shipped
budgets `pro` scored 0.542 on 12×12 and 0.958 on 16×16 — level on the smaller
board, far ahead on the larger. Given both tiers an equal thirty-second guard so
the deeper search has room, it scored 0.458 on 10×10, 0.417 on 12×12, 0.583 on
14×14 and 0.583 on 16×16, and every one of those has a 95% confidence floor below
0.5. So across that range the extra depth does not establish a reliable advantage.

Read plainly: the gap widens with the board. It is not established anywhere from
10×10 to 16×16 under an equal guard, and at the shipped budgets it is even on 12×12
and large on 16×16, so on a small board `pro` is a different opponent rather than a
better one and `intermediate` answers instantly. The 24×24 board the game ships
with is larger still, and the trend points the same way, but it has not been
measured on this protocol and no figure here stands for it. The figures, the
protocol and the number of games
behind each are recorded with the measurement in `internal/bot/strength_test.go`.

| Flag | Effect |
| --- | --- |
| `--tier beginner\|intermediate\|pro` | How hard the bot tries. |
| `--side vertical\|horizontal\|random` | Which connection you take. Required: there is no default. |
| `--ruleset std\|pp\|classic` | Which ruleset to play under. |
| `--size N` | Board side length, 6 to 48. |
| `--seed N` | Fix the bot's random seed. |
| `--hints` | Whether `?` may ask for advice on your turn. On by default. |

You pick your side before the first move, and on the command line `play bot` will
not start without it: leave `--side` out and it says so. The menu asks instead.
`--side random` is there for players who would rather not choose.

`--seed N` fixes the bot's randomness, which is what the beginner tier samples with.
Given the same seed, ruleset and moves, the two depth-capped tiers reply the same way
every time; the pro tier is bounded by a clock rather than by depth, so a machine
under load can stop its search a little earlier and answer differently. That is the
search doing what it was asked, not a fault, but it means a pro game is reproducible
in practice rather than by guarantee. The bot's own tests pin determinism with the
clock taken out of the question, for the same reason.

Asking for a hint runs that same search at full strength and gives you the move it
would play, a line on why, and the holes that reason is about, marked on the board.
It is available by default; `--hints=false` takes it away.

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

Both live transports behave the same once the two ends are talking. The host
chooses the ruleset, the board size and its own side — `--ruleset`, `--size`,
`--side`, with `--port` for a direct game on a port other than 4270 — and the
joining copy takes them from the handshake. Protocol version and ruleset are
compared as part of that handshake, so mismatched builds or mismatched rules are
refused before the first move rather than desyncing halfway through a game. If a
live connection drops, reconnecting replays the missing moves — and refuses to
continue if the two transcripts disagree.

**Correspondence.** No live connection at all, and no network requirement whatsoever.
Each move produces a short checksummed code beginning `TWX-`, which you send to your
opponent over any channel you like — chat, email, read out over the phone — and they
paste it into their own copy. Games live in your config directory, and you can have
several running at once.

```
# start a game and print an invitation to send
twixtui play correspondence --new

# accept an invitation, which is a code beginning TWXI-
twixtui play correspondence --join <code>

# open the game it is your turn in
twixtui play correspondence

# or name one, when several are waiting
twixtui play correspondence --game <id>
```

Inside the game, committing a move prints the code to send on a line of its own, and
`c` opens a field to paste your opponent's code into. A code carries the game it
belongs to and the position it was made in, so one pasted into the wrong game, pasted
twice, or mangled on the way is refused — and tells you which of those happened —
rather than corrupting the game. Codes can be re-sent: the same one applied twice is
refused the second time, so a lost message costs nothing.

There is no handshake and no port here. The invitation carries the ruleset, the
board size and the side the host took, and the joining copy takes them from it.

## The tutorial

```
twixtui learn           # the lesson list
twixtui learn blocking  # straight to one lesson
```

Lessons put a real position on a real board and then ask you to play into it. The rules
are taught by playing them rather than by reciting them: the blocking lesson ends by
asking you to play the one peg whose link cuts your opponent's chain off from their
border for good, and tells you what went wrong when it does not.

The lessons are `board`, `links`, `blocking`, `double-threat`, `winning`, `swap` and
`practice`.

## Profiles

Games played on this machine are recorded against a local username. There are no
passwords and no accounts — a profile is a name plus when it was created and last
used.

```
twixtui profile list                 # all profiles, most recently used first
twixtui profile create ada
twixtui profile use ada
twixtui profile whoami               # which profile is in force
twixtui profile rename ada ada.l
twixtui profile delete ada.l
```

`twixtui` asks which profile you are when no profile has been chosen on this
machine yet; otherwise it opens the menu as whoever played last. The prompt is both
a fuzzy search over the profiles you already have and a browsable list of them, so
a half-remembered name or a typo still finds the right one. `profile use`, and
Profile under the menu's Settings, change who that is.

`--profile NAME` plays one command as that profile. It resolves the name by exactly
the rules `profile use` applies and is refused wherever `profile use` would refuse
it, so a typo cannot split your history across two identities. It records that the
profile has played, but it does not change the stored choice: a scripted game
cannot retarget the next interactive one. The one profile it may create is the
first, on a machine that has none — there is no stored choice to retarget there and
no other name you could have meant, which is what lets a new player go from install
to a game in a single command.

## The leaderboard

Every game finished on this machine is recorded: who played, which side they took,
the ruleset, how many moves, how long it took and how it ended. A record read in
with `game import` is not, since nobody here played it: it is kept to be shown and
replayed, and it does not reach the standings.

```
twixtui leaderboard show --limit 20      # standings, best first
twixtui leaderboard reset --yes          # wipe the record
```

Saved games are kept too, and can be moved between machines:

```
twixtui game list --limit 20
twixtui game show <id>
twixtui game replay <id>
twixtui game export <id> --out ada-vs-pro.twixt
twixtui game import ada-vs-pro.twixt
twixtui game delete <id>
```

The identifier is the short string `game list` prints in its first column, such
as `zrh7y174`.

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
twixtui rules show
twixtui rules show crossing
twixtui rules show --provenance
twixtui play bot --ruleset classic --size 12 --side vertical
```

Which sources support which reading, and what the primary 1962 text does and does not
settle, is in [docs/rules.md](docs/rules.md) and
[docs/RULES-PROVENANCE.md](docs/RULES-PROVENANCE.md).

## The cover

The 1962 box lid — a man contemplating a board of tapered pegs — is drawn beside
the menu when the terminal is wide enough. Kitty graphics are not available to
everyone, so it is character cells and ANSI colour and nothing else.

Two artworks ship because neither wins at every size. One is a projection of the
project's own flat reduction of the lid composition, which is the same picture
with the photographic texture and the low-contrast paper wear taken out, those
being what a character grid cannot carry. The other is hand-composed character
art: the wordmark, the flanking pegs, the linked chain over a field of holes. The
projection is the better picture where there is room for it; the drawing answers
below the size at which the projection's wordmark stops reading, and on a
terminal with no colour at all, where a dithered photograph is noise.

Which one appears is chosen from the space left over and the colour available.
Two variables override that:

| Variable | Effect |
| --- | --- |
| `TWIXTUI_COVER_ART` | `homage` for the drawing or `photo` for the projection, whatever the size suggests. Any other value is reported once at startup and then ignored. |
| `TWIXTUI_COVER_IMAGE` | A JPEG or PNG of your own to project instead of the shipped picture. Refused, with the reason, if it cannot be decoded or is larger than four thousand pixels a side — more detail than a terminal can resolve. |

Both are read once, when the program starts, so a complaint about either arrives
before anything is drawn rather than over the top of it. `--no-color` and
`NO_COLOR` reach the artwork as well as the board: neither will emit colour.

The shipped picture is a nine-kilobyte reduction, decoded on first use rather
than at startup, and regenerable from a source committed beside it.
[docs/COVER.md](docs/COVER.md) records how the artworks were chosen, which
converters were tried and dropped and why, and what the choice costs;
[assets/README.md](assets/README.md) records what the picture is and where its
reference came from.

## Themes

```
twixtui theme list                   # the four built-in themes
twixtui theme show                   # the one in force
twixtui theme set slate
```

| Theme | Description |
| --- | --- |
| `classic` (default) | Red and indigo, after the printed board game, for a dark terminal. The printed game's second player is black, which cannot be seen on a dark terminal, so indigo stands in for it. |
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

The board is driven from the keyboard, vim-style. The bindings are unmodified
printable keys, plain uppercase letters, and the basic special keys — the arrows,
space, enter, escape and ctrl+c. Modified arrows and protocol-dependent
combinations are not reliable inside a terminal multiplexer, so the keymap uses
none of those.

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
| `?` | In a bot game: the move the bot would play, and why. |
| `s` | Take the swap option, while it is on offer. |
| `d` | Offer a draw, or accept the one on offer. |
| `r` | Resign. |
| `q` | Leave the game. Started from the menu, it goes back to the menu; started from the command line, there is nothing behind it, so the program ends and the game is saved. |
| `ctrl+c` | Leave the game and end the program, saving it either way. |

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
