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
Regenerate it from a full-resolution source with:

    go run ./assets/gen -in /path/to/poster.png -out assets/cover.png -colors 16

The generator reduces by area-averaging in linear light — the same filtering
the renderer in internal/cover applies — and then snaps the result back onto
its own dominant colours, so flat art stays flat instead of carrying
thousands of one-off edge blends.
