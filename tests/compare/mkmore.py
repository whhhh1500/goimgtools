# Inputs of 6000x4000 for the formats the comparison has no row for, made
# from the photograph big420.jpg: PCX, SGI, DDS, XBM by Pillow; PSD, FITS
# and XPM written here (Pillow writes none of them); an SVG of many shapes.
import random, struct, sys
from PIL import Image

src, out = sys.argv[1], sys.argv[2]
im = Image.open(src).convert("RGB")
w, h = im.size

im.save(out + r"\in.pcx")
im.save(out + r"\in.sgi")
im.save(out + r"\in.dds")
im.convert("1").save(out + r"\in.xbm")

# PSD: version 1, three 8-bit channels, raw (planar) image data.
with open(out + r"\in.psd", "wb") as f:
    f.write(b"8BPS" + struct.pack(">H6xHIIHH", 1, 3, h, w, 8, 3))
    f.write(struct.pack(">I", 0) * 3)  # colour mode data, resources, layers
    f.write(struct.pack(">H", 0))  # raw
    for band in im.split():
        f.write(band.tobytes())

# FITS: 8-bit grey, the header in 2880-byte blocks, the data padded too.
grey = im.convert("L")
cards = ["SIMPLE  =                    T", "BITPIX  =                    8",
         "NAXIS   =                    2", "NAXIS1  = %20d" % w, "NAXIS2  = %20d" % h, "END"]
head = "".join(c.ljust(80) for c in cards).encode("ascii")
head += b" " * (-len(head) % 2880)
data = grey.transpose(Image.FLIP_TOP_BOTTOM).tobytes()
with open(out + r"\in.fits", "wb") as f:
    f.write(head + data + b"\0" * (-len(data) % 2880))

# XPM: 256 colours, two characters a pixel.
pal = im.quantize(256)
rgb = pal.getpalette()[:768]
chars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789+-"
codes = [chars[i // 64 % 64] + chars[i % 64] for i in range(256)]
px = pal.tobytes()
with open(out + r"\in.xpm", "w", newline="\n") as f:
    f.write('/* XPM */\nstatic char *in[] = {\n"%d %d 256 2",\n' % (w, h))
    for i in range(256):
        f.write('"%s c #%02x%02x%02x",\n' % (codes[i], rgb[3 * i], rgb[3 * i + 1], rgb[3 * i + 2]))
    for y in range(h):
        row = px[y * w:(y + 1) * w]
        f.write('"' + "".join(codes[b] for b in row) + ('"};\n' if y == h - 1 else '",\n'))

# SVG: 3000 shapes with gradients, strokes and opacity, and some text.
random.seed(1)
parts = ['<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" viewBox="0 0 %d %d">' % (w, h, w, h),
         '<defs>']
for i in range(20):
    parts.append('<linearGradient id="g%d" x1="0" y1="0" x2="1" y2="1"><stop offset="0" stop-color="#%06x"/>'
                 '<stop offset="1" stop-color="#%06x"/></linearGradient>' % (i, random.getrandbits(24), random.getrandbits(24)))
parts.append('</defs><rect width="100%" height="100%" fill="#f4f1ea"/>')
for i in range(3000):
    x, y, r = random.uniform(0, w), random.uniform(0, h), random.uniform(10, 200)
    k = random.random()
    fill = 'url(#g%d)' % random.randrange(20) if k < 0.4 else '#%06x' % random.getrandbits(24)
    if k < 0.5:
        parts.append('<circle cx="%.1f" cy="%.1f" r="%.1f" fill="%s" opacity="0.7"/>' % (x, y, r, fill))
    elif k < 0.8:
        parts.append('<rect x="%.1f" y="%.1f" width="%.1f" height="%.1f" rx="12" fill="%s" stroke="#222" stroke-width="3"/>' % (x, y, r, r * 0.6, fill))
    else:
        pts = " ".join("%.1f,%.1f" % (x + random.uniform(-r, r), y + random.uniform(-r, r)) for _ in range(6))
        parts.append('<path d="M%s Z" fill="none" stroke="%s" stroke-width="5" stroke-linejoin="round"/>' % (pts, fill))
for i in range(40):
    parts.append('<text x="%d" y="%d" font-family="Arial" font-size="72" fill="#333">Sample text %d</text>' % (random.randrange(w - 800), random.randrange(100, h), i))
parts.append('</svg>')
with open(out + r"\in.svg", "w") as f:
    f.write("\n".join(parts))

# A lookup cube of 33 points a side, a warm grade, as colour graders export.
n = 33
with open(out + r"\warm_33.cube", "w", newline="\n") as f:
    f.write('TITLE "warm"\nLUT_3D_SIZE %d\n' % n)
    for b in range(n):
        for g in range(n):
            for r in range(n):
                R, G, B = r / (n - 1), g / (n - 1), b / (n - 1)
                f.write("%.6f %.6f %.6f\n" % (min(1, R * 1.08 + 0.02), G * 1.0 + 0.01, B * 0.9))

# Ten copies of the photograph, for the batch command.
import os, shutil
os.makedirs(out + r"\batch", exist_ok=True)
for i in range(10):
    shutil.copyfile(src, out + r"\batch\p%02d.jpg" % i)
print("done")
