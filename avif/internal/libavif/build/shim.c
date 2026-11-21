// The entry points of the WebAssembly module goimgtools/avif runs libavif
// through (translated to Go by wasm2go): a file is parsed, described and
// decoded to RGBA in steps, and RGBA is coded with the colour description,
// the metadata and the encoder options given.
#include <setjmp.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>

#include "avif/avif.h"

// A decoder: the file's, and the pixels once converted.
typedef struct {
  avifDecoder *dec;
  avifRGBImage rgb;
  int decoded;
} decoder;

// dec_open parses a file (which must stay in memory while the decoder is
// open); NULL if it is not one libavif reads. *result is libavif's code.
decoder *dec_open(const uint8_t *data, uint32_t size, int32_t *result) {
  decoder *d = calloc(1, sizeof(decoder));
  if (!d) {
    *result = AVIF_RESULT_OUT_OF_MEMORY;
    return NULL;
  }
  d->dec = avifDecoderCreate();
  if (!d->dec) {
    free(d);
    *result = AVIF_RESULT_OUT_OF_MEMORY;
    return NULL;
  }
  d->dec->maxThreads = 1;
  d->dec->strictFlags = AVIF_STRICT_DISABLED;
  avifResult r = avifDecoderSetIOMemory(d->dec, data, size);
  if (r == AVIF_RESULT_OK) {
    r = avifDecoderParse(d->dec);
  }
  *result = r;
  if (r != AVIF_RESULT_OK) {
    avifDecoderDestroy(d->dec);
    free(d);
    return NULL;
  }
  return d;
}

// dec_get returns a number about the image: 0 width, 1 height, 2 bit depth,
// 3 whether it has alpha, 4 the transform flags, 5 irot's angle, 6 imir's
// axis, 7-9 the colour primaries, transfer and matrix, 10 whether the range
// is full, 11 the chroma format, 12 the number of images, 13-20 clap's
// fractions, 21-23 the sizes of the ICC profile, EXIF and XMP.
uint32_t dec_get(decoder *d, int what) {
  const avifImage *im = d->dec->image;
  switch (what) {
  case 0: return im->width;
  case 1: return im->height;
  case 2: return im->depth;
  case 3: return d->dec->alphaPresent;
  case 4: return im->transformFlags;
  case 5: return im->irot.angle;
  case 6: return im->imir.axis;
  case 7: return im->colorPrimaries;
  case 8: return im->transferCharacteristics;
  case 9: return im->matrixCoefficients;
  case 10: return im->yuvRange == AVIF_RANGE_FULL;
  case 11: return im->yuvFormat;
  case 12: return d->dec->imageCount;
  case 13: return im->clap.widthN;
  case 14: return im->clap.widthD;
  case 15: return im->clap.heightN;
  case 16: return im->clap.heightD;
  case 17: return im->clap.horizOffN;
  case 18: return im->clap.horizOffD;
  case 19: return im->clap.vertOffN;
  case 20: return im->clap.vertOffD;
  case 21: return (uint32_t)im->icc.size;
  case 22: return (uint32_t)im->exif.size;
  case 23: return (uint32_t)im->xmp.size;
  }
  return 0;
}

// dec_blob returns the ICC profile (0), EXIF (1) or XMP (2).
const uint8_t *dec_blob(decoder *d, int what) {
  const avifImage *im = d->dec->image;
  switch (what) {
  case 0: return im->icc.data;
  case 1: return im->exif.data;
  case 2: return im->xmp.data;
  }
  return NULL;
}

// dec_pixels decodes the first image and converts it to RGBA, 8 bits a
// sample (16 if depth16, for more than 8), colour not multiplied by alpha;
// *row_bytes is the distance between rows. NULL on failure, *result then
// libavif's code.
uint8_t *dec_pixels(decoder *d, int depth16, uint32_t *row_bytes, int32_t *result) {
  avifResult r = AVIF_RESULT_OK;
  if (!d->decoded) {
    r = avifDecoderNextImage(d->dec);
    if (r == AVIF_RESULT_OK) {
      avifRGBImageSetDefaults(&d->rgb, d->dec->image);
      d->rgb.format = AVIF_RGB_FORMAT_RGBA;
      d->rgb.depth = depth16 ? 16 : 8;
      d->rgb.maxThreads = 1;
      d->rgb.alphaPremultiplied = AVIF_FALSE;
      r = avifRGBImageAllocatePixels(&d->rgb);
      if (r == AVIF_RESULT_OK) {
        r = avifImageYUVToRGB(d->dec->image, &d->rgb);
      }
      if (r == AVIF_RESULT_OK) {
        d->decoded = 1;
      }
    }
  }
  *result = r;
  if (!d->decoded) {
    return NULL;
  }
  *row_bytes = d->rgb.rowBytes;
  return d->rgb.pixels;
}

// dec_close frees a decoder.
void dec_close(decoder *d) {
  if (d->rgb.pixels) {
    avifRGBImageFreePixels(&d->rgb);
  }
  avifDecoderDestroy(d->dec);
  free(d);
}

