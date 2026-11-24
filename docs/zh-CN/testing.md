[English](../testing.md) | 中文

# 测试

```bash
go test ./...
```

完整运行约需 3–5 分钟（各包并行执行）。WebP 编码器与 libwebp 的交叉验证须安装带 Pillow 的 Python，否则相关测试自动跳过。

库本身的行为测试均位于 `tests/`，仅通过公开接口调用本库；各组件（`internal/` 下的各格式、`pipeline`）的测试与其代码放在一起。

**已验证平台。** 下列平台均实际运行测试，而非仅编译：

| 系统 | 处理器 | 运行内容 | 运行环境 |
|---|---|---|---|
| Windows | x86-64 | 全部测试 | GitHub Actions |
| Linux | x86-64 | 全部测试 | GitHub Actions |
| Linux | arm64 | 全部测试 | GitHub Actions |
| macOS | arm64 | 全部测试 | GitHub Actions |
| Linux | 32 位 x86 | 全部测试，包括 6 万像素宽高及各格式最大边长的测试组 | GitHub Actions |
| FreeBSD 15、OpenBSD 8、NetBSD 11、DragonFly BSD 6.4、Solaris 11.4、illumos（OmniOS） | x86-64 | 库的行为测试、`pipeline`、`inspect`、缩放、基本操作、命令行 | GitHub Actions 中各系统的虚拟机 |
| Android 11 | x86-64 | 库的行为测试、`pipeline`、`inspect`、缩放、基本操作、命令行 | GitHub Actions 中的 Android 模拟器（以 NDK 编译，经 adb 推送并运行） |
| iOS | arm64 | 库的行为测试、`pipeline`、`inspect`、缩放、基本操作 | GitHub Actions 中的 iOS 模拟器（以 Go 自带的 `go_ios_exec` 打包为 App 运行） |
| Linux | 32 位 ARM、MIPS（大端序）、s390x（大端序）、riscv64、ppc64le、loong64 | 库的行为测试、`pipeline`、`inspect`、缩放、基本操作 | GitHub Actions，qemu 模拟 |
| WebAssembly（WASI） | wasm | 库的行为测试、`pipeline`、`inspect`、缩放、基本操作 | GitHub Actions，wasmtime |
| WebAssembly（用于浏览器 / node 的 `GOOS=js`） | wasm | 同上；运行整套行为测试时 node 内存峰值 425–455MB | GitHub Actions，Linux 上的 node 18、20、22 |

BSD、Solaris、Android、iOS、模拟处理器与 WebAssembly 仅运行上述各包，不运行各格式解码器、编码器自身的测试（这些测试在模拟环境中过慢）；但行为测试本身会对各种格式逐一读写。同一图像在 Linux 与 Windows 上输出的文件逐字节一致。

自动测试配置见 [.github/workflows/test.yml](../../.github/workflows/test.yml)。每次推送均用最新的 Go 1.27 运行上表中的测试，并用支持的最低版本（`go.mod` 中的 1.26）运行全部测试，另含竞态检测、各模糊测试的短时运行、其余目标平台的编译与静态检查，以及导出接口与上一发布版本的比较，有任何不兼容改动即失败（见 [CONTRIBUTING](../../.github/CONTRIBUTING.md)）。

仅编译与静态检查、**未实际运行**：AIX 与 Plan 9（无可用测试机）。

除各格式与标准库的逐字节比对测试外，另有四类专门覆盖边界情况的测试：

- **极限情况**（[tests/limits_test.go](../../tests/limits_test.go)）：1×1、1×N、N×1 以及 8 / 16 像素块边界附近的尺寸，每种像素类型写入每种格式后再读回；这些小图像经过每一种操作和每一种缩放算法；各格式可容纳的最大边长（JPEG、GIF 65535，有损 WebP 16383，无损 WebP 16384）以及超出一个像素时返回的错误；宽 / 高 6 万像素的图像在默认内存上限内完成处理；一百余组参数极值（质量、颜色数、角度、比例、偏移、字号的上下界以及无穷大、NaN）；每次仅提供一个字节的输入流、中途断开的输入流；覆盖输入文件、输出目录不存在等文件系统情形。
- **扩展接口**（[tests/extend_test.go](../../tests/extend_test.go)、[example_test.go](../../example_test.go)）：仅使用公开包实现的操作、输入与文件格式；示例在包外编译并运行。
- **多线程与跨平台**（[tests/threads_test.go](../../tests/threads_test.go)、[tests/platform_test.go](../../tests/platform_test.go)）：分摊工作的操作在 0、1、3、7 个辅助线程下，无论数据驻留内存还是写入临时文件，结果均逐字节一致，且预算全部归还；新操作及各编码器的输出与在 x86-64 上记录的校验值比对，自动测试在每种处理器上均运行此项。自动测试还会为带乘加融合指令的处理器（ARM64、POWER、s390x、RISC-V、LoongArch）编译本库，只要编译器在任何地方把浮点乘法与加法融合即失败，因为这可能让结果与 x86 不同，而测试图像未必能体现出来。
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

`FuzzDecode` 将任意字节作为图像解码，`FuzzChain` 将任意字节送入任意操作组合并以任意格式写出，`FuzzText` 绘制任意文字，`FuzzExif` 将任意字节作为 EXIF 解析（要求读出的数值均在合理范围内）。判定条件：不发生 panic、不挂起、各条带恰好覆盖全图、内存预算从不超出且全部归还、不遗留临时文件。种子为 Go 与 `x/image` 自带的测试图像，以及本库写出的各格式小图像。
