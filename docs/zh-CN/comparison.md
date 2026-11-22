[English](../comparison.md) | 中文

# 与其他工具的对比

本包的每一项能力，都与该项最强的工具对照：同一任务、同一输入，两边的数字都在同一台机器上实测。每一行的结论是**领先**、**持平**或**落后**；落后的项列在文末，写明正在怎么处理，或为什么不做。

测量于 2026 年 11 月 22 日：Windows 10，Intel Core i7-12700K，Go 1.27.1。对手：libvips 8.18.7（其 `vips` 与 `vipsthumbnail` 程序，带 mozjpeg、libpng、libwebp、cgif、librsvg），sharp 0.35.5（Node 24.14.1），Pillow 12.1.1（Python 3.14.3），libwebp 1.6.0（`cwebp`、`dwebp`），jpegli（提交 031a007），gifsicle 1.98，LittleCMS（经 Pillow 的 `ImageCms`），以及 libjxl 的 SSIMULACRA2 与 butteraugli。

- **时间**是整个进程（启动、读取、处理、写出）三次运行中最短的一次；**内存**是操作系统记录的该进程的峰值工作集。只启动不干活时，本包占 7.3MB，`vips` 7.8MB，加载了 sharp 的 Node 56.6MB，加载了 Pillow 的 Python 18.2MB。
- 内存排在时间之前：内存更低、时间相差不到一成的行算领先。
- 除非另有说明，各用一个线程：本包 `-threads 1`，libvips `VIPS_CONCURRENCY=1`，sharp `sharp.concurrency(1)`（libvips 仍在单独的线程上写出，所以它的 CPU 时间多于耗时的地方会一并给出）。
- 输入为 6000×4000 像素：基线 JPEG（4:2:0，7.6MB），同一张图的 PNG（42MB）、有损 WebP、GIF 与未压缩 TIFF。

## 速度与内存

