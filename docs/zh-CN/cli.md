[English](../cli.md) | 中文

# 命令行

用于查看和检查图像的子命令（`info`、`check`、`compare`、`stats`、`hash`、`exif`、`formats`）详见[用法](usage.md#小工具查看和检查图片)。

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

各操作按固定顺序执行：旋转 → 翻转 → 去边 → 裁剪 → 缩放 → 扩边 → 灰度化 → 色彩调整 → 模糊 → 锐化 → 各 `-op` 步骤（按指定顺序）→ 铺底色 → 水印。

| 选项 | 说明 |
|---|---|
| `-rotate auto\|度数` | `auto` 按 EXIF 校正方向；数字表示顺时针旋转的度数。角度不是 90 的倍数时画布扩大，四角以 `-background` 填充 |
| `-flip` / `-flop` | 上下 / 左右镜像 |
| `-trim 差值` | 去除与左上角颜色相同的边框；差值 0–255（10 可容忍 JPEG 噪点） |
| `-crop X,Y,W,H` | 裁剪 |
| `-resize WxH` | 目标尺寸；可只指定一边（`1536x`、`x1024`） |
| `-fit` | `inside`（默认）、`cover`、`contain`、`outside`、`fill` |
| `-background 颜色` | `-fit contain` 的填充、`-extend` 的边框以及 `-rotate` 斜向旋转后四角的颜色：`white`、`black`、`transparent`、`#rrggbb`、`#rrggbbaa` |
| `-kernel` | `nearest`、`linear`、`cubic`（默认）、`mitchell`、`lanczos2`、`lanczos3` |
| `-no-enlarge` | 不放大图像 |
| `-fast-shrink` | JPEG 缩小至一半以下时，先在解码阶段缩小 |
| `-extend T,R,B,L` | 四周扩边（单位为像素），颜色由 `-background` 指定 |
| `-grayscale` | 转为灰度 |
| `-modulate B,S,H` | 亮度系数、饱和度系数、色相度数，例如 `1.2,0.8,0` |
| `-blur sigma` | 高斯模糊 |
| `-sharpen mild\|SIGMA[,AMOUNT[,THRESHOLD]]` | `mild` 为轻度锐化；否则为反锐化掩模（`AMOUNT` 默认 1.5，`THRESHOLD` 默认 0） |
| `-flatten 颜色` | 以指定颜色铺底 |
| `-watermark 文件` / `-watermark-text 文字` | 图片水印 / 文字水印（`\n` 表示换行） |
| `-watermark-width N\|N%` | 将水印缩放至 N 像素宽，或主图宽度的 N%（批量处理尺寸不一的图像时水印大小一致） |
| `-gravity`、`-offset X,Y` | 九宫格位置与像素偏移；仅使用这两项时，默认距边缘 24 像素 |
| `-watermark-ratio X,Y` | 按主图宽高的比例偏移 |
| `-watermark-anchor`、`-watermark-pivot` | 锚点、旋转中心：九宫格位置名或 `X,Y` |
| `-watermark-angle 度` | 顺时针旋转角度 |
| `-watermark-opacity 0–1` | 整体不透明度 |
| `-watermark-tile`、`-watermark-gap X,Y` | 平铺与间距 |
| `-text 文字` | 无需输入文件，直接将文字渲染为图像 |
| `-font`、`-font-size`、`-text-color`、`-text-background`、`-text-width` | 字体（以逗号分隔，可为文件或字体名）、字号、颜色、背景、换行宽度 |
| `-stroke-width`、`-stroke-color` | 描边 |
| `-shadow-color`、`-shadow-offset X,Y`、`-shadow-blur` | 阴影 |
| `-format` | `jpeg`、`png`、`webp`、`gif`、`bmp`、`tiff`；默认由扩展名决定 |
| `-quality N` | JPEG 或有损 WebP 的质量 |
| `-jpeg-optimize`、`-jpeg-progressive` | JPEG：优化编码表；渐进式编码（文件更小，像素不变） |
| `-jpeg-trellis` | JPEG：网格量化（同等观感下文件更小，像素会改变；可与上述两项同时使用） |
| `-jpeg-quant-table N` | JPEG：量化表 0–8，编号与 mozjpeg 的 `-quant-table` 一致；默认 0 为标准表，3 为 mozjpeg 的默认表（见[压缩](compression.md)） |
| `-png-colors N`、`-no-dither` | PNG：减至 N 色（2–256）；关闭抖动 |
| `-max-size 大小` | 文件大小不超过此值（`200KB`、`1.5MB` 或字节数），自动降低质量（JPEG、有损 WebP）；与 `-animated` 同用时动画报错 |
| `-skip-larger` | 结果不小于原文件（同格式）时保留原文件；适用于仅做压缩的任务 |
| `-webp-lossless` | 无损 WebP |
| `-png-level N`、`-gif-colors N`、`-gif-fixed-palette`、`-tiff-compression none\|deflate` | 各格式专用选项 |
| `-animated` | 处理 GIF 动画的每一帧，按输出格式写为 GIF 或 WebP 动画；`-gif-colors`、`-no-dither` 适用于 GIF，WebP 的选项适用于 WebP。输出为其他格式，或输入为 WebP 动画、APNG 时报错 |
| `-op NAME[:ARG,ARG...]` | 按名称执行操作（`goimgtools ops` 列出全部操作，包括内置操作与程序注册的操作）；可多次指定，按指定顺序在锐化之后执行 |
| `-keep-exif`、`-strip-icc` | 元数据 |
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

颜色可写作 `white`、`black`、`#rrggbb` 或 `#rrggbbaa`。
