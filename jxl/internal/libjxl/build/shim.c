// The entry points of the module: decoding a JPEG XL file to RGBA (its
// description, its colour profile, EXIF and XMP, and the pixels of its first
// frame, upright, the colour not multiplied by alpha, as djxl gives them by
// default), and encoding RGBA as one.
#include <stdint.h>
#include <stdlib.h>
#include <string.h>

#include <jxl/color_encoding.h>
#include <jxl/decode.h>
#include <jxl/encode.h>

typedef struct {
	uint8_t *p;
	size_t n, cap;
} buf;

static int grow(buf *b, size_t need) {
	if (need <= b->cap) {
		return 1;
	}
	size_t c = b->cap ? b->cap : 4096;
	while (c < need) {
		c *= 2;
	}
	uint8_t *p = realloc(b->p, c);
	if (!p) {
		return 0;
	}
	b->p = p;
	b->cap = c;
	return 1;
}

// budget bounds what libjxl allocates while decoding: a file that asks
// for more than its image could need is refused rather than given it.
typedef struct {
	size_t used, cap;
} budget;

static void *mm_alloc(void *opaque, size_t size) {
	budget *b = opaque;
	if (size > b->cap - b->used) {
		return NULL;
	}
	// The size before the block, which keeps malloc's 16-byte alignment.
	size_t *p = malloc(size + 16);
	if (!p) {
		return NULL;
	}
	p[0] = size;
	b->used += size;
	return (char *)p + 16;
}

static void mm_free(void *opaque, void *address) {
	if (!address) {
		return;
	}
	budget *b = opaque;
	size_t *p = (size_t *)((char *)address - 16);
	b->used -= p[0];
	free(p);
}

// maxBox bounds the EXIF and XMP kept (the pipeline's MaxMetaBytes): a
// larger box, compressed ones decompressed, is skipped.
#define maxBox (8 << 20)

typedef struct {
	JxlBasicInfo info;
	int gray, alpha, depth16, srgb, frames, orientation;
	budget mem;
	buf icc, exif, xmp;
	uint8_t *pix;
	size_t pixsize, stride;
} dec;

// What dec_get answers.
enum {
	GET_WIDTH,
	GET_HEIGHT,
	GET_DEPTH,      // bits a sample
	GET_ALPHA,      // 1 if the image has alpha
	GET_GRAY,       // 1 if the image is grey
	GET_SRGB,       // 1 if the pixels are sRGB (no profile needed)
	GET_ORIENTATION, // the EXIF orientation the pixels were turned by
	GET_ANIMATED,   // 1 if the file is an animation
	GET_STRIDE,
	GET_ICC_SIZE,
	GET_EXIF_SIZE,
	GET_XMP_SIZE,
	GET_FLOAT,      // 1 if the samples are floating point
};

// keep reads the box being read into b, the first of its type only.
static void release(JxlDecoder *d, buf *b, int *reading) {
	if (*reading) {
		size_t left = JxlDecoderReleaseBoxBuffer(d);
		b->n -= left;
		*reading = 0;
	}
}

