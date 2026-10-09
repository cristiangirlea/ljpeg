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
	restart   int   // Restart interval in MCUs, or 0 for none.
}

// encodeLossless writes a lossless (SOF3) JPEG image, as specified in
// Annex H. planes holds one slice of w*h samples per component, in
// row-major order. Components are interleaved in a single scan.
func encodeLossless(out io.Writer, planes [][]uint16, w, h int, o losslessOptions) error {
	nComp := len(planes)
	switch {
	case nComp != 1 && nComp != 3:
		return errors.New("jpeg: lossless images must have 1 or 3 components")
	case w < 1 || h < 1 || w > 0xffff || h > 0xffff:
		return errors.New("jpeg: bad image dimensions")
	case o.precision < 2 || o.precision > 16:
		return errors.New("jpeg: bad precision")
	case o.predictor < 1 || o.predictor > 7:
		return errors.New("jpeg: bad predictor")
	case int(o.pt) >= o.precision:
		return errors.New("jpeg: bad point transform")
	case o.restart < 0 || o.restart > 0xffff:
		return errors.New("jpeg: bad restart interval")
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

	// Compute the differences in scan order, using the same prediction rules
	// as the decoder, and count how often each category occurs.
	diffs := make([]int32, 0, nComp*w*h)
	var freq [17]int
	initial := int32(1) << (o.precision - int(o.pt) - 1)
	start, startRow, mcu := 0, 0, 0
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			i := y*w + x
			for c := 0; c < nComp; c++ {
				pix := shifted[c]
				var pred int32
				switch {
				case i == start:
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
			mcu++
			if o.restart > 0 && mcu%o.restart == 0 && mcu < w*h {
				start, startRow = mcu, mcu/w
			}
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

	bw := &bitWriter{w: bufio.NewWriter(out)}
	header := []byte{0xff, soiMarker}
	// SOF3: precision, height, width and each component, with no
	// subsampling and no quantization table.
	header = append(header, 0xff, sof3Marker, 0, byte(8+3*nComp), byte(o.precision),
		byte(h>>8), byte(h), byte(w>>8), byte(w), byte(nComp))
	for c := 0; c < nComp; c++ {
		header = append(header, byte(c+1), 0x11, 0)
	}
	// DHT: one table, used for every component.
	header = append(header, 0xff, dhtMarker, 0, byte(2+1+16+len(vals)), 0x00)
	header = append(header, counts[1:17]...)
	header = append(header, vals...)
	if o.restart > 0 {
		header = append(header, 0xff, driMarker, 0, 4, byte(o.restart>>8), byte(o.restart))
	}
	// SOS: Ss is the predictor and Al is the point transform (table H.2).
	header = append(header, 0xff, sosMarker, 0, byte(6+2*nComp), byte(nComp))
	for c := 0; c < nComp; c++ {
		header = append(header, byte(c+1), 0x00)
	}
	header = append(header, o.predictor, 0, o.pt)
	bw.write(header)

	rst := byte(0)
	for k, d := range diffs {
		if o.restart > 0 && k > 0 && k%(o.restart*nComp) == 0 {
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
