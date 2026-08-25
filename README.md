# Tetsuhiro WOS — 波动光学模拟器（Wave Optics Simulator）

科学、准确性优先的波动光学 + 量子光学仿真内核（Golang）+ 鼠标/键盘双可用 Web GUI。

- **数值内核**：自研并行 FFT、角谱法（ASM，亥姆霍兹方程精确解，含 **零填充 `asm_pad`** 与 **离轴频移 `asm_shift`** 高精度变体）、菲涅耳（两种形式）、夫琅禾费远场；衰逝波处理、奈奎斯特带限正则化、菲涅耳数/采样有效性自动告警。
- **光学元件**：20+ 种可调参数元件（透镜、光阑、光栅、轴锥镜、波带片、涡旋相位板、楔形棱镜、漫射体、反射镜、凹面镜、凸面镜、泽尼克像差板、偏振片、波片、旋光片、自定义琼斯矩阵、分束器、合束器……），全部参数可调。
- **量子光学**：Fock 基线性光学内核（Fock/相干/压缩/双模压缩/热态、相移/分束器/位移/压缩/损耗门），光子数分布、g²(0)、正交分量、联合分布——Hong-Ou-Mandel、单光子马赫-曾德尔、压缩、EPR、混合态/损耗等效应可复现，并支持 PNG 图表导出（docs/QUANTUM.md）。
- **物理完备性**：琼斯矢量偏振（2 分量）、折返光路（反射镜/迈克尔逊）、分束臂与相干合束（马赫-曾德尔等干涉仪）、功率归一化（SI 单位）、质心/RMS/Strehl 等指标、一维剖面。
- **精度验证**：go test ./optics/ 内含 30 项物理与数值测试——艾里斑峰值与暗环、单缝 sinc²、光栅 Raman-Nath 级数、双缝条纹、高斯束腰演化与 Gouy 相位、琼斯计算、马赫-曾德尔/迈克尔逊干涉能量守恒、波带片效率、散斑对比度、功率守恒、Fresnel/ASM 互证、ASM 高精度变体、HOM/相干/压缩/Fock 量子统计等。
- **GUI**：浏览器页面，**鼠标与键盘双可用**（Tab/方向键/快捷键，见 docs/GUI.md）；支持 n 新建、o 打开、s 保存配置 JSON 文件、m 切换量子光学模式。
- **接入**：内核即库（import "twos/optics"），HTTP API 供任意语言调用。详见 docs/INTEGRATION.md（以内核开发为主）。

## 快速开始

    go build -o wos ./cmd/wos
    ./wos -addr :8080        # 打开 http://localhost:8080（按 ? 查看快捷键）

> 构建缓存异常时：GOCACHE=$PWD/.gocache go build -o wos ./cmd/wos

运行精度测试套件：

    go test ./optics/ -v        # 30 项物理与数值测试
    go run ./examples/demo      # 内核库用法：透镜聚焦，输出 PNG 与指标
    go run ./examples/interferometer
    go run ./examples/quantum   # 量子光学：HOM / 相干 / 压缩 / EPR
    python3 examples/python_client.py http://localhost:8080

## 目录结构

    optics/          内核包（FFT、传播、元件、模拟器、指标、目录、校验、量子光学）
    server/          HTTP API 服务（提交/轮询、二进制与 PNG 输出、运行缓存、/api/quantum）
    cmd/wos/         wos 可执行文件（内嵌 web GUI，单二进制分发）
    examples/        Go 库用法示例、干涉仪示例、量子光学示例、Python 客户端
    docs/            物理模型 / 内核开发 / 量子光学 / 接入说明 / API / GUI 文档

## 文档索引

| 文档 | 内容 |
|---|---|
| docs/PHYSICS.md | 物理模型：约定、标量衍射、角谱法推导（含 asm_pad/asm_shift）、菲涅耳/夫琅禾费有效性条件、薄元件、琼斯计算、折返光路相位、指标定义 |
| docs/KERNEL.md | **内核开发**：架构、核心类型与 API、如何新增元件/传播算法/光源、量子内核、线程与内存、测试方法 |
| docs/QUANTUM.md | **量子光学**：Fock 基模型、态/门/测量、HOM/压缩/EPR、Go 与 HTTP API |
| docs/INTEGRATION.md | **接入说明**：作为 Go 库嵌入、HTTP 接入（curl/JS/Python）、量子接口、二进制格式、性能与精度调参 |
| docs/API.md | HTTP API 参考（端点、参数、错误） |
| docs/GUI.md | GUI 键盘/鼠标操作完整指南 |

## 性能参考（8 核，标量模式）

| 网格 | 单次 ASM 传播 | 1024² 典型光路（含 5 个平面） |
|---|---|---|
| 512² | ~40 ms | ~0.3 s |
| 1024² | ~150 ms | ~1.5 s |
| 2048² | ~0.7 s | ~7 s（约 128 MB/平面） |

琼斯偏振开启时成本 ×2（两个分量）。
