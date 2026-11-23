[English](../CHANGELOG.md) | 中文

# 变更记录

## 未发布

### 新增

- `ICOOptions.CompressionLevel` 与 `ICNSOptions.CompressionLevel`（命令行 `-png-level`）：图标文件中 PNG 图像的级别，不设时仍为 9。从 6000×4000 的 JPEG 写到 1024 的各标准尺寸：9 级 1.56 秒、1.90MB，7 级 0.36 秒、2.08MB（Pillow 0.43 秒、2.33MB）。
- 能读更多种 TIFF：BigTIFF（8 字节偏移，两种字节序；颜色配置、XMP、分辨率与 EXIF 块的读法同普通 TIFF，所以 `AutoOrient()` 会按它的方向转正）、2 位与 4 位的灰度和调色板、灰度带透明（8 位与 16 位，预乘或不预乘，以黑或以白为零）、CMYK（8 位；16 位降为 8 位），CMYK 经文件自带的配置转成 sRGB，没有配置时用无配置 CMYK JPEG 所用的那个 CMYK 配置。核对：灰度与调色板对照 Pillow（像素相同），CMYK 转换对照 LittleCMS（255 级中相差不超过 1），libvips 写出的十三种 BigTIFF（条带、瓦片、金字塔、各种压缩、16 位、CMYK、透明、调色板）对照内容相同的普通 TIFF（像素相同）。
- TIFF 再增：zstd 压缩的块（压缩方式 50000，libtiff 的编号；用 Go 标准库的解码器，拷到 `internal/zstd`，窗口不超过一个块、最大 8MB）与 WebP 图像作块（50001；有损或无损，带或不带透明的 RGB，像素与 libvips 读出的相同）；浮点样本（16、32、64 位；灰度、RGB、带透明的 RGB；横向预测器与 libtiff 的浮点预测器）按 0 到 1 的线性光读取（libvips 如此读，本库读 PFM 也如此），转成 16 位 sRGB（libvips 写出的四种文件与 libvips 读出的逐像素相同）；CIELAB（8 位、16 位与浮点，以及 ICC 的偏移编码）经 D50（libtiff 与 Photoshop 给 Lab TIFF 定的白点）转成 sRGB，与公式相差不超过半级（LittleCMS 在表里插值，与公式最多差 15；libvips 自己写的 Lab 是相对 D65 的，读出来与 libvips 的结果最多差 17）。Windows 上的 libvips 8.18.7 与 Pillow 12.1.1 都不读 zstd 压缩的 TIFF，Pillow 也不读浮点 RGB 与浮点 Lab。
- IPTC：`KeepIptc()`（`-keep-iptc`）把输入的 IPTC 记录（新闻图片的元数据：说明、关键词、作者、版权声明）保留到 JPEG 与 TIFF 输出；`Info.HasIptc`、`inspect.ReadIptc`（记录里的各数据集，同 sharp 对 TIFF 给出的 `metadata().iptc`），自定义格式可用 `EncodeOptions.Iptc` 与 `pipeline.Meta.Iptc`；`goimgtools info` 会标出。可从 JPEG（APP13 段里 Photoshop 的 0x0404 资源，单段或跨多段）、TIFF（标签 33723，字节型或 Photoshop 写的 LONG 型，包在 Photoshop 资源里的也认）和 Photoshop 文件读取；写入 JPEG 时按 Photoshop 的写法（sharp 从输出读到的字节与从输入读到的相同），写入 TIFF 时是记录本身。默认丢弃，同 EXIF 与 XMP。（libvips 8.18.7 把 JPEG 的记录拷到 TIFF 时只留下四分之一：实测文件里 128 字节剩 32 字节。）
- 新增六种可读格式（都不写），每种都逐条读取，不论图像多大只占几百 KB：**PCX**（`.pcx`：每平面 1 到 8 位、1 到 4 个平面、游程压缩或不压缩，文件头里的调色板或文件末尾的 256 色调色板；文件头调色板全黑时按 EGA 颜色）、**SGI**（`.sgi`、`.rgb`、`.rgba`、`.bw`：1 到 4 个通道，8 位或 16 位，原样或游程压缩，自下而上的行就地读取）、**XBM**（`.xbm`：置位为黑，X11 的画法、ffmpeg 的写法；Pillow 的读写正相反）、**XPM** 第 3 版（`.xpm`：每像素 1 到 4 个字符，最多 65536 色，`None` 透明，颜色按 X11 的读法：`#rgb` 的每位是高位，支持其颜色名）、**DDS**（`.dds`，顶层图像：任意通道掩码的未压缩格式、DXT1/3/5、ATI1/2，以及 DX10 头下的 BC1 到 BC5 与 BC7；50 个文件与 Pillow 逐像素相同）、**FITS**（`.fits`、`.fit`、`.fts`：主单元或第一个图像扩展里的图像，8 到 64 位整数与 32、64 位浮点，带 BZERO 与 BSCALE，按标准的大端读取——Pillow 12.1.1 把 16、32 位的字节读反且忽略 BZERO——8 位出 8 位，其余限幅到 16 位）。逐文件对照 Pillow 与 ffmpeg（见各包的测试）；损坏文件报错，内存不超出上限。
- `ColorLUT(cube)`（`-lut file.cube`），同 Pillow 用 `Color3DLUT` 的 `Image.filter`，逐字节相同：每个像素取颜色查找立方里对应的颜色，在周围八个点之间插值。立方（`LUT3D`）由 `ParseCube` 从 Adobe / DaVinci Resolve 的 `.cube` 文件读入（每边 2 到 65 个点，支持 `DOMAIN_MIN` 与 `DOMAIN_MAX`），或由 `NewLUT3D` 从函数生成（即 Pillow 的 `Color3DLUT.generate`）。用七个立方对照 Pillow 12.1.1 核对，含超出 0 到 1 与小于 0 的取值。
- `Draw` 新增三种图形，同 Pillow 的 ImageDraw：`Arc(cx, cy, rx, ry, start, end)`（沿椭圆的一段线）、`Pie(...)`（连到圆心的扇形）、`Chord(...)`（两端用直线封闭的弓形），角度以度计、从三点钟方向顺时针；按名字是 `arc`、`pie`、`chord`，可用于 `Do` 与 `-op`。
- `Noise(width, height, NoiseOptions{Mean, Sigma, Seed})`：高斯噪声的灰度图，同 Pillow 的 `Image.effect_noise` 与 libvips 的 `gaussnoise`（十二个随机数之和，同 libvips）；同一种子在各处理器上、不论怎样切条结果都相同；按行现生成，不占与尺寸成比例的内存。
- `FloodFill(x, y, colour, FloodFillOptions{Threshold, Border})`，同 Pillow 的 `ImageDraw.floodfill`，逐像素相同：把某点周围同色的区域（与它上下左右相连、颜色相同或在阈值之内的像素；给了边界色时，则是到该颜色为止的全部像素）涂成指定颜色。图像放在工作存储里（超出内存份额的部分落临时文件），内存里每像素两位，2400 万像素 6MB；区域靠对各行上下来回扫描直到不再变化求出，所以形状复杂只费时间不费内存。用 16 个用例对照 Pillow 12.1.1 核对（阈值、边界色、螺旋通道、带透明的图）。
- TIFF 可按瓦片写出、可写成金字塔，同 libvips 与 sharp：`TIFFOptions.Tile`（`-tiff-tile N`：边长 N 像素的方形瓦片，16 的倍数，各自独立压缩——不压缩、Deflate、LZW、可带预测器，或共用表的 JPEG）与 `TIFFOptions.Pyramid`（`-tiff-pyramid`：图像之后依次写它的二分之一、四分之一……直到能放进一个瓦片，每层一个标为「缩小图」的目录），即图像服务器（IIIF）与缩放查看器所读的格式。样本为 8 位（灰度、RGB、带透明的 RGB）。各层与 libvips 的金字塔逐像素相同（每个样本取四个的平均），libvips、Pillow 与 `x/image` 读出的像素与本包相同。写的时候每层在内存里留一行瓦片：6000×4000 的 JPEG 写成金字塔，Deflate 用 1.41s、21.7MB（libvips 1.31s、54.9MB），JPEG 瓦片用 0.30s、30.6MB（libvips 0.16s、39.6MB）。
- `GIFOptions.Effort`（`-gif-effort N`），1（最快）到 9，同 `WebPOptions.Effort` 与 sharp 对 GIF 的 `effort`；0 即 9，就是一直以来的做法，逐字节不变。选出的颜色向像素靠拢的遍数：7 起三遍、4 到 6 一遍、更低不做；只有 9 才把动图的每帧按五种方式试编码（码表满了就清，或推迟清）取最小。实测 18 张照片的动图缩到 384 宽：9 档 0.70s、942KB，5 档 0.38s、946KB，1 档 0.30s、947KB（gifsicle：0.31s、1416KB）。
- [与其他工具的对比](comparison.md)：每项能力对照该项最强的工具（libvips 8.18.7、sharp 0.35.5、Pillow 12.1.1、cwebp、mozjpeg、jpegli、gifsicle、LittleCMS、librsvg），同一任务同一输入，整个进程的时间与峰值内存，按 SSIMULACRA2 与 butteraugli 比画质，各家的格式与功能，以及落后项的清单；测量脚本在 `tests/compare`。
- SVG 文字绘制 COLR 第 1 版的彩色字形（Noto Color Emoji 即此格式，与 Chrome 一致）：按其绘制图——裁剪用的字形轮廓、纯色、线性 / 径向 / 扫描渐变（颜色按预乘插值；色标可超出 0 到 1，延伸方式 pad、repeat、reflect）、变换（平移、缩放、旋转、斜切，可绕中心）、裁剪框与合成（各混合模式；Porter-Duff 模式中 clear、源、目标照做，其余按源覆盖），前景色为文字的填充色。含此类字形的字体会被选作表情的彩色字体。此类字体的可变数据不应用。`Text()` 绘制的文字仍以单色绘制它们。
- 可变字体（含 `fvar` 表：带 `gvar` 的 TrueType 轮廓或 CFF2）中的 SVG 文字按样式对应的实例绘制，与 Chrome 的设定一致：`font-weight` 对应 `wght` 轴，`font-stretch`（关键字与百分比，现已读取）对应 `wdth`，`font-style: oblique`（及其角度）对应 `slnt`，`italic` 对应 `ital`，字号对应 `opsz`（光学尺寸，CSS 的默认行为），`font-variation-settings` 优先于以上各项；轮廓、前进宽度与整形都随之变化（原先按字体的默认实例绘制，只有一个可变字体文件的字族，粗体也画成常规）。只有这类字体才读取轮廓及其变化数据，并计入内存上限。
- SVG 竖排文字：text 元素上的 `writing-mode` 为 `tb`、`tb-rl`、`vertical-rl`、`vertical-lr` 时（可继承，同 Chrome；写在 tspan 上不生效），从 `y` 向下排，`x` 处为其中线：多数文字的字母横躺；中日韩文字及其标点、全角字符与表情直立居中，按字体的竖排度量（`vhea` 与 `vmtx`，字形顶端在笔位下方 top bearing 处，同 HarfBuzz 为 Chrome 放置的位置；没有竖排度量的字体按字高与上升高度）前进与放置，括号与破折号横躺；`text-orientation` 为 `upright` 时全部直立，为 `sideways` 时全部横躺。`x`、`y`、`dx`、`dy`、`text-anchor`、`rotate`、装饰线与文字的包围盒都随这一行。路径上的文字仍为横排。
- SVG 路径上的文字按 Chrome 的方式排：支持 SVG 2 中 `textPath` 的 `path` 属性（取解析到第一个错误之前的部分，优先于 `href`）；路径上字符的绝对 `x` 即沿路径的距离（text 上或 textPath 内 tspan 上的 `x` 会让文字沿路径移动，原先被忽略）；写在 textPath 自身上的 `x`、`y` 不是它的属性，不再采用（原先采用）。
- SVG：CSS 的滤镜函数（`filter="blur(2px) drop-shadow(4px 5px 6px blue) grayscale(0.5)"`：blur、brightness、contrast、drop-shadow、grayscale、hue-rotate、invert、opacity、saturate、sepia，也可夹 url()），各按 Filter Effects 规范给出的等效原语依次计算，在 sRGB 中进行，区域为元素的包围盒（含描边）加上各函数所及的范围（url() 引用的滤镜裁到其自身区域），与 Chrome 相同：drop-shadow 的第三个长度即模糊的标准差，不带单位的长度按像素；Chrome 视为无效的写法（blur 用百分比、角度不带单位、参数间用逗号）则不加滤镜。resvg 的滤镜函数测试 44 个通过 36 个（原 15 个；其余只差在标签字体）。
- SVG 样式表应用媒体查询成立的规则，条件按浅色方案、屏幕显示判断（`@media (prefers-color-scheme: light)`、`screen`、`not print`，支持 `and`、`not` 与逗号）；涉及尺寸或其他特性的查询视为不成立。原先所有 `@media` 中的规则都不用（深色方案的规则，例如为 GitHub 准备的图片所带的，仍然不用）。
- SVG：绘制 `foreignObject` 中的 HTML（mermaid 的图表标签就是这样写的，且没有对应的 SVG 文字）：文字按其 CSS 给出的字体、字号、字重与颜色（同 HTML 用 `color` 属性；文档的样式表也作用于这些元素），由 `br`、段落、标题等块元素分行（块的外边距同浏览器；`line-height: normal` 按该行实际用到的字体计算，回退字体更高时行也更高，同 Chrome），按 `text-align` 对齐（可从所在的 SVG 元素继承），行内元素与块的背景画在文字下面，HTML 里的 SVG 文档按 SVG 绘制，超出框的部分裁掉；浏览器不显示的元素（style、script、head）不画；比框宽的行在 `white-space` 允许时（如 mermaid 长标签的 `break-spaces`）于空格处、字母间的连字符后以及中日韩文字的字与字之间折行（闭标点前不断），被行内元素分开的同一个词不在中间断开。在取自开源项目的 7 个 mermaid 图表上，标签位置与 Chrome 一致（原先没有标签）；draw.io 的标签仍用其导出时附带的 SVG 文字。
- SVG 文字按 CSS 的规则为 `font-weight` 选取字族中的字体，与 Chrome 相同：600 用字族的半粗体（Segoe UI Semibold），300 用细体，900 用特粗体，按字体为各字重取的名字（Thin、ExtraLight、Light、Semilight、Medium、Semibold、Bold、ExtraBold、Black）依 CSS 的尝试顺序查找；`bolder` 与 `lighter` 按 CSS 由父元素的字重推算。原先只分常规与粗体（600 到 900 用粗体，其余用常规体）。
- SVG（对照 resvg 测试集与 Chrome 的画法）：XML 声明为 UTF-8 以外编码的文档，按浏览器认的编码名读取（Windows、ISO 8859、KOI8 与 Mac 的单字节编码，GBK 与 GB18030、Big5、Shift_JIS、EUC-JP、ISO-2022-JP、EUC-KR；iso-8859-1 与 us-ascii 同浏览器一样按 windows-1252 读；不认识的按 UTF-8 读），以及 UTF-16 的文档（这些原先都读不了）；doctype 声明的文本实体（Illustrator 会这样写）；`transform-origin`（关键字、长度与视口的百分比，属性或 CSS 皆可；用于元素、裁剪路径、渐变与图案），CSS 的 `transform` 属性（各函数与单位、`none`；优先于属性），根元素的 `transform`（默认绕中心，同 Chrome）；`context-fill` 与 `context-stroke`（SVG 2），用于标记与 `use` 绘制的内容；`paint-order` 也安排标记的先后（同一部分出现两次或含未知词的值视为无效）；根元素的 CSS 背景色（`background-color`、`background`，mermaid 与 draw.io 会这样写），同浏览器一样画在最底层；`image` 元素中的 SVG 图片（data URI，也可用 gzip 压缩），按其在画面上的大小绘制，最多嵌套四层，各层图片的像素合计受同一上限；样式表中的属性选择器（`[x]`、`=`、`~=`、`|=`、`^=`、`$=`、`*=`、` i`）；type 不是 CSS 的 style 元素不读；`style` 属性中的注释；标记的 `orient` 可用 `rad`、`grad`、`turn`；`stop-color`、`stop-opacity`、`opacity` 的 `inherit`；渐变的 `color-interpolation: linearRGB`。一并修正：渐变色标的样式按渐变所在位置计算（原先 `currentColor` 取被填充元素的）；径向渐变引用线性渐变（或反之）时继承色标、单位、变换与延展方式，不继承对方的几何属性；无法解析的 `gradientTransform` 或 `patternTransform` 改用所引用渐变的，压扁一切的变换不用；其他命名空间的属性（Inkscape 的 `sodipodi:cx`、任何声明过的前缀）不再当作同名属性；`use` 引用 `symbol` 或 `svg` 元素时其透明度生效，被 `use` 绘制的 `svg` 元素保留自己的 `x`、`y`；`display: none` 隐藏的内容不计入包围盒，根元素为 `display: none` 时什么都不画；没有色标的渐变用其后的备用颜色绘制。在 resvg 的测试集上（按其参考图尺寸渲染，平均差小于 2 算通过），1,801 个测试通过 1,223 个（原 1,134）；不计用其自带字体的文字测试，1,414 个通过 1,202 个（原 1,113）：transform-origin 21/23（原 3），context 15/18（原 3），paint-order 12/14（原 6），image 39/56（原 25；其余多为引用外部文件，不读取），linearGradient 50/51（原 42），stop 32/32（原 29），style 21/21（原 18），use 42/42（原 39），svg 39/48（原 34）。用于检查的 399 个图标与图表 SVG 文件像素与原先相同。命令行程序增大 0.75MB（16.9MB 到 17.7MB），主要是编码表。
- SVG：`mix-blend-mode`（按 W3C《合成与混合》的十六种模式及 plus-lighter）与 `isolation`，与 Chrome 一样只从 CSS 读取；feBlend 也支持 CSS 的模式；裁剪路径自身的 `clip-path` 及其子元素各自的 `clip-path`，引用自身的裁剪路径（第二次视为无裁剪，同 Chrome），`clip-rule` 按裁剪路径所在位置继承；根 `svg` 元素的 `clip-path`、`mask` 与 `opacity`。在 resvg 的测试集上（按其参考图尺寸渲染），clipPath 测试通过 56 个中的 51 个（原 36 个；其余 5 个是用其自带字体的文字），mix-blend-mode 21/21（原 5），isolation 2/2（原 1），feBlend 11/11（原 9）。
- 彩色表情（`Text()`、水印与 SVG）：由图层构成的字形（COLR 表第 0 版及其 CPAL 调色板，如 Windows 的 Segoe UI Emoji）按其颜色绘制，取文字颜色的图层用文字颜色；位图彩色字体（CBDT，如 Linux 上的 Noto Color Emoji；sbix，如 Apple Color Emoji）的图片按文字大小缩放（同 Pillow 的 Lanczos 滤镜；SVG 中按 image 元素的方式取样）；文字的透明度作用于全部；默认以表情显示（Unicode 的 Emoji_Presentation）或由 U+FE0F 要求以表情显示的字符，取第一个有其彩色字形的字体；表情序列（U+200D 连接的、带肤色的、键帽、旗帜）按字体整形为其对应的单个字形。Linux 与 macOS 的后备字体加入 Noto Color Emoji 与 Apple Color Emoji（已安装时）。与 Chrome 对照，SVG 中含 ❤️ ✨ 😀 👍🏽 👨‍👩‍👧 ☕（Segoe UI Emoji）的一行文字字迹范围相同，Noto Color Emoji 的两行（一行用 `rotate` 旋转、一行半透明）也相同；`Text()` 中 Noto Color Emoji 相差不超过两个像素。
- 文字按字体自带的表格整形（`Text()`、水印与 SVG），用 HarfBuzz 整形器的 Go 移植（`github.com/go-text/typesetting`）：印度系文字（天城文、孟加拉文、泰米尔文等）、泰文、老挝文、藏文、缅甸文、高棉文等字母需连写、重排或叠放的文种，阿拉伯文与波斯文的全部连字与标记，以及任何文种中加在字母上的组合符号，都按字体的规定绘制；拉丁文、中日韩文等仍逐字排版，输出不变；但字体默认对拉丁文做连字或上下文变形时（Calibri、Cambria、Consolas、Corbel、Candara、Constantia 如此，Arial、Segoe UI、Times New Roman 不然），其文字也整形，与 Chrome 相同（含 ffi、fi 的一行 Calibri 与 Chrome 字迹范围相同，原先宽三个像素）。整形时字体经一份只列整形所需表格的表目录读取，轮廓与图片永不读取（微软雅黑整体解析需 39MB，这样只需 0.8MB）；这些表格按其大小的三倍计入内存预算。与 Chrome 用同一字体对照，印地语、孟加拉语、泰米尔语、泰语、阿拉伯语、波斯语各行的字迹在横向相差不超过一个像素（`Text()` 与 SVG 皆然）。命令行程序增大 2.8MB（13.9MB 到 16.8MB）。
- 从右向左书写的文字（`Text()`、水印与 SVG）：阿拉伯文与波斯文字母按前后字母连写（按字体的表格，见上一条；字体表格无法读取时用 Unicode 的表现形式，lam 与 alef 合成连字；零宽不连字符断开连写，零宽连字符保持连写），每行按完整的 Unicode 双向算法排成所见次序（显式嵌入、强制方向与隔离，成对括号，镜像字形；移植自 golang.org/x/text 所收的 Unicode 参考实现，Unicode 17 的 BidiTest.txt 全部 770,241 例、BidiCharacterTest.txt 全部 91,707 例顺序都对），层级在整段上解析后再断行，段落方向取隔离之外的第一个强字符（SVG 中按 `direction` 属性，它也翻转 `text-anchor`）。Windows 的后备字体加入带阿拉伯文与希伯来文的字体（Segoe UI、Tahoma）。与 Chrome 对照，阿拉伯文、希伯来文与混排各行（阿拉伯文夹拉丁文与数字、句末带括号短语、含隔离、强制方向或从右向左标记）的字迹范围相差不超过一个像素。
- 不显示的字符（零宽连接符与不连接符、方向标记、嵌入与隔离、变体选择符如表情后的 FE0F、标签字符、零宽空格、软连字符）在 `Text()`、水印与 SVG 中不占宽度、不画方框，与 HarfBuzz、Chrome 一致；此前会画出字体里的字形或缺字方框，变体选择符还占一个空格宽。`Text()` 设了宽度时可在零宽空格处断行，也可在软连字符处断行，断开处显示连字符。
- `Pages(images)`（`goimgtools pages 图片... out.pdf`）：多页 PDF，每张图一页、按顺序，各自按自己的配方在写到它那一页时读取，无论多少页内存只占一页；当作图像读取时是第一页。各页的操作与设置（Resize、Density、ToProfile 等）设在该页的图上，设在整份文档上会报错；文档或任一页上的 WithContext 都能中止它。命令行的最后一个参数须以 .pdf 结尾，以免忘写 PDF 名时覆盖了图片。
- `ToProfile(icc)`（`-profile p3|adobe-rgb|文件.icc`），同 sharp 的 `withIccProfile`：把颜色转换到由原色与色调曲线描述的 RGB 配置并随输出写入；输入有自己的配置时直接从它转换，sRGB 表现不了的颜色得以保留（Display P3 的红转到 Adobe RGB 是 LittleCMS 的 (239, 0, 0)，而不是经 sRGB 裁剪后的 (219, 0, 0)）；在 32³ 个颜色上与 LittleCMS 相差不超过 1 级；结果每通道 8 位，同 `ToSRGB`。`ProfileDisplayP3()` 与 `ProfileAdobeRGB()` 给出这两个色彩空间的配置。用于瓦片或 `Animated` 保留的动画时报错。
- ICNS，macOS 的图标文件（`ICNS(ICNSOptions{Sizes})`、`.icns`、`-icns-sizes`）：写出为不超过原图的各标准尺寸的正方形 PNG，图像按比例放入、居中、四周透明（Pillow 会把图拉伸到每个尺寸、一直到 1024），每个类型同时充当一半尺寸的 Retina 图，带目录，同 iconutil 的写法；读取最大的一张，PNG 的或旧式行程编码 RGB 加遮罩的，与 Pillow 逐像素相同。
- PDF 输出（`PDF(PDFOptions{Quality})`、`.pdf`，`-quality` 得 JPEG 页面）：单页，页面大小按图像分辨率，图像无损存储（Flate，逐行按 PNG 的方式滤波）或存为 JPEG，透明作为软蒙版，保留颜色配置（通道数与图像不符的配置除外，如变成灰度的图上的 RGB 配置）；超过 Acrobat 上限 200 英寸的页面按比例缩小，图像像素不变；边读图像边写（软蒙版暂存于工作存储）。pdfium 渲染结果与原图相差不超过 1 级。
- SVG 输入（`.svg`）：用本包的抗锯齿光栅化器逐条带渲染，内存与渲染尺寸无关。支持形状与路径（弧线、曲线）、变换、嵌套视口与 viewBox、填充（非零、奇偶）与描边（虚线；斜接、圆、斜切连接；平头、圆头、方头端点）、线性与径向渐变（焦点、延展方式、单位、继承）、形状与组的不透明度、CSS（style 属性与样式表：类型、类、id、后代与子选择器）、use 与 symbol、switch（`requiredFeatures` 与 `requiredExtensions` 同 librsvg 视为不支持，draw.io 的标签因而回退到其文字）、剪切路径（形状与文字）、亮度与 alpha 蒙版（`mask-type`）、图案、标记、`paint-order`、`vector-effect: non-scaling-stroke`、用已安装字体排版的 text 与 tspan（粗体、斜体、对齐、字距、缺字回退；字体属性可来自任一祖先元素；字间距与词间距，下划线、上划线与删除线，dominant-baseline 与 alignment-baseline 按字体自身的度量、同 Chrome 的位置，baseline-shift，逐字旋转，textLength 按间距或拉伸字形，以及 textPath：字形沿路径排列并随之转向（librsvg 后四项与词间距都不画））和内嵌图片（data URI；不读外部文件；画得不到原尺寸一半的图片（或整张放不进剩余内存的一半时，只要画得比原尺寸小）按画出的尺寸解码，取每个像素覆盖范围的平均，同 cairo 缩小图片的方式：2400 万像素的照片画成 600×400 用时 0.25 秒，librsvg 则拒绝超过 10MB 的 data URI）。use 在别处重画的形状共用轮廓：matplotlib 两万个点的散点图轮廓只占 5MB，用时 0.6 秒。链首的缩放直接按目标尺寸渲染矢量，同 sharp 任意倍数清晰。与 librsvg（sharp 0.35.5）相比只在边缘有差异（八个测试文档平均差 0.04–0.27 级，模糊 1.0 级）。滤镜：规范里的全部原语（高斯模糊、偏移、填色、合成、合并、颜色矩阵、混合、投影、分量传递、湍流（规范的参考噪声，含拼接）、形态学、卷积、置换、平铺、图像（图片或元素）、漫反射与镜面光照，含平行光、点光源与聚光灯）及其子区域，按规范在线性光中计算；与 librsvg 相差一两级以内（librsvg 不把光照颜色转到线性光，规范与 Chrome 都转）。原语逐条带计算，每条带多取各原语所及的上下行；后面原语不再读的浮点图边算边归还：只有元素本身的图与结果按字节整幅保存，因此 1400×1000 的斜面或 700×700 的噪点纹理在默认 64MB 内按全分辨率计算（与不限内存时像素相同，峰值 60MB），原先要降分辨率；只有连几行的条带都放不进剩余内存一半的区域才降分辨率（此时把元素本身放在最上层的滤镜，即投影，之后再按全分辨率画元素本身，线性光下限于元素全用不透明颜色时）。CSS 自定义属性（`--name` 区分大小写、`:root` 上的也算，`var(--name, 缺省值)`，VS Code 的图标用到），`font` 简写，字号的父级百分比与 CSS 关键字（`small` 到 `xx-large`、`larger`、`smaller`），通用字体族按浏览器的选择（Times New Roman、Arial、Consolas 等），其他系统的字体按浏览器的替代（Helvetica 用 Arial、Courier 用 Consolas），所列字体都没有时按 CSS 用默认的衬线体；先折叠空白再把 x、y、dx、dy、rotate 的位置分给字符，中外文混排的一行用首选字体的基线与下划线。上下标按字体高度的一半升降，上划线立在上伸线上，同 Chrome。与 Chrome 对照，65 个文字特性文档中 62 个的字迹范围相差不超过一个像素（其余：中文系统上 Chrome 的默认字体，以及它对一个单位高的字号的取整）；测试里有一份这些特性的文档与 Chrome 的图比较。要求过多（use 层层相乘）的文档在内存上限处停止。
- `ParseColor`（因而命令行与 `Do`）接受所有 CSS 颜色：148 个颜色名、`#rgba`、`rgb()`、`rgba()`、`hsl()`、`hsla()`。
- TGA（Truevision Targa，`TGA()`、`.tga`），游戏贴图常用：读取调色板、15 到 32 位真彩与灰度图，未压缩或 RLE 压缩（允许一个包跨到下一行），任意角起始，遵从 TGA 2.0 扩展区的 alpha 属性（无 alpha 或预乘）；16 位像素只在头部给了 1 个属性位时把该位当不透明度，置位为不透明（Pillow 不看头部，置位即当透明），5 位通道按 Pillow 的方式展开（v × 255 / 31）。写出为 RLE 压缩、自上而下，与 Pillow 以 compression `"tga_rle"`、orientation 1 写出的文件逐字节相同。
- QOI（"Quite OK Image"，`QOI()`、`.qoi`）：无损 RGB 或 RGBA，流式读写，文件与参考编码器和 Pillow 逐字节相同（只是颜色空间标为 sRGB，Pillow 需指定才这样标）。
- Netpbm（`PNM()`、`.ppm`、`.pgm`、`.pbm`、`.pnm`）：读取文本或二进制的 PBM、PGM、PPM，以及 PAM（带透明）与 PFM，任意最大值按 Pillow 的方式缩放（超过 255 时缩放到 16 位，Pillow 对彩色只留 8 位），二进制中超过最大值的值当作最大值、文本中的则报错，同 Pillow；写出灰度图为 PGM、其余为 PPM，与 Pillow 逐字节相同，图像有 16 位时写 16 位；透明部分铺黑底（同 JPEG）。
- 读取 Photoshop 文件 PSD 与 PSB（`.psd`、`.psb`）：Photoshop 随图层一起保存的合成图（不读各图层），各种颜色模式（位图、灰度、索引、RGB、CMYK 按其配置文件转换（同其他 CMYK 文件）、多通道与双色调按灰度、Lab 转 sRGB），1、8、16、32 位（32 位文档是线性光，按 sRGB 曲线编码），不压缩或 PackBits 压缩，带颜色配置文件、EXIF、XMP 与分辨率，从文件逐条读取。透明通道按 psd-tools 的规则找（作为存储选区的第四通道不当透明），并去掉 Photoshop 垫在下面的白底。Pillow 只读 8 位 PSD，把 RGB 文件的任何第四通道都当透明、不去白底，且读带额外通道（带专色的 CMYK）的 PackBits 合成图时位置算错；在 psd-tools 测试用的 82 个它能读的文件上，除这几处差异外像素与 Pillow 相同。专色通道不叠加到合成图上。
- PNG 10 级（`PNGOptions{CompressionLevel: 10}`、`-png-level 10`），比 9 级更小、慢得多：每行的滤波把各行连同前几行用五种滤波分别真实压缩一遍后选取（libdeflate 10 级，前 32KB 作字典；五种共用录入字典的工作，并在空闲线程上同时压缩），每 256KB 行的三种版本（各行自选的滤波、Brute 的、不滤波）也真实压缩后取最小，数据用 Zopfli 的 Go 移植（`internal/zopfli`，按其设置时与 Zopfli 输出逐字节相同；这里用 60 轮最优解析，数据不到 1MB 的图用与 1MB 耗时相当的轮数，最多 200 轮（17 张里不到 1MB 的 5 张小 0.013%，其中一张小 0.07%），并以选项加上 ECT 的改进：之后按码长计价再解析、不限分块数、每块的码与码表头在更多方案中取最小、短匹配更省时写成字面量，小 0.026%；另外（ECT 没有）以 `FineSplit` 在符号统计变化处把数据再切分，解析前（在 Zopfli 用以切分的贪心解析上，各段因而按自己的统计解析）与解析后各一次，候选点一遍扫描按两侧符号的熵加码表头估算找出，Zopfli 的搜索每轮只试 9 个点，会漏掉短的一段（如图标末尾的透明行）和每隔几 KB 交替的两类行：小 0.14%，一张照片小 0.46%）按其 1MB 主块压缩，各块在各自线程上、Zopfli 每块切出的各段在空闲线程上压缩，再逐比特拼成 Zopfli 对整段数据写出的同一条流；某块用 9 级压缩器更小时改用它；调色板按各颜色首次出现的顺序写（与 ECT 相同），去掉没有像素用到的颜色（像素不变）。17 张截图、照片与游戏美术图上：比 9 级小 0.39%，比 ECT 0.9.5 `-9` 小 0.23%（15 张更小），8 线程用时 438 秒（9 级 31.5 秒，ECT 6 核 606 秒）。每个压缩线程约占 50MB，默认内存上限下只用一个线程压缩。移植版写出与 Zopfli 相同的字节，耗时约为 Zopfli 的三分之一（照片 1MB、60 轮、单核：10.5 秒对 29 秒）：从最优解析的第一轮起保留每个位置的全部匹配（Zopfli 每位置只存 8 个，其余每轮重新搜索），长度与距离的代价每轮只查一次。1 至 7 级输出字节不变。
- `pipeline.Env.TryGo`：从不在调用方协程上执行的 `Go`，用于环节可推迟到有空闲辅助线程时再做、自己先接着干活的工作。
- 近无损 WebP（`WebPOptions{NearLossless: true}`、`-webp-near-lossless`），同 libwebp 的近无损（sharp 的 `nearLossless`、`cwebp -near_lossless`），照其预测器（`predictor_enc.c`）移植：某像素与四个邻像素之一相差超过一点时，各通道残差舍入到小于该差的 2 的幂的倍数，像素随即改成解码器将还原出的值，误差不累积；平滑区保持精确。`Quality` 表示程度：100 不变（即无损文件），80–99 每通道至多差 1 级，60–79 差 2，40–59 差 4，20–39 差 8，更低差 16；默认 75。每块的预测模式按舍入后残差的代价选（同 libwebp），一段行要等到其末行的下一行到达再处理，使每个像素都按四个邻像素判断。另有一个保持无损的候选同时参与比较，图案精确重复时由它胜出（某图案无损 86 字节、舍入后 1176 字节）。舍入步长 4 及以上时，预测器的块用 8×8 而非 4×4（相对舍入后的残差，模式的开销更重：小 0.24%）。60 档：6 张 Kodak 照片比无损小 37.0%，比 `cwebp -near_lossless 60`（libwebp 1.6.0）小 2.9%；连同 17 张截图、照片与游戏美术共 23 张比 cwebp 小 7.3%（80 档 6.6%；单张从大 6.6%（一个 5KB 的图标）到小 13.4%），耗时比无损多 25%；1200 万像素照片单核 8.6 秒（无损 3.7 秒；cwebp 近无损也要其无损的 2.6 倍时间），内存 22MB（无损 23–25MB）。动图 WebP 同样支持。
- `WebPOptions.Exact`（`-webp-exact`）精确保留全透明像素的颜色，同 libwebp 的 `exact`（见「变更」）。
- `Boolean(operand, op)` 与 `Bandbool(op)`（`BoolAnd`、`BoolOr`、`BoolEor`），同 sharp 的 `boolean` 与 `bandbool`：每个像素的红绿蓝与同尺寸图像（逐行同步读取）对应像素逐位运算，或自身三个通道逐位运算后成灰；透明不变。
- `JoinChannel(mask)`，同 sharp 的 `joinChannel`：把同尺寸图像的灰度作为透明通道（与原有透明相乘），即 `ExtractChannel(ChannelAlpha)` 的逆操作；蒙版与图像逐行同步读取，与两者的条带切分无关。
- 供 `Do` 与 `-op` 用的具名操作：`rect`、`ellipse`、`circle`、`polygon`、`line`（先给数字，再给 `fill=颜色`、`stroke=颜色`、`width=宽度`，矩形还可 `radius=半径`），以及 `bandbool:and|or|eor`；`goimgtools ops` 列出它们。例：`-op rect:10,10,100,60,fill=#ffcc00,stroke=black,width=3,radius=8`。
- `Draw(shapes...)`：矩形（可圆角）、椭圆与圆、多边形、线与多段折线（圆角连接、方头），带填充与描边，同 Pillow 的 ImageDraw 而边缘平滑：每个像素按图形覆盖它的比例上色，算法同 FreeType 的平滑光栅化（`internal/raster`：以 1/256 像素为单位的整数运算，边与行、列的交点都由两端点直接求得，所以任意分带、任意处理器结果相同；曲线化为折线，误差在 1/20 像素内，余弦来自 `internal/trig`）。白底黑图形时，小数边界矩形覆盖的像素灰度与其覆盖比例相差不超过 1.5 级；半径 30 的圆面积误差 0.3%。不论图和图形多大只占几行的内存。
- `MinSSIM(s)`（`-min-ssim`）：SSIM 不低于 `s` 的最小文件，同 jpeg-recompress 的找法：对质量区间二分，取解码后测得达标的最低质量（PNG、GIF 为最少颜色数）（JPEG、WebP、PNG 上与逐个尝试得到的文件相同）；没指定质量时最高搜到 100。Kodak 照片在 80 质量时只有 0.95–0.97，所以 0.98 要更高质量（83–162KB，而 80 质量为 49–105KB）；文档截图 0.98 比 80 质量小 22%，0.97 小 37%；这些耗时 0.3–0.9 秒。适用于 JPEG、有损 WebP、PNG、GIF 与 JPEG 压缩的 TIFF。
- `inspect.SSIM(a, b)`（`goimgtools compare -ssim`）：两张图的结构相似度，算法同 Wang 等的参考实现 `ssim_index`（11×11、σ 1.5 的高斯窗及其常数，在窗口放得下的位置取平均；在 Rec. 601 亮度上计算，透明处铺白），照片与其 JPEG、模糊版的值与参考实现在小数点后十位一致；按几行一次读取（11 行的五个和），不融合运算，各处理器结果相同。
- `inspect.BlurHash` 与 `inspect.ThumbHash`（`goimgtools placeholder`）：网页在图片加载完之前显示的模糊占位图，由转正并缩到 100×100 以内的图计算。10 张照片、图标与抠图上，ThumbHash 与 JavaScript 参考实现（thumbhash 0.1.1）逐字节相同，BlurHash 与 woltapp 的 C 编码器相同（其 TypeScript 版按分量的最大值而非最大绝对值缩放，10 张中 2 张不同）。余弦（由 [0, π/4] 上的多项式按对称性得到）与 sRGB 曲线（查表）都由本库计算，不融合运算，各处理器结果相同。
- `Loop(n)` 与 `FrameDelay(d...)`（`-loop`、`-frame-delay`），同 sharp 的 `loop` 与 `delay`：`Animated` 保留的动画播放几次（0 为无限）、每帧显示多久（一个时长用于全部帧，或逐帧给出，最后一个用于其余帧），适用于 GIF（以百分之一秒计，四舍五入）、WebP 与 APNG 输出。
- TIFF 输出的 JPEG 压缩（`TIFFOptions{Compression: TIFFJPEG, Quality}`、`-tiff-compression jpeg`），同 libtiff 为 libvips 与 sharp 写的方式（sharp 的 TIFF 默认即 JPEG）：彩色存 YCbCr（质量低于 90 时 4:2:0，90 起 4:4:4；sharp 从 90 起存 RGB，大 2.4 倍：两张照片和一张截图在 95 档为 602KB/310KB/823KB，本库 248KB/152KB/346KB，SSIMULACRA2 低 1.4–2.7），灰度存灰度，每个条带是由整数个块行组成的 JPEG 流，共用 JPEGTables；质量 0 即 80。50 与 80 档文件与 sharp 的只差几十字节、分数相同（SSIMULACRA2 差不超过 0.05）；Pillow（libtiff）解出的像素与本库相同。MaxFileSize 可降其质量。不能保留透明：有透明像素时报错。
- ICO：Windows 与网站的图标文件（`ICO(ICOOptions{Sizes})`、`.ico`、`-ico-sizes`），读写同 Pillow：写一张（至多 256×256）或多个尺寸（`[]int{16, 32, 48, 256}`，把图存入工作存储后按比例缩放到每个尺寸以内，Lanczos3），每个尺寸存为 9 级压缩、带透明的 PNG；读取（ICO 与 CUR）时取最大的一张（同尺寸取色深最大的，即 Windows 显示的那张；Pillow 取最浅的，8 位的而不是带 alpha 的 32 位），PNG 项流式解码，1/4/8/24/32 位的位图项整张解码并用其透明掩码（32 位按 alpha，同 Windows，不论目录里写的位数；alpha 全为 0 时按掩码）。Windows 与各程序自带的 18 个图标解码与 Pillow 12.1 逐像素相同，系统里另找的 60 个图标除上述取舍不同的 6 个外也都相同；Pillow 能读出写出的每个尺寸。
- `SetExif(ExifFields{...})`（`-exif-copyright`、`-exif-artist`、`-exif-description`）：往 JPEG、PNG、WebP、TIFF 输出的 EXIF 块写入文字字段（版权、作者、描述、软件、时间），同 sharp 的 `withExif` 与 `withExifMerge`：配合 `KeepExif` 时写进输入的块——新的第一目录及其取值追加在块尾、文件头改指向它，其余字段、目录和偏移（包括厂商注释的）原样不动；否则单独成块。
- `Join(images, JoinOptions{Across, Gap, Background, Align})`：把多张图排成网格，同 sharp 的 `join`（及 ImageMagick 的 `montage`），每张放在与最大的图一样大的格子里，按 `Align`（九个位置之一）放置，间隔与较小图周围填 `Background`（不给则透明）。每张图按其自身的处理链、在写到它那一行时按几行一次读取：40 张 300×200 的图、每行 8 张，在 6MB 内存上限下完成。其 `Info.Format` 为 `"join"`。
- `Blank(width, height, colour)`：单一颜色的图像，同 sharp 的 `create`、Pillow 的 `Image.new`，可作 `Composite` 的画布；按行现生成（6000×4000 的画布在 4MB 内存上限下写成 JPEG）。其 `Info.Format` 为 `"blank"`。
- `JPEGOptions.OvershootDeringing`（`-jpeg-overshoot-deringing`），同 mozjpeg 的过冲去振铃、sharp 的 `overshootDeringing`，与 sharp 逐字节相同（截图与照片质量 30–90 共 32 个文件，合成文字、饱和色与噪点的 4:2:0、4:4:4 共 18 个文件）：块中有 255 的像素与其他像素相邻时，变换前把这些像素按之字形顺序的每一段沿 Catmull-Rom 曲线抬到 255 以上（上限由 DC 量化值定，并保证块均值不超过 255），白底上的硬边缘振铃就少；被改动的块走整数变换，曲线以 float32 计算、不融合运算，各处理器相同。一张文档截图 50 档：SSIMULACRA2 61.5 → 70.0，字节多 3.2%，与不开时的 75 档相当而文件小 25%；照片差别不超过 0.03。
- `WebPOptions.AlphaQuality`（`-webp-alpha-quality`），同 libwebp 的 `alpha_q`、sharp 的 `alphaQuality`：有损 WebP 的透明度在编码前降到较少的级数（质量 70 以下 2–16 级，99 时最多 256 级），级数按 libwebp 的 `QuantizeLevels` 对该图的取值做 k-means 选出（以 float64 移植、不融合运算，各处理器结果相同），0 与 255 不变；ALPH 块中标明。6 张带透明的图，透明通道块 50 档 35.1KB、20 档 24.2KB，精确保留时 71.1KB（libwebp 1.6：36.5KB、23.9KB、112.5KB）。0 与 100 精确保留，为默认。

