// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package jpeg

import (
	"bufio"
	"errors"
	"io"
	mathbits "math/bits"
)

// losslessOptions are the parameters of encodeLossless.
type losslessOptions struct {
	precision int   // Sample precision, 2 to 16.
	predictor uint8 // Predictor selection value, 1 to 7.
	pt        uint8 // Point transform.
	restart   int   // Restart interval in rows, or 0 for none.
	// separateScans codes each component in its own scan, with its own
	// Huffman table, instead of one interleaved scan.
	separateScans bool
	// ids are the component identifiers; nil means 1, 2, 3, 4.
	ids []uint8
}

// encodeLossless writes a lossless (SOF3) JPEG image, as specified in
// Annex H. planes holds one slice of w*h samples per component, in
// row-major order.
func encodeLossless(out io.Writer, planes [][]uint16, w, h int, o losslessOptions) error {
	nComp := len(planes)
	switch {
	case nComp < 1 || nComp > 4:
		return errors.New("jpeg: lossless images must have 1 to 4 components")
	case w < 1 || h < 1 || w > 0xffff || h > 0xffff:
		return errors.New("jpeg: bad image dimensions")
	case o.precision < 2 || o.precision > 16:
		return errors.New("jpeg: bad precision")
	case o.predictor < 1 || o.predictor > 7:
		return errors.New("jpeg: bad predictor")
	case int(o.pt) >= o.precision:
		return errors.New("jpeg: bad point transform")
	case o.restart < 0 || o.restart > 0xffff/w:
		return errors.New("jpeg: bad restart interval")
	}
	ids := o.ids
	if ids == nil {
		ids = []uint8{1, 2, 3, 4}[:nComp]
	}
	for c := range ids {
		for j := 0; j < c; j++ {
			if ids[j] == ids[c] {
				return errors.New("jpeg: repeated component identifier")
			}
		}
	}
	shifted := make([][]uint16, nComp)
	for c, p := range planes {
		if len(p) != w*h {
			return errors.New("jpeg: plane has the wrong length")
		}
		shifted[c] = make([]uint16, w*h)
		for i, v := range p {
			if int(v)>>o.precision != 0 {
				return errors.New("jpeg: sample exceeds precision")
			}
			shifted[c][i] = v >> o.pt
		}
	}

	// Each scan codes a set of components, interleaved, with one Huffman
	// table (table B.3 uses the DC table selector).
	var scans [][]int
	if o.separateScans {
		for c := 0; c < nComp; c++ {
			scans = append(scans, []int{c})
		}
	} else {
		all := make([]int, nComp)
		for c := range all {
			all[c] = c
		}
		scans = [][]int{all}
	}

	bw := &bitWriter{w: bufio.NewWriter(out)}
	header := []byte{0xff, soiMarker}
	// SOF3: precision, height, width and each component, with no
	// subsampling and no quantization table.
	header = append(header, 0xff, sof3Marker, 0, byte(8+3*nComp), byte(o.precision),
		byte(h>>8), byte(h), byte(w>>8), byte(w), byte(nComp))
	for c := 0; c < nComp; c++ {
		header = append(header, ids[c], 0x11, 0)
	}
	if o.restart > 0 {
		// Section H.1.1: the interval is a whole number of MCU rows.
		ri := o.restart * w
		header = append(header, 0xff, driMarker, 0, 4, byte(ri>>8), byte(ri))
	}
	bw.write(header)

	for si, comps := range scans {
		// Compute the differences in scan order, using the same prediction
		// rules as the decoder, and count how often each category occurs.
		diffs := make([]int32, 0, len(comps)*w*h)
		var freq [17]int
		initial := int32(1) << (o.precision - int(o.pt) - 1)
		startRow := 0
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				i := y*w + x
				for _, c := range comps {
					pix := shifted[c]
					var pred int32
					switch {
					case y == startRow && x == 0:
						pred = initial
					case y == startRow:
						pred = int32(pix[i-1])
					case x == 0:
						pred = int32(pix[i-w])
					default:
						pred = predict(o.predictor, pix[i-1], pix[i-w], pix[i-w-1])
					}
					d := int32(int16(uint16(int32(pix[i]) - pred)))
					diffs = append(diffs, d)
					freq[category(d)]++
				}
			}
			if o.restart > 0 && (y+1)%o.restart == 0 {
				startRow = y + 1
			}
		}
		counts, vals := optimalHuffman(freq[:])
		var codes [17]uint16
		var lengths [17]uint8
		code, k := uint16(0), 0
		for l := 1; l <= 16; l++ {
			for j := 0; j < int(counts[l]); j++ {
				codes[vals[k]], lengths[vals[k]] = code, uint8(l)
				code++
				k++
			}
			code <<= 1
		}

		// DHT for this scan, as DC table si, then the SOS header: Ss is the
		// predictor, Se is 0 and Al is the point transform (table B.3).
		th := byte(si & 3)
		hdr := []byte{0xff, dhtMarker, 0, byte(2 + 1 + 16 + len(vals)), th}
		hdr = append(hdr, counts[1:17]...)
		hdr = append(hdr, vals...)
		hdr = append(hdr, 0xff, sosMarker, 0, byte(6+2*len(comps)), byte(len(comps)))
		for _, c := range comps {
			hdr = append(hdr, ids[c], th<<4)
		}
		hdr = append(hdr, o.predictor, 0, o.pt)
		bw.write(hdr)

		rst, perInterval := byte(0), o.restart*w*len(comps)
		for k, d := range diffs {
			if perInterval > 0 && k > 0 && k%perInterval == 0 {
				bw.flush()
				bw.write([]byte{0xff, rst0Marker + rst})
				rst = (rst + 1) & 7
			}
			t := category(d)
			bw.emit(uint32(codes[t]), uint(lengths[t]))
			if 0 < t && t < 16 {
				if d < 0 {
					d += 1<<t - 1
				}
				bw.emit(uint32(d), uint(t))
			}
		}
		bw.flush()
	}
	bw.write([]byte{0xff, eoiMarker})
	if bw.err != nil {
		return bw.err
	}
	return bw.w.Flush()
}

