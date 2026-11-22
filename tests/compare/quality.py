"""Lossy encoders at equal perceptual quality.
python quality.py jpeg|webp ref1.png ...
Environment: GI (our CLI), VIPS, CJPEGLI, CWEBP, DWEBP, BIN (ssimulacra2, butteraugli_main), CACHE.
Each configuration encodes every image at a list of qualities; for every point of the
baseline (the first configuration) the size the others need for the same SSIMULACRA2 and
the same butteraugli (3-norm) is interpolated. Prints how much larger (+) each is."""
import json, math, os, re, subprocess, sys, tempfile

E = os.environ
kind, refs = sys.argv[1], sys.argv[2:]
tmp = tempfile.mkdtemp()
CACHE = E.get('CACHE', 'quality_cache.json')
cache = json.load(open(CACHE)) if os.path.exists(CACHE) else {}
sh = lambda a, **k: subprocess.run(a, check=True, capture_output=True, **k)

def gi(flags):
    def f(ref, q, out):
        sh([E['GI'], '-threads', '1', '-quality', str(q)] + flags + [ref, out])
    return f

def vipsjpeg(opts):
    def f(ref, q, out):
        sh([E['VIPS'], 'copy', ref, '%s[Q=%d%s]' % (out, q, opts)])
    return f

def jpegli(ref, q, out):
    ppm = os.path.join(tmp, os.path.basename(ref) + '.ppm')
    if not os.path.exists(ppm):
        sh([E['GI'], ref, ppm])
    sh([E['CJPEGLI'], ppm, out, '-q', str(q)])

def cwebp(flags):
    def f(ref, q, out):
        sh([E['CWEBP'], '-quiet', '-q', str(q)] + flags + [ref, '-o', out])
    return f

if kind == 'jpeg':
    ext, qs = 'jpg', [50, 60, 70, 78, 85, 90, 94]
    configs = [
        ('mozjpeg (libvips, all its options)', vipsjpeg(',optimize_coding,interlace,trellis_quant,overshoot_deringing,optimize_scans,quant_table=3')),
        ('jpegli', jpegli),
        ('libjpeg-turbo settings (libvips default)', vipsjpeg('')),
        ('goimgtools default', gi([])),
        ('goimgtools progressive, trellis, table 3', gi(['-jpeg-progressive', '-jpeg-trellis', '-jpeg-quant-table', '3'])),
        ('goimgtools progressive, trellis, table 9', gi(['-jpeg-progressive', '-jpeg-trellis', '-jpeg-quant-table', '9'])),
    ]
else:
    ext, qs = 'webp', [45, 60, 72, 82, 90, 95]
    configs = [
        ('cwebp -m 6 -af -sharp_yuv', cwebp(['-m', '6', '-af', '-sharp_yuv'])),
        ('cwebp default', cwebp([])),
        ('goimgtools default', gi([])),
        ('goimgtools effort 1', gi(['-webp-effort', '1'])),
    ]

def metrics(ref, path):
    png = path
    if ext == 'webp':
        png = path + '.png'
        sh([E['DWEBP'], path, '-o', png])
    s2 = float(subprocess.run([os.path.join(E['BIN'], 'ssimulacra2.exe'), ref, png], capture_output=True, text=True, check=True).stdout.split()[-1])
    out = subprocess.run([os.path.join(E['BIN'], 'butteraugli_main.exe'), ref, png, '--pnorm', '3'], capture_output=True, text=True, check=True).stdout
    return s2, float(re.search(r'3-norm:\s*([\d.]+)', out).group(1))

def size_at(curve, idx, m):
    pts = sorted(curve, key=lambda p: p[idx])
    for a, b in zip(pts, pts[1:]):
        if a[idx] <= m <= b[idx] and b[idx] > a[idx]:
            t = (m - a[idx]) / (b[idx] - a[idx])
            return math.exp(math.log(a[0]) + t * (math.log(b[0]) - math.log(a[0])))
    return None

base = configs[0][0]
tot = {n: [[], []] for n, _ in configs[1:]}
for ref in refs:
    curves = {}
    for name, enc in configs:
        pts = []
        for q in qs:
            key = '%s|%s|%s|%d' % (kind, name, os.path.basename(ref), q)
            if key not in cache:
                out = os.path.join(tmp, 'o.' + ext)
                enc(ref, q, out)
                cache[key] = [os.path.getsize(out), *metrics(ref, out)]
                json.dump(cache, open(CACHE, 'w'))
            pts.append(tuple(cache[key]))
        curves[name] = pts
    for name, _ in configs[1:]:
        for k, idx in enumerate((1, 2)):
            r = [s / p[0] - 1 for p in curves[base] for s in [size_at(curves[name], idx, p[idx])] if s]
            if r:
                tot[name][k].append(100 * sum(r) / len(r))
    print(os.path.basename(ref), 'done', flush=True)
print('%d images; mean %% larger (+) or smaller (-) than "%s" at equal SSIMULACRA2 / equal butteraugli:' % (len(refs), base))
for name, t in tot.items():
    print('  %-44s %+6.1f%% (%d) / %+6.1f%% (%d)' % (name, sum(t[0]) / max(len(t[0]), 1), len(t[0]), sum(t[1]) / max(len(t[1]), 1), len(t[1])))
