"""The lossy outputs quality.py leaves out, each tool at the same setting.
python -I quality_more.py ref1.png ...
Environment: GI, VIPS, CWEBP, GIFSICLE, NODE_SHARP (more_sharp.js), PIL (more_pil.py),
SHARP_DIR, BIN (ssimulacra2, butteraugli_main), CACHE.
For every task and tool: the total size of the outputs and the mean SSIMULACRA2
(higher is better) and butteraugli 3-norm (lower is better) against the
originals, every output decoded by libvips."""
import json, os, re, subprocess, sys, tempfile

E = os.environ
refs = sys.argv[1:]
tmp = tempfile.mkdtemp()
CACHE = E.get('CACHE', 'quality_more_cache.json')
cache = json.load(open(CACHE)) if os.path.exists(CACHE) else {}
sh = lambda a: subprocess.run(a, check=True, capture_output=True)


def gi(*flags):
    return lambda ref, out: sh([E['GI'], '-threads', '1'] + list(flags) + [ref, out])


def vips(opts):
    return lambda ref, out: sh([E['VIPS'], 'copy', ref, out + opts])


def sharp(task):
    return lambda ref, out: sh(['node', E['NODE_SHARP'], task, ref, out, '1'])


def pil(task):
    return lambda ref, out: sh(['python', '-I', E['PIL'], task, ref, out])


def gifsicle_lossy(ref, out):
    # gifsicle reads only GIF: this package's GIF of the image, made lossy.
    g = out + '.src.gif'
    sh([E['GI'], '-threads', '1', ref, g])
    sh([E['GIFSICLE'], '-O3', '--lossy=24', g, '-o', out])


def cwebp(*flags):
    return lambda ref, out: sh([E['CWEBP'], '-quiet'] + list(flags) + [ref, '-o', out])


tasks = [
    ('PNG, 256 colours', 'png', [
        ('goimgtools', gi('-png-colors', '256')), ('libvips (libimagequant)', vips('[palette]')),
        ('sharp (libimagequant)', sharp('png256')), ('Pillow', pil('png256'))]),
    ('GIF, 256 colours', 'gif', [
        ('goimgtools', gi()), ('libvips (cgif)', vips('')), ('sharp (cgif)', sharp('gif')), ('Pillow', pil('gif'))]),
    ('GIF, lossy 24', 'gif', [
        ('goimgtools', gi('-gif-lossy', '24')), ('gifsicle -O3 --lossy=24', gifsicle_lossy)]),
    ('WebP near-lossless 60', 'webp', [
        ('goimgtools', gi('-webp-near-lossless', '-quality', '60')), ('cwebp', cwebp('-near_lossless', '60')),
        ('libvips', vips('[near_lossless,Q=60]')), ('sharp', sharp('webp_nl60'))]),
    ('TIFF with JPEG, quality 85', 'tif', [
        ('goimgtools', gi('-tiff-compression', 'jpeg', '-quality', '85')), ('libvips', vips('[compression=jpeg,Q=85]')),
        ('sharp', sharp('tiff_jpeg')), ('Pillow', pil('tiff_jpeg'))]),
    ('JPEG quality 85', 'jpg', [
        ('goimgtools', gi('-quality', '85')), ('libvips', vips('[Q=85]')), ('sharp', lambda r, o: sh(['node', E['NODE_SHARP'], 'jpg_opt', r, o, '1'])),
        ('Pillow', lambda r, o: sh(['python', '-I', E['PIL'], 'jpg_opt', r, o]))]),
    ('JPEG 85, trellis', 'jpg', [
        ('goimgtools', gi('-quality', '85', '-jpeg-trellis')), ('libvips', vips('[Q=85,trellis_quant]')), ('sharp', sharp('jpg_trellis'))]),
    ('JPEG 85, 4:4:4', 'jpg', [
        ('goimgtools', gi('-quality', '85', '-jpeg-chroma', '444')), ('libvips', vips('[Q=85,subsample_mode=off]')),
        ('sharp', sharp('jpg_444')), ('Pillow', pil('jpg_444'))]),
    ('JPEG 85, overshoot deringing', 'jpg', [
        ('goimgtools', gi('-quality', '85', '-jpeg-overshoot-deringing')), ('libvips', vips('[Q=85,overshoot_deringing]')), ('sharp', sharp('jpg_dering'))]),
]


def metrics(ref, path):
    png = path + '.png'
    sh([E['VIPS'], 'copy', path, png])
    s2 = float(subprocess.run([os.path.join(E['BIN'], 'ssimulacra2.exe'), ref, png], capture_output=True, text=True, check=True).stdout.split()[-1])
    out = subprocess.run([os.path.join(E['BIN'], 'butteraugli_main.exe'), ref, png, '--pnorm', '3'], capture_output=True, text=True, check=True).stdout
    return s2, float(re.search(r'3-norm:\s*([\d.]+)', out).group(1))


for title, ext, tools in tasks:
    print('%s, %d images:' % (title, len(refs)))
    for name, enc in tools:
        size, s2, ba = 0, 0.0, 0.0
        for ref in refs:
            key = '%s|%s|%s' % (title, name, os.path.basename(ref))
            if key not in cache:
                out = os.path.join(tmp, 'o.' + ext)
                enc(ref, out)
                cache[key] = [os.path.getsize(out), *metrics(ref, out)]
                json.dump(cache, open(CACHE, 'w'))
            b, m2, m3 = cache[key]
            size, s2, ba = size + b, s2 + m2, ba + m3
        n = len(refs)
        print('  %-28s %10d bytes   SSIMULACRA2 %6.2f   butteraugli %6.3f' % (name, size, s2 / n, ba / n), flush=True)
