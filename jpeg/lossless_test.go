// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package jpeg

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"math/rand/v2"
	"os"
	"testing"
)

// samples returns the samples of a decoded lossless image, scaled back from
// the full 8-bit or 16-bit range to precision bits.
func samples(t *testing.T, m image.Image, nComp, precision int) [][]uint16 {
	t.Helper()
	b := m.Bounds()
	p := make([][]uint16, nComp)
	for c := range p {
		p[c] = make([]uint16, 0, b.Dx()*b.Dy())
	}
	maxSample := uint32(1)<<precision - 1
	from8 := func(v uint8) uint16 { return uint16((uint32(v)*maxSample + 0x7f) / 0xff) }
	from16 := func(v uint16) uint16 { return uint16((uint32(v)*maxSample + 0x7fff) / 0xffff) }
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			switch m := m.(type) {
			case *image.Gray:
				p[0] = append(p[0], from8(m.GrayAt(x, y).Y))
			case *image.Gray16:
				p[0] = append(p[0], from16(m.Gray16At(x, y).Y))
			case *image.RGBA:
				c := m.RGBAAt(x, y)
				p[0] = append(p[0], from8(c.R))
				p[1] = append(p[1], from8(c.G))
				p[2] = append(p[2], from8(c.B))
			case *image.RGBA64:
				c := m.RGBA64At(x, y)
				p[0] = append(p[0], from16(c.R))
				p[1] = append(p[1], from16(c.G))
				p[2] = append(p[2], from16(c.B))
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
							for _, restart := range []int{0, 1, 2} {
								for _, separate := range []bool{false, true} {
									if separate && nComp == 1 {
										continue
									}
									o := losslessOptions{precision, predictor, pt, restart, separate, nil}
									name := fmt.Sprintf("P%d/n%d/%s/%dx%d/psv%d/pt%d/rst%d/sep%v",
										precision, nComp, kind, w, h, predictor, pt, restart, separate)
									testRoundTrip(t, name, planes, w, h, o)
								}
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
	if got := category(0 - 32768); got != 16 {
		t.Fatalf("category(-32768) = %d, want 16", got)
	}
	// The first sample is predicted as 32768, so 0 is a difference of -32768,
	// which is coded as category 16 with no additional bits.
	planes := [][]uint16{{0, 65535, 0, 65535}}
	testRoundTrip(t, "category16", planes, 4, 1, losslessOptions{16, 1, 0, 0, false, nil})
}

func TestLosslessFullRange(t *testing.T) {
	// The maximum sample maps to white, as in image/png.
	for _, precision := range []int{2, 8, 12, 16} {
		maxv := uint16(1<<precision - 1)
		var buf bytes.Buffer
		if err := encodeLossless(&buf, [][]uint16{{0, maxv}}, 2, 1, losslessOptions{precision, 1, 0, 0, false, nil}); err != nil {
			t.Fatal(err)
		}
		m, err := Decode(bytes.NewReader(buf.Bytes()))
		if err != nil {
			t.Fatal(err)
		}
		if _, _, _, a := m.At(1, 0).RGBA(); a != 0xffff {
			t.Errorf("P%d: alpha = %#x, want 0xffff", precision, a)
		}
		if r, _, _, _ := m.At(1, 0).RGBA(); r != 0xffff {
			t.Errorf("P%d: white decoded as %#x, want 0xffff", precision, r)
		}
		if r, _, _, _ := m.At(0, 0).RGBA(); r != 0 {
			t.Errorf("P%d: black decoded as %#x, want 0", precision, r)
		}
	}
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
		if err := encodeLossless(&buf, planes, 5, 3, losslessOptions{tc.precision, 1, 0, 0, false, nil}); err != nil {
			t.Fatal(err)
		}
		cfg, err := DecodeConfig(bytes.NewReader(buf.Bytes()))
		if err != nil {
			t.Fatal(err)
		}
		if cfg.ColorModel != tc.want || cfg.Width != 5 || cfg.Height != 3 {
			t.Errorf("n%d P%d: got %v %dx%d, want %v 5x3", tc.nComp, tc.precision, cfg.ColorModel, cfg.Width, cfg.Height, tc.want)
		}
		m, err := Decode(bytes.NewReader(buf.Bytes()))
		if err != nil {
			t.Fatal(err)
		}
		if m.ColorModel() != cfg.ColorModel {
			t.Errorf("n%d P%d: Decode color model %v != DecodeConfig %v", tc.nComp, tc.precision, m.ColorModel(), cfg.ColorModel)
		}
	}
}

// TestLosslessPointTransformPerComponent checks that each component keeps
// its own point transform when coded in separate scans.
func TestLosslessPointTransformPerComponent(t *testing.T) {
	w, h := 6, 4
	planes := testPlanes("gradient", 3, w, h, 12)
	var buf bytes.Buffer
	if err := encodeLossless(&buf, planes[:1], w, h, losslessOptions{12, 1, 0, 0, false, nil}); err != nil {
		t.Fatal(err)
	}
	// Build a 3-component file by hand: one SOF, then the scans of three
	// single-component encodings with point transforms 0, 2 and 1.
	var out []byte
	for c, pt := range []uint8{0, 2, 1} {
		var b bytes.Buffer
		if err := encodeLossless(&b, planes[c:c+1], w, h, losslessOptions{12, 4, pt, 0, false, nil}); err != nil {
			t.Fatal(err)
		}
		data := b.Bytes()
		sof := bytes.Index(data, []byte{0xff, sof3Marker})
		dht := bytes.Index(data, []byte{0xff, dhtMarker})
		if c == 0 {
			out = append(out, data[:sof]...)
			out = append(out, 0xff, sof3Marker, 0, 8+3*3, 12, byte(h>>8), byte(h), byte(w>>8), byte(w), 3,
				1, 0x11, 0, 2, 0x11, 0, 3, 0x11, 0)
		}
		scan := data[dht : len(data)-2] // DHT + SOS + entropy data, without EOI.
		// Point the scan's component selector at component c+1.
		sos := bytes.Index(scan, []byte{0xff, sosMarker})
		scan = bytes.Clone(scan)
		scan[sos+5] = byte(c + 1)
		out = append(out, scan...)
	}
	out = append(out, 0xff, eoiMarker)

	m, err := Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	got := samples(t, m, 3, 12)
	for c, pt := range []uint8{0, 2, 1} {
		for i, v := range planes[c] {
			if want := v >> pt << pt; got[c][i] != want {
				t.Fatalf("component %d (pt %d) sample %d: got %d, want %d", c, pt, i, got[c][i], want)
			}
		}
	}
}

func TestLosslessErrors(t *testing.T) {
	w, h := 8, 8
	planes := testPlanes("noise", 1, w, h, 12)
	encode := func(o losslessOptions) []byte {
		var buf bytes.Buffer
		if err := encodeLossless(&buf, planes, w, h, o); err != nil {
			t.Fatal(err)
		}
		return buf.Bytes()
	}
	valid := encode(losslessOptions{12, 4, 0, 0, false, nil})
	sos := bytes.Index(valid, []byte{0xff, sosMarker})
	sof := bytes.Index(valid, []byte{0xff, sof3Marker})
	// The SOS header is FF DA, length (2 bytes), Ns, Cs/Td/Ta, Ss, Se, AhAl.
	ss, se, ahal := sos+7, sos+8, sos+9

	for _, tc := range []struct {
		name   string
		data   []byte
		mutate func(b []byte) []byte
		want   error // nil means any error
	}{
		{"predictor 0", valid, func(b []byte) []byte { b[ss] = 0; return b }, nil},
		{"predictor 8", valid, func(b []byte) []byte { b[ss] = 8; return b }, nil},
		{"Se not 0", valid, func(b []byte) []byte { b[se] = 1; return b }, nil},
		{"Ah not 0", valid, func(b []byte) []byte { b[ahal] = 0x10; return b }, nil},
		{"point transform too large", valid, func(b []byte) []byte { b[ahal] = 12; return b }, nil},
		{"zero width", valid, func(b []byte) []byte { b[sof+7], b[sof+8] = 0, 0; return b }, nil},
		{"zero height", valid, func(b []byte) []byte { b[sof+5], b[sof+6] = 0, 0; return b }, nil},
		{"precision 17", valid, func(b []byte) []byte { b[sof+4] = 17; return b }, nil},
		{"truncated", valid, func(b []byte) []byte { return b[:sos+20] }, nil},
		// A restart interval that is not a multiple of the row width
		// (section H.1.1).
		{"restart interval", encode(losslessOptions{12, 4, 0, 2, false, nil}), func(b []byte) []byte {
			dri := bytes.Index(b, []byte{0xff, driMarker})
			b[dri+4], b[dri+5] = 0, byte(w+1)
			return b
		}, FormatError("bad restart interval")},
		// Repeating the scan for a component already decoded.
		{"repeated scan", valid, func(b []byte) []byte {
			return append(append(bytes.Clone(b[:len(b)-2]), b[sos:len(b)-2]...), 0xff, eoiMarker)
		}, FormatError("repeated lossless scan")},
	} {
		_, err := Decode(bytes.NewReader(tc.mutate(bytes.Clone(tc.data))))
		if err == nil {
			t.Errorf("%s: got nil error", tc.name)
		} else if tc.want != nil && err != tc.want {
			t.Errorf("%s: got %v, want %v", tc.name, err, tc.want)
		}
	}
}

// TestLosslessComponents checks 2- and 4-component frames, which Decode
// cannot represent as an image.Image, but which must parse.
func TestLosslessComponents(t *testing.T) {
	for _, nComp := range []int{2, 4} {
		planes := testPlanes("gradient", nComp, 7, 5, 16)
		var buf bytes.Buffer
		if err := encodeLossless(&buf, planes, 7, 5, losslessOptions{16, 1, 0, 0, false, nil}); err != nil {
			t.Fatal(err)
		}
		// Decode and DecodeConfig agree: neither can represent the image.
		for name, f := range map[string]func() error{
			"Decode":       func() error { _, err := Decode(bytes.NewReader(buf.Bytes())); return err },
			"DecodeConfig": func() error { _, err := DecodeConfig(bytes.NewReader(buf.Bytes())); return err },
		} {
			if _, ok := f().(UnsupportedError); !ok {
				t.Errorf("n%d: %s: got %v, want UnsupportedError", nComp, name, f())
			}
		}
		// The raw API returns the samples.
		m, err := DecodeLossless(bytes.NewReader(buf.Bytes()))
		if err != nil {
			t.Fatalf("n%d: DecodeLossless: %v", nComp, err)
		}
		if m.Width != 7 || m.Height != 5 || m.Precision != 16 || len(m.Components) != nComp {
			t.Errorf("n%d: got %dx%d P%d with %d components", nComp, m.Width, m.Height, m.Precision, len(m.Components))
		}
		for c := range planes {
			if m.Components[c].ID != uint8(c+1) || !bytes.Equal(u16bytes(m.Components[c].Samples), u16bytes(planes[c])) {
				t.Errorf("n%d: component %d (ID %d) samples differ", nComp, c, m.Components[c].ID)
			}
		}
	}
}

// TestLosslessRawAPI round-trips images through the exported API, and checks
// that it reports the point transform, rejects what Decode rejects, and
// keeps samples within the precision on hostile input.
func TestLosslessRawAPI(t *testing.T) {
	for _, nComp := range []int{1, 2, 3, 4} {
		planes := testPlanes("gradient", nComp, 9, 4, 12)
		in := &Lossless{Width: 9, Height: 4, Precision: 12}
		for c, p := range planes {
			in.Components = append(in.Components, LosslessComponent{ID: uint8(10 + c), Samples: p})
		}
		var buf bytes.Buffer
		if err := EncodeLossless(&buf, in, &LosslessOptions{Predictor: 5, PointTransform: 2, RestartRows: 2, SeparateScans: nComp > 1}); err != nil {
			t.Fatal(err)
		}
		out, err := DecodeLossless(bytes.NewReader(buf.Bytes()))
		if err != nil {
			t.Fatal(err)
		}
		for c := range planes {
			oc := out.Components[c]
			if oc.ID != uint8(10+c) || oc.PointTransform != 2 {
				t.Errorf("n%d: component %d: ID %d pt %d, want ID %d pt 2", nComp, c, oc.ID, oc.PointTransform, 10+c)
			}
			for i, v := range planes[c] {
				if oc.Samples[i] != v>>2<<2 {
					t.Fatalf("n%d: component %d sample %d: got %d, want %d", nComp, c, i, oc.Samples[i], v>>2<<2)
				}
			}
		}
	}
	for _, tc := range []struct {
		name string
		o    *LosslessOptions
	}{{"predictor 8", &LosslessOptions{Predictor: 8}}, {"predictor 257", &LosslessOptions{Predictor: 257}}, {"pt 256", &LosslessOptions{PointTransform: 256}}} {
		in := &Lossless{Width: 2, Height: 2, Precision: 8, Components: []LosslessComponent{{Samples: make([]uint16, 4)}}}
		if err := EncodeLossless(io.Discard, in, tc.o); err == nil {
			t.Errorf("EncodeLossless %s: got nil error", tc.name)
		}
	}

	// A lossy file is not a lossless JPEG.
	var lossy bytes.Buffer
	Encode(&lossy, image.NewGray(image.Rect(0, 0, 8, 8)), nil)
	if _, err := DecodeLossless(bytes.NewReader(lossy.Bytes())); err == nil {
		t.Error("DecodeLossless(lossy): got nil error")
	}

	// A subsampled multi-component frame is unsupported, through every entry point.
	planes := testPlanes("gradient", 3, 4, 4, 8)
	var buf bytes.Buffer
	if err := encodeLossless(&buf, planes, 4, 4, losslessOptions{8, 1, 0, 0, false, nil}); err != nil {
		t.Fatal(err)
	}
	b := buf.Bytes()
	sof := bytes.Index(b, []byte{0xff, sof3Marker})
	b[sof+11] = 0x21 // First component H=2.
	for name, f := range map[string]func() error{
		"Decode":         func() error { _, err := Decode(bytes.NewReader(b)); return err },
		"DecodeConfig":   func() error { _, err := DecodeConfig(bytes.NewReader(b)); return err },
		"DecodeLossless": func() error { _, err := DecodeLossless(bytes.NewReader(b)); return err },
	} {
		if _, ok := f().(UnsupportedError); !ok {
			t.Errorf("subsampled: %s: got %v, want UnsupportedError", name, f())
		}
	}

	// A hostile difference can reconstruct a sample above the precision
	// modulo 2^16; the raw API must still report samples below 1<<P.
	// Category 16 adds 32768 to the predicted 128 of an 8-bit image.
	hostile := []byte{
		0xff, soiMarker,
		0xff, sof3Marker, 0, 8 + 3, 8, 0, 1, 0, 1, 1, 1, 0x11, 0,
		0xff, dhtMarker, 0, 2 + 1 + 16 + 1, 0, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 16,
		0xff, sosMarker, 0, 6 + 2, 1, 1, 0, 1, 0, 0,
		0x7f, // code 0 (category 16), then padding.
		0xff, eoiMarker,
	}
	m, err := DecodeLossless(bytes.NewReader(hostile))
	if err != nil {
		t.Fatal(err)
	}
	if v := m.Components[0].Samples[0]; v >= 1<<8 {
		t.Errorf("hostile sample = %d, want < 256", v)
	}
}

func u16bytes(p []uint16) []byte {
	b := make([]byte, 2*len(p))
	for i, v := range p {
		b[2*i], b[2*i+1] = byte(v>>8), byte(v)
	}
	return b
}

// TestLosslessHugeHeader checks that a tiny file declaring a huge image
// fails without allocating the whole image first.
func TestLosslessHugeHeader(t *testing.T) {
	data := []byte{
		0xff, soiMarker,
		0xff, sof3Marker, 0, 8 + 3*3, 16, 0xff, 0xff, 0xff, 0xff, 3, 1, 0x11, 0, 2, 0x11, 0, 3, 0x11, 0,
		0xff, dhtMarker, 0, 2 + 1 + 16 + 1, 0, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
		0xff, sosMarker, 0, 6 + 2*3, 3, 1, 0, 2, 0, 3, 0, 1, 0, 0,
	}
	if _, err := Decode(bytes.NewReader(data)); err == nil {
		t.Fatal("got nil error for a truncated 65535x65535 image")
	}
	allocs := testing.AllocsPerRun(1, func() {
		Decode(bytes.NewReader(data))
	})
	// Only the rows actually started should be allocated: the test passes if
	// this did not run out of memory, but also check nothing excessive.
	if allocs > 1000 {
		t.Errorf("too many allocations: %v", allocs)
	}
}

// TestDecodeLosslessFiles decodes the lossless files in testdata/lossless,
// which were generated from the upstream PNG images, and compares them with
// those images.
func TestDecodeLosslessFiles(t *testing.T) {
	for _, tc := range []struct {
		name, golden string
		pt           uint
	}{
		{"video-005.gray.psv1.jpeg", "video-005.gray.png", 0},
		{"video-005.gray.psv7.rst.jpeg", "video-005.gray.png", 0},
		{"video-001.rgb.psv4.jpeg", "video-001.png", 0},
		{"video-001.rgb.psv6.sep.jpeg", "video-001.png", 0},
		{"video-001.rgb.psv1.pt2.jpeg", "video-001.png", 2},
	} {
		f, err := os.Open("../testdata/lossless/" + tc.name)
		if err != nil {
			t.Fatal(err)
		}
		m, err := Decode(f)
		f.Close()
		if err != nil {
			t.Errorf("%s: %v", tc.name, err)
			continue
		}
		g, err := os.Open("../testdata/" + tc.golden)
		if err != nil {
			t.Fatal(err)
		}
		want, err := png.Decode(g)
		g.Close()
		if err != nil {
			t.Fatal(err)
		}
		if m.Bounds() != want.Bounds() {
			t.Errorf("%s: bounds %v, want %v", tc.name, m.Bounds(), want.Bounds())
			continue
		}
		b := want.Bounds()
		mask := uint32(0xff) >> tc.pt << tc.pt << 8
		for y := b.Min.Y; y < b.Max.Y; y++ {
			for x := b.Min.X; x < b.Max.X; x++ {
				r0, g0, b0, _ := m.At(x, y).RGBA()
				r1, g1, b1, _ := want.At(x, y).RGBA()
				if r0&mask != r1&mask || g0&mask != g1&mask || b0&mask != b1&mask {
					t.Errorf("%s: pixel (%d, %d): got %v, want %v", tc.name, x, y, m.At(x, y), want.At(x, y))
					break
				}
			}
		}
	}
}

func BenchmarkDecodeLossless(b *testing.B) {
	for _, tc := range []struct {
		name        string
		nComp, prec int
		predictor   uint8
	}{
		{"Gray8", 1, 8, 1},
		{"Gray12", 1, 12, 1},
		{"Gray16", 1, 16, 1},
		{"RGB8", 3, 8, 1},
		{"RGB16", 3, 16, 7},
	} {
		w, h := 640, 480
		planes := testPlanes("gradient", tc.nComp, w, h, tc.prec)
		var buf bytes.Buffer
		if err := encodeLossless(&buf, planes, w, h, losslessOptions{tc.prec, tc.predictor, 0, 0, false, nil}); err != nil {
			b.Fatal(err)
		}
		data := buf.Bytes()
		b.Run(tc.name, func(b *testing.B) {
			b.SetBytes(int64(w * h * tc.nComp))
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if _, err := Decode(bytes.NewReader(data)); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
