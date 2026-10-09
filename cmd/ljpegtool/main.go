// Command ljpegtool converts between lossless JPEG and PNM, for checking the
// decoder and encoder against libjpeg-turbo:
//
//	ljpegtool dec in.jpg out.pnm P      (writes raw P-bit samples, like djpeg -pnm)
//	ljpegtool enc in.pnm out.jpg PSV PT RESTART
package main

import (
	"bufio"
	"fmt"
	"image"
	"os"
	"strconv"

	"ljpegmvp/jpeg"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "ljpegtool:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 4 && args[0] == "dec" {
		precision, _ := strconv.Atoi(args[3])
		f, err := os.Open(args[1])
		if err != nil {
			return err
		}
		defer f.Close()
		m, err := jpeg.Decode(bufio.NewReader(f))
		if err != nil {
			return err
		}
		return writePNM(args[2], m, precision)
	}
	if len(args) == 6 && args[0] == "enc" {
		planes, w, h, precision, err := readPNM(args[1])
		if err != nil {
			return err
		}
		psv, _ := strconv.Atoi(args[3])
		pt, _ := strconv.Atoi(args[4])
		rst, _ := strconv.Atoi(args[5])
		out, err := os.Create(args[2])
		if err != nil {
			return err
		}
		defer out.Close()
		return jpeg.EncodeLosslessPlanes(out, planes, w, h, precision, psv, pt, rst)
	}
	return fmt.Errorf("usage: ljpegtool dec in.jpg out.pnm P | enc in.pnm out.jpg PSV PT RESTART")
}

func writePNM(name string, m image.Image, precision int) error {
	b := m.Bounds()
	var nComp int
	switch m.(type) {
	case *image.Gray, *image.Gray16:
		nComp = 1
	default:
		nComp = 3
	}
	f, err := os.Create(name)
	if err != nil {
		return err
	}
	defer f.Close()
	w := bufio.NewWriter(f)
	magic := "P5"
	if nComp == 3 {
		magic = "P6"
	}
	fmt.Fprintf(w, "%s\n%d %d\n%d\n", magic, b.Dx(), b.Dy(), 1<<precision-1)
	put := func(v uint32, bits int) {
		v >>= bits - precision
		if precision > 8 {
			w.WriteByte(byte(v >> 8))
		}
		w.WriteByte(byte(v))
	}
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			switch m := m.(type) {
			case *image.Gray:
				put(uint32(m.GrayAt(x, y).Y), 8)
			case *image.Gray16:
				put(uint32(m.Gray16At(x, y).Y), 16)
			case *image.RGBA:
				c := m.RGBAAt(x, y)
				put(uint32(c.R), 8)
				put(uint32(c.G), 8)
				put(uint32(c.B), 8)
			case *image.RGBA64:
				c := m.RGBA64At(x, y)
				put(uint32(c.R), 16)
				put(uint32(c.G), 16)
				put(uint32(c.B), 16)
			default:
				return fmt.Errorf("unexpected image type %T", m)
			}
		}
	}
	return w.Flush()
}

func readPNM(name string) (planes [][]uint16, w, h, precision int, err error) {
	f, err := os.Open(name)
	if err != nil {
		return nil, 0, 0, 0, err
	}
	defer f.Close()
	r := bufio.NewReader(f)
	var magic string
	var maxval int
	if _, err := fmt.Fscan(r, &magic, &w, &h, &maxval); err != nil {
		return nil, 0, 0, 0, err
	}
	r.ReadByte() // The single whitespace after maxval.
	nComp := 1
	if magic == "P6" {
		nComp = 3
	}
	for precision = 1; 1<<precision-1 < maxval; precision++ {
	}
	planes = make([][]uint16, nComp)
	for c := range planes {
		planes[c] = make([]uint16, w*h)
	}
	for i := 0; i < w*h; i++ {
		for c := 0; c < nComp; c++ {
			var v uint16
			b, _ := r.ReadByte()
			v = uint16(b)
			if maxval > 255 {
				b2, _ := r.ReadByte()
				v = v<<8 | uint16(b2)
			}
			planes[c][i] = v
		}
	}
	return planes, w, h, precision, nil
}
