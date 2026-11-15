[English](../cli.md) | 中文

# 命令行

查看和检查图片的子命令（`info`、`check`、`compare`、`stats`、`hash`、`exif`、`formats`）见[用法](usage.md#小工具查看和检查图片)。

```
goimgtools [选项] 输入文件 输出文件
goimgtools batch [选项] 输入目录 输出目录
goimgtools -text "文字" [选项] 输出文件
goimgtools info [-json] 文件...
goimgtools check 文件...
goimgtools compare a b
goimgtools stats [-json] 文件...
goimgtools hash [-similar 位数] 文件...
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

执行顺序固定为：旋转 → 翻转 → 去边 → 裁剪 → 缩放 → 加边 → 灰度 → 调色 → 模糊 → 锐化 → `-op` 各步（按给的顺序）→ 铺底色 → 水印。

| 选项 | 说明 |
|---|---|
| `-rotate auto\|度数` | `auto` 按 EXIF 转正；数字为顺时针度数，90 的倍数以外的角度画布变大，四角用 `-background` |
| `-flip` / `-flop` | 上下 / 左右镜像 |
| `-trim 差值` | 去掉和左上角同色的边，差值 0–255（10 能吃掉 JPEG 噪点） |
| `-crop X,Y,W,H` | 裁剪 |
| `-resize WxH` | 目标尺寸；可只写一边（`1536x`、`x1024`） |
| `-fit` | `inside`（默认）、`cover`、`contain`、`outside`、`fill` |
| `-background 颜色` | `-fit contain` 补边、`-extend` 的边、`-rotate` 斜转后四角的颜色：`white`、`black`、`transparent`、`#rrggbb`、`#rrggbbaa` |
| `-kernel` | `nearest`、`linear`、`cubic`（默认）、`mitchell`、`lanczos2`、`lanczos3` |
| `-no-enlarge` | 不放大 |
| `-fast-shrink` | JPEG 缩到一半以下时在解码阶段先缩小 |
| `-extend T,R,B,L` | 四周加边（像素），颜色用 `-background` |
| `-grayscale` | 变灰度 |
| `-modulate B,S,H` | 亮度系数、饱和度系数、色相度数，如 `1.2,0.8,0` |
| `-blur sigma` | 高斯模糊 |
| `-sharpen mild\|SIGMA[,AMOUNT[,THRESHOLD]]` | `mild` 轻度锐化；或反锐化掩模（`AMOUNT` 默认 1.5，`THRESHOLD` 默认 0） |
| `-flatten 颜色` | 铺底色 |
| `-watermark 文件` / `-watermark-text 文字` | 图片水印 / 文字水印（`\n` 换行） |
| `-watermark-width N\|N%` | 把水印缩到 N 像素宽，或大图宽度的 N%（批量处理大小不一的图时水印一样大） |
| `-gravity`、`-offset X,Y` | 九宫格位置和像素偏移；只用这两项时默认离边 24 像素 |
| `-watermark-ratio X,Y` | 按大图宽高比例偏移 |
| `-watermark-anchor`、`-watermark-pivot` | 锚点、旋转点：九宫格名或 `X,Y` |
| `-watermark-angle 度` | 顺时针旋转 |
| `-watermark-opacity 0–1` | 整体透明度 |
| `-watermark-tile`、`-watermark-gap X,Y` | 平铺和间隔 |
| `-text 文字` | 不要输入文件，直接把文字生成图片 |
| `-font`、`-font-size`、`-text-color`、`-text-background`、`-text-width` | 字体（逗号分隔，文件或字体名）、字号、颜色、背景、折行宽度 |
| `-stroke-width`、`-stroke-color` | 描边 |
| `-shadow-color`、`-shadow-offset X,Y`、`-shadow-blur` | 阴影 |
| `-format` | `jpeg`、`png`、`webp`、`gif`、`bmp`、`tiff`；默认按扩展名 |
| `-quality N` | JPEG 或有损 WebP 的质量 |
| `-jpeg-optimize`、`-jpeg-progressive` | JPEG：优化编码表、渐进式（更小，像素不变） |
| `-jpeg-trellis` | JPEG：网格量化（同样观感下更小，像素会变；可与上两项合用） |
| `-png-colors N`、`-no-dither` | PNG：减到 N 色（2–256）；不抖动 |
| `-max-size 大小` | 文件不超过这个大小（`200KB`、`1.5MB` 或字节数），自动降质量 |
| `-skip-larger` | 结果不比原文件小（同格式）时保留原文件；用于只做压缩的场合 |
| `-webp-lossless` | 无损 WebP |
| `-png-level N`、`-gif-colors N`、`-gif-fixed-palette`、`-tiff-compression none\|deflate` | 各格式的选项 |
| `-animated` | 处理动图 GIF 的每一帧；`-gif-colors`、`-no-dither` 同样适用 |
| `-op NAME[:ARG,ARG...]` | 按名字执行操作（`goimgtools ops` 列出全部：内置的和程序注册的），可以给多个，按给的顺序在锐化之后执行 |
| `-keep-exif`、`-strip-icc` | 元数据 |
| `-srgb` | 按色彩配置把颜色换算成 sRGB，输出不带配置（转 GIF、BMP 时自动） |
| `-mem MB`、`-threads N`、`-tmp 目录` | 内存上限（默认 64）、线程数（默认 1，最多到 CPU 核数，见 [CPU 与线程](performance.md#cpu-与线程)）、临时目录 |
| `-max-pixels N` | 接受和产生的最大图片，单位百万像素（默认 268，即 16383×16383）；`-animated` 时按所有帧合计 |

## 批量处理目录

```bash
goimgtools batch -resize 1920x1920 -no-enlarge -quality 80 -format jpeg 输入目录 输出目录
```

对输入目录里的每张图（`.jpg .jpeg .png .webp .gif .bmp .tif .tiff`）执行同样的处理，写到输出目录的同名文件。单文件模式的选项都能用，另有：

| 选项 | 说明 |
|---|---|
| `-jobs N` | 同时处理几个文件，默认 3；任意数都可以，`0` 表示不限制（全部同时） |
| `-recursive` | 连子目录一起处理，在输出目录里重建目录结构 |
| `-overwrite` | 覆盖已存在的输出文件（默认跳过）；输入输出是同一目录时加它才会原地替换 |
| `-quiet` | 只打印失败和汇总 |

- 指定 `-format` 时扩展名改成对应格式，否则每个文件保持原格式。
- 某个文件失败会报出来，其余继续；有失败时退出码非零。
- 每个文件最多用 `-mem` 的工作内存和 `-threads` 个核，总量是它乘以并发数。不限制并发时文件一多，内存和 CPU 都会成倍上去。

实测 42 张真实照片（共 2.5 亿像素，20 核），缩到 1920 并转 JPEG：

| 并发 | 耗时 | 峰值内存 |
|---|---|---|
| 1 | 4.6s | 13MB |
| 3（默认） | 1.5s | 19.5MB |
| 8 | 0.87s | 32MB |
| 不限制（42 个同时） | 0.83s | 46MB |

颜色写 `white`、`black`、`#rrggbb` 或 `#rrggbbaa`。
