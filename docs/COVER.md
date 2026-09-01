# The cover, and how it is drawn

The 1962 box lid shows a man contemplating a board of tapered pegs joined by taut
links. `twixtui` draws it beside the menu. This records what ships, how it was
chosen, and what the choice costs, because the decision was made by looking at
frames and a reader deserves the reasoning rather than the conclusion alone.

## What ships

Two artworks, because neither wins at every size.

**The projection.** A flat reduction of the lid composition — the wordmark, the
figure, the flanking pegs, the linked chain over a field of holes — with the
photographic texture, the paper wear and the low-contrast dust taken out, those
being exactly what a character grid cannot carry. It ships as a 213×320
sixteen-colour PNG of about nine kilobytes, embedded in the binary and decoded on
first use rather than at startup. `assets/README.md` records what the picture is
and where its reference came from; `assets/cover-source.png` is the
full-resolution original it was reduced from, so the shipped file can be rebuilt:

    go run ./assets/gen -in assets/cover-source.png -out assets/cover.png -colors 16

**The drawing.** Hand-composed character art of the same composition, about five
hundred lines. It is not a fallback in the apologetic sense: it is the better
picture below the size at which the projection's wordmark stops reading, and it is
the only one of the two that means anything on a terminal with no colour, where a
dithered photograph is noise.

## Which one appears

`cover.Best` decides from the space available and the colour allowed:

| Box | With colour | Without colour |
| --- | --- | --- |
| 60×30 | drawing | drawing |
| 84×30 | drawing | drawing |
| 120×40 | projection | drawing |
| 200×60 | projection | drawing |

Monochrome always takes the drawing. With colour the projection answers once the
grid its picture occupies is at least 44 columns by 22 rows, which is where the
wordmark and the figure were still legible when the two were compared frame by
frame. Below that the drawing takes over.

The menu asks for the artwork only after taking the columns it needs, so at eighty
columns there is no room and there is simply no picture. A picture may not cost an
entry.

`TWIXTUI_COVER_ART` overrides the choice with `homage` or `photo`.
`TWIXTUI_COVER_IMAGE` replaces the shipped picture with a JPEG or PNG of your own.
Both are read once at startup, so a complaint about either arrives before anything
is drawn rather than over the top of it.

## The converters, and the two that were dropped

The picture is projected by sampling it into character cells. Four ways were
implemented and compared on the same sources at 40×20, 60×30, 80×40 and 120×60.

**Quadrant blocks** ship for colour. One cell carries four sub-cells through the
quadrant glyphs with a foreground and a background colour, which is the finest
grid available without asking the terminal for graphics.

**Braille** ships for monochrome. Eight dots a cell is the highest spatial
resolution any of these reach, at the cost of all colour.

**Half blocks** were dropped. Two sub-cells a cell against quadrant's four, and
quadrant beat it at every size on both sources.

**A luminance ramp** of ASCII characters was dropped. Braille beat it everywhere,
and the terminals too old for braille are older than the ones this program
otherwise assumes.

Sextants were rejected without implementing them: font coverage is too patchy to
rely on.

Aspect ratio is the trap in all of this. A terminal cell is roughly twice as tall
as it is wide, and a converter that ignores that produces a picture melted to half
its height. The projection corrects for it and a test asserts a circle comes out
round to within a fifth.

## The 256-colour palette

`internal/cover` can also render to the 256-colour palette, snapping each colour
to the nearest entry of the xterm cube or its grey ramp. The method is arithmetic
— the cube's ramp is uneven, so the midpoints between its levels are precomputed
and each channel is placed by five comparisons, then the result is weighed against
the nearest grey — and a test holds that shortcut to the answer an exhaustive
search over all 240 entries gives, so the colour chosen is the nearest one and not
merely a close one. The error that remains is the geometry of xterm's palette
rather than a property of the search: mean 28.1 and worst 74.7 over a lattice of
sample colours, which shows as a slight wash rather than as lost structure.

**The program does not currently select this path.** It resolves colour once, into
"colour" or "no colour", and has no terminal-capability detection: a terminal that
cannot manage 24-bit is sent 24-bit escapes and degrades them itself, which most
do tolerably. Wiring the palette in needs capability detection the program does
not have, which is a change worth making deliberately rather than as a footnote to
the artwork. Until then this is a facility of the package rather than a behaviour
of the program, and saying otherwise would be describing a path nothing takes.

## What the drawing looks like with no colour at all

Rendered at 80×24, monochrome, which is the state `--no-color` and `NO_COLOR`
produce:

```
             ██████████  ██          ██  ██  ██▄    ▄██  ██████████
             ▀▀▀▀██▀▀▀▀  ██    ▄▄    ██        ██  ██    ▀▀▀▀██▀▀▀▀
                 ██      ██    ██    ██  ██      ██          ██
                 ██      ██  ██  ██  ██  ██    ██  ██        ██
               ▄▄██▄▄     ▀██▀    ▀██▀   ██  ██▀    ▀██    ▄▄██▄▄

                           A GAME OF BARRIERS FOR TWO

                 ▓▓▓▓▓──╲              ▓▓▓───╲             ▓▓▓
                  ▓▓▓    ───▓▓▓─────────▓     ───▓▓▓────────▓
                   ▓         ▓          ▓         ▓         ▓
 █████──────███    ▓         ▓          ▓         ▓         ▓             █████
 ▝███▘      ▝█▘    ▓         ▓          ▓         ▓         ▓             ▝███▘
```

The two tall pegs flank; the shorter ones stand in a row joined by links; the
board's holes are the field beneath. Nothing here emits an escape byte, which a
test asserts by scanning for control characters — including tabs, whose width a
terminal decides for itself.

## Licensing

The original lid artwork is © 1962 Minnesota Mining and Manufacturing Company.
What ships here is the project's own reduction of that composition, and the owner
of this repository decided to ship it on the basis that projecting a picture into
character cells is itself the alteration. `assets/README.md` records the reference
used and states that it is not redistributed.
