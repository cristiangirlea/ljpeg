// Command dngextract pulls the lossless JPEG tiles (or strips) out of the
// main raw image of a DNG file, and, for a Compression=1 DNG, the raw
// samples of the same tile, so the two files can be compared.
//
//	dngextract info in.dng
//	dngextract tile in.dng N out.jpg      (Compression=7: the N-th tile as a JPEG)
//	dngextract raw in.dng N out.pam       (Compression=1: the N-th tile's samples)
//
// Throwaway tool for checking the decoder against real files.
package main

import (
	"encoding/binary"
	"fmt"
	"os"
	"strconv"
)

type ifd struct {
	tags map[uint16][]uint32 // numeric tag values (first value for scalars)
	subs []uint32
}

var bo binary.ByteOrder

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "usage: dngextract info|tile|raw in.dng [N out]")
		os.Exit(2)
	}
	b, err := os.ReadFile(os.Args[2])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	switch string(b[:2]) {
	case "II":
		bo = binary.LittleEndian
	case "MM":
		bo = binary.BigEndian
	default:
		fmt.Fprintln(os.Stderr, "not a TIFF")
		os.Exit(1)
	}
	// Walk IFD0 and its SubIFDs; pick the one with NewSubFileType == 0 (the
	// main image).
	var all []*ifd
	var walk func(off uint32)
	walk = func(off uint32) {
		for off != 0 && int(off)+2 <= len(b) {
			d := readIFD(b, off)
			all = append(all, d)
			for _, s := range d.subs {
				walk(s)
			}
			n := int(bo.Uint16(b[off:]))
			off = bo.Uint32(b[int(off)+2+12*n:])
			if len(all) > 32 {
				break
			}
		}
	}
	walk(bo.Uint32(b[4:]))
	var main *ifd
	for _, d := range all {
		if v, ok := d.tags[254]; ok && v[0] == 0 {
			main = d
			break
		}
	}
	if main == nil {
		fmt.Fprintln(os.Stderr, "no main image IFD")
		os.Exit(1)
	}
	get := func(t uint16) uint32 {
		if v, ok := main.tags[t]; ok && len(v) > 0 {
			return v[0]
		}
		return 0
	}
	w, h, comp, spp, bps := get(256), get(257), get(259), get(277), get(258)
	tw, th := get(322), get(323)
	offs, counts := main.tags[324], main.tags[325]
	if tw == 0 { // strips
		tw, th = w, get(278)
		offs, counts = main.tags[273], main.tags[279]
	}
	switch os.Args[1] {
	case "info":
		fmt.Printf("image %dx%d compression=%d samples=%d bits=%d photometric=%d tile=%dx%d tiles=%d cfaPattern=%v dngVersion=%v\n",
			w, h, comp, spp, bps, get(262), tw, th, len(offs), main.tags[33422], all[0].tags[50706])
		if comp == 7 && len(offs) > 0 {
			j := b[offs[0] : offs[0]+counts[0]]
			fmt.Printf("tile0: %d bytes, SOF marker %02x", len(j), sofMarker(j))
			if sof := sofIndex(j); sof > 0 {
				fmt.Printf(", P=%d, %dx%d, Nf=%d", j[sof+4], int(j[sof+7])<<8|int(j[sof+8]), int(j[sof+5])<<8|int(j[sof+6]), j[sof+9])
				if sos := sosIndex(j); sos > 0 {
					ns := int(j[sos+4])
					fmt.Printf(", Ns=%d, Ss=%d, Pt=%d", ns, j[sos+5+2*ns], j[sos+7+2*ns]&0x0f)
				}
			}
			fmt.Println()
		}
	case "tile":
		n, _ := strconv.Atoi(os.Args[3])
		if comp != 7 {
			fmt.Fprintln(os.Stderr, "not lossless JPEG compressed")
			os.Exit(1)
		}
		os.WriteFile(os.Args[4], b[offs[n]:offs[n]+counts[n]], 0o644)
	case "raw":
		n, _ := strconv.Atoi(os.Args[3])
		if comp != 1 {
			fmt.Fprintln(os.Stderr, "not uncompressed")
			os.Exit(1)
		}
		data := b[offs[n] : offs[n]+counts[n]]
		f, _ := os.Create(os.Args[4])
		defer f.Close()
		fmt.Fprintf(f, "P7\nWIDTH %d\nHEIGHT %d\nDEPTH %d\nMAXVAL %d\nTUPLTYPE X\nENDHDR\n", tw, th, spp, (1<<bps)-1)
		if bps <= 8 {
			f.Write(data)
		} else {
			// Samples are stored in the file's byte order; write big-endian.
			for i := 0; i+1 < len(data); i += 2 {
				v := bo.Uint16(data[i:])
				f.Write([]byte{byte(v >> 8), byte(v)})
			}
		}
	}
}

func readIFD(b []byte, off uint32) *ifd {
	d := &ifd{tags: map[uint16][]uint32{}}
	n := int(bo.Uint16(b[off:]))
	for i := 0; i < n; i++ {
		e := int(off) + 2 + 12*i
		tag, typ, cnt := bo.Uint16(b[e:]), bo.Uint16(b[e+2:]), bo.Uint32(b[e+4:])
		size := map[uint16]uint32{1: 1, 2: 1, 3: 2, 4: 4, 6: 1, 7: 1, 8: 2, 9: 4, 13: 4}[typ]
		if size == 0 || cnt > 1<<20 {
			continue
		}
		vo := uint32(e + 8)
		if size*cnt > 4 {
			vo = bo.Uint32(b[e+8:])
		}
		vals := make([]uint32, 0, min(cnt, 4096))
		for j := uint32(0); j < cnt && j < 4096; j++ {
			switch size {
			case 1:
				vals = append(vals, uint32(b[vo+j]))
			case 2:
				vals = append(vals, uint32(bo.Uint16(b[vo+2*j:])))
			case 4:
				vals = append(vals, bo.Uint32(b[vo+4*j:]))
			}
		}
		d.tags[tag] = vals
		if tag == 330 {
			d.subs = vals
		}
	}
	return d
}

func sofMarker(j []byte) byte {
	if i := sofIndex(j); i > 0 {
		return j[i+1]
	}
	return 0
}

func sofIndex(j []byte) int {
	for i := 2; i+1 < len(j); {
		if j[i] != 0xff {
			return -1
		}
		m := j[i+1]
		if m >= 0xc0 && m <= 0xcf && m != 0xc4 && m != 0xc8 && m != 0xcc {
			return i
		}
		if m == 0xd8 || (m >= 0xd0 && m <= 0xd7) {
			i += 2
			continue
		}
		i += 2 + int(j[i+2])<<8 + int(j[i+3])
	}
	return -1
}

func sosIndex(j []byte) int {
	for i := 2; i+3 < len(j); {
		if j[i] != 0xff {
			return -1
		}
		m := j[i+1]
		if m == 0xda {
			return i
		}
		if m == 0xd8 || (m >= 0xd0 && m <= 0xd7) {
			i += 2
			continue
		}
		i += 2 + int(j[i+2])<<8 + int(j[i+3])
	}
	return -1
}
