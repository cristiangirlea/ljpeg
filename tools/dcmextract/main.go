// Command dcmextract pulls the first frame's JPEG stream out of a DICOM file
// with encapsulated pixel data, and prints the attributes a reader needs
// (rows, columns, bits, PixelRepresentation, PhotometricInterpretation).
//
// It is a throwaway tool for checking the lossless decoder against real
// files; it is not a DICOM parser. It handles explicit VR little endian
// only, which is what all encapsulated transfer syntaxes use.
package main

import (
	"encoding/binary"
	"fmt"
	"os"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: dcmextract in.dcm out.jpg")
		os.Exit(2)
	}
	b, err := os.ReadFile(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if len(b) < 132 || string(b[128:132]) != "DICM" {
		fmt.Fprintln(os.Stderr, "not a DICOM part-10 file")
		os.Exit(1)
	}
	p := 132
	le := binary.LittleEndian
	attrs := map[string]any{}
	for p+8 <= len(b) {
		group, elem := le.Uint16(b[p:]), le.Uint16(b[p+2:])
		vr := string(b[p+4 : p+6])
		var length int
		var hdr int
		switch vr {
		case "OB", "OW", "OF", "SQ", "UT", "UN":
			length = int(le.Uint32(b[p+8:]))
			hdr = 12
		default:
			length = int(le.Uint16(b[p+6:]))
			hdr = 8
		}
		tag := fmt.Sprintf("%04x,%04x", group, elem)
		if group == 0x7fe0 && elem == 0x0010 {
			if length != 0xffffffff {
				fmt.Fprintln(os.Stderr, "pixel data is not encapsulated")
				os.Exit(1)
			}
			// Encapsulated: a basic offset table item, then fragment items.
			q := p + hdr
			frame := []byte(nil)
			item := 0
			for q+8 <= len(b) {
				g, e := le.Uint16(b[q:]), le.Uint16(b[q+2:])
				l := int(le.Uint32(b[q+4:]))
				if g == 0xfffe && e == 0xe0dd {
					break
				}
				if g != 0xfffe || e != 0xe000 {
					fmt.Fprintln(os.Stderr, "bad fragment item")
					os.Exit(1)
				}
				data := b[q+8 : q+8+l]
				if item > 0 {
					// Concatenate the fragments of the first frame; stop at the
					// next frame's start (a new SOI) once we have one.
					if len(frame) > 0 && len(data) >= 2 && data[0] == 0xff && data[1] == 0xd8 {
						break
					}
					frame = append(frame, data...)
				}
				item++
				q += 8 + l
			}
			// Strip trailing padding after EOI.
			for i := len(frame) - 2; i > 0; i-- {
				if frame[i] == 0xff && frame[i+1] == 0xd9 {
					frame = frame[:i+2]
					break
				}
			}
			if err := os.WriteFile(os.Args[2], frame, 0o644); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			fmt.Printf("TransferSyntax=%v Rows=%v Columns=%v SamplesPerPixel=%v BitsAllocated=%v BitsStored=%v HighBit=%v PixelRepresentation=%v Photometric=%v frame=%d bytes\n",
				attrs["0002,0010"], attrs["0028,0010"], attrs["0028,0011"], attrs["0028,0002"], attrs["0028,0100"], attrs["0028,0101"], attrs["0028,0102"], attrs["0028,0103"], attrs["0028,0004"], len(frame))
			return
		}
		if length == 0xffffffff {
			// An undefined-length sequence: skip to its delimiter, counting
			// nested sequences and items of undefined length.
			q, depth := p+hdr, 1
			for q+8 <= len(b) && depth > 0 {
				g, e := le.Uint16(b[q:]), le.Uint16(b[q+2:])
				l := int(le.Uint32(b[q+4:]))
				switch {
				case g == 0xfffe && e == 0xe0dd: // sequence delimiter
					depth--
					q += 8
				case g == 0xfffe && e == 0xe000: // item
					if l == 0xffffffff {
						q += 8
					} else {
						q += 8 + l
					}
				case g == 0xfffe && e == 0xe00d: // item delimiter
					q += 8
				default:
					// A data element inside an undefined-length item.
					v := string(b[q+4 : q+6])
					if v == "OB" || v == "OW" || v == "OF" || v == "SQ" || v == "UT" || v == "UN" {
						l = int(le.Uint32(b[q+8:]))
						if l == 0xffffffff {
							depth++
							q += 12
						} else {
							q += 12 + l
						}
					} else {
						q += 8 + int(le.Uint16(b[q+6:]))
					}
				}
			}
			p = q
			continue
		}
		val := b[p+hdr : p+hdr+length]
		switch vr {
		case "US":
			if len(val) >= 2 {
				attrs[tag] = le.Uint16(val)
			}
		case "UI", "CS", "LO", "SH":
			attrs[tag] = string(val)
		}
		p += hdr + length
	}
	fmt.Fprintln(os.Stderr, "no pixel data found")
	os.Exit(1)
}
