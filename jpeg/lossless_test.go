// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package jpeg

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"math/rand/v2"
	"testing"
)

// samples returns the samples of a decoded lossless image, scaled back to
// precision bits.
func samples(t *testing.T, m image.Image, nComp, precision int) [][]uint16 {
	t.Helper()
	b := m.Bounds()
	p := make([][]uint16, nComp)
	for c := range p {
		p[c] = make([]uint16, 0, b.Dx()*b.Dy())
	}
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			switch m := m.(type) {
			case *image.Gray:
				p[0] = append(p[0], uint16(m.GrayAt(x, y).Y)>>(8-precision))
			case *image.Gray16:
				p[0] = append(p[0], m.Gray16At(x, y).Y>>(16-precision))
			case *image.RGBA:
				c := m.RGBAAt(x, y)
				p[0] = append(p[0], uint16(c.R)>>(8-precision))
				p[1] = append(p[1], uint16(c.G)>>(8-precision))
				p[2] = append(p[2], uint16(c.B)>>(8-precision))
			case *image.RGBA64:
				c := m.RGBA64At(x, y)
				p[0] = append(p[0], c.R>>(16-precision))
				p[1] = append(p[1], c.G>>(16-precision))
				p[2] = append(p[2], c.B>>(16-precision))
			default:
				t.Fatalf("unexpected image type %T", m)
			}
		}
	}
	return p
}

// testPlanes returns nComp planes of w*h samples of the given kind.
func testPlanes(kind string, nComp, w, h, precision int) [][]uint16 {
	r := rand.New(rand.NewPCG(uint64(w), uint64(h*precision)))
	maxv := 1<<precision - 1
	p := make([][]uint16, nComp)
	for c := range p {
		p[c] = make([]uint16, w*h)
		for i := range p[c] {
			x, y := i%w, i/w
			var v int
			switch kind {
			case "noise":
				v = r.IntN(maxv + 1)
			case "gradient":
				v = (x*7 + y*13 + c*50) * maxv / 400
				v += r.IntN(maxv/20+1) - maxv/40
			case "extremes":
				// Alternating 0 and max gives the largest differences, and at
				// 16 bits, a difference of 32768 (category 16).
				if (x+y+c)%2 == 1 {
					v = maxv
				}
			case "constant":
				v = maxv / 3
			}
			p[c][i] = uint16(min(maxv, max(0, v)))
		}
	}
	return p
}

func TestLosslessRoundTrip(t *testing.T) {
	sizes := [][2]int{{1, 1}, {1, 9}, {9, 1}, {37, 23}}
	for _, precision := range []int{2, 5, 8, 9, 12, 16} {
		for _, nComp := range []int{1, 3} {
			for _, kind := range []string{"noise", "gradient", "extremes", "constant"} {
				for _, sz := range sizes {
					w, h := sz[0], sz[1]
					planes := testPlanes(kind, nComp, w, h, precision)
					for predictor := uint8(1); predictor <= 7; predictor++ {
						for _, pt := range []uint8{0, 1} {
							for _, restart := range []int{0, w, 2 * w} {
								o := losslessOptions{precision, predictor, pt, restart}
								name := fmt.Sprintf("P%d/n%d/%s/%dx%d/psv%d/pt%d/rst%d",
									precision, nComp, kind, w, h, predictor, pt, restart)
								testRoundTrip(t, name, planes, w, h, o)
							}
						}
					}
				}
			}
		}
	}
}

func testRoundTrip(t *testing.T, name string, planes [][]uint16, w, h int, o losslessOptions) {
	t.Helper()
	var buf bytes.Buffer
	if err := encodeLossless(&buf, planes, w, h, o); err != nil {
		t.Fatalf("%s: encode: %v", name, err)
	}
	m, err := Decode(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("%s: decode: %v", name, err)
	}
	got := samples(t, m, len(planes), o.precision)
	for c := range planes {
		for i, v := range planes[c] {
			if want := v >> o.pt << o.pt; got[c][i] != want {
				t.Fatalf("%s: component %d sample %d: got %d, want %d", name, c, i, got[c][i], want)
			}
		}
	}
}

func TestLosslessCategory16(t *testing.T) {
	// The first sample is predicted as 32768, so 0 is a difference of -32768,
	// which is coded as category 16 with no additional bits.
	planes := [][]uint16{{0, 65535, 0, 65535}}
	var buf bytes.Buffer
	if err := encodeLossless(&buf, planes, 4, 1, losslessOptions{16, 1, 0, 0}); err != nil {
		t.Fatal(err)
	}
	if got := category(0 - 32768); got != 16 {
		t.Fatalf("category(-32768) = %d, want 16", got)
	}
	testRoundTrip(t, "category16", planes, 4, 1, losslessOptions{16, 1, 0, 0})
}

func TestLosslessDecodeConfig(t *testing.T) {
	for _, tc := range []struct {
		nComp, precision int
		want             color.Model
	}{
		{1, 8, color.GrayModel},
		{1, 12, color.Gray16Model},
		{3, 6, color.RGBAModel},
		{3, 16, color.RGBA64Model},
	} {
		var buf bytes.Buffer
		planes := testPlanes("gradient", tc.nComp, 5, 3, tc.precision)
		if err := encodeLossless(&buf, planes, 5, 3, losslessOptions{tc.precision, 1, 0, 0}); err != nil {
			t.Fatal(err)
		}
		cfg, err := DecodeConfig(bytes.NewReader(buf.Bytes()))
		if err != nil {
			t.Fatal(err)
		}
		if cfg.ColorModel != tc.want || cfg.Width != 5 || cfg.Height != 3 {
			t.Errorf("n%d P%d: got %v %dx%d, want %v 5x3", tc.nComp, tc.precision, cfg.ColorModel, cfg.Width, cfg.Height, tc.want)
		}
	}
}

func TestLosslessErrors(t *testing.T) {
	var buf bytes.Buffer
	planes := testPlanes("noise", 1, 8, 8, 12)
	if err := encodeLossless(&buf, planes, 8, 8, losslessOptions{12, 4, 0, 0}); err != nil {
		t.Fatal(err)
	}
	valid := buf.Bytes()
	sos := bytes.Index(valid, []byte{0xff, sosMarker})
	// The SOS header is FF DA, length (2 bytes), Ns, Cs/Td/Ta, Ss, Se, AhAl.
	ss, al := sos+7, sos+9

	for _, tc := range []struct {
		name   string
		mutate func(b []byte) []byte
	}{
		{"predictor 0", func(b []byte) []byte { b[ss] = 0; return b }},
		{"predictor 8", func(b []byte) []byte { b[ss] = 8; return b }},
		{"point transform", func(b []byte) []byte { b[al] = 12; return b }},
		{"truncated", func(b []byte) []byte { return b[:sos+20] }},
	} {
		b := tc.mutate(bytes.Clone(valid))
		if _, err := Decode(bytes.NewReader(b)); err == nil {
			t.Errorf("%s: got nil error", tc.name)
		}
	}
}
