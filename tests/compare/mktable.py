"""The tables of docs/comparison.md for the features bench_more.ps1 measures.
python -I mktable.py more.txt en|zh out.md
Each line of more.txt: "group | feature | tool  T s  W MB  cpu C s  priv P MB  wrote N  out O  temp X MB".
A cell: time, peak working set / peak private memory, and what went to
temporary files when there is any. The verdict weighs memory first: the
working set against the smallest of the other tools'; then the time, noted
only when it is more than twice the fastest's."""
import re, sys
from collections import OrderedDict

src, lang, dst = sys.argv[1:4]
ZH = lang == 'zh'
LINE = re.compile(r'^(\w+) \| (.+?) \| (.+?)\s+([\d.]+)s\s+([\d.]+)MB\s+cpu\s+([\d.]+)s\s+priv\s+([\d.]+)MB\s+wrote \d+(?:\s+out ([\d,]+))?\s+temp\s+(-?[\d.]+)MB')
COLS = ['goimgtools', 'libvips', 'sharp', 'Pillow', 'other']


def column(tool):
    if tool == 'goimgtools':
        return 'goimgtools'
    if tool.startswith(('libvips', 'vips')):
        return 'libvips'
    if tool in ('sharp', 'Pillow'):
        return tool
    return 'other'


rows = OrderedDict()
for line in open(src, encoding='utf-8', errors='replace'):
    m = LINE.match(line.strip())
    if not m:
        continue
    g, feat, tool, t, ws, cpu, priv, out, temp = m.groups()
    rows.setdefault((g, feat), []).append(dict(tool=tool, t=float(t), ws=float(ws), priv=float(priv),
                                               out=int(out.replace(',', '')) if out else 0, temp=float(temp)))


def mb(v):
    return '%.1fMB' % v if v < 100 else '%.0fMB' % v


def size(b):
    return '%.1fMB' % (b / 2**20) if b >= 2**20 else '%.0fKB' % (b / 2**10)


def cell(e, show_out, named):
    s = '%.2fs, %s / %s' % (e['t'], mb(e['ws']), mb(e['priv']))
    if ZH:
        s = s.replace(', ', '、')
    if e['temp'] >= 0.5:
        s += ('、临时文件 %s' if ZH else ', %s to temporary files') % mb(e['temp'])
    if show_out and e['out']:
        s += ('、文件 %s' if ZH else ', file %s') % size(e['out'])
    if named:
        s = '%s: %s' % (e['tool'], s) if not ZH else '%s：%s' % (e['tool'], s)
    return s


def verdict(ours, others):
    if not others:
        return '只有本包' if ZH else 'this package only'
    best_ws = min(o['ws'] for o in others)
    best_t = min(o['t'] for o in others)
    if ours['ws'] <= best_ws * 1.05:
        mem = ('内存领先' if ours['ws'] < best_ws * 0.95 else '内存持平') if ZH else ('less memory' if ours['ws'] < best_ws * 0.95 else 'memory level')
    else:
        mem = '内存**落后**' if ZH else '**more memory**'
    if ours['t'] > 2 * best_t and ours['t'] - best_t > 0.05:
        mem += ('，时间是最快者的 %.1f 倍' if ZH else ', %.1f times the fastest\'s time') % (ours['t'] / best_t)
    return mem