### 修复

- SVG 中从右向左书写的文字：`y` 与 `dy` 按文字的逻辑顺序累加，之后再把各段排成所见次序（同 SVG 的排版算法与 Chrome）：tspan 用 `dy` 下移阿拉伯词的一部分时，下移的就是这一部分（原先下移的是视觉上靠前的那些字母）。
- doctype 声明了标记实体（`<!ENTITY r "<rect …/>">`）的 SVG 文档，引用处放入这些元素（同 Chrome 所用的 libxml；原先只读文本实体，元素缺失），只处理自身不再引用实体的，最多增加 4MB 且计入内存上限；无法解析的渐变坐标取默认值（原先为 0，渐变常变成单色），即使所引用的渐变设了该坐标（同 Chrome）；焦点圆与终止圆重合的径向渐变，圆内为第一个颜色、圆外为最后一个（同 Chrome；原先不画）。
- 旋转或斜切元素上的 SVG 滤镜在元素自身的坐标轴上计算，结果再映射到图上（同 Chrome）：滤镜区域与子区域是转过的矩形（原先是包住它的正矩形），偏移、平铺、光源与模糊在用户空间中保持形状；另外几处也按 Chrome：组或 `use` 的包围盒是各子元素包围盒按其变换映射后的外接矩形（SVG 2 的算法；原先是转过的形状本身的外接矩形，按其单位的图案与渐变因此偏小）；图案内容按图案在文档中的位置继承属性（原先取初始值：继承来的填充又指向该图案时，现在与 Chrome 一样不画，原先画成黑色）；焦点在圆外的径向渐变是焦点圆到终止圆之间的圆锥，圆锥外不画（SVG 1.1 会把焦点移到圆内）；`color-interpolation: linearRGB` 的蒙版按线性光计算亮度；`feMorphology` 一个方向半径为 0 时另一个方向照常处理（原先整个不处理），多于两个数视为无效；光照常数小于 0 时取 0，镜面指数限制在 1–128（同 Chrome）。
- SVG 按 Chrome 与 resvg 的画法（每条都与两者对照过）：`feImage` 引用元素时画在其子区域的左上角（原先画在元素本身的位置，差一个区域偏移）；输入 `BackgroundImage`、`BackgroundAlpha`、`FillPaint`、`StrokePaint` 当作没写输入（原先是透明）；`flood-color`、`lighting-color` 为 `currentColor` 或 `inherit` 时在原语所在处取值，`lighting-color` 未设时为白色；`stdDeviation` 多于两个数时不模糊；`feConvolveMatrix` 的核与阶数不符（或目标点在核外）时结果透明（原先原样传出输入）；`symbol` 上的 `x`、`y` 生效；标记或视口上的 `overflow="inherit"` 取父元素的值；沿直线的曲线包围盒宽度为零（按包围盒单位的渐变不画）；备用值不是颜色或 `none` 的绘制值（`url(#g) green icc-color(…)`）整体无效；带逗号的 `rgb()`、`rgba()` 不能数字与百分比混用。
- SVG 中从右向左书写的文字：tspan 用 `dx` 或 `dy` 移动的一行（d3 用 `x` 加 `dy` 分行就是这样）整行移动；原先只有阅读顺序上的第一个字母（最右边那个）移动，其余仍留在上一行。
- SVG 文字：tspan 上的 `textLength`（及 `lengthAdjust`）让其字符恰好排满该长度，行内其后的文字随之移动（原先只认 text 元素上的）；tspan 上的 `xml:space="preserve"` 保留其空格（原先只读 text 元素上的）；路径上的文字可延续到路径后面的子路径，`startOffset` 的百分比按全部子路径的总长计算（原先只沿第一个子路径）。均与 Chrome 用同一字体对照。
- SVG 的 `text-anchor` 按块内字形的实际范围对齐，与 SVG 2 及 Chrome 相同：`dx` 让某个字形退到第一个字形之前时，`start` 让最左的字形从 `x` 开始，`middle`、`end` 按全部字形的范围居中或右对齐（原先按第一个笔位到最后一个笔位的距离）。
- SVG：文档树占用的内存每 1024 个节点预约一次，HTML 与文字元素中的文字节点也计入；原先以文字形式出现的节点可能漏算。
- SVG：命名空间不是 SVG 的元素（由前缀或默认命名空间绑定）无论叫什么名字都不画，与 Chrome 相同；用前缀绑定到 SVG 命名空间的元素（`svg:rect`）照常绘制，`foreignObject` 中的 HTML 也照常处理。
- SVG：同一个滤镜再用在已应用它的元素内部的元素上时也绘制（原先内部元素不画）；只有在滤镜自身 feImage 所绘制的元素中再次遇到它时才跳过，以防循环。
- SVG 样式表：每个 style 元素各自解析（一个末尾残缺，例如多出一个引号，不再连累下一个的第一条规则）；没有块的 at 规则（mermaid 放在规则前的 `@import`）到分号结束，原先会吞掉其后的一条规则，mermaid 图因此丢了字体与颜色。
- 已安装字体按英文名识别。名字表中先列其他语言名字的字体，原先按那种语言命名：Windows 上 Segoe UI 与微软雅黑的粗体样式被读作 "Negreta"（加泰罗尼亚语的粗体），因此 `Text()` 与命令行找不到 "Segoe UI Bold"、"Microsoft YaHei Bold"，SVG 中 `font-weight: bold` 的微软雅黑文字画成了常规体。现在先取 Windows 美国英语的名字，其次取 Mac 的英语名字。
- SVG 文字的包围盒与 Chrome 相同，为各字符的字框（横向为其前进宽度，纵向为字体的上伸到下伸）。原先文字没有包围盒：文字上的蒙版（其区域默认按包围盒单位）与包围盒单位的裁剪路径会让文字整个不显示，包围盒单位的滤镜则按整幅画面计算。
- SVG（按 Chrome 的画法）：无法解析的 `transform` 属性（含 CSS 那样的单位）视为没有，元素照常绘制（原先不画）；椭圆负的 `rx` 或 `ry` 按 auto 取另一个半径；不以移动命令开头的路径数据什么都不画；包围盒单位的渐变或图案用于包围盒没有宽或高的图形（直线）时，用其后的备用颜色；包围盒单位的滤镜用于空的 g，以及没有任何原语的滤镜，什么都不画；原语的输入指向此前没有的结果时取上一个结果（原先是透明）；通过 `href` 引用另一个滤镜的滤镜，从它继承自己没有的区域、单位与原语（原先不继承）。
- SVG 的 `text-decoration: line-through` 画在 Chrome 画的位置：线的中间在基线上方字体上升高度的三分之一处；原先在 x 高度的一半，40 像素时比 Chrome 低 4–5 像素（Arial、Segoe UI、Times New Roman、微软雅黑、Tahoma、Verdana、Georgia；现在相差不超过一个像素，Calibri 不超过三个，因 Chrome 用其 Windows 上升高度）。
- SVG：字体族加载失败（打开字体时到达内存上限）时归还其字体已预留的内存；原先每个字体 256KB 一直占到文档关闭。
- 文字（`Text()`、水印、SVG）遇到所用字体都没有的字符（如符号 ✨、★、箭头，或字体不含的文字）时，从系统里的符号与其他文字的字体取（Windows 的 Segoe UI Symbol 与 Emoji、Nirmala UI 等；macOS 的 Apple Symbols；Linux 的 Noto Symbols、DejaVu Sans），只在遇到这样的字符时才打开；原来画成缺字的空框。
- 带非 sRGB 颜色配置的图（如手机照片的 Display P3）切成瓦片（`ToTiles`）时转换为 sRGB，同不能带配置的格式：瓦片不带配置写出，原先颜色显示不对（P3 的红显示成 sRGB 的 (234, 51, 35)）。
- 折线与多边形描边在各段相接处偏暗：部分覆盖的像素上各段与拐点的覆盖率直接相加；现按它们的并集计算（同 cairo、Skia 的扫描），与 64×64 采样相差不超过 1.24 级，过折线上一点的线与直线相同，点距 0.4 像素的曲线涂黑面积等于长×宽（原来多 24.8%）。
- `Join` 遇到 `FromReader`（只能读一次）的图就失败；现在这种图在开头打开、留到它那一行。
- `Boolean` 让不透明的图在 `EnsureAlpha` 看来像有透明，于是不加 alpha。
- TIFF 的 JPEG 压缩每条只预约了约一半内存（缓冲多出的容量和一份复制没算）；那份复制已去掉。
- `SetExif` 遇到以 `Exif\0\0` 开头的 EXIF 块（有的工具在 PNG 里这样存）会整块替换，而不是在其中设置字段。

