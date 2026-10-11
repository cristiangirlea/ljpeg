// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package jpeg

import (
	"image"
	"image/color"
	"math"
)

// A lossless image is decoded into one plane of samples per component. The
// samples are reconstructed as coded, before the point transform is undone.
type losslessPlane struct {
	pix     []uint16 // Row-major samples, grown one row at a time as decoded.
	pt      uint8    // Point transform, as per section H.1.2.1.
	decoded bool
}

// processLosslessSOS decodes a lossless scan, as specified in Annex H. Each
// sample is coded as the difference from a prediction made from its
// already-decoded neighbors: Ra to the left, Rb above and Rc above-left.
func (d *decoder) processLosslessSOS(scan []scanComponent, ss, se, ahal uint8) error {
	// Table B.3: Ss is the predictor, Se is 0, Ah is 0 and Al is the point
	// transform.
	if ss < 1 || 7 < ss {
		return FormatError("bad predictor selection value")
	}
	if se != 0 || ahal>>4 != 0 {
		return FormatError("bad lossless scan parameters")
	}
	pt := ahal & 0x0f
	if int(pt) >= d.precision {
		return FormatError("bad point transform")
	}
	w, h := d.width, d.height
	for _, sc := range scan {
		if d.lsPlanes[sc.compIndex].decoded {
			return FormatError("repeated lossless scan")
		}
	}
	// Section H.1.1: in lossless mode, the restart interval is a multiple of
	// the number of MCUs in a row. Each MCU here is one sample per component.
	if d.ri%w != 0 {
		return FormatError("bad restart interval")
	}
	for _, sc := range scan {
		p := &d.lsPlanes[sc.compIndex]
		p.pix, p.pt = make([]uint16, 0, w), pt
	}

	// The first sample of a scan or restart interval is predicted from
	// 1<<(P-Pt-1), the rest of that row from Ra, and the first sample of
	// every other row from Rb, as per section H.1.2.1.
	initial := int32(1) << (d.precision - int(pt) - 1)
	startRow := 0

	d.bits = bits{}
	expectedRST := uint8(rst0Marker)
	for y := 0; y < h; y++ {
		for _, sc := range scan {
			p := &d.lsPlanes[sc.compIndex]
			p.pix = append(p.pix, make([]uint16, w)...)
		}
		for x := 0; x < w; x++ {
			i := y*w + x
			for _, sc := range scan {
				pix := d.lsPlanes[sc.compIndex].pix
				var pred int32
				switch {
				case y == startRow && x == 0:
					pred = initial
				case y == startRow:
					pred = int32(pix[i-1])
				case x == 0:
					pred = int32(pix[i-w])
				default:
					pred = predict(ss, pix[i-1], pix[i-w], pix[i-w-1])
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
		}
		if d.ri > 0 && (y+1)*w%d.ri == 0 && y+1 < h {
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
			startRow = y + 1
		}
	}
	for _, sc := range scan {
		d.lsPlanes[sc.compIndex].decoded = true
	}
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

// losslessImage returns the decoded lossless samples as an image. Like
// image/png, samples are scaled to the full 8-bit or 16-bit range, so that
// the maximum sample value maps to white.
func (d *decoder) losslessImage() (image.Image, error) {
	if d.nComp != 1 && d.nComp != 3 {
		return nil, UnsupportedError("lossless image with this number of components")
	}
	for i := 0; i < d.nComp; i++ {
		if !d.lsPlanes[i].decoded {
			return nil, FormatError("missing SOS marker")
		}
	}
	w, h := d.width, d.height
	if uint64(w)*uint64(h)*8 > math.MaxInt {
		return nil, UnsupportedError("image too large")
	}
	r := image.Rect(0, 0, w, h)
	maxSample := uint32(1)<<d.precision - 1
	// sample returns component c of pixel i, scaled to the range 0..maxOut.
	sample := func(c, i int, maxOut uint32) uint16 {
		p := &d.lsPlanes[c]
		v := uint32(p.pix[i]) << p.pt & maxSample
		return uint16(v * maxOut / maxSample)
	}
	switch {
	case d.nComp == 1 && d.precision <= 8:
		m := image.NewGray(r)
		for i := range m.Pix {
			m.Pix[i] = uint8(sample(0, i, 0xff))
		}
		return m, nil
	case d.nComp == 1:
		m := image.NewGray16(r)
		for i := 0; i < w*h; i++ {
			v := sample(0, i, 0xffff)
			m.Pix[2*i], m.Pix[2*i+1] = uint8(v>>8), uint8(v)
		}
		return m, nil
	case d.precision <= 8:
		m := image.NewRGBA(r)
		for i := 0; i < w*h; i++ {
			m.Pix[4*i+0] = uint8(sample(0, i, 0xff))
			m.Pix[4*i+1] = uint8(sample(1, i, 0xff))
			m.Pix[4*i+2] = uint8(sample(2, i, 0xff))
			m.Pix[4*i+3] = 0xff
		}
		return m, nil
	}
	m := image.NewRGBA64(r)
	for i := 0; i < w*h; i++ {
		for c := 0; c < 3; c++ {
			v := sample(c, i, 0xffff)
			m.Pix[8*i+2*c], m.Pix[8*i+2*c+1] = uint8(v>>8), uint8(v)
		}
		m.Pix[8*i+6], m.Pix[8*i+7] = 0xff, 0xff
	}
	return m, nil
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
