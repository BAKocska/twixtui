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

The front page's hero: the 1962 lid re-staged for a wide box, on a terminal
screen. What the lid communicates, and what this picture is built to carry,
is contemplation rather than fun. The lid's camera stands at board level,
inside the game, so the pieces are architecture — two red pegs tower over
the frame while the linked black chain reads as a bridge going up — and the
player is the smallest thing in the picture: a suited man, faceless, chin
on fist, behind the chain being built across his chest, because the
position is bigger than anyone inside it. Over half the canvas is flat
empty violet, the silence around concentration, and the one bright thing in
it is the man's lit face and hand. The banner restates those devices at
1280x320 rather than cropping the lid into it: a monumental red peg at
each flank, taller than everything, one nearer and one farther; a chain of
black pegs racing the whole width of a board of drilled holes, feet
receding while the caps hold almost level, linked cap to cap by a taut
line; the watcher small behind the board with the chain crossing his
chest; the wordmark floating in the empty sky. The terminal carries it:
a cream bezel frames an ink screen whose top band is chrome — a
`$ twixtui` prompt with a block cursor, a status line — and the lid is
what the command printed, which is true to the program, since the cover
really is drawn beside the menu.

The terminal is drawn, not captured: nothing here claims to be a
screenshot. The prompt says what starting the program looks like, and the
cursor after it is the character `█` in the same run of text, so it sits
where the reader's own monospace font puts it instead of where some other
font's metrics were guessed to.

Two files rather than one because README.md selects between them with a
`<picture>` element and `prefers-color-scheme`. The composition is
identical in both; the dark file is one step duskier, its sky `homageSky`
where the light file's is `homageSkyHigh` and its bezel `homageCream`
where the light file's is the lid's own cream, so the picture sits quietly
on either of GitHub's backgrounds without pretending to be one of them.

The colours are internal/cover/homage.go's, hex for hex, in the roles the
program's own cover gives them wherever this picture has the same role:
`homageInk` for the screen, the chain's links and the `ui` of the
wordmark; `homageBlack` for the chain's pegs; `homageRed` for the flanking
pegs and `twixt`; `homageBoard` for the board and `homageHole` for its
holes; `homageCream` for the prompt and cursor; and `homageSkyHigh`,
doing double duty, as the light file's sky and as the dim chrome text of
both files. Three values have no name in the code, all measured from
assets/cover-source.png: the light file's bezel `#f5eed4` and the maroon
`#561d25` are the dominant colours of the source's border and of the
watcher's jacket — the maroon now paints that jacket, his hair and the
Randolph line — and the face and fist take `#e9b23d`, the dominant colour
of the man's lit face and of the board's lit wedge, the lid's one bright
accent; a fresh cluster pass over the source reproduces all three within
a couple of least-significant steps. An earlier revision copied the
banner's colours from web/style.css so page and banner could not drift
apart; that pin is deliberately gone — the owner directed that the front
page evoke the 1962 lid, and the site keeps its own red and blue — so the
banner drifts with the cover art instead, and anyone re-tuning homage.go's
palette should carry these two files along.

The wordmark still splits the way the project's site splits it — `twixt`
is the vertical player and `ui` the horizontal, the two goals that cut
across each other — but in the colours of the 1962 set, red pegs against
black, rather than the site's red and blue.

The board is honest where it claims and silent where it cannot. The holes
are a grid of 22 columns at a fixed pitch and three rows of equal board
spacing drawn foreshortened — the far gap smaller, the near holes larger —
and every chain peg's foot stands on a grid point, with consecutive feet a
knight's move apart in board units, checked for all eleven links, and the
cap-to-cap lines checked pairwise to cross nowhere. The chain is a side
view: the linked line joins the pegs' caps the way the physical set's
links ride near the peg tops, and ownership is carried by colour alone.
The two red flankers stand on the board but outside the grid and claim
nothing about the position, and the fragment is a crop — what the chain
does beyond the frame is not claimed. The watcher is drawn in the lid's
own colours rather than the ghost the character-cell homage reduces him
to, because at SVG resolution the lid's device — the small thinker behind
the monumental pieces — survives intact; his torso is cut by the board's
far edge exactly as on the lid. Anyone editing the pegs should keep the
foot geometry true or unclaim it, because a banner showing an impossible
position teaches the rule wrongly to everybody who never reads on.

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