### 变更

- 动图 PNG 输出每种写法只压缩一次：行与之前试过的某种写法相同的（同一矩形不混合，或混合清掉的像素相同、或一个也不清）不再试，数据本来就一样。文件不变；18 张照片（768×512）的 GIF 转 APNG：3.06 秒 → 1.35 秒。
- `Dilate` 与 `Erode` 以 64 位字一次合并两个像素；横向宽度 9 以内逐个合并，更宽的分块且不再每像素做一次除法，输出逐字节不变。6000×4000 的 JPEG，宽度 2：0.37 秒 → 0.25 秒（libvips 0.18 秒）；宽度 40：1.77 秒 → 0.98 秒。
- `BlurLibvips` 在 amd64 上用 AVX2（`purego` 构建标签不用）：行的累计和、横向各盒的加权和与纵向的逐行移动，一次 8 个和，uint32 运算与 Go 代码相同，输出逐字节不变。6000×4000 的 JPEG 以 sigma 3 模糊再写出：0.84 秒 → 0.26 秒（libvips 0.20 秒）。
- 带 `FastShrinkOnLoad`（默认方法、不用线性光）时，金字塔 TIFF 从不小于缩小目标的最小一层读取（同 libvips 的缩略图挑页）：输出与不带此选项时略有不同（缩的是那一层自己的像素）。6000×4000 的金字塔缩成 1536 宽的 JPEG：0.31 秒 → 0.088 秒，内存 13.3MB。
- 动图 GIF 输出占用更少内存：每帧「变化部分」的另外两种试法（不带透明、在连片处带透明）逐帧写进同一块存储，不再每帧新复制（18 帧 768×512 共分配 75MB，其中 51MB 来自这里）。缩到原尺寸的 GIF，有损 24：34.8MB → 23.8MB；无损 35.1MB → 24.4MB；文件相同。
- 同 Pillow 的旋转（`RotatePillow`，双三次）：算一个点所用的 4×4 像素落在同一个缓存小块里时一次读出，否则沿用上一个小块，不再每个像素各查一次缓存：6000×4000 的照片转 15° 用 2.44 秒（原 4.60 秒），像素相同。
- `Recomb`、`Convolve`、`Linear`、各种色阶操作、混合模式以及经查找表色彩配置的转换，限幅改用内置的 `min` 与 `max`，不再调用 `math.Min` 与 `math.Max`（对任何值结果都相同，含 NaN 与正负零，只是省了函数调用）：6000×4000 照片的 `Recomb` 0.73 秒 → 0.34 秒，3×3 的 `Convolve` 0.83 秒 → 0.64 秒，按 `multiply` 平铺的水印 0.66 秒 → 0.52 秒；像素相同。
- 不透明图像的 TIFF 写成 RGB，每像素三个样本，同 libtiff、libvips、sharp 与 Pillow 的写法（原先按 `x/image` 写 RGBA：像素相同，数据多四分之一）：6000×4000 的照片不压缩 91.6MB → 72.0MB，Deflate 加预测器 51.3MB → 48.0MB。凡写成条带 TIFF 的不透明图像，输出都会变（按瓦片写的 TIFF 原本就是 RGB）。
- PDF：无损存储（不给 `Quality`）时，没有任何操作、不转换颜色的 JPEG 原样放进文档（DCTDecode），连同它的颜色配置与分辨率；原先是解码后用 Flate 存储。像素就是这张 JPEG 解码出的像素，文件大小就是 JPEG 的大小。6000×4000 的照片转 PDF 用 0.009 秒（原 2.02 秒），7.6MB（原 46.3MB）；三张合成三页的文档（`Pages`、`goimgtools pages`）用 0.014 秒（原 5.86 秒），20.0MB（原 133.6MB）。CMYK、无损与算术编码的 JPEG 不在此列，照旧解码。这类 JPEG 的输出会变。
- 写到文件时（`ToFile`），TIFF 的压缩数据（条带，以及按瓦片写的 TIFF 的第一层）与 BMP 的各行直接写到它们在文件里的位置，文件头最后补上，不再经过临时文件：6000×4000 的照片写成 Deflate TIFF 原先要写 45.8MB 临时文件，LZW 50.8MB，瓦片 52.8MB（金字塔 70.3MB → 17.4MB，即各缩小层），BMP 68.7MB；现在都不写，输出逐字节相同。写到 `io.Writer`（`ToWriter`、`ToBytes`）时照旧。
- 瓦片（`ToTiles`、`.dzi`）用自己的预算编码：原先计入整张图的预算，而那里为每层留着一行瓦片，没有余地把一块瓦片的 JPEG 暂存在内存里，每块都写了临时文件（6000×4000 的照片共 17.8MB）；瓦片像素的存储复用；小的暂存数据不再占满一整块 1MB（`pipeline.Spill` 按 `Expect` 定第一块的大小）。6000×4000 照片的 Deep Zoom 瓦片：0.39 秒（原 0.51 秒），39 到 49MB（原 42 到 94MB，峰值每次不同；总分配量 336MB → 118MB），文件相同。
- `inspect.ReadWatermark` 占用的内存大为减少，读出的结果不变。6000×4000 的照片（缩到 1536×1024 读取），含程序本身：单核时有水印 58.4MB、无水印 51.6MB（原先 177MB 与 177MB，且每次运行差别很大：无水印时测到过 525MB），四核 69.1MB 与 58.3MB（原先 170MB 与 309MB），八核 80.0MB 与 62.9MB（原先 161MB 与 265MB）；3000×2000 单核时 53.9MB 与 57.4MB（原先 135MB 与 251MB）。有水印时用时不变，无水印时慢 2% 到 5%（各阶段之间的回收）。颜色只在第二次读取时才取，为此把图再读一遍，放进随后检测要用的存储里；每一阶段的数组放进前一阶段腾出的内存（亮度、第一遍白化的结果、变换的内存——先放频谱，再放它的两种平滑）；被调用成千上万次的部分（折叠、相关、小变换的列缓冲、译码器及其列表搜索）的临时存储保留下来复用，多核时在任务间传递；每一阶段留下的垃圾在下一阶段开始前回收，并把内存还给系统。48 张照片（打水印后缩小、压缩、裁剪、旋转、转灰度，或根本没有水印）读出的结果与原先完全相同。
- 不再依赖 `crypto`：隐形水印的密钥与 ICC 配置缓存改用本包自带的 SHA-256 计算（结果相同，以前打的水印照样读出）。`crypto/sha256` 会引入 FIPS 140 模块，其中 32MB 的熵缓冲区是个只在 FIPS 模式下才用的零数组，Windows 从进程启动起就把它计入已提交内存：本包的命令启动时提交的内存由 45.5MB 降到 13.9MB（工作集两者都是 7MB）。
- 旋转四分之一圈（`Rotate(90)`、`Rotate(270)`，以及交换宽高的 EXIF 方向）：每一轮的行作为一整块写入暂存区，输出的条带再从各块拼出，不再每轮对结果的每一行各写一次（2400 万像素要写 14 万次）：6000×4000 的 JPEG 旋转后再写出，单线程从 0.68s 降到 0.31s，内存仍是 14MB，输出逐字节不变。
- x86-64 上的模糊与反锐化掩模（`Blur`、带 sigma 的 `Sharpen`）：横向的方框遍改为从整行的累加和取每个窗口的和（AVX2，一次两个像素），不再让窗口一个像素一个像素地滑。6000×4000 的 JPEG 以 sigma 2 模糊，单线程 0.31s→0.27s，四线程 0.14s→0.12s；以 sigma 2 锐化 0.38s→0.35s；输出逐字节不变，峰值内存不变。
- 动图 GIF 输出少分配内存：选调色板期间各帧只暂存一份（原先为「每帧独立调色板」的写法另存了一份，现在从同一份行数据写出），小动图不再因第二份而落临时文件；原先每帧、或每次试算「怎样编码最小」都新建的缓冲改为复用（GIF 帧的解码条带与 LZW 读取器、画布条带、试编码器的表：每次 64KB，每帧约十五次）。18 帧 768×512 的动图缩到 384 宽，总分配量 48MB→18MB。文件逐字节不变；整个程序的时间与峰值内存与原先相同（0.71s、23MB，原因见对比表）。
- GIF 解码快约 2.2 倍：帧的 LZW 数据改由本包自己的读取器解码，不再用 `compress/lzw`：位从已缓冲的子块里一次取 8 字节，每个码代表的字节串直接写进行缓冲、一次写 8 字节（每个码把串的末尾几个字节存成一个字，做法同 Wuffs 的解码器），而标准读取器是倒着写进自己的缓冲再拷贝两次。任何输入下像素、错误以及读到文件的位置都相同（与标准读取器在 6000 条码流上对照：完整的、截断的、改了位的、末尾带多余数据的、表满后清与不清的）。6000×4000 的 GIF 缩成 1536 宽的 JPEG：单线程 0.24s→0.17s（libvips 0.19s），四线程 0.13s→0.12s。
- SVG：从 `style` 属性中查找元素的各项属性时，不再每查一项就把整个属性重新解析一遍：Inkscape 写法的 30,000 条路径的 SVG 绘制由 1.34 秒降到 0.70 秒，峰值内存由 150MB 降到 113MB（`-mem 512`），像素相同。
- 至多 16 色的无损 WebP：不打包、不用预测器的像素与颜色表打包的像素同时参与比较，在分 Huffman 组之前小 5% 以上就用它：打包使重复只能整符号开始和结束，少级数的平滑图（降级后的透明通道、蒙版）因此吃亏（降到 2–12 级的透明通道小 5.6%，一张小 9%；`AlphaQuality` 50 与 20 时透明通道块小 2.7%）。不打包胜出时这类图耗时最多约为原来的 5 倍。
- WebP 默认不再保留全透明像素的颜色，与 libwebp、sharp、Pillow 一致（`WebPOptions.Exact` 可保留）：无损输出把它们存为透明黑，有预测器时存为邻像素预测出的颜色（libwebp 的 `WebPReplaceTransparentPixels` 及其预测器中的清理）；有损输出与以前一样把它们的样本抹平（设了 `Exact` 则不抹）。看得见的部分不变；透明处下面有噪点的抠图照片无损小 27%，图标与绘图变化约 1%，有大有小。不透明图像文件不变。
- PNG 8、9 级在符号统计变化处把 deflate 块再细分，与 10 级相同（deflate 移植版的 `FineSplit`，用整数运算，各处理器输出仍相同），不再限 15 段（8 级小 0.018%，9 级小 0.014%，一张每隔几 KB 交替两类行的照片小 0.044%（9 级））；每块的码与码表头在更多方案中取最小，与 ECT 相同（`MoreCodes`：计数原样、按 zopfli 与按 Brotli 平滑，码长上限 14 到 9 位；码表头用或不用 16、17、18；8 级小 0.019%，9 级小 0.015%）：17 张截图、照片与游戏美术图合计 8 级小 0.037%，9 级小 0.029%，耗时不变；9 级比 ECT `-9` 大 0.16%，原为 0.19%。
- PNG 8 至 10 级的图像数据改为按 1MB 一个 IDAT 块写出（不超过 1MB 的数据只有一块，与 ECT、oxipng 相同），不再按 32KB 或压缩器较大一次写出的大小分块。每块多占 12 字节：17 张截图、照片与游戏美术图 9 级小 0.023%（比 ECT `-9` 大 0.19%，原为 0.21%），10 级小 0.007%；压缩数据不变。这几级最多多占 1MB 内存，小图更少。
- `pipeline.Env.Parallel` 在还有调用没做时，也把它们交给中途空出来的辅助线程（调用方协程在两次调用之间查看），而不只交给开始时空闲的。
- 不再依赖 `github.com/klauspost/compress`，依赖只剩 `golang.org/x/image`、`x/text` 与 `x/sys`。Go 1.27 及以上版本中，1 至 6 级下超过一块的 PNG 数据改由 Go 1.27 的 `compress/flate` 压缩，它已复制进本库（`internal/gflate`），另加了带字典复位的方法，并在小端处理器上按字读取（与 `klauspost/compress` 的做法相同）。字节与 0.2.0 相同（Go 1.26 下也相同，那里仍和以前一样每块用它自己的 `compress/flate` 压缩）；速度与原来相当：6000×4000 的图存 6 级，单核 720ms 对 707ms，四核 276ms 对 268ms。PNG 中压缩的文字与 ICC 配置文件改由 `compress/zlib` 解压。
- 有损 WebP 解码在 `Threads(4)` 及以上时分摊工作，与 libwebp 的多线程解码器相同：使用上一段宏块行的同时解码下一段，解析下一行的同时重建并滤波这一行；像素不变。6000×4000、质量 80 的 WebP 用 `MethodLibvips` 在解码时缩到 1536 以内：五线程 0.21s，原为 0.27s，与 sharp 一样快（0.21s；它的 libwebp 在第二个线程上解码）。
- 无损 WebP：码表开销大于所省的哈夫曼组会被合并（同 libwebp 对直方图的贪心合并），之后每块在剩下的组中重新选择（4 百万像素以内的图）。24 张 Kodak 照片默认力度小 0.4%，力度 9 小 0.8%；17 张截图、照片与游戏美术图小 0.1%；30 张 Windows 图标小 0.7%。照片不再出现力度越高文件越大（原先力度 7 比默认大 0.5%，力度 4 比力度 3 大）。分组的各轮在每块的符号上进行（由第一轮存下），条件是放得进内存预算已到过的峰值以内、且不超过预算的十六分之一：Kodak 照片默认力度单核 11.2s，原为 13.4s；力度 9 为 18.6s，原为 24.0s。超过 4 百万像素的图耗时与内存同以前。
- 无损 WebP 力度 4 也做最优解析（原先从力度 5 起）：截图、图画与游戏美术最多小 12%（17 张这类图与照片合计小 5.2%，单核 25s，原为 9.5s，默认力度为 42s），30 张 Windows 图标小 2.6%，照片小 0.2%（24 张 Kodak 照片 5.2s，原为 3.4s）。
- 无损 WebP 在 `Threads(4)` 及以上时，除预测器选择外还分摊：参与竞赛的各变体同时处理每一行；最优解析的匹配搜索每次一段 8K 像素，分给多个核心（整段先进入哈希链，每次搜索从自己像素的链节点出发，该段覆盖的旧链节点从副本读取，所以找到的匹配与逐个像素搜索相同）；哈夫曼分组的各轮重放也在多个核心上为每块选组。字节不变。默认力度八线程：17 张截图、照片与游戏美术 21s，原 41–43s；3426×1912 的调色板截图 5.6s，原 14s；24 张 Kodak 照片 7.1s，原 11.1–11.6s；峰值内存多 2–7MB。
- PNG 9 级：为每行挑滤波时，找匹配看前面 256 个位置而非 16 个（8 级不变）：17 张截图、照片与游戏美术小 0.05%，耗时多 15%；比 ECT `-9` 大 0.21%，原为 0.26%。
- 减色（GIF、调色板 PNG）在 `Threads(4)` 及以上时分摊 k-means 迭代，结果不变。24 张 Kodak 照片存 GIF：六线程 2.0s，原为 3.0s；6000×4000 的 JPEG 缩到 1536 再存 GIF：0.38s，原为 0.56s。

