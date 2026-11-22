import sys, os
from PIL import Image, ImageChops, ImageStat
root, d = sys.argv[1], sys.argv[2]
def load(p):
    im = Image.open(p).convert('RGBA'); bg = Image.new('RGBA', im.size, (255,255,255,255)); return Image.alpha_composite(bg, im).convert('RGB')
n = ok = 0
for name in sorted(os.listdir(d)):
    rel = name[:-8].replace('__', os.sep)
    ref = os.path.join(root, rel + '.png')
    if not os.path.exists(ref): continue
    n += 1
    r, m = load(ref), load(os.path.join(d, name))
    if m.size == r.size and sum(ImageStat.Stat(ImageChops.difference(r, m)).mean) / 3 < 2: ok += 1
print('pass', ok, 'of', n)