// category returns the difference category SSSS of d, as per table H.2.
// A difference of -32768 is the same as 32768 modulo 2^16, category 16.
func category(d int32) int {
	if d == -32768 {
		return 16
	}
	if d < 0 {
		d = -d
	}
	return mathbits.Len32(uint32(d))
}

// optimalHuffman returns the code length counts (indexed 1 to 16) and the
// symbols of a Huffman table for the given symbol frequencies, using the
// procedure of section K.2, which limits codes to 16 bits and never assigns
// the all-ones code.
func optimalHuffman(f []int) (counts [33]byte, vals []byte) {
	n := len(f)
	freq := make([]int, n+1)
	copy(freq, f)
	freq[n] = 1 // Reserved symbol, so that no code is all ones.
	codesize := make([]int, n+1)
	others := make([]int, n+1)
	for i := range others {
		others[i] = -1
	}
	for {
		c1, c2 := -1, -1
		for i, v := range freq {
			if v != 0 && (c1 < 0 || v <= freq[c1]) {
				c1 = i
			}
		}
		for i, v := range freq {
			if v != 0 && i != c1 && (c2 < 0 || v <= freq[c2]) {
				c2 = i
			}
		}
		if c2 < 0 {
			break
		}
		freq[c1] += freq[c2]
		freq[c2] = 0
		for codesize[c1]++; others[c1] >= 0; codesize[c1]++ {
			c1 = others[c1]
		}
		others[c1] = c2
		for codesize[c2]++; others[c2] >= 0; codesize[c2]++ {
			c2 = others[c2]
		}
	}
	var bitCounts [33]int
	for _, s := range codesize {
		if s > 0 {
			bitCounts[s]++
		}
	}
	// Limit code lengths to 16 bits, as per figure K.3.
	for i := 32; i > 16; i-- {
		for bitCounts[i] > 0 {
			j := i - 2
			for bitCounts[j] == 0 {
				j--
			}
			bitCounts[i] -= 2
			bitCounts[i-1]++
			bitCounts[j+1] += 2
			bitCounts[j]--
		}
	}
	// Remove the reserved symbol from the longest codes.
	i := 16
	for bitCounts[i] == 0 {
		i--
	}
	bitCounts[i]--
	for i := 1; i <= 16; i++ {
		counts[i] = byte(bitCounts[i])
	}
	for size := 1; size <= 32; size++ {
		for sym := 0; sym < n; sym++ {
			if codesize[sym] == size {
				vals = append(vals, byte(sym))
			}
		}
	}
	return counts, vals
}

// bitWriter writes bits MSB first, with byte stuffing (section F.1.2.3).
type bitWriter struct {
	w   *bufio.Writer
	acc uint32
	n   uint
	err error
}

func (b *bitWriter) write(p []byte) {
	if b.err == nil {
		_, b.err = b.w.Write(p)
	}
}

func (b *bitWriter) emit(v uint32, n uint) {
	b.acc = b.acc<<n | v&(1<<n-1)
	b.n += n
	for b.n >= 8 {
		c := byte(b.acc >> (b.n - 8))
		if b.err == nil {
			b.err = b.w.WriteByte(c)
		}
		if c == 0xff && b.err == nil {
			b.err = b.w.WriteByte(0)
		}
		b.n -= 8
	}
	b.acc &= 1<<b.n - 1
}

// flush pads the last byte with 1 bits.
func (b *bitWriter) flush() {
	if b.n > 0 {
		b.emit(1<<(8-b.n)-1, 8-b.n)
	}
}