// dec_open decodes data: its description, profile and boxes, and unless
// configOnly the pixels of its first frame (RGBA, 8 bits a sample, or 16
// big endian above 8 bits), libjxl allocating up to capMB megabytes. err gets
// the step that failed: 1 out of memory, 2 the file is not one, 3 it ended
// early.
dec *dec_open(const uint8_t *data, int32_t n, int32_t configOnly, int32_t capMB, int32_t *err) {
	*err = 0;
	dec *r = calloc(1, sizeof(dec));
	JxlDecoder *d = NULL;
	if (r) {
		r->mem.cap = (size_t)capMB << 20;
		JxlMemoryManager mm = {&r->mem, mm_alloc, mm_free};
		d = JxlDecoderCreate(&mm);
	}
	if (!r || !d) {
		*err = 1;
		goto fail;
	}
	int events = JXL_DEC_BASIC_INFO | JXL_DEC_COLOR_ENCODING | JXL_DEC_BOX;
	if (!configOnly) {
		events |= JXL_DEC_FULL_IMAGE;
	}
	if (JxlDecoderSubscribeEvents(d, events) != JXL_DEC_SUCCESS ||
	    JxlDecoderSetKeepOrientation(d, JXL_FALSE) != JXL_DEC_SUCCESS ||
	    JxlDecoderSetUnpremultiplyAlpha(d, JXL_TRUE) != JXL_DEC_SUCCESS ||
	    JxlDecoderSetCoalescing(d, JXL_TRUE) != JXL_DEC_SUCCESS ||
	    JxlDecoderSetDecompressBoxes(d, JXL_TRUE) != JXL_DEC_SUCCESS ||
	    JxlDecoderSetInput(d, data, (size_t)n) != JXL_DEC_SUCCESS) {
		*err = 2;
		goto fail;
	}
	JxlDecoderCloseInput(d);
	JxlPixelFormat format = {4, JXL_TYPE_UINT8, JXL_BIG_ENDIAN, 0};
	buf *box = NULL;
	int reading = 0, haveImage = 0;
	for (;;) {
		JxlDecoderStatus s = JxlDecoderProcessInput(d);
		if (s == JXL_DEC_ERROR) {
			*err = 2;
			goto fail;
		}
		if (s == JXL_DEC_NEED_MORE_INPUT) {
			// The input is closed: the file ends early; a header and its
			// first frame read are enough.
			if (haveImage || configOnly && r->info.xsize) {
				break;
			}
			*err = 3;
			goto fail;
		}
		if (s == JXL_DEC_SUCCESS) {
			break;
		}
		if (s == JXL_DEC_BASIC_INFO) {
			if (JxlDecoderGetBasicInfo(d, &r->info) != JXL_DEC_SUCCESS) {
				*err = 2;
				goto fail;
			}
			r->gray = r->info.num_color_channels == 1;
			r->alpha = r->info.alpha_bits > 0;
			r->depth16 = r->info.bits_per_sample > 8 || r->info.exponent_bits_per_sample > 0;
			r->orientation = r->info.orientation;
			r->frames = r->info.have_animation;
			if (r->depth16) {
				format.data_type = JXL_TYPE_UINT16;
			}
			continue;
		}
		if (s == JXL_DEC_COLOR_ENCODING) {
			size_t size = 0;
			JxlColorEncoding ce;
			if (JxlDecoderGetColorAsEncodedProfile(d, JXL_COLOR_PROFILE_TARGET_DATA, &ce) == JXL_DEC_SUCCESS &&
			    (ce.color_space == JXL_COLOR_SPACE_RGB || ce.color_space == JXL_COLOR_SPACE_GRAY) &&
			    ce.white_point == JXL_WHITE_POINT_D65 &&
			    (ce.color_space == JXL_COLOR_SPACE_GRAY || ce.primaries == JXL_PRIMARIES_SRGB) &&
			    ce.transfer_function == JXL_TRANSFER_FUNCTION_SRGB) {
				r->srgb = 1;
			}
			if (!r->srgb && JxlDecoderGetICCProfileSize(d, JXL_COLOR_PROFILE_TARGET_DATA, &size) == JXL_DEC_SUCCESS && size > 0 &&
			    size <= maxBox) {
				if (!grow(&r->icc, size)) {
					*err = 1;
					goto fail;
				}
				if (JxlDecoderGetColorAsICCProfile(d, JXL_COLOR_PROFILE_TARGET_DATA, r->icc.p, size) != JXL_DEC_SUCCESS) {
					*err = 2;
					goto fail;
				}
				r->icc.n = size;
			}
			continue;
		}
		if (s == JXL_DEC_NEED_IMAGE_OUT_BUFFER) {
			size_t size;
			if (JxlDecoderImageOutBufferSize(d, &format, &size) != JXL_DEC_SUCCESS) {
				*err = 2;
				goto fail;
			}
			r->pix = malloc(size);
			if (!r->pix) {
				*err = 1;
				goto fail;
			}
			r->pixsize = size;
			r->stride = (size_t)r->info.xsize * 4 * (r->depth16 ? 2 : 1);
			if (JxlDecoderSetImageOutBuffer(d, &format, r->pix, size) != JXL_DEC_SUCCESS) {
				*err = 2;
				goto fail;
			}
			continue;
		}
		if (s == JXL_DEC_FULL_IMAGE) {
			// The first frame: the others are skipped.
			haveImage = 1;
			JxlDecoderSkipFrames(d, 1 << 30);
			continue;
		}
		if (s == JXL_DEC_BOX) {
			release(d, box, &reading);
			JxlBoxType type;
			if (JxlDecoderGetBoxType(d, type, JXL_TRUE) != JXL_DEC_SUCCESS) {
				*err = 2;
				goto fail;
			}
			box = NULL;
			if (!memcmp(type, "Exif", 4) && !r->exif.n) {
				box = &r->exif;
			} else if (!memcmp(type, "xml ", 4) && !r->xmp.n) {
				box = &r->xmp;
			}
			if (box) {
				if (!grow(box, 65536)) {
					*err = 1;
					goto fail;
				}
				box->n = box->cap;
				JxlDecoderSetBoxBuffer(d, box->p, box->cap);
				reading = 1;
			}
			continue;
		}
		if (s == JXL_DEC_BOX_NEED_MORE_OUTPUT) {
			size_t left = JxlDecoderReleaseBoxBuffer(d);
			size_t have = box->cap - left;
			if (box->cap >= maxBox) {
				// Too large to keep: the rest is skipped.
				box->n = 0;
				reading = 0;
				box = NULL;
				continue;
			}
			if (!grow(box, box->cap * 2)) {
				*err = 1;
				goto fail;
			}
			box->n = box->cap;
			JxlDecoderSetBoxBuffer(d, box->p + have, box->cap - have);
			continue;
		}
		// Other events are not subscribed to.
		*err = 2;
		goto fail;
	}
	release(d, box, &reading);
	JxlDecoderDestroy(d);
	return r;
fail:
	if (d) {
		JxlDecoderDestroy(d);
	}
	if (r) {
		free(r->icc.p);
		free(r->exif.p);
		free(r->xmp.p);
		free(r->pix);
		free(r);
	}
	return NULL;
}

