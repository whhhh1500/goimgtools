// The entry points of the WebAssembly module goimgtools/heic runs Kvazaar
// through (translated to Go by wasm2go): a picture of 8-bit samples is
// allocated, its planes are filled from Go, and it is coded as one intra
// picture, the Annex B stream returned.
#include <stdint.h>
#include <stdlib.h>
#include <string.h>

#include "kvazaar.h"

static const kvz_api *api(void) { return kvz_api_get(8); }

// picture allocates a picture of width x height in the chroma format csp
// (0 4:0:0, 1 4:2:0, 3 4:4:4); NULL if out of memory. Kvazaar codes
// pictures of a multiple of 8 samples each way: a picture of another size
// is allocated to the next multiple, the samples past it repeating its
// edge, and encode is given its size.
kvz_picture *picture(int csp, int width, int height) {
  const kvz_api *a = api();
  if (!a) {
    return NULL;
  }
  return a->picture_alloc_csp((enum kvz_chroma_format)csp, width, height);
}

// plane returns plane i (0 Y, 1 Cb, 2 Cr) of a picture.
uint8_t *plane(kvz_picture *pic, int i) {
  return i == 0 ? pic->y : i == 1 ? pic->u : pic->v;
}

// stride returns the distance between the rows of plane i.
int stride(kvz_picture *pic, int i) { return i == 0 ? pic->stride : pic->stride_c; }

// deltas gives the picture a map of w x h QP deltas, one for each region of
// that grid over it, and returns it for the caller to fill; NULL if out of
// memory. Kvazaar frees it with the picture.
int8_t *deltas(kvz_picture *pic, int w, int h) {
  int8_t *m = calloc((size_t)w * (size_t)h, 1);
  if (m) {
    free(pic->roi.roi_array);
    pic->roi.roi_array = m;
    pic->roi.width = w;
    pic->roi.height = h;
  }
  return m;
}

// release frees a picture that was not handed to encode.
void release(kvz_picture *pic) {
  const kvz_api *a = api();
  if (a && pic) {
    a->picture_free(pic);
  }
}

// set applies the options: lines of name=value, as Kvazaar's command line
// names them without the dashes. It returns 0 if one is not understood.
static int set(const kvz_api *a, kvz_config *cfg, const char *opts) {
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
      ok = a->config_parse(cfg, line, eq + 1);
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

// encode codes the picture (which it frees), width x height of it, at the
// QP with the options. It returns the stream, allocated with malloc, and its
// length in *out_len; NULL on failure, *out_len then 1 if an option was not
// understood.
uint8_t *encode(kvz_picture *pic, int width, int height, int qp, const char *opts, uint32_t *out_len) {
  const kvz_api *a = api();
  *out_len = 0;
  if (!a || !pic) {
    release(pic);
    return NULL;
  }
  kvz_config *cfg = a->config_alloc();
  kvz_encoder *enc = NULL;
  uint8_t *out = NULL;
  size_t len = 0, cap = 0;
  if (!cfg || !a->config_init(cfg)) {
    goto done;
  }
  if (!set(a, cfg, opts)) {
    *out_len = 1;
    goto done;
  }
  if (width <= 0 || height <= 0 || width > pic->width || height > pic->height ||
      pic->width - width >= 8 || pic->height - height >= 8) {
    goto done;
  }
  cfg->width = width;
  cfg->height = height;
  cfg->qp = qp;
  cfg->threads = 0;
  cfg->owf = 0;
  cfg->intra_period = 1;
  cfg->hash = KVZ_HASH_NONE;
  cfg->aud_enable = 0;
  cfg->calc_psnr = 0;
  cfg->add_encoder_info = 0;
  cfg->input_bitdepth = 8;
  // Both the format read and the format coded.
  static const char *const formats[] = {"P400", "P420", "P422", "P444"};
  if ((unsigned)pic->chroma_format > 3 || !a->config_parse(cfg, "input-format", formats[pic->chroma_format])) {
    goto done;
  }
  enc = a->encoder_open(cfg);
  if (!enc) {
    goto done;
  }
  pic->pts = 0;
  for (int i = 0; i < 64; i++) {
    kvz_data_chunk *chunks = NULL;
    uint32_t n = 0;
    if (!a->encoder_encode(enc, i == 0 ? pic : NULL, &chunks, &n, NULL, NULL, NULL)) {
      free(out);
      out = NULL;
      goto done;
    }
    for (kvz_data_chunk *c = chunks; c; c = c->next) {
      if (len + c->len > cap) {
        size_t nc = cap ? cap * 2 : 65536;
        while (nc < len + c->len) {
          nc *= 2;
        }
        uint8_t *b = realloc(out, nc);
        if (!b) {
          a->chunk_free(chunks);
          free(out);
          out = NULL;
          goto done;
        }
        out = b;
        cap = nc;
      }
      memcpy(out + len, c->data, c->len);
      len += c->len;
    }
    a->chunk_free(chunks);
    if (i > 0 && n == 0) {
      break;
    }
  }
  *out_len = (uint32_t)len;
done:
  a->picture_free(pic);
  if (enc) {
    a->encoder_close(enc);
  }
  if (cfg) {
    a->config_destroy(cfg);
  }
  return out;
}
