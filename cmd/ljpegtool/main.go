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
	"strings"

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
	if len(args) == 4 && args[0] == "decraw" {
		// Writes a PAM file with the raw samples of any number of components.
		precision, _ := strconv.Atoi(args[3])
		f, err := os.Open(args[1])
		if err != nil {
			return err
		}
		defer f.Close()
		m, err := jpeg.DecodeLossless(bufio.NewReader(f))
		if err != nil {
			return err
		}
		planes := make([][]uint16, len(m.Components))
		for i, c := range m.Components {
			planes[i] = c.Samples
		}
		return writePAM(args[2], planes, m.Width, m.Height, precision)
	}
	if len(args) >= 6 && args[0] == "encpam" {
		planes, w, h, precision, err := readPAM(args[1])
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
		return encode(out, planes, w, h, precision, psv, pt, rst, len(args) > 6 && args[6] == "separate")
	}
	if len(args) >= 6 && args[0] == "enc" {
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
		return encode(out, planes, w, h, precision, psv, pt, rst, len(args) > 6 && args[6] == "separate")
	}
	return fmt.Errorf("usage: ljpegtool dec in.jpg out.pnm P | decraw in.jpg out.pam P | enc in.pnm out.jpg PSV PT RESTARTROWS [separate]")
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

func writePAM(name string, planes [][]uint16, w, h, precision int) error {
	f, err := os.Create(name)
	if err != nil {
		return err
	}
	defer f.Close()
	bw := bufio.NewWriter(f)
	tupl := map[int]string{1: "GRAYSCALE", 2: "GRAYSCALE_ALPHA", 3: "RGB", 4: "RGB_ALPHA"}[len(planes)]
	fmt.Fprintf(bw, "P7\nWIDTH %d\nHEIGHT %d\nDEPTH %d\nMAXVAL %d\nTUPLTYPE %s\nENDHDR\n", w, h, len(planes), 1<<precision-1, tupl)
	for i := 0; i < w*h; i++ {
		for _, p := range planes {
			if precision > 8 {
				bw.WriteByte(byte(p[i] >> 8))
			}
			bw.WriteByte(byte(p[i]))
		}
	}
	return bw.Flush()
}

func readPAM(name string) (planes [][]uint16, w, h, precision int, err error) {
	f, err := os.Open(name)
	if err != nil {
		return nil, 0, 0, 0, err
	}
	defer f.Close()
	r := bufio.NewReader(f)
	depth, maxval := 0, 0
	for {
		line, err := r.ReadString(10)
		if err != nil {
			return nil, 0, 0, 0, err
		}
		var n int
		switch {
		case strings.HasPrefix(line, "WIDTH "):
			fmt.Sscanf(line, "WIDTH %d", &n)
			w = n
		case strings.HasPrefix(line, "HEIGHT "):
			fmt.Sscanf(line, "HEIGHT %d", &n)
			h = n
		case strings.HasPrefix(line, "DEPTH "):
			fmt.Sscanf(line, "DEPTH %d", &depth)
		case strings.HasPrefix(line, "MAXVAL "):
			fmt.Sscanf(line, "MAXVAL %d", &maxval)
		case strings.HasPrefix(line, "ENDHDR"):
			goto body
		}
	}
body:
	for precision = 1; 1<<precision-1 < maxval; precision++ {
	}
	planes = make([][]uint16, depth)
	for c := range planes {
		planes[c] = make([]uint16, w*h)
	}
	for i := 0; i < w*h; i++ {
		for c := 0; c < depth; c++ {
			b, _ := r.ReadByte()
			v := uint16(b)
			if maxval > 255 {
				b2, _ := r.ReadByte()
				v = v<<8 | uint16(b2)
			}
			planes[c][i] = v
		}
	}
	return planes, w, h, precision, nil
}

func encode(out *os.File, planes [][]uint16, w, h, precision, psv, pt, rst int, separate bool) error {
	m := &jpeg.Lossless{Width: w, Height: h, Precision: precision}
	for i, p := range planes {
		m.Components = append(m.Components, jpeg.LosslessComponent{ID: uint8(i + 1), Samples: p})
	}
	return jpeg.EncodeLossless(out, m, &jpeg.LosslessOptions{Predictor: psv, PointTransform: pt, RestartRows: rst, SeparateScans: separate})
}