| 任务 | goimgtools | libvips | sharp | Pillow | 其他 | 结论 |
|---|---|---|---|---|---|---|
| JPEG 缩到 1536 宽的 JPEG | 开 `FastShrinkOnLoad`（其他几家也都在解码时缩小）0.10s、9.2MB；不开 0.21s、11.7MB | `vipsthumbnail` 0.14s、48.7MB | 0.17s、86.6MB | 用 `draft` 0.21s、61.6MB；不用 0.34s、142MB | | 领先 |
| JPEG 转 JPEG，同尺寸 | 0.17s、7.9MB | 0.11s（CPU 0.19s）、32.9MB | 0.24s、217MB | 0.25s、204MB | | 内存领先，CPU 时间持平；4 线程时 0.10s、12.7MB 对 0.11s、40.5MB |
| PNG 缩到 1536 宽的 PNG | 0.53s、14.0MB，文件 2.1MB | `vipsthumbnail` 0.36s、40.6MB，4.0MB | 0.43s、86MB，4.0MB | 0.62s、142MB，2.4MB | | 内存与文件领先，时间**落后**（PNG 编码） |
| PNG 转 PNG，同尺寸 | 1.86s、12.5MB、42.2MB（默认的 7 级）；1.40s、9.5MB、43.9MB（6 级） | 1.38s、26.0MB、42.6MB（`filter=all`）；1.16s、25.9MB、66.2MB（它的默认，不滤波） | 1.45s、74MB、42.6MB（`adaptiveFiltering`）；1.21s、74MB、66.2MB（默认） | 1.82s、113MB、42.6MB | | 内存领先；文件一样大时时间**落后**三分之一（4 线程：0.87s、18.1MB，对它不滤波文件的 1.11s） |
| PNG 转 JPEG，同尺寸（PNG 解码） | 0.37s、8.7MB | 0.33s（CPU 0.39s）、25.3MB | 0.44s、210MB | 0.46s、204MB | | 内存领先，时间持平 |
| JPEG 转有损 WebP，质量 75 | 4.02s、21.6MB、3.49MB（默认的力度 5）；2.46s、21.4MB（力度 3）；1.59s、20.2MB、2.79MB（力度 1） | 1.86s、198MB、3.60MB | 1.95s、246MB；6.2s、425MB（力度 6、`smartSubsample`） | 3.80s、314MB | `cwebp` 1.85s、207MB、3.60MB；14.9s、294MB、3.51MB（`-m 6 -af -sharp_yuv`，它的最好设置） | 领先：默认档的文件与 cwebp 最好设置一样小（见画质），只用它 0.27 的时间、0.07 的内存；力度 1 比 cwebp 默认更快，同等 SSIMULACRA2 下文件小 5%，同等 butteraugli 下一样大 |
| PNG 转无损 WebP | 4.91s、27.7MB、28.9MB | 8.89s、752MB、29.8MB | 8.98s、799MB | 10.2s、784MB | `cwebp -lossless` 8.68s、677MB、29.8MB | 三项都领先 |
| 有损 WebP 缩到 1536 宽的 JPEG | 开 `FastShrinkOnLoad` 0.36s、11.8MB；不开 0.41s、11.9MB | `vipsthumbnail` 0.23s（CPU 0.31s）、29.2MB | 0.29s、83.9MB | 0.77s、394MB | | 内存领先，CPU 时间持平（0.33s） |
| 有损 WebP 解码（写成 BMP） | 0.32s、9.0MB | 0.29s、200MB（写成 TIFF） | | | `dwebp` 0.30s、79.0MB | 内存领先，时间持平（多 8%） |
| GIF 缩到 1536 宽的 JPEG | 0.24s、9.9MB | 0.19s、154MB | 0.25s、197MB | 0.20s、45MB | | 内存领先，时间**落后**四分之一（GIF 解码） |
| TIFF 缩到 1536 宽的 JPEG | 0.14s、11.6MB | 0.30s、80.6MB | 0.36s、133MB | 0.37s、236MB | | 领先 |
| JPEG 缩到 1536 宽的 GIF | 0.49s、15.9MB、442KB | | 1.99s、127MB、500KB | 0.58s、142MB、409KB | | 领先 |
| 模糊，sigma 2（JPEG 进出） | 0.27s、12.1MB | 0.15s（CPU 0.22s）、31.8MB | 0.27s、268MB | 0.59s、295MB | | 内存领先，时间**落后** libvips（CPU 时间 0.28s 对它的 0.22s，它分摊在不止一个核上；它的核在峰值的五分之一处截断），与 sharp 持平 |
| 锐化，sigma 2 | 0.35s、12.5MB | 0.98s、34.9MB | 1.07s、219MB | 0.72s、295MB | | 领先 |
| 旋转 90° | 0.31s、14.2MB | 0.17s、97.1MB | 0.33s、267MB | 0.28s、204MB | | 内存领先（七分之一），时间**落后** libvips（它把整张图放在内存里） |
| 旋转 30° | 0.64s、14.1MB | 0.42s（CPU 0.53s）、98.5MB | 0.54s、398MB | 1.42s、376MB | | 内存领先，时间**落后** libvips |
| 灰度 | 0.21s、8.7MB | 0.18s（CPU 0.22s）、30.1MB | 0.28s、220MB | 0.27s、135MB | | 内存领先，CPU 时间持平 |
| 亮度、饱和度与色相 | 0.46s、9.4MB | | 1.14s、230MB | | | 领先 |
| 叠加一个标志 | 0.18s、9.7MB | | 0.30s、217MB | 0.27s、204MB | | 领先 |
| 动图 GIF（18 帧，768×512）缩到 384 宽 | 0.71s、23.0MB、942KB | | 2.03s、75.1MB、1405KB | | gifsicle 0.31s、17.9MB、1416KB（它的默认取样）；0.46s、21.1MB、1428KB（`--resize-method lanczos3 -O3`） | 文件领先（小三分之一）；时间与内存**落后** gifsicle |
| 动图 GIF 转动图 WebP | 0.51s、26.7MB、854KB（力度 1）；1.28s、25.8MB、940KB（默认） | 0.54s、29.9MB、946KB | 0.60s、78.0MB、946KB | 0.45s、35.2MB、1339KB | | 力度 1 时领先 |

四个线程（`-threads 4`、`VIPS_CONCURRENCY=4`、`sharp.concurrency(4)`）：

