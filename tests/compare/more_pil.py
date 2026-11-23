# python -I more_pil.py task in out : one task of bench_more.ps1 with Pillow,
# as this package does it there. JPEG out at quality 85 unless the task
# says otherwise.
import io, os, sys
from PIL import (Image, ImageChops, ImageCms, ImageDraw, ImageEnhance,
                 ImageFilter, ImageFont, ImageOps, ImageStat)

Image.MAX_IMAGE_PIXELS = None
task, src, dst = sys.argv[1:4]
FONT = os.environ.get('FONT', r'C:\Windows\Fonts\arial.ttf')


def jpeg(im, **kw):
    im.convert('RGB').save(dst, quality=85, **kw)


def thumb(im, w=1536, method=Image.LANCZOS):
    return im.resize((w, round(im.height * w / im.width)), method)


def cube(path):
    # An Adobe / Resolve .cube file as Pillow's Color3DLUT.
    size, table = 0, []
    for line in open(path):
        p = line.split()
        if not p or p[0].startswith('#') or p[0] == 'TITLE':
            continue
        if p[0] == 'LUT_3D_SIZE':
            size = int(p[1])
        elif p[0][0] in '-.0123456789':
            table.extend(float(v) for v in p[:3])
    return ImageFilter.Color3DLUT(size, table)


def draw(im, fn):
    im = im.convert('RGB')
    fn(ImageDraw.Draw(im, 'RGBA'))
    return im


if task == 'text_image':
    words = 'The quick brown fox jumps over the lazy dog. ' * 40
    font = ImageFont.truetype(FONT, 48)
    lines, line = [], ''
    for w in words.split():
        t = (line + ' ' + w).strip()
        if font.getlength(t) > 2000 and line:
            lines.append(line)
            line = w
        else:
            line = t
    lines.append(line)
    im = Image.new('RGB', (2000, 58 * len(lines)), 'white')
    d = ImageDraw.Draw(im)
    for i, l in enumerate(lines):
        d.text((0, 58 * i), l, font=font, fill='black')
    im.save(dst)
    sys.exit()

if task == 'batch':
    os.makedirs(dst, exist_ok=True)
    for f in sorted(os.listdir(src)):
        if f.lower().endswith('.jpg'):
            thumb(Image.open(os.path.join(src, f))).save(os.path.join(dst, f), quality=85)
    sys.exit()

if task == 'pages':
    ims = [Image.open(p).convert('RGB') for p in src.split(';')]
    ims[0].save(dst, save_all=True, append_images=ims[1:])
    sys.exit()

im = Image.open(src)

if task == 'thumb':
    jpeg(thumb(im.convert('RGB') if im.mode not in ('RGB', 'L') else im))
elif task == 'svg':
    raise SystemExit('no SVG in Pillow')
# Writing, same size.
elif task in ('bmp', 'qoi', 'ppm', 'tga'):
    im.save(dst)
elif task == 'pdf85':
    im.save(dst, quality=85)
elif task == 'tiff_none':
    im.save(dst)
elif task == 'tiff_deflate':
    im.save(dst, compression='tiff_adobe_deflate')
elif task == 'tiff_lzw':
    im.save(dst, compression='tiff_lzw')
elif task == 'tiff_jpeg':
    im.save(dst, compression='jpeg', quality=85)
elif task == 'ico4':
    im.save(dst, sizes=[(16, 16), (32, 32), (48, 48), (256, 256)])
