"""Downscaling accuracy: each tool's Lanczos 3 result against the exact Lanczos 3 convolution
(Pillow's resize of each channel in 32-bit floats, rounded once).
python resizeq.py width ref1.png ...   Environment: GI, VIPS (vips.exe), VTH (vipsthumbnail.exe), SHARP_DIR, CMP (dir of bench_sharp.js)."""
import math, os, subprocess, sys, tempfile
from PIL import Image, ImageChops, ImageStat
Image.MAX_IMAGE_PIXELS = None
E = os.environ
width, refs = int(sys.argv[1]), sys.argv[2:]
tmp = tempfile.mkdtemp()
sh = lambda a: subprocess.run(a, check=True, capture_output=True)

def exact(ref, w, h):
    im = Image.open(ref).convert('RGB')
    chans = [c.convert('F').resize((w, h), Image.LANCZOS) for c in im.split()]
    import numpy as np
    return Image.merge('RGB', [Image.fromarray(np.clip(np.rint(np.asarray(c)), 0, 255).astype('uint8')) for c in chans])

def psnr(a, b):
    d = ImageChops.difference(a, b)
    st = ImageStat.Stat(d)
    mse = sum(x / n for x, n in zip(st.sum2, st.count)) / 3
    return (99.0 if mse == 0 else 10 * math.log10(255 * 255 / mse)), max(e[1] for e in d.getextrema())

def gi(method, extra=()):
    def f(ref, w, h, out):
        sh([E['GI'], '-threads', '1', '-resize', '%dx%d' % (w, h), '-fit', 'fill', '-kernel', 'lanczos3', '-resize-method', method, *extra, ref, out])
    return f

def sharp(task):
    def f(ref, w, h, out):
        sh(['node', os.path.join(E['CMP'], 'resize_sharp.js'), task, ref, out, str(w), str(h)])
    return f

def vips_resize(ref, w, h, out):
    im = Image.open(ref)
    sh([E['VIPS'], 'resize', ref, out, repr(w / im.width), '--vscale', repr(h / im.height), '--kernel', 'lanczos3'])

def pil8(ref, w, h, out):
    Image.open(ref).convert('RGB').resize((w, h), Image.LANCZOS).save(out)

tools = [('goimgtools (default method)', gi('draw')), ('goimgtools, libvips method', gi('libvips')), ('goimgtools, pillow method', gi('pillow')),
         ('sharp', sharp('plain')), ('vips resize', vips_resize), ('Pillow', pil8)]
tot = {n: [0.0, 0, 0] for n, _ in tools}
for ref in refs:
    im = Image.open(ref)
    w, h = width, round(im.height * width / im.width)
    ex = exact(ref, w, h)
    for name, f in tools:
        out = os.path.join(tmp, 'o.png')
        f(ref, w, h, out)
        o = Image.open(out).convert('RGB')
        if o.size != ex.size:
            print(name, 'size', o.size, 'want', ex.size); continue
        p, mx = psnr(ex, o)
        t = tot[name]; t[0] += p; t[1] += 1; t[2] = max(t[2], mx)
print('%d images to %d wide; mean PSNR against the exact Lanczos 3 convolution, largest difference of a channel:' % (len(refs), width))
for name, t in tot.items():
    print('  %-30s %6.2f dB   max %d' % (name, t[0] / max(t[1], 1), t[2]))
