/* ljdecraw: decode any lossless JPEG with libjpeg-turbo's library API and
 * write the raw samples as a PAM file (any component count, 2-16 bits). */
#include <stdio.h>
#include <stdlib.h>
#include <jpeglib.h>

int main(int argc, char **argv) {
    if (argc != 3) { fprintf(stderr, "usage: ljdecraw in.jpg out.pam\n"); return 2; }
    FILE *in = fopen(argv[1], "rb");
    if (!in) { perror(argv[1]); return 1; }
    struct jpeg_decompress_struct c;
    struct jpeg_error_mgr err;
    c.err = jpeg_std_error(&err);
    jpeg_create_decompress(&c);
    jpeg_stdio_src(&c, in);
    jpeg_read_header(&c, TRUE);
    c.out_color_space = c.jpeg_color_space; /* no colour conversion */
    jpeg_start_decompress(&c);
    int w = c.output_width, h = c.output_height, n = c.output_components, p = c.data_precision;
    FILE *out = fopen(argv[2], "wb");
    const char *tupl = n == 1 ? "GRAYSCALE" : n == 2 ? "GRAYSCALE_ALPHA" : n == 3 ? "RGB" : "RGB_ALPHA";
    fprintf(out, "P7\nWIDTH %d\nHEIGHT %d\nDEPTH %d\nMAXVAL %d\nTUPLTYPE %s\nENDHDR\n", w, h, n, (1 << p) - 1, tupl);
    if (p <= 8) {
        JSAMPROW row = malloc(w * n);
        while (c.output_scanline < h) { jpeg_read_scanlines(&c, &row, 1); fwrite(row, 1, w * n, out); }
    } else if (p <= 12) {
        J12SAMPROW row = malloc(w * n * sizeof(J12SAMPLE));
        while (c.output_scanline < h) { jpeg12_read_scanlines(&c, &row, 1); for (int i = 0; i < w * n; i++) { fputc(row[i] >> 8, out); fputc(row[i] & 0xff, out); } }
    } else {
        J16SAMPROW row = malloc(w * n * sizeof(J16SAMPLE));
        while (c.output_scanline < h) { jpeg16_read_scanlines(&c, &row, 1); for (int i = 0; i < w * n; i++) { fputc(row[i] >> 8, out); fputc(row[i] & 0xff, out); } }
    }
    fclose(out);
    jpeg_finish_decompress(&c);
    jpeg_destroy_decompress(&c);
    return 0;
}