// set applies codec specific options, lines of name=value; 0 if one is not
// understood.
static int set(avifEncoder *e, const char *opts) {
  size_t n = strlen(opts);
  char *buf = malloc(n + 1);
  if (!buf) {
    return 0;
  }
  memcpy(buf, opts, n + 1);
  int ok = 1;
  for (char *line = buf; ok && *line;) {
    char *end = strchr(line, '\n');
    if (end) {
      *end = 0;
    }
    char *eq = strchr(line, '=');
    if (eq) {
      *eq = 0;
      ok = avifEncoderSetCodecSpecificOption(e, line, eq + 1) == AVIF_RESULT_OK;
    } else if (*line) {
      ok = 0;
    }
    if (!end) {
      break;
    }
    line = end + 1;
  }
  free(buf);
  return ok;
}

// enc codes width x height pixels of 8-bit RGBA (colour not multiplied by
// alpha; alpha used unless opaque) as one image: yuv is 1 for 4:2:0, 3 for
// 4:4:4; lossless codes G, B and R as they are; icc, exif and xmp may be
// NULL. Without a profile the colours are described as sRGB (BT.709
// primaries and the sRGB curve), with one as unspecified. It returns the
// file, and its size in *out_size; NULL on failure, *result then libavif's
// code (or -1 for an option not understood).
uint8_t *enc(const uint8_t *rgba, uint32_t width, uint32_t height, int opaque, int yuv, int lossless,
             int quality, int quality_alpha, int speed, const uint8_t *icc, uint32_t icc_size,
             const uint8_t *exif, uint32_t exif_size, const uint8_t *xmp, uint32_t xmp_size,
             const char *opts, uint32_t *out_size, int32_t *result) {
  *out_size = 0;
  avifResult r = AVIF_RESULT_OUT_OF_MEMORY;
  avifImage *im = avifImageCreate(width, height, 8, lossless ? AVIF_PIXEL_FORMAT_YUV444 : (avifPixelFormat)yuv);
  avifEncoder *e = NULL;
  avifRWData out = AVIF_DATA_EMPTY;
  if (!im) {
    goto done;
  }
  im->yuvRange = AVIF_RANGE_FULL;
  im->colorPrimaries = icc ? AVIF_COLOR_PRIMARIES_UNSPECIFIED : AVIF_COLOR_PRIMARIES_BT709;
  im->transferCharacteristics = icc ? AVIF_TRANSFER_CHARACTERISTICS_UNSPECIFIED : AVIF_TRANSFER_CHARACTERISTICS_SRGB;
  im->matrixCoefficients = lossless ? AVIF_MATRIX_COEFFICIENTS_IDENTITY : AVIF_MATRIX_COEFFICIENTS_BT601;
  if (icc && (r = avifImageSetProfileICC(im, icc, icc_size)) != AVIF_RESULT_OK) {
    goto done;
  }
  if (exif && (r = avifImageSetMetadataExif(im, exif, exif_size)) != AVIF_RESULT_OK) {
    goto done;
  }
  if (xmp && (r = avifImageSetMetadataXMP(im, xmp, xmp_size)) != AVIF_RESULT_OK) {
    goto done;
  }
  avifRGBImage rgb;
  avifRGBImageSetDefaults(&rgb, im);
  rgb.format = AVIF_RGB_FORMAT_RGBA;
  rgb.depth = 8;
  rgb.maxThreads = 1;
  rgb.alphaPremultiplied = AVIF_FALSE;
  rgb.ignoreAlpha = opaque ? AVIF_TRUE : AVIF_FALSE;
  rgb.pixels = (uint8_t *)rgba;
  rgb.rowBytes = width * 4;
  if ((r = avifImageRGBToYUV(im, &rgb)) != AVIF_RESULT_OK) {
    goto done;
  }
  e = avifEncoderCreate();
  if (!e) {
    r = AVIF_RESULT_OUT_OF_MEMORY;
    goto done;
  }
  e->maxThreads = 1;
  e->speed = speed;
  e->quality = lossless ? AVIF_QUALITY_LOSSLESS : quality;
  e->qualityAlpha = lossless ? AVIF_QUALITY_LOSSLESS : quality_alpha;
  if (!set(e, opts)) {
    r = -1;
    goto done;
  }
  if ((r = avifEncoderAddImage(e, im, 1, AVIF_ADD_IMAGE_FLAG_SINGLE)) != AVIF_RESULT_OK) {
    goto done;
  }
  if ((r = avifEncoderFinish(e, &out)) != AVIF_RESULT_OK) {
    goto done;
  }
  *out_size = (uint32_t)out.size;
done:
  *result = r;
  if (e) {
    avifEncoderDestroy(e);
  }
  if (im) {
    avifImageDestroy(im);
  }
  if (r != AVIF_RESULT_OK) {
    avifRWDataFree(&out);
    return NULL;
  }
  return out.data;
}

// aom keeps setjmp for its error path, which WebAssembly without exception
// handling cannot take: setjmp returns once, and longjmp, called only on an
// error, traps, which the Go side turns into an error.
int setjmp(jmp_buf env) {
  (void)env;
  return 0;
}

_Noreturn void longjmp(jmp_buf env, int val) {
  (void)env;
  (void)val;
  __builtin_trap();
}