int32_t dec_get(dec *r, int32_t what) {
	switch (what) {
	case GET_WIDTH:
		return (int32_t)r->info.xsize;
	case GET_HEIGHT:
		return (int32_t)r->info.ysize;
	case GET_DEPTH:
		return (int32_t)r->info.bits_per_sample;
	case GET_ALPHA:
		return r->alpha;
	case GET_GRAY:
		return r->gray;
	case GET_SRGB:
		return r->srgb;
	case GET_ORIENTATION:
		return r->orientation;
	case GET_ANIMATED:
		return r->frames;
	case GET_STRIDE:
		return (int32_t)r->stride;
	case GET_ICC_SIZE:
		return (int32_t)r->icc.n;
	case GET_EXIF_SIZE:
		return (int32_t)r->exif.n;
	case GET_XMP_SIZE:
		return (int32_t)r->xmp.n;
	case GET_FLOAT:
		return r->info.exponent_bits_per_sample > 0;
	}
	return 0;
}

// dec_blob is the profile (0), EXIF (1), XMP (2) box, or the pixels (3).
const uint8_t *dec_blob(dec *r, int32_t what) {
	switch (what) {
	case 0:
		return r->icc.p;
	case 1:
		return r->exif.p;
	case 2:
		return r->xmp.p;
	case 3:
		return r->pix;
	}
	return NULL;
}

void dec_close(dec *r) {
	free(r->icc.p);
	free(r->exif.p);
	free(r->xmp.p);
	free(r->pix);
	free(r);
}

