// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package jpeg

import (
	"image"
	"image/color"
)

// processLosslessSOS decodes a lossless scan, as specified in Annex H. Each
// sample is coded as the difference from a prediction made from its
// already-decoded neighbors: Ra to the left, Rb above and Rc above-left.
func (d *decoder) processLosslessSOS(scan []scanComponent, ps, pt uint8) error {
	if ps < 1 || 7 < ps {
		return FormatError("bad predictor selection value")
	}
	if int(pt) >= d.precision {
		return FormatError("bad point transform")
	}
	if len(scan) > 1 {
		// Interleaved scans are only supported without subsampling, so that
		// each MCU is one sample of each component.
		for _, sc := range scan {
			if c := d.comp[sc.compIndex]; c.h != 1 || c.v != 1 {
				return UnsupportedError("subsampled lossless image")
			}
		}
	}
	w, h := d.width, d.height
	if w <= 0 || h <= 0 {
		return FormatError("bad image dimensions")
	}
	for _, sc := range scan {
		if d.lsPix[sc.compIndex] == nil {
			d.lsPix[sc.compIndex] = make([]uint16, w*h)
		}
	}

	// The first sample of a scan or restart interval is predicted from
	// 1<<(P-Pt-1), the rest of that line from Ra, and the first sample of
	// every other line from Rb, as per section H.1.2.1.
	initial := int32(1) << (d.precision - int(pt) - 1)
	start, startRow := 0, 0

	d.bits = bits{}
	mcu, expectedRST := 0, uint8(rst0Marker)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			i := y*w + x
			for _, sc := range scan {
				pix := d.lsPix[sc.compIndex]
				var pred int32
				switch {
				case i == start:
					pred = initial
				case y == startRow:
					pred = int32(pix[i-1])
				case x == 0:
					pred = int32(pix[i-w])
				default:
					pred = predict(ps, pix[i-1], pix[i-w], pix[i-w-1])
				}

				t, err := d.decodeHuffman(&d.huff[dcTable][sc.td])
				if err != nil {
					return err
				}
				var diff int32
				switch {
				case t == 0:
				case t < 16:
					if diff, err = d.receiveExtend(t); err != nil {
						return err
					}
				case t == 16:
					// Category 16 has no additional bits (table H.2).
					diff = 32768
				default:
					return FormatError("bad lossless difference category")
				}
				// Samples are reconstructed modulo 2^16 (section H.1.2.1).
				pix[i] = uint16(pred + diff)
			}

			mcu++
			if d.ri > 0 && mcu%d.ri == 0 && mcu < w*h {
				if err := d.readFull(d.tmp[:2]); err != nil {
					return err
				} else if d.tmp[0] != 0xff || d.tmp[1] != expectedRST {
					if err := d.findRST(expectedRST); err != nil {
						return err
					}
				}
				expectedRST++
				if expectedRST == rst7Marker+1 {
					expectedRST = rst0Marker
				}
				d.bits = bits{}
				start, startRow = mcu, mcu/w
			}
		}
	}
	d.lsShift = pt
	return nil
}

// predict returns the prediction for predictor ps from the neighbors Ra
// (left), Rb (above) and Rc (above left), as per table H.1.
func predict(ps uint8, a, b, c uint16) int32 {
	ra, rb, rc := int32(a), int32(b), int32(c)
	switch ps {
	case 1:
		return ra
	case 2:
		return rb
	case 3:
		return rc
	case 4:
		return ra + rb - rc
	case 5:
		return ra + (rb-rc)>>1
	case 6:
		return rb + (ra-rc)>>1
	}
	return (ra + rb) >> 1
}

// losslessImage returns the decoded lossless samples as an image. Samples
// with more than 8 bits are scaled to 16 bits; others are scaled to 8 bits.
func (d *decoder) losslessImage() (image.Image, error) {
	for i := 0; i < d.nComp; i++ {
		if d.lsPix[i] == nil {
			return nil, FormatError("missing SOS marker")
		}
	}
	w, h := d.width, d.height
	r := image.Rect(0, 0, w, h)
	// sample returns component c of pixel i, shifted to bits significant bits.
	sample := func(c, i, bits int) uint16 {
		v := uint32(d.lsPix[c][i]) << d.lsShift & (1<<d.precision - 1)
		if bits >= d.precision {
			return uint16(v << (bits - d.precision))
		}
		return uint16(v >> (d.precision - bits))
	}
	switch {
	case d.nComp == 1 && d.precision <= 8:
		m := image.NewGray(r)
		for i := range m.Pix {
			m.Pix[i] = uint8(sample(0, i, 8))
		}
		return m, nil
	case d.nComp == 1:
		m := image.NewGray16(r)
		for i := 0; i < w*h; i++ {
			v := sample(0, i, 16)
			m.Pix[2*i], m.Pix[2*i+1] = uint8(v>>8), uint8(v)
		}
		return m, nil
	case d.nComp == 3 && d.precision <= 8:
		m := image.NewRGBA(r)
		for i := 0; i < w*h; i++ {
			m.Pix[4*i+0] = uint8(sample(0, i, 8))
			m.Pix[4*i+1] = uint8(sample(1, i, 8))
			m.Pix[4*i+2] = uint8(sample(2, i, 8))
			m.Pix[4*i+3] = 0xff
		}
		return m, nil
	case d.nComp == 3:
		m := image.NewRGBA64(r)
		for i := 0; i < w*h; i++ {
			for c := 0; c < 3; c++ {
				v := sample(c, i, 16)
				m.Pix[8*i+2*c], m.Pix[8*i+2*c+1] = uint8(v>>8), uint8(v)
			}
			m.Pix[8*i+6], m.Pix[8*i+7] = 0xff, 0xff
		}
		return m, nil
	}
	return nil, UnsupportedError("lossless image with this number of components")
}

// losslessColorModel returns the color model of the image returned by
// losslessImage.
func (d *decoder) losslessColorModel() color.Model {
	switch {
	case d.nComp == 1 && d.precision <= 8:
		return color.GrayModel
	case d.nComp == 1:
		return color.Gray16Model
	case d.precision <= 8:
		return color.RGBAModel
	}
	return color.RGBA64Model
}
