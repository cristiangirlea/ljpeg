// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package jpeg_test

import (
	"bytes"
	"fmt"
	"image"
	"log"

	"github.com/cristiangirlea/ljpeg/jpeg"
)

// A lossless JPEG decodes like any other JPEG. Images with more than 8 bits
// per sample come back as *image.Gray16 or *image.RGBA64, scaled to the
// full range, so that an existing program sees them as ordinary images.
func ExampleDecode_lossless() {
	data := encodeSample(12, 1) // A 4x2 12-bit lossless JPEG with one component.

	m, err := jpeg.Decode(bytes.NewReader(data))
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("%T %v\n", m, m.Bounds())
	fmt.Println(m.(*image.Gray16).Gray16At(1, 0).Y)
	// Output:
	// *image.Gray16 (0,0)-(4,2)
	// 65535
}

// A DICOM or DNG reader needs the samples as coded, at the image's own
// precision and with any number of components. DecodeLossless returns them
// without scaling or color conversion.
func ExampleDecodeLossless() {
	data := encodeSample(12, 2) // A 4x2 12-bit lossless JPEG with two components.

	m, err := jpeg.DecodeLossless(bytes.NewReader(data))
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("%dx%d, %d bits, %d components\n", m.Width, m.Height, m.Precision, len(m.Components))
	for _, c := range m.Components {
		fmt.Printf("component %d: %v\n", c.ID, c.Samples)
	}
	// A DICOM reader with PixelRepresentation 1 sign-extends the samples
	// from the image's precision; a DNG reader reshapes them by the tile
	// width.
	shift := 16 - m.Precision
	signed := int16(m.Components[0].Samples[1]<<shift) >> shift
	fmt.Println(signed)
	// Output:
	// 4x2, 12 bits, 2 components
	// component 1: [0 4095 1000 2000 3000 4000 100 200]
	// component 2: [10 20 30 40 50 60 70 80]
	// -1
}

// EncodeLossless writes samples of any precision from 2 to 16 bits, with
// 1 to 4 components. The default predictor, 1, is the one every decoder
// supports; the others can give smaller files.
func ExampleEncodeLossless() {
	m := &jpeg.Lossless{
		Width:     4,
		Height:    2,
		Precision: 16,
		Components: []jpeg.LosslessComponent{
			{ID: 1, Samples: []uint16{0, 65535, 1000, 2000, 3000, 4000, 100, 200}},
		},
	}
	var buf bytes.Buffer
	if err := jpeg.EncodeLossless(&buf, m, &jpeg.LosslessOptions{Predictor: 4}); err != nil {
		log.Fatal(err)
	}

	back, err := jpeg.DecodeLossless(&buf)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(back.Components[0].Samples)
	// Output:
	// [0 65535 1000 2000 3000 4000 100 200]
}

// encodeSample returns a 4x2 lossless JPEG with the given precision and
// number of components, for the examples above.
func encodeSample(precision, nComp int) []byte {
	m := &jpeg.Lossless{Width: 4, Height: 2, Precision: precision}
	maxv := uint16(1<<precision - 1)
	planes := [][]uint16{
		{0, maxv, 1000, 2000, 3000, 4000, 100, 200},
		{10, 20, 30, 40, 50, 60, 70, 80},
	}
	for c := 0; c < nComp; c++ {
		m.Components = append(m.Components, jpeg.LosslessComponent{ID: uint8(c + 1), Samples: planes[c]})
	}
	var buf bytes.Buffer
	if err := jpeg.EncodeLossless(&buf, m, nil); err != nil {
		log.Fatal(err)
	}
	return buf.Bytes()
}