### 移除

- 不支持 HEIC、AVIF 与 JPEG XL。模块 `github.com/whhhh1500/goimgtools/heic` 与 `github.com/whhhh1500/goimgtools/avif`（随 0.2.0 发布为 `heic/v0.2.0`、`avif/v0.2.0`，这两个版本原样保留，导入它们的代码可继续使用）不再维护；JPEG XL 模块（libjxl 0.12.0）做出后、发布前即移除。原因是这些编解码器转写为 Go 后所占内存远超本库对自己的要求。在一张 1200 万像素的照片上实测：AVIF 用 aom 编码约 340MB、解码 111MB，模块编译要 4.9GB；JPEG XL 编码 371 至 509MB（libjxl 本身即 cjxl 开流式输入输出也要 196 至 268MB），编译要 5.5GB；HEIC 解码 27 至 56MB（10 位画面 120MB），编译要 1GB。需要时可用 `RegisterFormat` 从外部加入格式。

## 0.2.0（2026-11-20）

### 从 0.1.0 升级

本版定下了 API：此后导出的名字只增不改、不删（每次推送都用 `apidiff` 与本版比较）。为此 0.1.0 的几个名字改了：

| 0.1.0 | 现在 |
|---|---|
| `JPEG(85)` | `JPEG(JPEGOptions{Quality: 85})`，与 `PNG`、`WebP`、`GIF`、`TIFF` 传设置的方式一致（`JPEG()` 为质量 80） |
| `JPEG(85, JPEGOptions{Progressive: true})` | `JPEG(JPEGOptions{Quality: 85, Progressive: true})` |
| `Rotate()` | `AutoOrient()` |
| `Rotate(90)` | `Rotate(90)`，现在接受任意角度（`float64`，单位为度）与 `RotateOptions` |
| `Extract(left, top, w, h)` | `Crop(left, top, w, h)` |
| `ResizeOptions{Filter: &Filter{...}}` | `ResizeOptions{CustomKernel: &CustomKernel{...}}` |
| `Info.HasProfile` | `Info.HasICC` |