| 任务 | goimgtools | libvips | sharp |
|---|---|---|---|
| JPEG 缩到 1536 宽的 JPEG | 0.09s、12.3MB（`FastShrinkOnLoad`）；0.12s、16.6MB | 0.13s、86.3MB | 0.16s、95.7MB |
| JPEG 转 JPEG，同尺寸 | 0.10s、12.7MB | 0.11s、40.5MB | 0.24s、223MB |
| PNG 缩到 1536 宽的 PNG | 0.30s、20.0MB | 0.35s、75.4MB | 0.42s、114MB |
| PNG 转 PNG，同尺寸 | 0.87s、18.1MB、42.2MB | 1.11s、34.3MB、66.2MB | 1.19s、82.2MB、66.2MB |
| JPEG 转有损 WebP | 2.08s、20.8MB；0.83s、20.0MB（力度 1） | 1.83s、198MB | 1.89s、246MB |
| PNG 转无损 WebP | 3.94s、33.0MB | 8.11s、752MB | 8.58s、800MB |
| 模糊，sigma 2 | 0.12s、20.1MB | 0.13s、35.3MB | 0.28s、270MB |
| 锐化，sigma 2 | 0.15s、21.3MB | 0.31s、45.4MB | 0.43s、231MB |
| 旋转 90° | 0.22s、18.0MB | 0.17s、99.5MB | 0.33s、267MB |
| 旋转 30° | 0.41s、19.9MB | 0.21s、104MB | 0.38s、399MB |

## 画质

| 能力 | 测试 | 指标 | goimgtools | 其他 | 结论 |
|---|---|---|---|---|---|
| 缩小（Lanczos 3） | 24 张 Kodak 照片缩到 300 宽，以及 6000×4000 的 PNG 缩到 1536 宽，对照精确的 Lanczos 3 卷积（各通道用 32 位浮点缩放，只舍入一次） | 平均 PSNR；单个通道的最大差 | 默认方法：55.7dB、1（Kodak）；54.7dB、1（2400 万像素）。`ResizePillow` 与 `ResizeLibvips` 逐字节给出那两家的像素 | sharp 与 `vips resize`：53.6dB、15；54.0dB、2。Pillow：56.2dB、19；57.7dB、1 | 持平：都在肉眼可见的范围之外；默认方法的最大误差最小，Pillow 的平均值最好（它的像素在本包是一个选项） |
| JPEG 编码 | 24 张 Kodak 照片、7 档质量；达到同一分数所需的文件大小，以全开的 mozjpeg（trellis、扫描优化、量化表 3、过冲去振铃）为基准 | 同等 SSIMULACRA2 / 同等 butteraugli（3 范数）下的文件大小 | `Progressive`、`Trellis`、`QuantTableJpegli`：小 10.7% / 9.8%。用 mozjpeg 的量化表 3：小 1.8% / 1.8%。默认（与 libjpeg-turbo 逐字节相同）：大 11.5% / 6.5% | jpegli：小 6.7% / 13.0%。libvips 默认：大 10.1% / 8.0% | 领先 mozjpeg；对 jpegli，按 SSIMULACRA2 领先 4.0 个点，按 butteraugli **落后** 3.2 个点 |
| 有损 WebP 编码 | 同样 24 张照片、6 档质量，以 `cwebp -m 6 -af -sharp_yuv` 为基准 | 同上两项 | 默认：大 0.7% / 小 0.2%。力度 1：大 4.2% / 6.6% | `cwebp` 默认：大 9.9% / 6.5% | 与 libwebp 的最好设置持平，用时是它的四分之一 |
| 色彩管理 | 覆盖整个颜色立方体的网格图，分别带 Display P3、Adobe RGB 与两个查找表 CMYK 配置（RSWOP、Chemical proof），转换到 sRGB | 与 LittleCMS（感知意图）的差 | 最大差 1；0.7% 到 1.7% 的值有差，没有差超过 1 的 | 以 LittleCMS 为准 | 持平 |
| 无损压缩后的大小（PNG、GIF、WebP） | 见[压缩](compression.md) | 文件大小 | 17 张 PNG：9 级比 7 级小 13.35%，oxipng 10.2 `-o max` 小 12.5%；10 级比 ECT 0.9.5 `-9` 小 0.23%。三个动图 GIF：比 gifsicle `-O3` 小 0.2–0.45%。无损 WebP：比 `cwebp -lossless` 小 2.8%（上表） | oxipng、ECT、gifsicle、cwebp | 领先 |

