// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package jpeg

import (
	"errors"
	"io"
)

// Lossless holds the samples of a lossless JPEG image as coded, without
// scaling or color conversion. It is what DICOM and DNG readers need: the
// frame's own geometry and precision, with 1 to 4 components.
//
// A DNG tile is often coded as a frame whose width and component count
// differ from the tile's; the samples are the same, read in row-major
// order. A DICOM reader reinterprets the samples as signed when the data
// set says so.
type Lossless struct {
	Width, Height int
	// Precision is the number of bits per sample, 2 to 16. Samples are in
	// the range [0, 1<<Precision).
	Precision int
	// Components holds one plane per component, each of Width*Height
	// samples in row-major order.
	Components []LosslessComponent
}

// LosslessComponent is one component of a Lossless image.
type LosslessComponent struct {
	ID uint8 // The component identifier from the frame header.
	// PointTransform is the number of low bits the encoder discarded; the
	// samples have been shifted back up by this amount.
	PointTransform uint8
	Samples        []uint16
}

// DecodeLossless reads a lossless JPEG image from r and returns its
// samples. Unlike Decode, it supports 2- and 4-component images and
// returns the samples unscaled.
func DecodeLossless(r io.Reader) (*Lossless, error) {
	d := decoder{raw: true}
	if _, err := d.decode(r, false); err != nil {
		return nil, err
	}
	if !d.lossless {
		return nil, UnsupportedError("not a lossless JPEG")
	}
	m := &Lossless{Width: d.width, Height: d.height, Precision: d.precision}
	mask := uint16(1)<<d.precision - 1
	for i := 0; i < d.nComp; i++ {
		p := &d.lsPlanes[i]
		if !p.decoded {
			return nil, FormatError("missing SOS marker")
		}
		for j, v := range p.pix {
			p.pix[j] = v << p.pt & mask
		}
		m.Components = append(m.Components, LosslessComponent{d.comp[i].c, p.pt, p.pix})
	}
	return m, nil
}

// LosslessOptions are the encoding parameters for EncodeLossless.
type LosslessOptions struct {
	// Predictor is the predictor selection value, 1 to 7 (table H.1).
	// Zero means 1, which every decoder supports.
	Predictor int
	// PointTransform discards this many low bits of each sample before
	// coding. Zero keeps the image lossless.
	PointTransform int
	// RestartRows is the restart interval in rows, or zero for none.
	RestartRows int
	// SeparateScans codes each component in its own scan with its own
	// Huffman table, instead of one interleaved scan.
	SeparateScans bool
}

// EncodeLossless writes m to w as a lossless JPEG image.
func EncodeLossless(w io.Writer, m *Lossless, o *LosslessOptions) error {
	if m == nil || len(m.Components) == 0 {
		return errors.New("jpeg: lossless image has no components")
	}
	var opts LosslessOptions
	if o != nil {
		opts = *o
	}
	if opts.Predictor == 0 {
		opts.Predictor = 1
	}
	if opts.Predictor < 1 || opts.Predictor > 7 {
		return errors.New("jpeg: bad predictor")
	}
	if opts.PointTransform < 0 || opts.PointTransform >= m.Precision {
		return errors.New("jpeg: bad point transform")
	}
	planes := make([][]uint16, len(m.Components))
	ids := make([]uint8, len(m.Components))
	for i, c := range m.Components {
		planes[i], ids[i] = c.Samples, c.ID
	}
	return encodeLossless(w, planes, m.Width, m.Height, losslessOptions{
		m.Precision, uint8(opts.Predictor), uint8(opts.PointTransform), opts.RestartRows, opts.SeparateScans, ids,
	})
}