能按 libvips（sharp）或 Pillow 计算的操作共用一个类型 `Method`（`MethodDefault`、`MethodLibvips`、`MethodPillow`），见 `ResizeOptions`、`BlurOptions`、`SharpenOptions`、`RotateOptions` 与 `Convolution`。

为 0.1.0 写的代码仍可能遇到的变化：

- `Animated()` 遇到无法保留的动画（GIF、WebP 或 PNG 动画转 JPEG、BMP 或 TIFF）时返回包装 `errors.ErrUnsupported` 的错误，0.1.0 会静默只写第一帧；输出为 PNG 时写出 APNG。去掉 `Animated()` 即得第一帧。
- 有损 WebP 解码为 `*image.NRGBA` 条带，不再是 `*image.YCbCr`，以便颜色与 libwebp 一致；`Metadata()` 仍报告 `ycbcr`。对解码结果做类型断言的代码应接受任意 `image.Image`。
- GIF 的 `Frames().LoopCount` 为播放次数（与文档一致），比 0.1.0 返回的多 1。
- PNG 默认级别为 7（原为 6），按 libdeflate 7 级的方式压缩：文件更小，写得更慢，工作内存多约 2MB（小图更少）；`MemoryLimit` 小于此时返回 `ErrMemoryLimit`。`PNGOptions{CompressionLevel: 6}` 可得原来的文件，所需内存也更少。
- `Threads(n)` 不再以 3 为上限：n 更大时即用更多核心。
- JPEG 按 libjpeg-turbo 的方式写出（即 Pillow、sharp 的字节），不再按 `image/jpeg` 的方式：同一质量下像素相差约一个色阶，文件大小相当。
- 颜色未乘以 alpha 的图像（PNG、WebP 等大多数图像）的 `Flatten`，以及带透明图像的 JPEG 输出（铺黑底），改按 libvips 的算法取色：`(颜色×alpha + 背景×(255−alpha)) / 255`，舍去小数，与 sharp 逐字节相同；以前个别像素相差 1。
- 不带色彩配置的 CMYK 文件（JPEG、TIFF）按 libvips 的 CMYK 配置转为 sRGB，与 libvips、sharp 相同，不再按 `color.CMYK` 的简单公式：印刷油墨呈现为印刷出的颜色（满版青色为 RGB 0,153,228，不再是 0,255,255）。交给 `FromImage` 的 `*image.CMYK` 仍保持 `color.CMYK` 的值。

### 新增