elif task == 'icns':
    s = min(im.size)
    im.crop(((im.width - s) // 2, (im.height - s) // 2, (im.width + s) // 2, (im.height + s) // 2)).resize((1024, 1024), Image.LANCZOS).save(dst)
elif task in ('png1', 'png4', 'png9'):
    im.save(dst, compress_level=int(task[3:]))
elif task == 'png256':
    im.quantize(256).save(dst)
elif task == 'gif':
    im.save(dst)
elif task == 'gif1536':
    thumb(im).save(dst)
elif task == 'jpg_prog':
    jpeg(im, progressive=True)
elif task == 'jpg_opt':
    jpeg(im, optimize=True)
elif task == 'jpg_444':
    jpeg(im, subsampling=0)
# Options.
elif task == 'crop':
    jpeg(im.crop((1500, 1000, 4500, 3000)))
elif task == 'trim':
    bg = Image.new(im.mode, im.size, im.getpixel((0, 0)))
    diff = ImageChops.difference(im, bg).convert('L').point(lambda v: 255 if v > 10 else 0)
    jpeg(im.crop(diff.getbbox() or (0, 0) + im.size))
elif task == 'flip':
    jpeg(im.transpose(Image.FLIP_TOP_BOTTOM))
elif task == 'flop':
    jpeg(im.transpose(Image.FLIP_LEFT_RIGHT))
elif task == 'rot180':
    jpeg(im.transpose(Image.ROTATE_180))
elif task == 'nearest':
    jpeg(thumb(im, method=Image.NEAREST))
elif task == 'contain':
    jpeg(ImageOps.pad(im, (1536, 1536), Image.LANCZOS, color='white'))
elif task == 'enlarge':
    jpeg(thumb(im, 12000))
elif task == 'blur10':
    jpeg(im.filter(ImageFilter.GaussianBlur(10)))
elif task == 'lut33':
    jpeg(im.filter(cube(os.environ['CUBE'])))
elif task == 'srgb':
    prof = ImageCms.ImageCmsProfile(io.BytesIO(im.info['icc_profile']))
    jpeg(ImageCms.profileToProfile(im, prof, ImageCms.createProfile('sRGB')))
elif task == 'keepmeta':
    jpeg(im, exif=im.info.get('exif', b''), icc_profile=im.info.get('icc_profile'))
# Operations.
elif task == 'affine':
    # sharp's affine: (x, y) to (a x + b y, c x + d y), the canvas holding it.
    a, b, c, d = 0.966, -0.259, 0.259, 0.966
    w, h = im.size
    xs = [a * x + b * y for x, y in ((0, 0), (w, 0), (0, h), (w, h))]
    ys = [c * x + d * y for x, y in ((0, 0), (w, 0), (0, h), (w, h))]
    W, H = round(max(xs) - min(xs)), round(max(ys) - min(ys))
    det = a * d - b * c
    ia, ib, ic, id_ = d / det, -b / det, -c / det, a / det
    ox, oy = min(xs), min(ys)
    jpeg(im.transform((W, H), Image.AFFINE, (ia, ib, ia * ox + ib * oy, ic, id_, ic * ox + id_ * oy), Image.BILINEAR, fillcolor='white'))
elif task == 'autocontrast':
    jpeg(ImageOps.autocontrast(im, cutoff=2))
elif task == 'boxblur':
    jpeg(im.filter(ImageFilter.BoxBlur(5)))
elif task == 'arc':
    jpeg(draw(im, lambda d: d.arc((1500, 1000, 4500, 3000), 30, 300, fill='red', width=12)))
elif task == 'chord':
    jpeg(draw(im, lambda d: d.chord((1500, 1000, 4500, 3000), 30, 300, fill='red')))
elif task == 'circle':
    jpeg(draw(im, lambda d: d.ellipse((1500, 500, 4500, 3500), fill=(255, 0, 0, 128), outline='blue', width=20)))
elif task == 'ellipse':
    jpeg(draw(im, lambda d: d.ellipse((500, 500, 5500, 3500), fill='yellow', outline='black', width=30)))
elif task == 'line':
    jpeg(draw(im, lambda d: d.line([(100, 100), (5900, 3900), (100, 3900)], fill='red', width=25, joint='curve')))
elif task == 'pie':
    jpeg(draw(im, lambda d: d.pieslice((1500, 1000, 4500, 3000), 30, 300, fill='red')))
elif task == 'polygon':
    jpeg(draw(im, lambda d: d.polygon([(500, 500), (5500, 800), (4000, 3500), (800, 3000)], fill=(0, 255, 0, 128), outline='black', width=10)))
elif task == 'rect':
    jpeg(draw(im, lambda d: d.rounded_rectangle((500, 500, 5500, 3500), 200, fill=(0, 0, 255, 96))))
elif task == 'convolve':
    jpeg(im.filter(ImageFilter.Kernel((3, 3), [-1, -1, -1, -1, 9, -1, -1, -1, -1], 1)))
elif task == 'dilate':
    jpeg(im.filter(ImageFilter.MaxFilter(5)))
elif task == 'erode':
    jpeg(im.filter(ImageFilter.MinFilter(5)))
elif task.startswith('enhance_'):
    kind, f = task.split('_')[1], {'color': 1.5, 'contrast': 1.3, 'brightness': 1.2, 'sharpness': 2}
    E = {'color': ImageEnhance.Color, 'contrast': ImageEnhance.Contrast, 'brightness': ImageEnhance.Brightness, 'sharpness': ImageEnhance.Sharpness}
    jpeg(E[kind](im).enhance(f[kind]))
elif task == 'equalize':
    jpeg(ImageOps.equalize(im))
elif task == 'extract_green':
    jpeg(im.getchannel('G'))
elif task.startswith('filter_'):
    F = {'blur': ImageFilter.BLUR, 'contour': ImageFilter.CONTOUR, 'detail': ImageFilter.DETAIL, 'edge-enhance': ImageFilter.EDGE_ENHANCE,
         'edge-enhance-more': ImageFilter.EDGE_ENHANCE_MORE, 'emboss': ImageFilter.EMBOSS, 'find-edges': ImageFilter.FIND_EDGES,
         'sharpen': ImageFilter.SHARPEN, 'smooth': ImageFilter.SMOOTH, 'smooth-more': ImageFilter.SMOOTH_MORE}
    jpeg(im.filter(F[task[7:]]))
elif task == 'gamma':
    jpeg(im.point([round(255 * (i / 255) ** (1 / 2.2)) for i in range(256)] * 3))
elif task == 'linear':
    jpeg(im.point(lambda v: 1.2 * v - 20))
elif task in ('median3', 'median7'):
    jpeg(im.filter(ImageFilter.MedianFilter(int(task[6:]))))
elif task == 'negate':
    jpeg(ImageOps.invert(im))
elif task == 'posterize':
    jpeg(ImageOps.posterize(im, 3))
elif task == 'rank':
    jpeg(im.filter(ImageFilter.RankFilter(5, 12)))
elif task == 'recomb':
    jpeg(im.convert('RGB', (0.393, 0.769, 0.189, 0, 0.349, 0.686, 0.168, 0, 0.272, 0.534, 0.131, 0)))
elif task == 'rotate15':
    jpeg(im.rotate(-15, Image.BICUBIC, expand=True, fillcolor='white'))
elif task == 'solarize':
    jpeg(ImageOps.solarize(im))
elif task == 'threshold':
    jpeg(im.convert('L').point(lambda v: 255 if v >= 128 else 0))
elif task == 'tint':
    jpeg(ImageOps.colorize(im.convert('L'), 'black', '#704214'))
elif task == 'perspective':
    jpeg(im.transform(im.size, Image.PERSPECTIVE, (1, 0.1, 0, 0.05, 1, 0, 0.00002, 0.00001), Image.BILINEAR))
elif task == 'ensure_alpha':
    im = im.convert('RGBA')
    im.putalpha(128)
    im.save(dst)
elif task == 'remove_alpha':
    im.convert('RGB').save(dst)
# Watermarks and text.
elif task == 'logo_tiled':
    logo = Image.open(os.environ['LOGO']).convert('RGBA')
    im = im.convert('RGB')
    for y in range(0, im.height, logo.height):
        for x in range(0, im.width, logo.width):
            im.paste(logo, (x, y), logo)
    jpeg(im)
elif task == 'text_mark':
    im = im.convert('RGB')
    d = ImageDraw.Draw(im, 'RGBA')
    font = ImageFont.truetype(FONT, 120)
    t = 'Copyright 2026'
    x0, y0, x1, y1 = d.textbbox((0, 0), t, font=font, stroke_width=3)
    d.text((im.width - x1 - 24, im.height - y1 - 24), t, font=font, fill=(255, 255, 255, 160), stroke_width=3, stroke_fill='black')
    jpeg(im)
# Animation.
elif task == 'anim_gif':
    im.save(dst, save_all=True)
elif task == 'anim_apng':
    im.save(dst, save_all=True)
# The commands that only look.
elif task == 'info':
    im.size, im.mode
elif task == 'check':
    im.load()
elif task == 'stats':
    ImageStat.Stat(im).mean
elif task == 'exif':
    dict(im.getexif())
else:
    raise SystemExit('task ' + task)
