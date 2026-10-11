# Lossless JPEG for Go

A proof of concept for [golang/go#22535](https://github.com/golang/go/issues/22535),
"image: support LJPEG".

This is a copy of the Go standard library's `image/jpeg` package, with
support for lossless JPEG added: the Huffman-coded, non-hierarchical lossless
process of ITU-T T.81 Annex H (SOF3), as used by DICOM medical images
(transfer syntaxes 1.2.840.10008.1.2.4.57 and .70) and by camera raw files
(DNG).

It exists so that the question raised on the issue, whether this belongs in
`image/jpeg` or in `golang.org/x/image`, can be answered by looking at working
code. Everything that is not lossless-specific is the upstream package,
unchanged.

## What is supported

| | |
|---|---|
| Precision | 2 to 16 bits per sample |
| Predictors | 1 to 7 (table H.1) |
| Point transform | 0 to P-1 |
| Components | 1 to 4, interleaved or one scan per component |
| Huffman tables | one per scan or per component |
| Restart intervals | yes, in whole rows as section H.1.1 requires |
| Difference category 16 | 32768 with no extra bits (table H.2) |

Not supported, and rejected with `UnsupportedError`: subsampled components
(Canon sRAW), arithmetic coding (SOF11), and hierarchical frames. Those are
rare outside specific camera formats and are better left to raw-file
libraries.

## Usage

```
go get github.com/cristiangirlea/ljpeg
```

The package is a drop-in replacement for `image/jpeg`. `jpeg.Decode` and
`jpeg.DecodeConfig` accept lossless images with 1 or 3 components and return
`*image.Gray`, `*image.Gray16`, `*image.RGBA` or `*image.RGBA64`, with samples
scaled to the full range as `image/png` does. No color conversion is applied:
3-component lossless data is returned as coded.

```go
import "github.com/cristiangirlea/ljpeg/jpeg"

m, err := jpeg.Decode(f)          // a 12-bit CT scan comes back as *image.Gray16
```

For DICOM and DNG, which need the samples as coded, there is a raw API:

```go
m, err := jpeg.DecodeLossless(f)  // *jpeg.Lossless: Width, Height, Precision, Components
for _, c := range m.Components {
	// c.Samples holds Width*Height samples in [0, 1<<m.Precision), row-major.
	// A DICOM reader with PixelRepresentation 1 sign-extends them from
	// m.Precision bits; a DNG reader reshapes the flat stream by the tile width.
}

err = jpeg.EncodeLossless(w, m, &jpeg.LosslessOptions{Predictor: 4})
```

Runnable examples are in `jpeg/example_lossless_test.go`.

`Lossless` carries one plane per component at the frame's own precision, so a
DICOM reader can apply `PixelRepresentation`, and a DNG reader can reshape a
frame whose geometry differs from the tile's (an Adobe-written CFA tile of
256×256 is coded as a 128-wide 2-component frame).

The raw API is part of this proof of concept. In the standard library it
would need a proposal; the `Decode` path would not.

## Verification

Everything in `jpeg/lossless*.go` was written from the specification. The
reference implementation, libjpeg-turbo 3.1.2, is used only as an external
check, through `testdata/crosscheck.sh`:

- libjpeg-turbo encodes, Go decodes: byte-exact on 280 combinations of
  precision, components, predictor, point transform and restart interval.
- Go encodes, libjpeg-turbo decodes: byte-exact on the same 280, plus
  one-scan-per-component files.
- 2- and 4-component streams: byte-exact against libjpeg-turbo's library
  API (`jpeg_read_scanlines` and `jpeg16_read_scanlines`), which its
  command-line tools cannot write. The small C program for this is in
  `tools/ljdecraw/`.

Real files, all freely redistributable, were decoded and compared with
libjpeg-turbo's library API, sample for sample:

| File | Source | Content |
|---|---|---|
| `SC_rgb_jpeg_gdcm.dcm` | pydicom | DICOM .70, RGB 8-bit |
| `JPEG-LL.dcm` | pydicom-data | DICOM .70, signed 16-bit, uses category 16 |
| `JPEGLossless_1s_1f_u_16_12.dcm` | pylibjpeg-data | DICOM .57, 12-bit, predictor 7 |
| `JPEGLosslessP14SV1_1s_1f_u_16_16.dcm` | pylibjpeg-data | DICOM .70, 16-bit |
| `JPEGLossless_1s_1f_i_16_16_bad.dcm` | pylibjpeg-data | DICOM .57, signed 16-bit |
| `JPGLosslessP14SV1_1s_1f_8b.dcm` | pydicom-data | DICOM .70, 8-bit |
| Adobe DNG Converter, Canon 5D III | raw.pixls.us (CC0) | 384 CFA tiles, 2-component 16-bit; 475 linear RGB tiles |
| Apple iPhone 12 Pro ProRAW | raw.pixls.us (CC0) | 64 tiles, 12-bit RGB, predictor 7 |
| Blackmagic Micro Cinema Camera | raw.pixls.us (CC0) | 2 tiles, 12-bit, predictor 6; libjpeg-turbo rejects this file's Huffman table, so Go's output (plausible 12-bit Bayer data) is unverified |

The Adobe DNG samples were also compared with the uncompressed DNG of the
same image.

The Go-only tests (`go test ./jpeg/`) round-trip 12,096 combinations through
the encoder and decoder, and the decoder was fuzzed for 30 minutes (287
million inputs) with the same harness as upstream
(`go test -fuzz=FuzzDecode ./jpeg/`). Those tests share the predictor and
category code between encoder and decoder, which is why the libjpeg-turbo
comparison matters: it is the only check with an independent implementation.

## Layout

- `jpeg/`: the package. `lossless.go` is the decoder, `lossless_encode.go`
  the encoder, `lossless_api.go` the raw API. The other files are upstream.
- `internal/imageutil/`: a copy of `image/internal/imageutil`, which the
  upstream package imports.
- `cmd/ljpegtool/`: converts between lossless JPEG and PNM/PAM, for the
  cross-check.
- `tools/`: throwaway extractors for DICOM pixel data and DNG tiles.
- `testdata/`: upstream test images plus `crosscheck.sh`.

## License

BSD 3-Clause, the same as Go and golang.org/x/image; see `LICENSE`. `PATENTS`
is Go's patent grant, which covers the code taken from Go.

If a program links both `image/jpeg` and this package, both register the
"jpeg" format with `image.Decode`, and the one initialized first is used.