- 隐形水印：`InvisibleWatermark`（`InvisibleWatermarkOptions`：`Kind`、`Key`、`Strength`、`Density`；`WatermarkKind`：`WatermarkBlind`、`WatermarkScreen`；`MaxWatermarkBytes`）藏入 1–32 个字节，用 `inspect.ReadWatermark` 与密钥读回，不需要原图：码片图块由正弦参考信号、同步图案与经卷积码编码并带 CRC 的字节组成，加在图像能藏住它的地方；读取时由频谱找出图块排布，叠加各副本，逐区域对准（照片的透视用单应变换），再译码。`WatermarkBlind` 经得起重新保存、缩放、裁剪、旋转、调亮度与对比度、截图；`WatermarkScreen` 还经得起对着屏幕拍照（见[隐形水印](watermark.md)）。按名字调用 `invisible-watermark`；命令行 `-mark`、`-mark-key`、`-mark-kind`、`-mark-strength`、`-mark-density` 与 `goimgtools readmark`。
- 图像调整操作：`Grayscale`、`Modulate`、`Blur`、`Sharpen`（轻度锐化或反锐化掩模）、`Extend`、`Trim`，以及任意角度的 `Rotate`（与 sharp 的 `rotate` 逐字节一致）。
- `inspect.Stats`、`inspect.Hash`（感知哈希）、`inspect.ReadExif` / `ParseExif`；子命令 `stats`、`hash`、`exif`。`Stats` 还报告 sharp 的 `stats()` 所报告的各项，数值一致到末几位：`Channels`（类型 `ChannelStats`）、`Entropy`、`Sharpness`。
- 按名称注册操作：`RegisterOperation`、`UnregisterOperation`、`LookupOperation`、`Operations`、`Image.Do`。包裹每一步骤的钩子：`Image.Around`。`ParseColor`。
- `cli` 包：以库的形式提供命令行（`cli.Main`、`cli.Run`），程序可向其中加入自定义操作；命令行选项 `-op 名字[:参数]` 与子命令 `ops`。
- `ToSRGB()` / `-srgb`：依据色彩配置将颜色转换至 sRGB。GIF、BMP 无法附带色彩配置，输出时自动转换。
- 支持灰度色彩配置（色调曲线，如 Photoshop 的网点扩大配置），转换后仍为灰度。支持查找表类色彩配置（lut8、lut16、lutAToB；RGB 与 CMYK，连接空间为 Lab 或 XYZ），按其感知表转换，插值方式与 LittleCMS 相同（CMYK 印刷配置与其感知转换相差不超过 1 级），黑点补偿也与 LittleCMS 相同。带色彩配置的 CMYK JPEG 解码时即经配置转为 sRGB，与 sharp 相同（以前按简单公式转换，并把 CMYK 配置写进 RGB 输出）。
- WebP、TIFF 支持读写色彩配置；WebP 支持读写 EXIF。
- `JPEGOptions.Trellis` / `-jpeg-trellis`：网格量化（采用 mozjpeg 的方法）。
- `JPEGOptions.QuantTable`（`QuantTable` 类型，每种选择有具名常量）/ `-jpeg-quant-table`：mozjpeg 的 9 套量化表，编号与其 `-quant-table` 一致；默认 `QuantTableStandard`（0）为 JPEG 标准表。
- `QuantTableJpegli`（`-jpeg-quant-table 9`）：按 jpegli 的方法生成 JPEG 量化表，不开 `Trellis` 时采用其死区；与 `Progressive`、`Trellis` 同用时，同等 SSIMULACRA2 下比 mozjpeg 默认设置小 15%。
- `-webp-effort`：命令行可设 WebP 的力度（库里为 `WebPOptions.Effort`）。
- `GIFOptions.Lossy` / `-gif-lossy`：GIF 有损 LZW 压缩，静态与动画均可（gifsicle `--lossy` 的方法）：照片类动画取 16 时小 24%，取 24 时小 39%。
- GIF 动图：各帧合计超过 256 色但每帧不超过 256 色时，每帧用自己的调色板，逐像素不变（以前会减成一个调色板，既改了像素又让文件变大）；变化的帧在三种写法中取编码最小者，LZW 字典写满后在划算时继续沿用：三段动画上比 gifsicle `-O3` 小 0.2–0.45%。
- WebP 动画输出：`Animated()` 且输出为 WebP 时，将 GIF 动画写为 WebP 动画（有损或无损），每帧仅存储变化的矩形区域。
- WebP 动画输入：读取其第一帧（此前作为无法读取的图像拒绝）；使用 `Animated()` 时逐帧处理并写为 GIF 或 WebP 动画：每帧按 libwebp 动画解码器的方式画到画面上（混合或直接覆盖，之后清除其矩形与否），逐像素一致；各帧时长与播放次数随之保留（写为 GIF 时以百分之一秒计，次数按首次之后的重复次数）。`Frames` 可统计 WebP 动画与 PNG 动画（APNG）的帧数，`info` 一并显示。
- PNG 动画（APNG）输入：使用 `Animated()` 时逐帧处理并写为 GIF 或 WebP 动画，每帧按 APNG 规范绘制（直接覆盖或按 apngdis 的方式合成在上；之后清除、复原或保留），逐帧与按规范编写的渲染程序对照一致。
- 输出 PNG 动画（APNG）：使用 `Animated()` 且输出为 PNG 时写出无损 APNG：所有帧共不超过 256 种颜色时共用一套调色板（否则为 RGB 或 RGBA），之后各帧存储与其底图不同的矩形（前一帧保留、清除或撤销，按 apngopt 的方法选择），原样写入或把未变像素设为透明后叠加，取最小者；相同的帧合并。Pillow 读回逐像素一致；一段小球移动的 GIF 动画缩小 30%，Pillow 写出的两段照片类 APNG 分别再小 0.1% 与 18%。
- `CompositeOptions.WidthRatio` / `HeightRatio`：按底图的比例确定水印尺寸；`-watermark-width N%`。
- 有损 WebP 分段量化：各段使用独立的量化级别（移植自 libwebp 的空间噪声整形）。
- `pipeline.Env.SetHelpers`、`Helpers`、`Parallel`、`Go`：供处理环节将工作分摊至多个核。
- TIFF 输出：`TIFFLZW` 压缩（libtiff 的写法）与 `TIFFOptions.Predictor`（水平差分，sharp 的默认设置）：一张 2400 万像素照片 LZW 82 → 52MB、Deflate 67 → 45MB；`-tiff-compression lzw`、`-tiff-predictor`。
- `SharpenOptions.Method` 新增 `MethodLibvips`，配合 `SharpenOptions.Curve`（`SharpenCurve`、`DefaultSharpenCurve()`）：sharp 带 sigma 的 `sharpen`，只锐化明度（仅在少数像素上相差 1 级）；按名字调用 `sharpen SIGMA,libvips[,FLAT,JAGGED,JAGGED_FROM,MAX_BRIGHTEN,MAX_DARKEN]`，命令行 `-sharpen` 同样写法。
- `Affine(a, b, c, d, AffineOptions{...})`：与 sharp 的 `affine` 逐字节一致（`Interpolator` 可选双线性、最近邻、双三次，含透明与不含透明）；按名字调用 `affine A,B,C,D[,BACKGROUND[,IDX,IDY,ODX,ODY]][,bilinear|nearest|bicubic]`。
- `EnsureAlpha`（与 sharp 的 `ensureAlpha` 逐字节一致）；按名字调用 `ensure-alpha [ALPHA]`。
- 缩放核 `KernelMKS2013` 与 `KernelMKS2021`（Magic Kernel Sharp，即 sharp 的 `mks2013`、`mks2021`）；命令行 `-kernel mks2013|mks2021`。
- `Rank`（Pillow 的 `RankFilter`、`MinFilter`、`MaxFilter`）、`Dilate` 与 `Erode`（sharp 的 `dilate`、`erode`），均逐字节一致；按名字调用 `rank SIZE,RANK`、`dilate [WIDTH]`、`erode [WIDTH]`。dilate 与 erode 的耗时几乎不随宽度增长（libvips 随宽度的平方增长）。
- `BlurOptions`（`Method`：默认 `MethodPillow` 即 Pillow 的算法，或 `MethodLibvips`，与 sharp 的 `blur` 逐字节一致，含透明图像；`MinAmplitude` 即 sharp 的 `minAmplitude`）；按名字调用 `blur SIGMA,libvips[,MIN_AMPLITUDE]`；命令行 `-blur-method`、`-blur-min-amplitude`。高斯核拆成至多 20 个嵌套方框计算，耗时几乎不随 sigma 增长。
- 仿照 sharp 的选项：`NegateOptions.Alpha`（透明度也取反，sharp 的默认做法）、`ThresholdOptions.PerChannel`（即 sharp 的 `greyscale: false`）、`ModulateOptions.Lightness`（即 sharp 的 `lightness`）与 `GrayscaleOptions.Linear`（即 sharp 的 `greyscale`，逐字节一致）；按名字调用 `negate alpha`、`threshold LEVEL,channels`、`modulate B,S,H,L`、`grayscale linear`；命令行 `-grayscale-linear`，`-modulate` 可加第四个数。
- XMP：`KeepXmp()` / `-keep-xmp` 在 JPEG、PNG、WebP、TIFF 输出中保留 XMP 数据包；`Info.HasXmp`、`inspect.ReadXmp`；自定义格式可用 `EncodeOptions.Xmp` 与 `pipeline.Meta.Xmp`。读写方式与 sharp 相同（在 sharp 写出的文件上双向核对）。
- `Trim` 的找边方式与 sharp 的 `trim`（libvips 的 `find_trim`）相同：在 3×3 中值滤波后的图像上找边，噪点与斑点不再妨碍裁剪（像素相同时裁剪范围与 sharp 完全一致）；`TrimOptions` 可指定 `Background`、`LineArt`（不做中值滤波，适用于细线条绘图）与 `Margin`；命令行 `-trim-background`、`-trim-line-art`、`-trim-margin`。
- `JPEGOptions.ChromaSubsampling`（类型 `ChromaSubsampling`：默认 `Chroma420`、`Chroma422`、`Chroma444`）/ `-jpeg-chroma`：JPEG 输出可保留全部色度或横向一半，普通、优化、渐进与网格量化均支持（即 sharp 的 `chromaSubsampling`）。
- 可读取 JPEG 压缩的 TIFF 文件（压缩方式 7：libtiff 的写法，libvips 与 sharp 的默认设置），条带与图块、彩色与灰度均支持。
- 分辨率：`Info.Density`（每英寸像素数，读取自 JFIF、EXIF、pHYs、TIFF 与 BMP），默认保留到输出，`Density(dpi)` / `-density` 可设置；自定义格式可用 `EncodeOptions.Density` 与 `pipeline.Meta.Density`。`info` 会显示它。
- `ExtendOptions`（`Background`；`Mode`，类型 `ExtendMode`：`ExtendCopy`、`ExtendRepeat`、`ExtendMirror`）/ `-extend-with`：用图像本身扩边，与 sharp 带 `extendWith` 的 `extend` 逐像素一致；`Do("extend")` 在颜色的位置接受 copy、repeat 或 mirror。
- `Filter`（类型 `ImageFilter`）：Pillow 的内置滤镜（模糊、轮廓、细节、边缘增强、浮雕、找边、锐化、平滑），以及 `Convolution.Method` 的 `MethodPillow`：其 `ImageFilter.Kernel`，逐字节一致；按名字调用 `filter NAME` 与 `convolve …,pillow`。
- `BoxBlur(radius)`（`boxblur`）：即 Pillow 的 `ImageFilter.BoxBlur`，逐字节一致，含小数半径。
- `RotateOptions`（`Background`；`Method`：`MethodLibvips` 即 sharp 的，或 `MethodPillow` 即 Pillow 的 `Image.rotate`，逐字节一致；`Interpolator`；`Expand`）；按名字调用 `rotate DEGREES[,BACKGROUND][,pillow][,nearest|bilinear|bicubic][,expand]`。
- `ToTiles(path, TileOptions)`：瓦片金字塔，与 sharp 的 `tile`（libvips 的 `dzsave`）相同，JPEG 瓦片及描述文件逐字节一致，带透明的图像也一样：Deep Zoom（`.dzi`）、Google 地图（`TileGoogle`，含 `blank.png`）、IIIF Image API 2 与 3（`TileIIIF`、`TileIIIF3`，含 `info.json`）、Zoomify（`TileZoomify`）；`TileOptions.Depth`（`TileDepth`）、`Background`、`Center`、`SkipBlanks`、`ID` 对应 sharp 的 `depth`、`background`、`centre`、`skipBlanks`、`id`；路径以 `.zip` 结尾时写入 ZIP 压缩包，或用 `ToTilesWriter` 写到任意 writer，同 sharp 的 `zip` 容器；流式处理，瓦片在辅助线程上编码；也可 `ToFile("x.dzi")`，命令行输出名为 `.dzi` 时配合 `-tile-size`、`-tile-overlap`、`-tile-layout`。
- sharp composite 的 Porter-Duff 混合模式及 add、saturate：`BlendClear` 至 `BlendXor`、`BlendAdd`、`BlendSaturate`（`BlendDestIn` 配遮罩只保留遮罩不透明处的底图）；`-watermark-blend` 也可用。
- 模块 `github.com/whhhh1500/goimgtools/heic`：用 gen2brain/h265（纯 Go 的 HEVC 编解码器，解码与一致性测试集逐位一致；有 SIMD，`-tags noasm` 时不用，像素相同）读写 HEIC：网格、透明、irot/imir/clap（随后 EXIF 方向设为 1）、EXIF、XMP、色彩配置（nclx 声明 Display P3 时补上 P3 配置）均读出并可写入；`heic.Options{Quality, Effort, Lossless, Chroma444}`，MaxFileSize 按其质量。
- 模块 `github.com/whhhh1500/goimgtools/avif`：读写 AVIF（libavif 配 dav1d 与 aom，编译为 WebAssembly，经 gen2brain/avif；无需 cgo），导入即注册；`avif.Options{Quality, Effort, Lossless, Chroma444}`，MaxFileSize 按其质量；按 irot、imir 转正。
- TIFF 输出使用 Deflate 或 LZW 时，各条带交给其他核心压缩（`Threads(4)` 及以上），文件不变；6000×4000 的照片用 8 线程写成 Deflate 只需 0.49 秒，原为 1.73 秒。
- `Curves(points, CurvesOptions)`：色调曲线（经过各点的单调三次样条），作用于全部颜色通道或单个通道；按名字调用 `curves`。
- 灰度图像变成彩色后（叠加彩色内容、`Tint`）不再保留其灰度色彩配置（彩色像素无法使用）：各通道按配置的曲线转到 sRGB。
- TIFF 文件的 EXIF：从第一个目录及其指向的 EXIF、GPS 目录读出（于是 `AutoOrient()` 按 TIFF 的方向转正，同 Pillow 的 `exif_transpose` 与 sharp；`KeepExif`、`inspect.ReadExif` 都能看到），使用 `KeepExif` 时也写到那里；Pillow 读回写入的标签不变。
- JPEG 解码在 `Threads(5)` 及以上时，顺序式 JPEG 的反 DCT 交给其他核心，与下一行块的熵解码同时进行；6000×4000 的 JPEG 解码从 172ms 降到 122–142ms（像素不变）。
- GIF 动画输出：没有任何变化的帧不再写出，而是延长前一帧（最长 655.35 秒），与 WebP、APNG 输出一致。
- `MaxFileSize` 支持 PNG 与 GIF：选用能满足的最多颜色数（PNG 先试无损）；`-max-size` 同。
- `Transform(width, height, t, TransformOptions)`：即 Pillow 的 `Image.transform`，逐字节一致，配合 `TransformAffine`、`TransformExtent`、`TransformPerspective` 与 `TransformQuad`，以及 `TransformPerspectiveQuad`（由四个点算出把输出四角映射到这四点的透视变换）；按名字调用 `transform WIDTH,HEIGHT,KIND,NUMBERS...[,INTERPOLATOR][,BACKGROUND]`。
- Pillow 的 `ImageOps` 与 `ImageEnhance`，逐字节一致：`Posterize`、`Solarize`、`Equalize`、`AutoContrast`（`AutoContrastOptions`）与 `Enhance`（类型 `Enhancement`：`EnhanceColor`、`EnhanceContrast`、`EnhanceBrightness`、`EnhanceSharpness`）；按名字调用 posterize、solarize、equalize、autocontrast、enhance。
- `ResizeOptions.Method`（类型 `Method`）/ `-resize-method`：`MethodLibvips` 即 sharp 的 `resize`，逐字节一致，涵盖所有核、fit、位置与背景，包括解码时缩小的 JPEG，以及配合 `FastShrinkOnLoad` 时按 libwebp 的方式在解码时缩放的 WebP（在 sharp 上核对了 578 次缩放）；`MethodPillow` 即 Pillow 的 `Image.resize`，逐字节一致（核对了 346 次缩放），各种 fit 即 `ImageOps` 的 `contain`、`cover`、`pad`、`fit`，配合 `ResizeOptions.ReducingGap`（`-reducing-gap`）即其 `reducing_gap` 与 `Image.thumbnail`；默认的 `MethodDefault` 与以前相同。`Do("resize")` 以第七个参数接受 `draw`、`libvips` 或 `pillow`。`KernelBox` 与 `KernelHamming`（`-kernel box`、`hamming`）：Pillow 的 `BOX` 与 `HAMMING`。
- `ResizeOptions.LinearLight` / `-linear-light`：在线性光下缩放，同 libvips `thumbnail` 的 `linear`；`Do("resize")` 以第六个参数 `linear` 接受。
- `ResizeOptions.Position`（`FitCover` 保留的部分、`FitContain` 放置图像的位置）与 `ResizeOptions.Interesting`（类型 `Interesting`：`InterestingEntropy`、`InterestingAttention`，即 sharp 的智能裁剪策略）/ `-position`。`Do("resize")` 以第五个参数接受它们。
- `Recomb`、`Unflatten`、`ExtractChannel`（类型 `Channel`）、`RemoveAlpha`，仿照 sharp 的 `recomb`、`unflatten`、`extractChannel`、`removeAlpha`（前两者逐字节一致），也可按名称调用：recomb、unflatten、extract-channel、remove-alpha。
- `CompositeOptions.Blend`（类型 `BlendMode`）/ `-watermark-blend`：sharp 合成的各种混合模式（正片叠底、滤色、叠加、变暗、变亮、颜色减淡与加深、强光与柔光、差值、排除），与 sharp 的结果相差不超过 1。
- 仿照 sharp 的色阶与滤镜：`Negate`、`Linear`、`Gamma`、`Threshold`、`Tint`、`Normalize`（`NormalizeOptions`）、`CLAHE`（`CLAHEOptions`）、`Median`、`Convolve`（`Convolution`），也可按名称调用（`Do`、`-op`）：negate、linear、gamma、threshold、tint、normalize、clahe、median、convolve。`Negate`、`Linear`、`Threshold`、`CLAHE`、`Median`、`Convolve` 与 sharp 的结果逐字节一致，`Normalize` 与 `Tint` 相差不超过 1；`Median` 也与 Pillow 的 `MedianFilter` 逐字节一致。

