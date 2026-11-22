# python bench_pil.py task in out : one task with Pillow, as the other tools do it.
import sys, os
from PIL import Image, ImageFilter, ImageOps, ImageEnhance
Image.MAX_IMAGE_PIXELS = None
task, src, dst = sys.argv[1:4]
im = Image.open(src)
ext = dst.rsplit('.', 1)[-1].lower()
if task == 'thumb':
    w, h = im.size
    im = im.resize((1536, round(h * 1536 / w)), Image.LANCZOS)
elif task == 'thumbdraft':
    w, h = im.size
    im.draft('RGB', (1536, round(h * 1536 / w)))
    im = im.resize((1536, round(h * 1536 / w)), Image.LANCZOS)
elif task in ('copy', 'lossless'):
    pass
elif task == 'blur':
    im = im.filter(ImageFilter.GaussianBlur(2))
elif task == 'sharpen':
    im = im.filter(ImageFilter.UnsharpMask(radius=2, percent=100, threshold=0))
elif task == 'rot90':
    im = im.transpose(Image.ROTATE_270)
elif task == 'rot30':
    im = im.rotate(-30, resample=Image.BICUBIC, expand=True, fillcolor='white')
elif task == 'gray':
    im = im.convert('L')
elif task == 'composite':
    logo = Image.open(os.environ['LOGO']).convert('RGBA')
    im = im.convert('RGB'); im.paste(logo, (im.width - logo.width, im.height - logo.height), logo)
else:
    raise SystemExit('task ' + task)
if ext == 'jpg':
    im.convert('RGB').save(dst, quality=85)
elif ext == 'webp':
    im.save(dst, lossless=True) if task == 'lossless' else im.save(dst, quality=75)
elif ext == 'gif':
    im.save(dst)
elif ext == 'tif':
    im.save(dst)
else:
    im.save(dst)
