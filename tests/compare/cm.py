"""Colour management against LittleCMS (Pillow's ImageCms): a grid over the whole cube of
colours, tagged with a profile, converted to sRGB by this package (-srgb) and by LittleCMS
(perceptual intent, with and without black point compensation).
python cm.py gi.exe outdir p3.icc adobe.icc cmyk.icm"""
import os, subprocess, sys
import numpy as np
from PIL import Image, ImageCms
gi, out = sys.argv[1], sys.argv[2]
srgb = ImageCms.createProfile('sRGB')
os.makedirs(out, exist_ok=True)
def grid_rgb():
    n = 64
    v = np.round(np.linspace(0, 255, n)).astype('uint8')
    r, g, b = np.meshgrid(v, v, v, indexing='ij')
    return Image.fromarray(np.stack([r, g, b], -1).reshape(512, 512, 3))
def grid_cmyk():
    n = 17
    v = np.round(np.linspace(0, 255, n)).astype('uint8')
    c, m, y, k = np.meshgrid(v, v, v, v, indexing='ij')
    return Image.fromarray(np.stack([c, m, y, k], -1).reshape(289, 289, 4), 'CMYK')
for path in sys.argv[3:]:
    icc = open(path, 'rb').read()
    prof = ImageCms.ImageCmsProfile(path)
    cmyk = prof.profile.xcolor_space.strip() == 'CMYK'
    name = os.path.splitext(os.path.basename(path))[0]
    src = os.path.join(out, name + ('.jpg' if cmyk else '.png'))
    if cmyk:
        grid_cmyk().save(src, quality=100, subsampling=0, icc_profile=icc)
    else:
        grid_rgb().save(src, icc_profile=icc)
    ours = os.path.join(out, name + '.ours.png')
    subprocess.run([gi, '-srgb', src, ours], check=True)
    a = np.asarray(Image.open(ours).convert('RGB')).astype(int)
    im = Image.open(src)
    for label, flags in (('', 0), (' + black point compensation', ImageCms.Flags.BLACKPOINTCOMPENSATION)):
        ref = ImageCms.profileToProfile(im, prof, srgb, renderingIntent=ImageCms.Intent.PERCEPTUAL, outputMode='RGB', flags=flags)
        d = np.abs(a - np.asarray(ref).astype(int))
        print('%-10s against LittleCMS%-30s largest difference %3d, mean %.3f, values that differ %5.2f%%, by more than 1: %.3f%%' % (name, label, d.max(), d.mean(), 100 * (d > 0).mean(), 100 * (d > 1).mean()))