### 变更

- 默认级别 7 的 PNG 改用移植的 libdeflate lazy 解析（其 7 级，逐字节相同）压缩，不再用 `compress/flate`，各 Go 版本结果相同。与 Go 1.27 的 7 级相比，126 张图像几何平均小 1.8%（从小 28% 到大 2.5%），单核端到端耗时多 19%。调色板图像（减色后的，或源图即为调色板的）获益最大，因为 compress/flate 的 7 级在这类行数据上表现差：256 色 PNG 照片小 2.4–3%，截图小 4.6–8%，平滑渐变小 3–25%（一张不抖动的渐变小到 1/2.3）。不论线程数与内存限制，文件都相同。多线程且内存限制较紧时，7 至 9 级的 PNG 压缩在放不下更多块时改为等待一块完成，此前会失败。压缩器所占内存计入内存限制，并按图像大小分配：150×103 的图像需 0.8MB，大图 2.3MB。
- JPEG（以及有损 WebP）的 `FastShrinkOnLoad`：其余缩放（对 Y、Cb、Cr 平面）改用 AVX2（arm64 上为 NEON）：14 位权重与样本的乘积以成对的 16 位乘法计算，横向每次 8 个目标样本，纵向每次 16 个，整数结果相同，字节因而相同。6000×4000 的 JPEG 缩到 1536 以内，解码加缩放单核 70ms，原为 106ms（sharp：77ms）；写成 WebP 缩略图 140ms，原为 179ms（sharp 用其默认力度 4，本模块默认为 5：149ms）；4:4:4 的写成 JPEG 160ms，原为 212ms。
- GIF 输出与 PNG 减色按 libimagequant（pngquant 的库，sharp 也用它）的方式比较颜色：在其 gamma 下（各通道取 1.21 次幂），绿色权重高于红、蓝（1、0.5、0.45），此前三者等同；抖动时超过 10 级的误差只传四分之三，同 libimagequant 对大误差的抑制，免得它们在后续像素上累积成错色斑点。12 张照片减到 256 色写成 GIF，SSIMULACRA2 由 75.6 升到 81.3，butteraugli 由 1.66 降到 1.07，文件小 2%（sharp：76.9、1.64）；800 万像素照片减到 256 色的 PNG 由 70.9 升到 78.5，文件小 4%，缩到四分之一尺寸时为 82.3，pngquant 为 79.5，文件小 11%；GIF 动画减到 128 色由 76.8 升到 82.8。PSNR 平等看待各通道，可能下降至多 1.5dB。用时多 1–2%。
- JPEG 按 libjpeg-turbo 的方式写出，逐字节一致（与 sharp 的文件完全相同，与 Pillow 的文件只差其 JFIF 段）：libjpeg 的颜色转换、下采样、精确整数正 DCT、边外填充块、优化哈夫曼表与标记，速度不变。分辨率按 libjpeg 写为 JFIF 1.01。
- `FastShrinkOnLoad` 也用于无 alpha 的有损 WebP：缩小至三分之一以下时，按 libwebp 解码时的缩放方式以结果两倍的尺寸解码，其余由缩放核完成（6000×4000 的缩到 400×400 以内 0.39s，原为 0.74s；与不用此选项的结果相比 48–51dB）。
- `FastShrinkOnLoad` 改用 libjpeg 的缩小反 DCT（`jidctred.c`）缩小各块，不再用自行推导的平均，速度不变：4:4:4 与灰度 JPEG 的平面与 libjpeg 的缩放解码（Pillow 的 `draft`）相同，与完整解码后缩放的结果一样接近（46.7–50.5dB）。
- JPEG 改为按 libjpeg-turbo 的方式解码，因此与 Pillow、sharp 逐字节一致（精确整数反 DCT、下采样色度的 fancy 上采样、YCbCr 转 RGB 查表），不再与 `image/jpeg` 相同：下采样色彩的边缘更平滑（一张 4:2:2 照片与原图的差距少 0.36 dB），所有与 Pillow、sharp 结果一致的操作在 JPEG 输入上也一致。彩色 JPEG 的行带改为 RGBA 而非 YCbCr，直接写成 JPEG 或有损 WebP 时除外。
- TIFF 解码：LZW 快 1.5 倍（6000×4000 的 RGBA 图像 0.45 秒，原为 0.68 秒；libtiff 0.31 秒），对任何输入的输出与错误都与 `x/image` 相同（经模糊测试核对）；Deflate 块按 64KB 读取（0.26 秒，原为 0.30 秒；libtiff 0.27 秒）。
- `FitCover` 居中裁剪的位置与 sharp 相同：图像比框多出奇数个像素时，多出的那一个从左侧（上方）而非右侧（下方）裁去。
- JPEG 编码：量化结果由 SIMD 内核直接按之字形顺序写出，非零系数掩码用 AVX2 求出：6000×4000 质量 85 单核 100→87ms，优化霍夫曼表 168→151ms。
- AVX2 下的 JPEG 编码：正向 DCT 改在 16 位通道中进行，一个寄存器放两行样本，照 libjpeg-turbo 的 jfdctint-avx2（一条指令完成成对样本的乘加），此前是 32 位通道和较慢的乘法；量化照 libjpeg-turbo 的 jquanti-avx2 以两次 16 位取高位乘法做除法，测试核对了对 32767 以内每个系数、每个量化值都等于四舍五入的商；之字形顺序用寄存器中各行的字节重排完成，此前是 64 个系数逐个搬动。字节不变。6000×4000 的内存 RGB 图像质量 85：单核 70ms，原为 74ms；优化表 107ms，原为 112ms。
- 无损 WebP 解码：每个像素解码时即放入颜色缓存（字面量立即放入，复制在复制后放入），此前是在每次查缓存前补放上次以来的像素；从缓存取出的像素不再放入（它本来就在要放的位置）；只有一个符号的编码（不透明图像的 alpha）不查表直接取值，下一个编码不必等它；逆预测 0–4 与 8–12 在 amd64 上用 AVX 一次 4 个像素（照 libwebp 的 SSE2 代码：Select 用绝对差之和，夹取的一种用 16 位通道），绿色加回一次两个像素。像素不变。6000×4000 照片单核 0.31s，原为 0.42s，带 SIMD 的 libwebp 1.6 `dwebp` 为 0.32s；250 万像素的照片 24ms，原为 32ms（dwebp 25ms）。
- 渐进 JPEG：扫描搜索对每个候选扫描的每个块，按 libjpeg-turbo 编码器的做法，从一个在该位平面不为零的系数直接跳到下一个，不再逐个查看；哪些系数不为零以无分支的方式求出（amd64 上用 AVX2，一次 64 个），每块最后一个非零系数也是如此。字节不变。6000×4000 的内存 RGB 图像质量 85：单核 0.75s，原为 1.85s（mozjpeg 的扫描搜索经 sharp：1.17s）。
- 内存中的图像（`FromImage`）编码时不再复制行：按每条 16 行交出（16 行不超过 1MB 时），使 JPEG 与 WebP 编码器的编码带落在同一条内、直接以视图传下，此前要从 5 行一条的条带中复制出来；`*image.RGBA` 与 `*image.NRGBA` 是否有透明像素一次查 8 个像素，JPEG 编码器只对 NRGBA 图像询问（只有它需要）。6000×4000 的 `*image.RGBA` 写成 JPEG（质量 85）单核 74ms，原为 87ms。字节不变。
- 有损 WebP 解码：系数在 amd64 与 arm64 上由汇编读取（只用普通整数指令，arm64 上 Go 1.26 也可用；purego 标签仍用 Go 代码），每读一位后都按查表重整区间，不再只在区间低于 127 时才重整——那需要一个难以预测的分支；结束一个块或跳过零系数的位直接跳到相应代码。读取窗口末尾、块可能需要尚未读入的字节时，仍由 Go 代码读取，报错仍在相同的行。只有 DC 系数的块（照片中约占一半）按 libwebp 的做法只加上 DC（AVX2 或 NEON），此前凡有系数的块都做完整逆变换；DC、竖直、水平预测整行写入。amd64 上的环路滤波改照 libwebp 的 SSE2 代码：8 位通道饱和运算，宏块亮度的三条内部边一次调用完成，载入的列（或行）留给下一条边，此前每条边各自载入、扩成 16 位并转置：耗时减为约三分之一。像素完全相同。6000×4000 照片（质量 80）单核解码 0.25s，原为 0.32s（带 SIMD 的 libwebp 1.6 `dwebp`：0.256s）；purego 标签下 0.51s，原为 0.53s；解码时缩小到 400×400 以内 0.27s，原为 0.39s。
- `Modulate` 记下每种颜色的结果，每个线程一个直接映射的 3.2 万色缓存（256KB，计入内存上限）：照片数百万像素中只有几十万种颜色，九成直接查到，不再重算，像素不变。JPEG 编码器重新把不透明的 NRGBA 条带（来自 `Modulate`、锐化、CLAHE 等操作）当作 RGBA 处理，字节不变——此前重切成编码带时丢了“不透明”这一信息。6000×4000 的照片做 `Modulate(1.1, 1.3, 20)`，单线程 0.41s，原为 0.71s。
- JPEG 编码：一个块的霍夫曼码字与数值位放进 64 位缓冲，满后一次写出 8 字节（同 libjpeg-turbo 的 put_buffer），标准表与优化表都如此，字节不变：6000×4000 的照片以质量 85 单核 78ms（原 85ms），优化表 118ms（原 151ms；libjpeg-turbo 经 Pillow 为 66ms 与 167ms）。有损 WebP 解码按 libwebp 的 VP8GetBitAlt 读位（区间仍大时不重整），快 1%。
- 有损 WebP 力度 1 不再等同于力度 2：16×16 与色度预测按预测误差（加模式代价）直接选定、只编码选中的一种（与 libwebp 方法 0 至 2 的选法相同），每个 4×4 块只编码排名前两种而非四种。6000×4000 的照片单核 1.52s（原 2.12s）；14 张图上，同等 SSIMULACRA2 与 butteraugli 下文件比力度 2 大 2.5% 与 4.0%。
- 颜色来源（PNG、缩放或铺底后的图像，而非 JPEG 的 Y'CbCr）的有损 WebP：sharp YUV 每一步按列分给辅助线程，上采样与修正用 AVX2 或 NEON，每个采样仍与 libwebp 相同：6000×4000 的照片铺白底后以质量 75 编码，单核 4.7s（原 5.2s），8 线程 1.2s（原 1.8s）。
- 有损 WebP 解码：逆变换改用 AVX2 或 NEON（与编码器重建所用为同一内核），每个字节不变：6000×4000 的照片单核解码 0.33s，原为 0.36s。
- `Blur`、反锐化掩模、`Median(3)` 与 `Trim` 改用 AVX2 或 NEON，每个字节不变：模糊的方框两遍（Pillow 的整数算术）横向一次处理四个通道、纵向一次八列，3×3 中值一次 32 字节。任意角度旋转直接从所在图块读取插值用的像素。6000×4000 的照片单线程：`Blur(2)` 增加 0.14s（原 0.48s，Pillow 0.38s），`Sharpen(SharpenOptions{Sigma: 2})` 0.22s（原 0.57s，Pillow 0.48s），`Trim(10)` 0.09s（原 0.91s），`RotateBy(30)` 0.51s（原 1.13s，含较大输出的编码）。
- 默认缩放（`x/image` 的算术，float64）改用 AVX2 或 NEON：上下、左右两遍求和一次处理一个像素的四个通道，每个乘积先舍入再相加（从不融合），顺序与 Go 代码相同，RGBA 行转为 float64 也同样处理；每个和都逐位相同，像素因此不变。6000×4000 的 JPEG 缩入 1536×1536 并写成 JPEG，单核 0.17s（原 0.25s）；1080p 放大到 4K 0.09s（原 0.12s）。
- 有损 WebP 编码：模式搜索中的 4×4 正变换（参照 libwebp 的 FTransform_SSE2）、逆变换、量化与平方误差，以及分析阶段的系数直方图，改用 AVX2 或 NEON；trellis 把每个系数的两个节点放在寄存器里，到达各节点的路径记为位掩码，并边走边累计该块的比特数；块的比特数求和不再有分支。每个数都与 Go 代码相同，文件不变：6000×4000 的照片以质量 80 单核编码，默认力度 4.4s（原 6.5s），力度 1 为 2.6s（原 4.0s）。
- `Resize` 默认方法保留 16 位采样：16 位的 PNG、TIFF 缩放为 16 位条带（`*image.RGBA64`、`*image.Gray16`），数值与 `x/image` 的 `Scale` 写入此类目标图相同，并按 16 位写出；此前会截为 8 位。
- PNG 编码：每行的五种过滤（以及 libpng 启发式据以选择的各项求和）用 AVX2 或 NEON 计算，过滤结果与选择不变：6000×4000 的照片转 PNG（7 级）单核 1.43s，原为 1.73s。
- arm64 使用 NEON（需 Go 1.27 及以上；`purego` 构建标签可关闭）：所有 AVX2 内核（JPEG 解码与编码、有损 WebP 解码、环路滤波强度搜索、`MethodLibvips` 的缩小）都有 NEON 版本，每个字节与 Go 代码相同。
- 有损 WebP 力度 3 在挑选 4×4 预测模式时不用网格量化，只对选中的模式使用（同 libwebp method 5）：比力度 4（此前两者相同）快 15%，同等 SSIMULACRA2 与 butteraugli 下文件约大 1%（14 张图）。
- 有损 WebP 编码使用 AVX2（amd64；`purego` 构建标签可关闭）：搜索环路滤波强度时，用解码器的 AVX2 环路滤波处理每个宏块，并以 AVX2 计算各窗口的 SSIM 求和，每个数与 Go 代码相同，文件不变：6000×4000 的照片以质量 80 单核 6.5s，原为 8.3s。
- 缩放使用 AVX2（amd64；`purego` 构建标签可关闭）：`MethodLibvips` 的纵向与横向缩小，以及 JPEG 解码时缩小所用 libjpeg 的 4×4 逆 DCT，以汇编执行，每个字节与 Go 代码相同（因而与 sharp 相同）；无透明图像的 NRGBA 条带编码为 JPEG 时与 RGBA 走同一转换。6000×4000 的 JPEG 缩到 1536×1536 以内（Lanczos 3、`FastShrinkOnLoad`）并写成 JPEG：单核 109ms，原为 196ms（sharp：92ms）。
- JPEG 编码使用 AVX2（amd64；`purego` 构建标签可关闭）：RGB 到 YCbCr 转换、色度下采样、正向 DCT 与量化以汇编执行，每个字节与 Go 代码相同；优化霍夫曼表的第一遍改为暂存符号（每个一字节），不再写出用标准表编码、第二遍还需解码的扫描数据。6000×4000 的照片以质量 85 写出：单核 100ms，原为 213ms（libjpeg-turbo 经 Pillow：66ms）；优化霍夫曼表 168ms，原为 388ms（167ms）。
- JPEG 解码使用 AVX2（amd64；`purego` 构建标签可关闭）：逆 DCT 与颜色转换（连同色度上采样）以汇编执行，参照 libjpeg-turbo 的 SIMD 代码，每个字节与 Go 代码相同：6000×4000 的照片（质量 90）单核解码 99ms（4:2:0）、109ms（4:2:2）、126ms（4:4:4），原为 208、226、250ms，与带 SIMD 的 libjpeg-turbo 相当（经 Pillow：101、110、121ms）。
- JPEG 解码快 45%，像素不变：熵编码数据一次读 64 位，经 11 位查表把多数系数的值连同其码一起解出，系数边解边反量化，逆 DCT 跳过全零的行（6000×4000 的基线照片单核 98ms，原为 188ms）。启用 `FastShrinkOnLoad` 时由系数直接解出原尺寸 1/2、1/4 或 1/8 的图（同 libjpeg），不再完整解码后求平均，其余缩放直接在 Y、Cb、Cr 平面上以定点进行，不再转成 RGBA：把这张照片缩到 400×400 以内用时 82ms，原为 231ms（libvips 的 `vipsthumbnail`：97ms）；缩到 1536×1536 以内 157ms，原为 320ms（148ms；2 线程 108ms，对方 172ms）。
- 从文件或内存读取的渐进式 JPEG 按扫描同步解码，逐行块进行，不再保存整幅图的系数（6000×4000 时 72MB，默认上限下写入临时文件），细化扫描只访问非零系数：6000×4000 的照片解码用时约为原来的三分之一（单核 0.24s，原为 0.70s），不用临时文件，内存多 0.5MB。启用 `FastShrinkOnLoad` 缩小 8 倍时每块即其像素的平均值，只解码 DC 系数的扫描：这张照片缩到 400×400 以内用时 50ms，原为 745ms（libvips 的 `vipsthumbnail`：228ms）。损坏的扫描留下未完的块结束游程时，游程随该扫描结束（同 libjpeg）；标准库会把它带入下一个扫描。
- JPEG 编码快一倍多，写出的字节与之前相同（即标准库 `jpeg.Encode` 的输出）：熵编码数据 64 位一组收集、32 位一组写出，一个块的位始终保存在寄存器中；只访问非零系数，每次查找四个；量化不再分支；RGBA、NRGBA 像素的颜色转换内联完成；4:2:0 子采样的 YCbCr 图像（解码得到的 JPEG 大多如此）直接取用其色度，不再先铺到每个像素再平均回去（4:4:4 则直接从平面求平均）。单核重新编码 6000×4000 的 JPEG：0.23s，原为 0.54s（libvips：0.12s；2 线程 0.13s，与其 0.13s 相当，4 线程 0.10s）。
- PNG 图像与 Deflate 压缩的 TIFF 图像解压快约一倍（`internal/inflate`，按 libdeflate 的方法编写：64 位位缓冲无分支补充，查表同时得到码的附加位，补充前先查好下一个码，匹配每次复制 8 字节），接受与拒绝的数据和以前相同，从文件读取的量也相同。单核解码 6000×4000 的 RGB PNG 用时 0.25s，原为 0.37s（libvips：0.25s）；转为 JPEG 用时 0.39s，原为 0.51s（0.25s）。
- 用 Paeth 滤波的 PNG 行改为一个像素的各通道一起、无分支地还原（标准库沿整行还原一个通道后再还原下一个）：6000×4000 的 RGBA 照片缩到 1536×1536 以内，单核 0.45s，原为 0.51s；2 线程 0.26s，原为 0.32s（vipsthumbnail：0.43s）。
- 默认 effort 的有损 WebP 编码快 25%，写出的文件不变：两遍之间保存的已编码记号按 libwebp 的位读取方式读回（每次 56 位），选择环路滤波强度所用的 SSIM 求和直接在固定的 16×16、8×8 块上进行，trellis 每个系数只查一次代价表。6000×4000 的照片：单核 7.5s，原为 10.1s；8 线程 1.55s，原为 1.79s。
- 有损 WebP 解码使用 AVX2（amd64；`purego` 构建标签可关闭）：环路滤波以及色度上采样与颜色转换以汇编执行，参照 libwebp 的 SSE2 代码，每个字节与 Go 代码相同：6000×4000 的照片单核解码 0.36s，原为 0.57s，快于带 SIMD 的 libwebp 1.6（经 Pillow 0.50s）。
- 有损 WebP 解码快 15–18%，像素不变：按 libwebp 的 VP8GetBit 读取位，一个块的系数用局部变量保存的读取状态解析（质量 95 的 6000×4000 图像：0.78s，原为 0.95s；dwebp 0.48s）。
- 有损 WebP 解码在有空闲核时分摊工作（`Threads` 为 4 及以上）：每行宏块的环路滤波在解码下一行时进行（同 libwebp 的多线程解码），转 RGB 也分摊到各核，像素不变。6000×4000 的照片缩到 1536×1536 以内，4 线程 0.49s，原为 0.57s。
- 无损 WebP（以及有损 WebP 的压缩 alpha）解码耗时减少 25–65%，像素与出错情形都与 `golang.org/x/image/webp` 相同：哈夫曼码按 libwebp 的方式查表解码（不完整的码仍用树），一次读入 64 位，预测变换按整段像素、一次处理四个通道（1600×1097 的照片单核 31ms，原为 42ms；1001×687 的 15ms，原为 22ms；libwebp 的 `dwebp`：30ms 与 13ms，用其 SIMD 时 25ms 与 11ms）。
- 色度在垂直方向子采样、上边缘位于 0 以上奇数行的 YCbCr 或 NYCbCrA 图像，编码为 JPEG（或其他需要重新分带的处理）时不再报内部错误。
- PNG 7 级起每 32KB 的行比较「不滤波」与各行自选的滤波，保留较小者：截图、图标、绘图类小 10–19%，照片不变。
- PNG 8、9 级改为搜索最小文件：各行按 oxipng 的 Brute 策略滤波（每种滤波都试），数据改用 libdeflate「接近最优解析」的 Go 移植压缩，不再用 `compress/flate`，并像 zopfli 那样按代价切分 deflate 块、按熵给符号计价。17 张截图、照片与游戏美术图上比 7 级小 12.2%（8 级）和 13.5%（9 级），oxipng `-o max` 为 12.8%（耗时是我们的 6 倍），ECT `-9` 为 13.8%；各 Go 版本输出同一文件。每线程多占约 4MB（8 级）和 6.5MB（9 级）内存。
- 最低 Go 版本降为 1.26（0.1.0 要求 1.27）。Go 1.27 重写了 `compress/flate`，因此 Go 1.26 与 Go 1.27 在 1 至 6 级写出的 PNG 文件字节不同，像素相同；默认的 7 级以及 8、9 级两者相同。
- 渐进式 JPEG 按 mozjpeg 的方法（`optimize_scans`）选择扫描：文件小 0.4–14%（18 张图平均 2.4%，与 `Trellis`、`QuantTable: QuantTableImageMagick` 同用时平均 3.9%；四者同用时同等 PSNR 下比 mozjpeg 默认设置小 0.7%），像素不变。单线程多用 3–19% 的时间；候选扫描的统计随 `Threads` 并行。
- `Animated()` 不再忽略无法保留的动画：GIF、WebP 或 PNG 动画输出为 GIF、WebP 以外的格式时，返回包装了 `errors.ErrUnsupported` 的错误，而不是写出第一帧。静态图像不受影响。`MaxFileSize` 与 `Animated()` 同用于动画时同样报错（此前被忽略）。
- `Threads(n)` 的上限由 3 改为 CPU 核数。自 4 线程起，缩放、模糊、锐化、色彩调整、旋转以及 JPEG、PNG、有损 WebP 编码将工作分摊至多个核。输出与线程数无关，逐字节一致。
- PNG 默认压缩级别由 6（标准库的默认值）改为 7（压缩方式见上文）：与 Go 1.27 的 6 级相比，38 张图像中渐变、天空、图标类小到 1/1.3 至 1/4.2，截图小 20–23%，照片小 1.8–7.1%，单核耗时为 1.85 倍。
- PNG 按 256KB 分块压缩（与 pigz 的做法相同），因而可由多核分摊。1 至 6 级下图像数据不超过一块时，文件与此前版本相同。Go 1.27 及以上版本中，超过一块的数据在 6 级及以下改用 `klauspost/compress` 的 flate 压缩：它写出的字节与 Go 1.27 的 `compress/flate` 相同，复位时可换成每块自己的字典，而 `compress/flate` 的压缩器每块都得新建。2400 万像素的 PNG 分配 3MB（原为 276MB），峰值 9.2MB（原为 16.6MB；4 线程 15.0MB，原为 23.0MB），快 6%，字节不变。
- GIF 动画输出：所有帧共用一套按画面内容选取的调色板（`GIFOptions` 的 `Colors`、`NoDither` 对动画生效），自第二帧起仅存储变化的矩形区域。`GIFOptions{FixedPalette: true}` 仍使用固定调色板逐帧写出整帧。在处理任何一帧之前即检查像素上限。`FixedPalette` 下亦可使用 `NoDither`。
- 有损 WebP：按 libwebp 的空间噪声整形做分段量化。
- 有损 WebP：`WebPOptions.Effort` 也作用于有损输出（原先只用于无损）。默认力度 5 按 libwebp 最好设置的做法，用 sharp YUV 换算颜色、对 4×4 块做网格量化、按测量选取环路滤波强度；所有力度都做色度直流误差扩散；量化与率失真权重按 SSIMULACRA2 与 butteraugli 调定。14 张图上，同等 SSIMULACRA2 / butteraugli 下比 libwebp 默认设置小 6.6% / 9.5%，比其最好设置（`-m 6 -af -sharp_yuv`）小 1.3% / 2.2%。编码耗时为原来的 2–2.6 倍；`Effort: 1` 保持原来的速度，仍比 libwebp 默认设置小 6.1% / 4.8%。
- 带透明的有损 WebP：透明通道改由无损编码器以其默认力度编码，取「不滤波」与水平滤波中较小者（力度 7 起四种滤波都试），4 百万像素以内的平面用最优解析（力度 7 起不限大小），并按 libwebp 的做法让透明区域的颜色尽量省字节：一张置于柔和圆形窗口中的照片小 50%（质量 75 下 146KB → 73KB，libwebp 为 88KB），透明通道块最多小到 1/6（Tux 比 libwebp 小 2%）。编码透明通道时最多多用 8MB 内存；内存上限很低时透明通道块（而非像素）可能不同，与无损文件相同。
- 有损 WebP 解码结果与 libwebp（因而与浏览器）逐字节相同：色度在样本之间插值，并采用 libwebp 的整数换算（原先每个色度样本覆盖整个 2×2 块，颜色平均相差约半个色阶，色彩边缘处最大 20–40）。有损 WebP 解码后为 RGB；`Metadata()` 仍报告为 `ycbcr`。
- 无损 WebP 按穿过像素的最便宜路径选取 LZ77 匹配（libwebp TraceBackwards 的思路），截图上搜索重复更远，哈夫曼分组试多种块大小与组数：默认力度下 17 张图小 5.5%（截图 6–9%），耗时为原来的 3.8 倍（单核 42s 对 11s；libwebp 默认设置为 24s）；总量比 libwebp 最慢的设置小 5.3%（力度 9 时 5.7%，耗时 106s，对方 308s）。每个像素处除哈希链的匹配外，还把上方与左侧最近各距离上最长的匹配交给路径选择（一张渐变蒙版小 16%）；前缀码在划算时按 libwebp 的做法平滑计数，让码长适于游程编码。4 百万像素以内的图另以带 libwebp 交叉颜色变换的变体参与竞赛（带颜色渐变的图标与界面图小 2–9%），哈夫曼分组也在不用颜色缓存的情况下再试一次（一个图标小 4%），不带预测的变体还另试只用距离 1 和上一行的匹配（libwebp 的「RLE」引用；颜色少的图标小 3–4%）。由 JPEG 解码而来的 6000×4000 照片满是噪点，最优解析找不到多少可省的：文件大小不变，耗时由 4.1s 变为 5.1s，峰值由 22.2MB 变为最多 25.6MB。
- 无损 WebP：每个行带的预测器依据该行带开始时的代价模型选择，以便并行选择；输出文件略有差异。
- 命令行代码由 `cmd/goimgtools` 移至 `cli` 包；`cmd/goimgtools` 构建的仍为同一程序。

