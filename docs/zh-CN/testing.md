[English](../testing.md) | 中文

# 测试

```bash
go test ./...
```

全量约 3–5 分钟（各包并行跑）。WebP 编码器与 libwebp 的交叉验证需要装有 Pillow 的 Python，没有时自动跳过。

库本身的行为测试都在 `tests/` 里，只通过公开接口使用这个库；各个零件（`internal/` 下的各格式、`pipeline`）的测试挨着它们的代码。

**在哪些系统上验证过。** 下面这些都是实跑测试，不只是编译：

| 系统 | 处理器 | 跑了什么 | 在哪里跑 |
|---|---|---|---|
| Windows | x86-64 | 全部测试 | GitHub 自动测试 |
| Linux | x86-64 | 全部测试 | GitHub 自动测试 |
| Linux | arm64 | 全部测试 | GitHub 自动测试 |
| macOS | arm64 | 全部测试 | GitHub 自动测试 |
| Linux | 32 位 x86 | 全部测试，包括 6 万像素宽高和各格式最大边长的那几组 | GitHub 自动测试 |
| FreeBSD 15、OpenBSD 8、NetBSD 11、DragonFly BSD 6.4、Solaris 11.4、illumos（OmniOS） | x86-64 | 库的行为测试，`pipeline`、`inspect`、缩放、基本操作、命令行 | GitHub 自动测试里各系统的虚拟机 |
| Android 11 | x86-64 | 库的行为测试，`pipeline`、`inspect`、缩放、基本操作、命令行 | GitHub 自动测试里的 Android 模拟器（用 NDK 编译，adb 推送运行） |
| iOS | arm64 | 库的行为测试，`pipeline`、`inspect`、缩放、基本操作 | GitHub 自动测试里的 iOS 模拟器（用 Go 自带的 `go_ios_exec` 打包成 App 运行） |
| Linux | 32 位 ARM、MIPS（大端序）、s390x（大端序）、riscv64、ppc64le、loong64 | 库的行为测试，`pipeline`、`inspect`、缩放、基本操作 | GitHub 自动测试，qemu 模拟 |
| WebAssembly（WASI） | wasm | 库的行为测试，`pipeline`、`inspect`、缩放、基本操作 | GitHub 自动测试，wasmtime |
| WebAssembly（浏览器 / node 用的 `GOOS=js`） | wasm | 同上；整套行为测试时 node 内存峰值 425～455MB | GitHub 自动测试，Linux 上的 node 18、20、22 |

BSD、Solaris、Android、iOS、模拟处理器和 WebAssembly 只跑上面这几个包，没有跑各格式解码器、编码器自己的测试（那些在模拟器里太慢）；不过行为测试本身会把六种格式都读写一遍。同一张图在 Linux 和 Windows 上输出的文件逐字节相同。

自动测试的配置在 [.github/workflows/test.yml](../../.github/workflows/test.yml)，每次推送都会跑上表的测试，外加竞态检测、四个模糊测试的短跑，以及其余目标的编译和静态检查。

只做了编译和静态检查、**没有实跑**的：AIX 和 Plan 9（没有可用的测试机）。

除了各格式与标准库逐字节对比的测试，还有四类专门守边界的：

- **极限情况**（[tests/limits_test.go](../../tests/limits_test.go)）：1×1、1×N、N×1 以及 8 / 16 像素块边界附近的尺寸，每种像素类型写成每种格式再读回；这些小图走每一个操作和每一种缩放算法；每种格式能容纳的最大边长（JPEG、GIF 65535，有损 WebP 16383，无损 WebP 16384）以及多一个像素时的报错；6 万像素宽 / 高的图在默认内存上限内跑完；一百多组参数极值（质量、颜色数、角度、比例、偏移、字号的上下界和无穷大、NaN）；一次只给一个字节的输入流、中途断开的输入流；覆盖输入文件、输出目录不存在等文件系统情况。
- **扩展接口**（[tests/extend_test.go](../../tests/extend_test.go)、[example_test.go](../../example_test.go)）：只用公开包写出的操作、输入和文件格式；示例在包外编译运行。
- **多线程与跨平台**（[tests/threads_test.go](../../tests/threads_test.go)、[tests/platform_test.go](../../tests/platform_test.go)）：分摊工作的操作在 0、1、3、7 个帮手、内存放得下与写临时文件时，结果逐字节相同、预算全部归还；新操作和各编码器的输出与 x86-64 上取的校验值比较，自动测试在每种处理器上都跑这一项。
- **模糊测试**（[tests/fuzz_test.go](../../tests/fuzz_test.go)）：

```bash
go test -run '^$' -fuzz FuzzDecode -fuzztime 5m ./tests
```

```bash
go test -run '^$' -fuzz FuzzChain -fuzztime 5m ./tests
```

```bash
go test -run '^$' -fuzz FuzzText -fuzztime 5m ./tests
```

```bash
go test -run '^$' -fuzz FuzzExif -fuzztime 5m ./tests
```

`FuzzDecode` 把任意字节当图片解码，`FuzzChain` 让任意字节走任意操作组合再写成任意格式，`FuzzText` 画任意文字，`FuzzExif` 把任意字节当 EXIF 解析（要求读出的数值都在合理范围内）。要求：不 panic、不卡住、各条带正好覆盖整张图、内存预算不超且全部归还、不留临时文件。种子是 Go 和 `x/image` 自带的测试图加上本库写出的各种格式的小图。