## 功能

各家能读（R）、能写（W）的格式。libvips 指上面那个构建（它的其他加载器是可选的，该构建里没有）；Pillow 的清单取自 `Image.OPEN` 与 `Image.SAVE`。

| 格式 | goimgtools | libvips / sharp | Pillow |
|---|---|---|---|
| JPEG、PNG、WebP、GIF、TIFF | R W | R W | R W |
| 动图 | GIF、WebP、PNG（APNG），R W | GIF、WebP，R W（没有 APNG） | GIF、WebP、APNG，R W |
| BMP、ICO、ICNS、TGA、QOI | R W | — | R W |
| PNM（PBM、PGM、PPM、PAM、PFM） | R W | — | R W |
| PSD、PSB | R | — | R（PSD） |
| CUR、MPO（第一张图） | R | — | R；MPO W |
| SVG | R（自己的渲染器） | R（librsvg） | — |
| PDF | W | —（可选） | W |
| 缩放查看器用的瓦片（Deep Zoom、Zoomify、Google） | W | W | — |
| AVIF、HEIC | — | R W | AVIF R W |
| JPEG XL | — | —（可选） | — |
| JPEG 2000 | — | —（可选） | R W |
| TIFF：写瓦片与金字塔 | —（能读：普通 TIFF 与 BigTIFF，条带与瓦片、金字塔的第一层；1、2、4、8、16 位的灰度与调色板，灰度带透明，带或不带透明的 RGB，CMYK、Lab、浮点；LZW、Deflate、PackBits、CCITT、JPEG、zstd、WebP） | R W（Windows 版不带 zstd） | R W（不写瓦片；Windows 版不带 zstd；不读浮点 RGB 与浮点 Lab） |
| DDS、PCX、SGI、XBM、DIB | — | — | R W |
| XPM、FITS、FLI、IM、MSP、SPIDER、BLP、DCX、FTEX、GBR、IMT、MCIDAS、PCD、PIXAR、SUN、WMF、EPS | — | — | R（部分 W） |
| UltraHDR（带增益图的 JPEG） | R（只读图像本身） | R W | R（只读图像本身） |

| 能力 | goimgtools | libvips / sharp | Pillow |
|---|---|---|---|
| 操作 | sharp 的全部操作（各种核与适配方式的缩放、裁取、加边、去边、各种混合模式的叠加、旋转、仿射、翻转、锐化、中值、模糊、膨胀、腐蚀、铺底、去底、gamma、反相、归一化、CLAHE、卷积、阈值、布尔、线性、通道重组、调色、着色、灰度、通道），带 `…Libvips` 选项时像素与它相同；Pillow 的 `ImageOps`、`ImageEnhance`、`ImageFilter` 的核与 `transform`，像素与它相同 | 自己的 | 自己的 |
| 缺少的操作 | 颜色查找立方（`Color3DLUT`）、逐像素表达式（`ImageMath`）、填充（flood fill）、弧与扇形、噪声图；CMYK 或 Lab 输出；全流程保持 16 位（只有默认缩放保持） | | |
| 元数据 | EXIF 读取并解析、保留、或写入文字字段；XMP 读取与保留；ICC 保留、移除、转换到 sRGB 或某个 RGB 配置；分辨率。没有 IPTC | EXIF、XMP、ICC、IPTC 保留；EXIF 字段、XMP、ICC 可设置 | EXIF 逐标签读写，XMP 读取，IPTC 读取，ICC |
| 文字 | 字体按顺序回退，按字体的表格整形（HarfBuzz 的移植），从右向左，换行，对齐，描边，阴影，彩色表情（COLR 第 0 版、位图）。没有竖排，不能选字体特性，没有标记语言 | Pango：字体回退、整形、从右向左、换行、对齐、两端对齐、标记语言；没有描边与阴影 | FreeType 与 Raqm：一次一个字体，整形、字体特性、竖排、可变字体、描边；换行由调用方自己做 |
| 水印 | 图片与文字，平铺、旋转、不透明度与混合；能扛住重新压缩的隐形水印，凭密钥读回 | 叠加 | 粘贴 |
| SVG | resvg 的 1,801 个测试通过 1,327 个（不含文字的 1,414 个通过 1,303 个） | librsvg：1,099（1,073） | — |
| 内存上限、流式处理 | 每张图一个上限（默认 64MB），按行流式处理，放不下的进临时文件 | 操作允许时流式，没有上限 | 整张图在内存里 |
| 指纹、占位图、检查 | 感知哈希、BlurHash、ThumbHash、SSIM、统计、损坏文件检查 | 统计 | 统计 |

