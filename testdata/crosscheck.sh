#!/bin/bash
# Cross-checks the Go lossless decoder and encoder against libjpeg-turbo.
# Run in the ljpeg container: cd /ws/ljpeg-mvp/testdata && bash crosscheck.sh
set -u
B=/opt/ljt/bin
T=../bin/ljpegtool
W=37
work=$(mktemp -d)

python3 - "$work" <<'PY'
import struct, random, sys
out = sys.argv[1]
random.seed(1)
W, H = 37, 23  # odd sizes on purpose
for p in (2, 6, 8, 12, 16):
    maxval = (1 << p) - 1
    for ch, magic, ext in ((1, "P5", "pgm"), (3, "P6", "ppm")):
        data = []
        for y in range(H):
            for x in range(W):
                for c in range(ch):
                    base = (x * 7 + y * 13 + c * 50) * maxval // 400
                    noise = random.randint(-maxval // 20, maxval // 20)
                    data.append(min(maxval, max(0, base + noise)))
        fmt = ">H" if maxval > 255 else "B"
        kind = "g" if ch == 1 else "c"
        with open("%s/src-%s%d.%s" % (out, kind, p, ext), "wb") as f:
            f.write(("%s\n%d %d\n%d\n" % (magic, W, H, maxval)).encode())
            f.write(b"".join(struct.pack(fmt, v) for v in data))
PY

passA=0 failA=0 passB=0 failB=0
for p in 2 6 8 12 16; do
  for kind in g c; do
    ext=pgm; [ $kind = c ] && ext=ppm
    src=$work/src-$kind$p.$ext
    for psv in 1 2 3 4 5 6 7; do
      for pt in 0 1; do
        for rows in 0 2; do
          id=$kind$p-psv$psv-pt$pt-rst$rows
          rflag=""; [ $rows -gt 0 ] && rflag="-restart $rows"
          # A: libjpeg-turbo encodes; Go must decode exactly like djpeg.
          $B/cjpeg -precision $p -lossless $psv,$pt $rflag $src > $work/a.jpg
          $B/djpeg -pnm $work/a.jpg > $work/a.ref
          if $T dec $work/a.jpg $work/a.got $p && cmp -s $work/a.ref $work/a.got; then
            passA=$((passA+1)); else failA=$((failA+1)); echo "A FAIL $id"; fi
          # B: Go encodes; djpeg must decode it exactly like Go does.
          $T enc $src $work/b.jpg $psv $pt $((rows*W))
          $B/djpeg -pnm $work/b.jpg > $work/b.ref 2>$work/b.err
          if $T dec $work/b.jpg $work/b.got $p && cmp -s $work/b.ref $work/b.got; then
            passB=$((passB+1)); else failB=$((failB+1)); echo "B FAIL $id: $(head -c 200 $work/b.err)"; fi
          # With no point transform, the round trip must be exact.
          if [ $pt = 0 ] && ! cmp -s $src $work/b.ref; then
            failB=$((failB+1)); echo "B FAIL $id: djpeg output differs from the source"; fi
        done
      done
    done
  done
done
echo "A (libjpeg-turbo encodes, Go decodes): $passA passed, $failA failed"
echo "B (Go encodes, libjpeg-turbo decodes): $passB passed, $failB failed"
rm -rf "$work"
