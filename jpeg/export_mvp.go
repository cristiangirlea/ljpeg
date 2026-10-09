// MVP only, not for upstream: exposes the unexported lossless encoder to
// cmd/ljpegtool, which checks it against libjpeg-turbo.

package jpeg

import "io"

// EncodeLosslessPlanes is a temporary wrapper around encodeLossless.
func EncodeLosslessPlanes(w io.Writer, planes [][]uint16, width, height, precision, predictor, pt, restartRows int, separate bool) error {
	return encodeLossless(w, planes, width, height, losslessOptions{precision, uint8(predictor), uint8(pt), restartRows, separate})
}

// DecodeLosslessPlanes is a temporary wrapper that returns the decoded
// planes of a lossless image with any number of components, unscaled.
func DecodeLosslessPlanes(r io.Reader) (planes [][]uint16, width, height, precision int, err error) {
	var d decoder
	if _, err = d.decode(r, false); err != nil {
		if _, ok := err.(UnsupportedError); !ok || !d.lossless {
			return nil, 0, 0, 0, err
		}
	}
	for i := 0; i < d.nComp; i++ {
		p := &d.lsPlanes[i]
		if !p.decoded {
			return nil, 0, 0, 0, FormatError("missing SOS marker")
		}
		pix := make([]uint16, len(p.pix))
		for j, v := range p.pix {
			pix[j] = v << p.pt
		}
		planes = append(planes, pix)
	}
	return planes, d.width, d.height, d.precision, nil
}