## 落后的项

| 哪一项 | 实测的差距 | 状态 |
|---|---|---|
| PNG 编码时间 | 文件小 0.8% 的情况下，比 libpng（libvips，`filter=all`）多用三分之一的时间：1.86s 对 1.38s | 待处理 |
| 模糊 | 单线程 0.27s，libvips 0.15s（CPU 时间 0.28s 对 0.22s：libvips 即使设为一个线程，解码和编码也在别的核上进行）；四线程持平（0.12s 对 0.13s）。它的高斯核在峰值五分之一处截断；本包的 `BlurLibvips` 逐字节用同一个核，要 0.73s。横向方框遍改为从整行的累加和取每个窗口的和之前是 0.31s | 待处理 |
| 旋转 90° | 0.31s 对 0.17s（把每一轮的行写成一整块之前是 0.68s） | 待处理：libvips 把整张图放在内存里（97MB）；本包经一个可能落到文件上的暂存区来转 |
| 其他角度的旋转 | 0.64s 对 0.42s | 待处理 |
| GIF 解码 | 2400 万像素的 GIF：0.24s 对 0.19s | 待处理 |
| 动图 GIF 缩放 | gifsicle 快一倍（0.31s 对 0.71s；它用 Lanczos 方法加 `-O3` 时 0.46s）、少用 5MB（17.9MB 对 23.0MB）；它的文件大一半。本包的时间里，三分之一用于为所有帧选出并细化一个调色板、带抖动地把像素映射过去，四分之一用于把每帧按十五种方式试编码取最小者——文件小三分之一就来自这里。内存里 7MB 是程序本身，6MB 是在用的数据（当前画面、颜色统计、处理中的一帧），其余是 Go 的垃圾回收在回收前允许堆增长的余量。让它更早回收（GOGC=25）可把这项降到 18.6MB，但实测会让流式处理的各项任务各多占 1 到 2MB，所以没有这样设；gifsicle 以 8 位索引保存各帧，不做真彩色处理 | 待处理：GIF 输出还没有更快的档位（少试几种、少细化）；总分配量已从 48MB 减到 18MB，但峰值没有因此下降 |
| JPEG 按 butteraugli 对 jpegli | 同等 butteraugli 下文件大 3.2 个点（同等 SSIMULACRA2 下小 4.0 个点） | 待处理 |
| TIFF 的各种变体 | 不写瓦片与金字塔。BigTIFF、CMYK、Lab、浮点、2 位与 4 位、灰度带透明、zstd 与 WebP 压缩现在都能读 | 待处理（读取已补齐） |
| DDS、PCX、SGI、XBM、DIB、XPM、FITS | 不读 | 待处理 |
| JPEG 2000 | 不读 | 待处理 |
| IPTC | 不读也不保留 | 待处理 |
| 文字：竖排、字体特性、可变字体、`Text()` 里的 COLR 第 1 版 | 没有（SVG 文字有竖排、可变字体与 COLR 第 1 版） | 待处理 |
| `Color3DLUT`、`ImageMath`、填充、弧、噪声；CMYK 与 Lab 输出；全流程 16 位 | 没有 | 待处理 |
| AVIF、HEIC、JPEG XL | 不读不写 | 有意不做：它们的编解码器转成 Go 之后，编译和运行占用的内存远超本包给自己定的限度；已于 2026 年 11 月 21 日移除 |
| FLI、IM、MSP、SPIDER、BLP、DCX、FTEX、GBR、IMT、MCIDAS、PCD、PIXAR、SUN、WMF、EPS | 不读 | 不做：上世纪八九十年代的格式，几乎遇不到；WMF 与 EPS 各需要一个自己的解释器 |
| 读取 PDF | 没有 | 不做：需要一个 PDF 解释器；libvips 与 Pillow 不带 Poppler 或 PDFium 时也没有 |

得出这些数字的脚本在 [`tests/compare`](../../tests/compare)。