### 修复

- 力度 5 及以上（默认）的有损 WebP：帧的环路滤波强度写成第一段的强度，而测量常把它定为 0（纹理最多的宏块所在的段）；解码器（libwebp、libvpx 以及本模块的）此时整帧都不滤波，不管其他段的强度是多少。现在写成各段强度中的最大值，各段强度照写。小尺寸的平滑照片因此失去了环路滤波：一张这样的照片及其 19 个编辑版本在质量 75 下，SSIMULACRA2 由 69.3 升到 73.6，butteraugli 由 1.28 降到 1.19，文件大小不变。
- 以 NRGBA 保存的带透明图像（带 alpha 的 PNG）做 `Extend` 与 `FitContain` 时经过预乘的 8 位 RGBA，半透明像素的颜色会偏移（最多 255/alpha 级：alpha 为 55 时 4 级）；现在保持不变。
- 对分量采样方式特殊、解码器无法自行缩小的 JPEG（Go 测试文件 121121、211211、221122、222112）使用 `FastShrinkOnLoad` 时报错 “shrink: unsupported pixel type”；现在改为对解码结果再做平均。

- 大图像的压缩 TIFF 只有一个条带，压缩数据超过 50MB 时 libvips（因而 sharp）拒绝读取：现在像素数据超过 1MB 的压缩图像按约 1MB 一条分条带写入。
- 在把乘法与加法融合为一条指令的处理器上（ARM64，如 Apple 芯片与 AWS Graviton；POWER、s390x、RISC-V、LoongArch），部分结果可能与 x86 相差一级或一个像素：`Linear`、混合模式、带小数权重的 `Convolve`、GIF 与少色 PNG 的调色板、Mitchell 核、按比例定位的水印、`Rotate` 的尺寸、`Blur` 的方框、文字描边，以及无损 WebP 与 jpegli 编码器所做的选择。现在每个乘积都先取整再相加（与 x86 相同），自动测试会检查编译器在这些处理器上没有融合任何一处。
- 操作中发生 panic 时，处理链的输入可能未被关闭，继续占用内存预算与临时文件。
- GIF 的 `Frames().LoopCount` 此前为其重复次数，比字段文档所述的播放次数少 1。

## 0.1.0（2026-11-14）

首个公开版本：在内存上限内按行读写 PNG、JPEG、WebP、GIF（含动画）、BMP、TIFF；缩放（6 种插值核、5 种适配模式）、裁剪、90 度倍数旋转、翻转、铺底色、图像叠加与水印、文字；JPEG 优化编码表与渐进式编码、PNG 减色、`MaxFileSize`；批量处理；扩展接口（`Pipe`、`Pixels`、`Apply`、`FromImage`、`FromSource`、`RegisterFormat`、`Stream`、`ToImage`，`pipeline` 包）；子命令 `info`、`check`、`compare`、`formats` 与 `inspect` 包；输入与输出像素上限、取消、panic 转为错误返回。