// enc codes w x h pixels (RGBA, 8 bits a sample, or 16 big endian if
// depth16; alpha left out if opaque) as a JPEG XL file at the distance
// given (or without loss), at an effort of 1 to 10; with the profile, EXIF
// (a TIFF header and what follows) and XMP given, if any. It returns the
// file, its size in *size, or NULL with err: 1 out of memory, 2 a setting
// libjxl refused, 3 the coding failed.
uint8_t *enc(uint8_t *pix, int32_t w, int32_t h, int32_t depth16, int32_t opaque, int32_t lossless,
             float distance, int32_t effort, const uint8_t *icc, int32_t niccs,
             const uint8_t *exif, int32_t nexif, const uint8_t *xmp, int32_t nxmp,
             int32_t *size, int32_t *err) {
	*err = 0;
	buf out = {0};
	JxlEncoder *e = JxlEncoderCreate(NULL);
	if (!e) {
		*err = 1;
		return NULL;
	}
	size_t bps = depth16 ? 2 : 1;
	size_t channels = 4;
	if (opaque) {
		// RGBA to RGB, in place.
		channels = 3;
		size_t n = (size_t)w * h;
		for (size_t i = 0; i < n; i++) {
			memmove(pix + i * 3 * bps, pix + i * 4 * bps, 3 * bps);
		}
	}
	JxlBasicInfo info;
	JxlEncoderInitBasicInfo(&info);
	info.xsize = (uint32_t)w;
	info.ysize = (uint32_t)h;
	info.bits_per_sample = depth16 ? 16 : 8;
	info.num_color_channels = 3;
	info.uses_original_profile = lossless ? JXL_TRUE : JXL_FALSE;
	if (!opaque) {
		info.alpha_bits = info.bits_per_sample;
		info.num_extra_channels = 1;
	}
	if (nexif || nxmp) {
		if (JxlEncoderUseBoxes(e) != JXL_ENC_SUCCESS) {
			*err = 2;
			goto fail;
		}
	}
	if (JxlEncoderSetBasicInfo(e, &info) != JXL_ENC_SUCCESS) {
		*err = 2;
		goto fail;
	}
	if (niccs > 0) {
		if (JxlEncoderSetICCProfile(e, icc, (size_t)niccs) != JXL_ENC_SUCCESS) {
			*err = 2;
			goto fail;
		}
	} else {
		JxlColorEncoding ce;
		JxlColorEncodingSetToSRGB(&ce, JXL_FALSE);
		if (JxlEncoderSetColorEncoding(e, &ce) != JXL_ENC_SUCCESS) {
			*err = 2;
			goto fail;
		}
	}
	JxlEncoderFrameSettings *fs = JxlEncoderFrameSettingsCreate(e, NULL);
	if (!fs || JxlEncoderFrameSettingsSetOption(fs, JXL_ENC_FRAME_SETTING_EFFORT, effort) != JXL_ENC_SUCCESS) {
		*err = 2;
		goto fail;
	}
	if (lossless) {
		if (JxlEncoderSetFrameLossless(fs, JXL_TRUE) != JXL_ENC_SUCCESS) {
			*err = 2;
			goto fail;
		}
	} else if (JxlEncoderSetFrameDistance(fs, distance) != JXL_ENC_SUCCESS) {
		*err = 2;
		goto fail;
	}
	// The alpha without loss, as cjxl codes it unless told otherwise.
	if (!opaque && JxlEncoderSetExtraChannelDistance(fs, 0, 0) != JXL_ENC_SUCCESS) {
		*err = 2;
		goto fail;
	}
	if (nexif) {
		// The EXIF box starts with the offset of the TIFF header in it.
		uint8_t *b = malloc((size_t)nexif + 4);
		if (!b) {
			*err = 1;
			goto fail;
		}
		memset(b, 0, 4);
		memcpy(b + 4, exif, (size_t)nexif);
		JxlEncoderStatus st = JxlEncoderAddBox(e, "Exif", b, (size_t)nexif + 4, JXL_FALSE);
		free(b);
		if (st != JXL_ENC_SUCCESS) {
			*err = 2;
			goto fail;
		}
	}
	if (nxmp && JxlEncoderAddBox(e, "xml ", xmp, (size_t)nxmp, JXL_FALSE) != JXL_ENC_SUCCESS) {
		*err = 2;
		goto fail;
	}
	JxlPixelFormat format = {(uint32_t)channels, depth16 ? JXL_TYPE_UINT16 : JXL_TYPE_UINT8, JXL_BIG_ENDIAN, 0};
	if (JxlEncoderAddImageFrame(fs, &format, pix, (size_t)w * h * channels * bps) != JXL_ENC_SUCCESS) {
		*err = 3;
		goto fail;
	}
	JxlEncoderCloseInput(e);
	if (!grow(&out, 1 << 16)) {
		*err = 1;
		goto fail;
	}
	for (;;) {
		uint8_t *next = out.p + out.n;
		size_t avail = out.cap - out.n;
		JxlEncoderStatus st = JxlEncoderProcessOutput(e, &next, &avail);
		out.n = (size_t)(next - out.p);
		if (st == JXL_ENC_SUCCESS) {
			break;
		}
		if (st != JXL_ENC_NEED_MORE_OUTPUT) {
			*err = JxlEncoderGetError(e) == JXL_ENC_ERR_OOM ? 1 : 3;
			goto fail;
		}
		if (!grow(&out, out.cap * 2)) {
			*err = 1;
			goto fail;
		}
	}
	JxlEncoderDestroy(e);
	*size = (int32_t)out.n;
	return out.p;
fail:
	JxlEncoderDestroy(e);
	free(out.p);
	return NULL;
}
