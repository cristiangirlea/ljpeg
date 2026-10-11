// MVP only, not for upstream: exposes the unexported lossless encoder to
// cmd/ljpegtool, which checks it against libjpeg-turbo.

package jpeg

import "io"

// EncodeLosslessPlanes is a temporary wrapper around encodeLossless.
func EncodeLosslessPlanes(w io.Writer, planes [][]uint16, width, height, precision, predictor, pt, restart int) error {
	return encodeLossless(w, planes, width, height, losslessOptions{precision, uint8(predictor), uint8(pt), restart})
}