ZH_FEAT = {
    'in.svg to PNG, same size': 'in.svg 原尺寸转 PNG', 'pdf quality 85': 'pdf，质量 85', 'tiff none': 'tiff 不压缩',
    'tiff deflate predictor': 'tiff deflate 加预测器', 'tiff lzw predictor': 'tiff lzw 加预测器', 'tiff jpeg': 'tiff 内 JPEG',
    'tiff tiles 256': 'tiff 256 瓦片', 'tiff pyramid': 'tiff 金字塔', 'ico 16,32,48,256': 'ico（16、32、48、256）',
    'icns standard sizes': 'icns 标准尺寸', 'png level 1': 'png 1 级', 'png level 4': 'png 4 级', 'png level 9': 'png 9 级',
    'png 256 colours': 'png 256 色', 'png level 10, 1536 wide': 'png 10 级，1536 宽', 'gif same size': 'gif 原尺寸',
    'gif lossy 24, 1536 wide': 'gif 有损 24，1536 宽', 'webp near-lossless 60': 'webp 近无损 60', 'webp lossless effort 9': 'webp 无损力度 9',
    'jpeg progressive': 'jpeg 渐进', 'jpeg optimize': 'jpeg 优化编码表', 'jpeg trellis': 'jpeg trellis 量化',
    'jpeg progressive trellis jpegli tables': 'jpeg 渐进 + trellis + jpegli 量化表', 'jpeg 4:4:4': 'jpeg 4:4:4',
    'jpeg overshoot deringing': 'jpeg 过冲去振铃', 'jpeg -max-size 1MB': 'jpeg `-max-size 1MB`', 'jpeg -min-ssim 0.98': 'jpeg `-min-ssim 0.98`',
    'deep zoom tiles': 'Deep Zoom 瓦片', 'google tiles': 'Google 瓦片', 'zoomify tiles': 'Zoomify 瓦片',
    'crop 3000x2000': '裁取 3000×2000', 'trim': '去边', 'extend 100 mirror': '镜像加边 100', 'flip': '上下翻转', 'flop': '左右翻转',
    'rotate 180': '旋转 180°', 'resize 1536 nearest': '缩到 1536，最近邻', 'resize 1536 libvips method': '缩到 1536，libvips 方法',
    'resize 1536 pillow method': '缩到 1536，Pillow 方法', 'resize 1536 linear light': '缩到 1536，线性光',
    'resize 1536x1536 cover attention': '1536×1536 填满，按注意力裁', 'resize 1536x1536 cover entropy': '1536×1536 填满，按信息量裁',
    'resize 1536x1536 contain': '1536×1536 装入并补边', 'enlarge to 12000 wide': '放大到 12000 宽', 'grayscale linear': '线性光灰度',
    'sharpen mild': '轻度锐化', 'sharpen libvips': 'libvips 式锐化', 'blur 10': '模糊 sigma 10', 'lut warm 9': '查找立方（9 点）',
    'lut 33': '查找立方（33 点）', 'srgb from Display P3': 'Display P3 转 sRGB', 'profile adobe-rgb': '转到 Adobe RGB',
    'keep exif xmp iptc': '保留 EXIF、XMP、IPTC', 'exif fields': '写 EXIF 字段',
    'logo, southeast': '标志，右下角', 'logo tiled, 30 degrees, opacity 0.3': '标志平铺、转 30°、不透明度 0.3',
    'logo 20% wide, multiply': '标志 20% 宽、正片叠底', 'text watermark': '文字水印', 'text watermark tiled': '文字水印平铺',
    'invisible mark, blind': '隐形水印（盲）', 'invisible mark, screen': '隐形水印（抗翻拍）', 'text image, wrapped': '文字成图（换行）',
    'APNG to GIF': 'APNG 转 GIF', 'GIF to APNG': 'GIF 转 APNG', 'animated WebP to GIF': '动图 WebP 转 GIF', 'GIF lossy 24': 'GIF 有损 24',
    'GIF effort 1, frame delay 50': 'GIF 力度 1、帧间隔 50', 'GIF watermark': 'GIF 加水印',
    'info': '信息', 'info, PSD': '信息（PSD）', 'check JPEG': '检查 JPEG', 'check PNG': '检查 PNG', 'compare': '比较', 'stats': '统计',
    'hash': '感知哈希', 'placeholder': '占位图', 'exif': 'EXIF', 'readmark blind': '读隐形水印', 'readmark, no mark': '读隐形水印（没有水印）',
    'pages: 3 photographs to PDF': '3 张照片合成 PDF', 'batch: 10 photographs to 1536 wide': '批量：10 张照片缩到 1536 宽',
    'batch -jobs 1': '批量，`-jobs 1`',
}

GROUPS = {
    'read': ('Reading: each format to a JPEG of 1536 wide', '读取：各格式缩成 1536 宽的 JPEG'),
    'write': ('Writing: the photograph in each format, the same size', '写出：照片原尺寸写成各格式'),
    'option': ('Options, JPEG in and out', '各选项，JPEG 进出'),
    'op': ('Named operations (`Do`, `-op`), JPEG in and out', '按名字的操作（`Do`、`-op`），JPEG 进出'),
    'mark': ('Watermarks and text', '水印与文字'),
    'anim': ('Animation (the GIF of 18 photographs, 768×512)', '动图（18 帧 768×512 的 GIF）'),
    'inspect': ('Commands that only look', '只读取、不生成图像的命令'),
}
out = []
last = None
for (g, feat), es in rows.items():
    if g != last:
        title = GROUPS.get(g, (g, g))[1 if ZH else 0]
        out.append('\n#### %s\n' % title)
        head = ['功能', 'goimgtools', 'libvips', 'sharp', 'Pillow', '其他', '结论'] if ZH else ['Feature', 'goimgtools', 'libvips', 'sharp', 'Pillow', 'Other', 'Verdict']
        out.append('| ' + ' | '.join(head) + ' |')
        out.append('|' + '---|' * len(head))
        last = g
    ours = [e for e in es if e['tool'] == 'goimgtools']
    if not ours:
        continue
    others = [e for e in es if e['tool'] != 'goimgtools']
    show_out = g in ('write', 'anim')
    cells = []
    for c in COLS:
        sel = [e for e in es if column(e['tool']) == c]
        cells.append('; '.join(cell(e, show_out, e['tool'] != c) for e in sel))
    name = ZH_FEAT.get(feat, feat) if ZH else feat
    if g == 'op':
        name = '`%s`' % feat
    out.append('| %s | %s | %s |' % (name.replace('|', '\\|'), ' | '.join(cells), verdict(ours[0], others)))
open(dst, 'w', encoding='utf-8', newline='\n').write('\n'.join(out) + '\n')
