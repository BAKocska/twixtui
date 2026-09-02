# Assets

## cover.png

The project's own reduction of the composition on the lid of the 1962 TwixT
box, published by the Minnesota Mining and Manufacturing Company (3M) in its
bookshelf game series: the wordmark, the player resting his chin on his hand,
two tall red pegs flanking a row of linked black pegs over a field of holes.
The reduction was drawn as a flat poster — hard edges, a handful of colours,
none of the photographic texture a terminal cannot represent. The reference
was a photograph of a 1962 box lid published at vintagegamenight.com, used as
reference only and not redistributed here. The original lid artwork is
© 1962 3M; the repository owner decided the project ships this reduction and
treats the projection into terminal characters as the alteration.

The shipped file is deliberately small (213x320, sixteen colours): the
picture is only ever drawn as character cells, so detail beyond about twice
the densest cell grid would be bytes every binary carries for nothing.
cover-source.png is that full-resolution source, 1024x1535, carried here so the
recipe below is not a dead one. A regeneration command whose input lives only in
somebody's temporary directory turns the shipped file into a blob the moment that
directory is cleared, which is the thing this note exists to prevent.

Regenerate with:

    go run ./assets/gen -in assets/cover-source.png -out assets/cover.png -colors 16

The generator reduces by area-averaging in linear light — the same filtering
the renderer in internal/cover applies — and then snaps the result back onto
its own dominant colours, so flat art stays flat instead of carrying
thousands of one-off edge blends.

The three files below are for the repository's front page rather than for the
program: nothing in the binary reads them. They are SVG, which is to say they
are their own source — text, diffable, edited in place — so unlike cover.png
they need no regeneration recipe. What they do need is the record of which
facts in the code they are repeating, because a picture that quietly stops
agreeing with the program is worse than no picture. That is what each note
below is for.

## banner-light.svg, banner-dark.svg

The front page's hero: the 1962 lid put on a terminal screen. A cream bezel
frames an ink screen; the screen's top band is terminal chrome — a
`$ twixtui` prompt with a block cursor, and a status line reading "TwixT in
the terminal" — and below it, as the command's output, the lid itself:
wordmark and watcher over a violet sky, an ochre board full of holes across
the bottom, two black chains marching over it with a red wall cutting
between them, and a tall red peg standing at each flank. Two files rather
than one because README.md selects between them with a `<picture>` element
and `prefers-color-scheme`. The composition is identical in both; the dark
file is one step duskier, its sky `homageSky` where the light file's is
`homageSkyHigh` and its bezel `homageCream` where the light file's is the
lid's own cream, so the picture sits quietly on either of GitHub's
backgrounds without pretending to be one of them.

The terminal is drawn, not captured: nothing here claims to be a
screenshot. The prompt says what starting the program looks like, and the
cursor after it is the character `█` in the same run of text, so it sits
where the reader's own monospace font puts it instead of where some other
font's metrics were guessed to.

The colours are internal/cover/homage.go's, hex for hex, in the roles the
program's own cover gives them wherever this picture has the same role:
`homageInk` for the screen, the black player's links and the `ui` of the
wordmark; `homageBlack` for the black player's pegs; `homageRed` for the
red player entire — wall, flankers and `twixt`;
`homageBoard` for the board and `homageHole` for its holes; `homageSkyDusk`
for the watcher; `homageCream` for the prompt and cursor; and
`homageSkyHigh`, doing double duty, as the light file's sky and as the dim
chrome text of both files. Two values have no name in the code: the light
file's bezel `#f5eed4` and the Randolph line's maroon `#561d25`, measured
from assets/cover-source.png as the dominant colours of its border and of
the watcher's jacket. An earlier revision copied the banner's colours from
web/style.css so page and banner could not drift apart; that pin is
deliberately gone — the owner directed that the front page evoke the 1962
lid, and the site keeps its own red and blue — so the banner drifts with
the cover art instead, and anyone re-tuning homage.go's palette should
carry these two files along.

The wordmark still splits the way the project's site splits it — `twixt` is
the vertical player and `ui` the horizontal, the two goals that cut across
each other — but in the colours of the 1962 set, red pegs against black,
rather than the site's red and blue.

The two tall tapered pegs flanking the board are the lid's composition —
two tall red pegs flanking a chain of linked pegs over a field of holes —
drawn as silhouettes seen from the side, standing on the board. They stand
outside the grid and claim nothing about the position; the discipline below
applies to the chains, not to them. The watcher is the lid's third element,
reduced the way the homage's drawWatcher reduces him: a head-and-shoulders
silhouette one shade duskier than the sky, cut off by the board, present
when looked for and ignorable when not.

The board fragment is honest geometry, not decoration. The holes are a grid
at a fixed pitch, every peg sits on a grid point, and every link joins two
pegs a knight's move apart. The black chain runs from the fragment's first
column to its last — the horizontal player racing between its borders — and
the red wall touches all four visible rows on its way across it. The gap in
the black chain is a link that is genuinely refused: the segment it wants,
from its last peg to the empty hole two columns on, was checked to
intersect the red link that crosses it, and the twenty-one links that are
drawn were checked pairwise to cross nowhere. The fragment is a crop of a
larger board, and what the two black fragments do to each other beyond the
frame is not claimed. Anyone editing the pegs should keep all of that true
or drop the gap, because a banner showing an impossible position teaches
the rule wrongly to everybody who never reads on.

## board.svg

The position printed as characters under "The board" in docs/MANUAL.md, drawn
again in colour. README.md's "The board" section shows this file on the front
page; the manual shows it below the character block. It is a rendering and not
a screenshot: the
glyphs are taken from that code block and the colours are applied to them here.
Nothing was captured from a terminal, and the file does not claim otherwise.

Both halves of that are pinned to the code. The palette is the `classic` scheme
from internal/theme/theme.go, hex for hex. Which colour reaches which glyph
follows internal/ui/theming.go and the styleID table in internal/ui/theme.go:
holes take `Grid`, pegs take `VerticalPeg` and `HorizontalPeg` in bold, link
strokes take `VerticalLink` and `HorizontalLink`, the peg just played takes
`LastMove` rather than its owner's colour, the cursor brackets take `Cursor`
while the hole between them stays a hole, and the gutter numbers of the top and
bottom rows and the outermost column letters take their own player's colour
unbolded, the rest `BorderRow`. Five of those roles were also checked against a
real render rather than only against the source: `twixtui --theme classic learn
board` in a 200x50 tmux pane, read back with `capture-pane -e`, emits exactly the
hex this file uses for holes, for the gutter and column labels, for both players'
pegs and for the cursor brackets. The link colours and `LastMove` are taken from
the source alone, because that lesson opens on a position with no links on it and
no peg just played, so the capture had nothing to say about them.

Which stroke belongs to which player cannot be read off a glyph, because a run
of `-` looks the same whoever owns it. It was resolved from the geometry: each
pair of same-coloured pegs a knight's move apart was tested for the cells the
renderer would draw it in, a steep link occupying the one diagonal cell between
its ends and a shallow link three cells across plus a corner. That accounted
for all fifty-three link cells with none contested and none left over, and it
left the M12-O11 link undrawn, which is the blocked link the caption is about.
A wrong guess at ownership would have coloured that argument backwards.

The background is the one colour here that no scheme provides: no theme paints
a background, since pegs and links sit on whatever colour the player's terminal
already is. `classic` is a scheme for a dark terminal, so the file supplies a
dark one to stand in for it.
