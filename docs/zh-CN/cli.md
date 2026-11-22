[English](../cli.md) | 中文

# 命令行

用于查看和检查图像的子命令（`info`、`check`、`compare`、`stats`、`hash`、`exif`、`formats`）详见[用法](usage.md#小工具查看和检查图片)。

```
goimgtools [选项] 输入文件 输出文件
goimgtools batch [选项] 输入目录 输出目录
goimgtools -text "文字" [选项] 输出文件
goimgtools info [-json] 文件...
goimgtools check 文件...
goimgtools compare [-min-psnr dB] [-ssim] a b
goimgtools stats [-json] 文件...
goimgtools hash [-similar 位数] 文件...
goimgtools placeholder [-components 4x3] 文件...
goimgtools exif [-json] 文件...
goimgtools formats
goimgtools fonts [-all]
goimgtools ops
```

安装：

```bash
go install github.com/whhhh1500/goimgtools/cmd/goimgtools@latest
```

或从源码编译：

```bash
go build -o goimgtools ./cmd/goimgtools
```

```bash
goimgtools -rotate auto -resize 1536x1536 -no-enlarge -flatten white -watermark logo.png -quality 85 in.jpg out.jpg
```

```bash
goimgtools -watermark-text "© 示例" -font-size 40 -text-color white -stroke-width 2 -watermark-tile -watermark-gap 80,60 -watermark-angle -30 -watermark-opacity 0.3 in.jpg out.webp
```

```bash
goimgtools -trim 10 -resize 1200x -extend 20,20,20,20 -background white -sharpen mild in.jpg out.jpg
```

```bash
goimgtools -op median:3 -op normalize -op "tint:#704214" scan.jpg out.jpg
```

各操作按固定顺序执行：旋转 → 翻转 → 去边 → 裁剪 → 缩放 → 扩边 → 灰度化 → 色彩调整 → 模糊 → 锐化 → 各 `-op` 步骤（按指定顺序）→ 铺底色 → 水印。

| 选项 | 说明 |
|---|---|
| `-rotate auto\|度数` | `auto` 按 EXIF 校正方向；数字表示顺时针旋转的度数。角度不是 90 的倍数时画布扩大，四角以 `-background` 填充 |
| `-flip` / `-flop` | 上下 / 左右镜像 |
| `-trim 差值` | 去除与左上角颜色相同的边框；差值 0–255（10 可容忍 JPEG 噪点） |
| `-trim-background 颜色`、`-trim-margin N`、`-trim-line-art` | 要去除的边框颜色（代替左上角颜色）、在找到的范围外保留的像素数，以及直接比较原像素而不先做可滤除噪点的中值滤波（适用于细线条绘图） |
| `-crop X,Y,W,H` | 裁剪 |
| `-resize WxH` | 目标尺寸；可只指定一边（`1536x`、`x1024`） |
| `-fit` | `inside`（默认）、`cover`、`contain`、`outside`、`fill` |
| `-linear-light` | 在线性光下缩放：缩小时细小的亮部细节保持亮度 |
| `-position` | `-fit cover` 保留的部分与 `-fit contain` 放置图像的位置：`center`（默认）、`northwest` … `southeast`；`cover` 还可用 `entropy`（细节最多处）或 `attention`（最引人注意处） |
| `-background 颜色` | `-fit contain` 的填充、`-extend` 的边框以及 `-rotate` 斜向旋转后四角的颜色：任意 CSS 颜色（见下文） |
| `-kernel` | `nearest`、`linear`、`cubic`（默认）、`mitchell`、`lanczos2`、`lanczos3`、`mks2013`、`mks2021`、`box`、`hamming` |
| `-reducing-gap` | 配合 `-resize-method pillow`：Pillow 的 `reducing_gap`（2 并加 `-no-enlarge -fast-shrink` 即 Pillow 的 `thumbnail`） |
| `-resize-method` | `draw`（默认）、`libvips`（与 sharp 的 resize 逐字节一致；sharp 默认核为 `lanczos3`）或 `pillow`（与 Pillow 的 resize 逐字节一致） |
| `-no-enlarge` | 不放大图像 |
| `-fast-shrink` | JPEG 缩小至一半以下、或无 alpha 的有损 WebP 缩小至三分之一以下时，先在解码阶段缩小（配合 `-resize-method libvips` 即 sharp 的 `fastShrinkOnLoad`，所有 WebP） |
| `-extend T,R,B,L` | 四周扩边（单位为像素），颜色由 `-background` 指定 |
| `-extend-with copy\|repeat\|mirror` | `-extend` 的边框改用图像本身：重复边缘像素、重复图像或镜像图像 |
| `-grayscale` | 转为灰度 |
| `-grayscale-linear` | 按线性光的亮度转为灰度，与 sharp 的 `greyscale` 相同 |
| `-modulate B,S,H[,L]` | 亮度系数、饱和度系数、色相度数与明度增量（L\*，0–100），例如 `1.2,0.8,0` |
| `-blur sigma` | 高斯模糊 |
| `-blur-method pillow\|libvips` | `pillow`（默认）：同 Pillow 的 `GaussianBlur`；`libvips`：同 sharp 的 `blur` |
| `-blur-min-amplitude F` | `-blur-method libvips` 截断高斯核的位置，0.001–1（默认 0.2） |
| `-sharpen mild\|SIGMA[,AMOUNT[,THRESHOLD]]` | `mild` 为轻度锐化；否则为反锐化掩模（`AMOUNT` 默认 1.5，`THRESHOLD` 默认 0） |
| `-sharpen SIGMA,libvips[,FLAT,JAGGED,JAGGED_FROM,MAX_BRIGHTEN,MAX_DARKEN]` | sharp 带 sigma 的锐化：只锐化明度，按其曲线（即 sharp 的 m1、m2、x1、y2、y3，默认 1,2,2,10,20） |
| `-flatten 颜色` | 以指定颜色铺底 |
| `-watermark 文件` / `-watermark-text 文字` | 图片水印 / 文字水印（`\n` 表示换行） |
| `-watermark-width N\|N%` | 将水印缩放至 N 像素宽，或主图宽度的 N%（批量处理尺寸不一的图像时水印大小一致） |
| `-gravity`、`-offset X,Y` | 九宫格位置与像素偏移；仅使用这两项时，默认距边缘 24 像素 |
| `-watermark-ratio X,Y` | 按主图宽高的比例偏移 |
| `-watermark-anchor`、`-watermark-pivot` | 锚点、旋转中心：九宫格位置名或 `X,Y` |
| `-watermark-angle 度` | 顺时针旋转角度 |
| `-watermark-opacity 0–1` | 整体不透明度 |
| `-watermark-blend MODE` | 水印颜色与底图的混合方式：over（默认）、multiply、screen、overlay、darken、lighten、color-dodge、color-burn、hard-light、soft-light、difference、exclusion |
| `-watermark-tile`、`-watermark-gap X,Y` | 平铺与间距 |
| `-mark 文字`、`-mark-key 密钥`、`-mark-kind blind\|screen`、`-mark-strength 强度`、`-mark-density 密度` | 1–32 字节的隐形水印，最后加入；用 `goimgtools readmark -key 密钥 文件...` 读回（见[隐形水印](watermark.md)） |
| `-text 文字` | 无需输入文件，直接将文字渲染为图像 |
| `-font`、`-font-size`、`-text-color`、`-text-background`、`-text-width` | 字体（以逗号分隔，可为文件或字体名）、字号、颜色、背景、换行宽度 |
| `-stroke-width`、`-stroke-color` | 描边 |
| `-shadow-color`、`-shadow-offset X,Y`、`-shadow-blur` | 阴影 |
| `-format` | `jpeg`、`png`、`webp`、`gif`、`bmp`、`tiff`；默认由扩展名决定 |
| `-quality N` | JPEG 或有损 WebP 的质量 |
| `-jpeg-optimize`、`-jpeg-progressive` | JPEG：优化编码表；渐进式编码（文件更小，像素不变） |
| `-jpeg-trellis` | JPEG：网格量化（同等观感下文件更小，像素会改变；可与上述两项同时使用） |
| `-jpeg-overshoot-deringing` | JPEG：减轻白底上深色文字与线条旁的振铃，同 mozjpeg（sharp 的 `overshootDeringing`，字节相同）：同一质量下截图与文档好看得多（截图 50 档相当于不开时的 75 档，文件小 25%），文件大几个百分点；照片基本不变 |
| `-jpeg-chroma 420\|422\|444` | JPEG：色度保留的精细程度：420（默认，横纵各一半）、422（横向一半）或 444（全部保留：彩色文字与边缘清晰，文件更大） |
| `-jpeg-quant-table N` | JPEG：量化表 0–8，编号与 mozjpeg 的 `-quant-table` 一致（默认 0 为标准表，3 为 mozjpeg 的默认表）；9 为按 jpegli 方法生成的表，文件最小（见[压缩](compression.md)） |
| `-png-colors N`、`-no-dither` | PNG：减至 N 色（2–256）；关闭抖动 |
| `-ico-sizes 16,32,48,256` | ICO：要写的图标尺寸，图像按比例缩放到每个尺寸以内；不给时原样写（至多 256×256）。输出文件扩展名为 `.ico` 即选 ICO |
| `-max-size 大小` | 文件大小不超过此值（`200KB`、`1.5MB` 或字节数），自动降低质量（JPEG、有损 WebP）或颜色数（PNG 先试无损；GIF）；与 `-animated` 同用时动画报错 |
| `-tile-size N`、`-tile-overlap N`、`-tile-layout 名称` | 输出文件名以 `.dzi`、`.dz` 或 `.zip`（写入 ZIP 压缩包）结尾时写瓦片金字塔（同 sharp 的 `tile`）：瓦片边长（256）、重叠像素（0）与布局：`dz`（Deep Zoom，默认）、`google`、`iiif`、`iiif3` 或 `zoomify`（瓦片写入以输出名去掉 `.dz` 命名的目录） |
| `-skip-larger` | 结果不小于原文件（同格式）时保留原文件；适用于仅做压缩的任务 |
| `-webp-lossless`、`-webp-effort N` | 无损 WebP；力度 1（最快）至 9（文件最小），默认 5。力度也作用于有损 WebP：5 已用上全部手段（5 以上只在透明通道上多做），2 耗时约减半，1 约为三分之一 |
| `-webp-near-lossless` | 近无损 WebP，同 libwebp：按无损编码，但与邻像素本就有差别的像素各通道可略作改动，程度由 `-quality` 定（100 不改，80–99 至多差 1 级，60–79 差 2，40–59 差 4，20–39 差 8，更低差 16；默认 75）；照片比无损小约 37% |
| `-webp-exact` | 精确保留全透明像素的颜色（默认像 libwebp、sharp、Pillow 一样丢掉，带透明的图因此更小） |
| `-webp-alpha-quality N` | 有损 WebP：透明度的质量 1–99，取值降到较少的级数（70 以下 2–16 级），0 与 255 不变；默认（100）精确保留 |
| `-png-level N`、`-gif-colors N`、`-gif-fixed-palette`、`-tiff-compression none\|deflate\|lzw\|jpeg`、`-tiff-predictor` | 各格式专用选项（JPEG 压缩的 TIFF 用 `-quality`） |
| `-gif-lossy N` | GIF：有损压缩，可接受的颜色差 1–255（照片类动画 16–32，带抖动的静态图 8–16；见[压缩](compression.md)） |
| `-animated` | 处理 GIF、WebP 或 PNG 动画（APNG）的每一帧，按输出格式写为 GIF、WebP 或 PNG 动画（APNG，无损）；`-gif-colors`、`-no-dither` 适用于 GIF，WebP 的选项适用于 WebP，`-png-level` 适用于 PNG。输出为其他格式时报错 |
| `-loop N`、`-frame-delay 毫秒[,毫秒...]` | 配合 `-animated`：播放次数（0 为无限）与每帧显示的毫秒数（一个值用于全部帧，或逐帧列出，最后一个用于其余帧） |
| `-op NAME[:ARG,ARG...]` | 按名称执行操作（`goimgtools ops` 列出全部操作，包括内置操作与程序注册的操作）；可多次指定，按指定顺序在锐化之后执行 |
| `-keep-exif`、`-keep-xmp`、`-keep-iptc`、`-strip-icc` | 元数据 |
| `-exif-copyright 文字`、`-exif-artist 文字`、`-exif-description 文字` | 写入这些 EXIF 字段（配合 `-keep-exif` 写进保留的块，否则单独成块） |
| `-density DPI` | 在输出中记录每英寸 DPI 个像素的分辨率（JPEG、PNG、TIFF、BMP）；默认保留输入的分辨率 |
| `-srgb` | 依据色彩配置将颜色转换至 sRGB，输出不含配置（转为 GIF、BMP 时自动执行） |
| `-mem MB`、`-threads N`、`-tmp 目录` | 内存上限（默认 64）、线程数（默认 1，最多为 CPU 核数，见 [CPU 与线程](performance.md#cpu-与线程)）、临时目录 |
| `-max-pixels N` | 可接受与生成的最大图像，单位为百万像素（默认 268，即 16383×16383）；使用 `-animated` 时按所有帧合计 |

## 目录批处理

```bash
goimgtools batch -resize 1920x1920 -no-enlarge -quality 80 -format jpeg 输入目录 输出目录
```

对输入目录中的每幅图像（`.jpg .jpeg .png .webp .gif .bmp .tif .tiff`）执行相同的处理，结果写入输出目录中的同名文件。支持单文件模式的全部选项，另有以下选项：

| 选项 | 说明 |
|---|---|
| `-jobs N` | 并发处理的文件数，默认 3；可为任意值，`0` 表示不限（全部同时处理） |
| `-recursive` | 包含子目录，并在输出目录中重建目录结构 |
| `-overwrite` | 覆盖已存在的输出文件（默认跳过）；输入与输出为同一目录时，须指定此选项才会原地替换 |
| `-quiet` | 仅输出失败信息与汇总 |

- 指定 `-format` 时，扩展名改为对应格式；否则各文件保持原格式。
- 单个文件失败时报告错误，并继续处理其余文件；存在失败时退出码非零。
- 每个文件最多使用 `-mem` 的工作内存和 `-threads` 个核，总量为其乘以并发数。不限并发且文件较多时，内存与 CPU 占用将成倍增加。

实测数据：42 张真实照片（共 2.5 亿像素，20 核），缩放至 1920 并转为 JPEG：

| 并发数 | 耗时 | 峰值内存 |
|---|---|---|
| 1 | 4.6s | 13MB |
| 3（默认） | 1.5s | 19.5MB |
| 8 | 0.87s | 32MB |
| 不限（42 个同时） | 0.83s | 46MB |

颜色按 CSS 的写法：颜色名（`white`、`black`、`steelblue` 等 CSS Color 4 的 148 个）、`transparent`、`#rgb`、`#rrggbb`、`#rrggbbaa`、`rgb(255, 0, 0)`、`rgba(...)`、`hsl(...)`。
