# Tree images

One folder per phase. Each holds the prompt that generated the image, and the
image itself.

```
docs/trees/
  STYLE.txt              the shared style block, pasted at the top of every prompt
  phase-00/
    prompt.txt           paste this into Gemini
    tree.png             the image it returned
  phase-01/
  phase-02/
```

## Adding a phase

1. Claude writes `docs/trees/phase-NN/prompt.txt` in the same message as the
   code for that phase.
2. Paste the whole file into Gemini.
3. Save the image as `docs/trees/phase-NN/tree.png`.

Nothing gets overwritten — every phase keeps its own folder, so the images are
a visual history of how the engine grew.

## Why the prompt is checked in next to the image

An image cannot be diffed or verified. The prompt can: every arrow in it is
one line, and those lines are checked against

```
go list -f '{{.ImportPath}} -> {{join .Imports " "}}' ./... | grep sanke08
```

before the prompt is written. If a future image looks wrong, the prompt is
where to find out whether the drawing is wrong or the graph changed.

## If the image comes back wrong

Common failures and the fix:

| What Gemini did | Fix in the prompt |
|---|---|
| **Dropped 4 arrows into a shared box, and invented 2 others** | The real cause is LAYOUT, not the arrow list. It placed `value` at the top-right, where arrows from `parser`/`eval`/`ast` would have to travel upward and across — so it silently dropped them, then connected those boxes downward to `go standard library` instead, because that direction was easy. **Fix: put the most-depended-on box near the BOTTOM**, so every arrow into it flows downward. State "all arrows point DOWNWARD" and give the box its own row. |
| Invented arrows | A FORBIDDEN ARROWS section listing the specific wrong edges by name, plus "only the N numbered arrows above exist" |
| Wrong arrow count | End with "TOTAL ARROWS IN THE WHOLE IMAGE: N. Count the arrowheads. If the total is not N, it is wrong." |
| Drew `value` two or three times | Repeat "draw it ONE TIME ONLY" and name the box again in a separate sentence |
| Added colour | Repeat "STRICTLY two colours only" and list the banned colours again |
| Filled the boxes | Repeat "transparent fill, the dark background shows through" |
| Overlapped text and lines | Add "if a label would cross a line, move the box instead" |

### The check that catches all of this

Before saving an image, count the arrowheads touching the most-depended-on
box. In phase 2 that is `value`, and it needs **four**. If a shared box has
fewer arrows than the count table says, the image is wrong no matter how good
it looks — and it will look fine, because a missing arrow leaves no gap.

## What each package does

```
   cmd/kite      reads a line, prints an answer
   eval          walks a tree, computes one Value
   parser        tokens -> tree. Decides what binds to what
   ast           the tree SHAPE. Holds a Value at every leaf
   scanner       text -> tokens. What a token IS
   value         what a SQL value IS, and how two of them compare
   internal/slt  runs the .slt corpus and reports the number
   fs            Open, File — the disk seam
   storage.go    Table, Cursor — interfaces, no implementation yet
```

## What each package does NOT know

The downward-only rule is what makes these true, and it is the useful half.

```
   value     nothing about expressions, tokens, or tables
             -> a change to the tree shape cannot break arithmetic

   scanner   nothing about what tokens mean together
             -> it emits two '-' tokens for "2- -3" and refuses to guess
                which is unary. That is the parser's call.

   ast       nothing about how text became a tree
             -> the same tree can later be built by a binder, or printed
                by EXPLAIN, without touching this package

   parser    nothing about what anything evaluates to
             -> it decides SHAPE only. 1/0 parses fine; eval is where it
                becomes NULL.

   eval      nothing about where the text came from
             -> a REPL line and a corpus file are identical to it
```
