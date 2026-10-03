# Tetsuhiro WOS — 波动光学模拟器（Wave Optics Simulator）

科学、准确性优先的波动光学 + 量子光学仿真内核（Golang，零第三方依赖，仅标准库）+ 鼠标/键盘双可用 Web GUI。

- **数值内核**：自研并行 FFT、角谱法（ASM，亥姆霍兹方程精确解，含 **零填充 `asm_pad`**、**离轴频移 `asm_shift`** 与 **频移+零填充 `asm_shift_pad`** 高精度变体，支持非 2 的幂网格（Bluestein FFT））、菲涅耳（两种形式）、夫琅禾费远场；衰逝波处理、奈奎斯特带限正则化、菲涅耳数/采样有效性自动告警。
- **高级传播算法**：**全矢量角谱法**（`method=vectorial`，重构纵向分量 Ez，非傍轴）、**非均匀/复折射率介质**（split-step BPM，分层/梯度/吸收/增益介质）、**宽带谱叠加**（polychromatic，逐波长非相干叠加）、**部分相干光**（Gaussian Schell 模型）、**各向异性/双折射介质**（单轴晶体 `uniaxial`、Berreman 4×4 双轴晶体 `biaxial`）、**3-D 体传播**（一次传播到多个 z，输出 x,y,z 体积场）。
- **光学元件**：20+ 种可调参数元件（透镜、光阑、光栅、轴锥镜、波带片、涡旋相位板、楔形棱镜、漫射体、反射镜、凹面镜、凸面镜、泽尼克像差板、偏振片、波片、旋光片、自定义琼斯矩阵、分束器、合束器、单轴晶体、均匀介质、双轴晶体……），全部参数可调。
- **脚本元件（元件定义文件）**：把新元件的复透过率直接写成 `elements/<名字>.json` 里的**表达式**（`phase`/`amp`/可选琼斯矩阵 + 参数表），加载后与内置元件同等可用——出现在 GUI 插入对话框与参数面板、可写进场景 JSON、可走 Go API，**不需要重编译或重启**；服务端每 2 秒比对定义集合（新增、改写、删除都会跟上）自动热重载，GUI 有「↻ 定义」按钮与「掩膜预览」（|t| 与包裹相位）。定型后可 `wos -gen-go elements/x.json -gen-name NAME` 打印等价的原生 Go 元件入库。示例：`elements/metalens.json`（超表面透镜）、`elements/sine_amp_grating.json`（正弦振幅光栅）。
- **量子光学**：Fock 基线性光学内核（Fock/相干/压缩/双模压缩/热态、相移/分束器/位移/压缩/损耗门），光子数分布、g²(0)、正交分量、联合分布——Hong-Ou-Mandel、单光子马赫-曾德尔、压缩、EPR、混合态/损耗等效应可复现，并支持 PNG/SVG 图表导出（docs/QUANTUM.md）。
- **定位场景光路（1.0 光路模型）**：光路存储**元件的位置与几何形状**（`scene.components[]`：pos/yaw/pitch/roll/shape/params），而非“依次经过的元件序列”；光路由最近命中与反射/透射几何**推导**，元件顺序由位置决定。元件带真实轮廓（圆/方/矩形/椭圆/三角/环/多边形/双缝/十字/星形/超椭圆/自定义顶点/细缝），轮廓即通光孔径。旧的元件序列配置可一键自动转换为定位场景（`POST /api/convert`，GUI 载入旧预设时自动完成）。
- **多光源与相干分组**：`sources[]` 任意多个光源，各有位置/方向/波长/功率/偏振与相干组；同组同波长相干叠加（产生干涉条纹），不同组/不同波长按强度叠加；每个平面保留各光源单元的复场，可按通道单独查看。
- **真实光色与明暗**：波长→线性 sRGB（CIE 1931 拟合），图像视图按各光源波长加权着色、亮度为真实强度（可调曝光），曲线（剖面）同时保留。
- **偏振与相位检查**：斯托克斯参数、偏振椭圆（ψ/χ/轴比/手性）、偏振度、二维相位去包裹与波前 PV/RMS；偏振视图叠加椭圆阵列，**点击**图像上任意一点即在下方展开该点的琼斯矢量、斯托克斯参数与相位；无光区域自动掩膜。
- **立体视图**：整套光路的三维渲染（元件板、光束段按光源颜色与相对功率、遮挡显示、平台网格），可旋转/缩放/平移复位。
- **物理完备性**：琼斯矢量偏振（2 分量）、全矢量 Ez（3 分量）、折返光路（反射镜/迈克尔逊）、分束臂与相干合束（马赫-曾德尔等干涉仪）、功率归一化（SI 单位）、质心/RMS/Strehl 等指标、一维剖面。
- **精度验证**：`go test ./optics/` 内含 114 项物理与数值测试（含 16 个内置模板的端到端测试）——艾里斑峰值与暗环、单缝 sinc²、光栅 Raman-Nath 级数、双缝条纹、高斯束腰演化与 Gouy 相位、琼斯计算、马赫-曾德尔/迈克尔逊干涉能量守恒、波带片效率、散斑对比度、功率守恒、Fresnel/ASM 互证、ASM 高精度变体、复场级解析解对比（倾斜平面波/Fresnel-Gaussian/夫琅禾费远场/矢量 Ez）、双折射/Berreman、部分相干、宽带谱、HOM/相干/压缩/Fock 量子统计等。
- **GUI**：浏览器页面，**鼠标与键盘双可用**（Tab/方向键/快捷键，见 docs/GUI.md）；视图标签条 1 图像 / 2 相位 / 3 偏振 / 4-6 各分量强度 / 7-8 分量相位 / **0 立体视图**，浏览器标签页带自制图标（淡蓝底 + 白色光栅衍射强度曲线），内置 16 个模板，支持 n 新建、o 打开、s 保存配置 JSON 文件、m 切换量子光学模式、h 隐藏中心图样、自定义网格大小、毛玻璃视觉主题。
- **接入**：内核即库（import "twos/optics"），HTTP API 供任意语言调用。详见 docs/INTEGRATION.md（以内核开发为主）。

## 下载与安装

本项目分两个发布通道：

- **Release**（`v1.3.0`）：源码 + 预编译二进制包（Linux/Windows 单文件与 zip），见 [Releases](https://github.com/ZhehaoTetsuhiro/Tetsuhiro-WOS/releases)。
- **Package**（GitHub Packages 容器镜像）：`ghcr.io/zhehaotetsuhiro/tetsuhiro-wos:v1.3.0`，`docker/podman run` 直接运行。

预编译二进制：

| 文件 | 平台 |
|---|---|
| `wos-linux-amd64` / `wos-linux-amd64.zip` | Linux x86-64（zip 内含二进制 + 说明） |
| `wos-windows-amd64.exe` / `wos-windows-amd64.zip` | Windows x86-64（zip 内含 exe + 说明） |

容器镜像：

    docker pull ghcr.io/zhehaotetsuhiro/tetsuhiro-wos:v1.3.0
    docker run -p 1120:1120 ghcr.io/zhehaotetsuhiro/tetsuhiro-wos:v1.3.0

## 快速开始

    go build -o wos ./cmd/wos
    ./wos -addr :1120        # 打开 http://localhost:1120（按 ? 查看快捷键）

> 构建缓存异常时：GOCACHE=$PWD/.gocache go build -o wos ./cmd/wos
> Windows 交叉编译：GOOS=windows GOARCH=amd64 go build -o wos.exe ./cmd/wos

元件定义文件（脚本元件）放在 `elements/`（可执行文件旁、当前目录或 `~/.wos/elements/`，
也可用 `-elements 目录` 追加）：写好后在 GUI 里点「↻ 定义」或等几秒自动重载（**删除定义文件也会自动跟上**）；
`./wos -check-elements` 只校验定义文件（错误以退出码 1 结束），
`./wos -gen-go elements/metalens.json -gen-name my_lens` 把定义打印成等价的原生 Go 元件。

运行精度测试套件：

    go test ./optics/ -v        # 143 项物理与数值测试
    go run ./examples/demo      # 内核库用法：透镜聚焦，输出 PNG 与指标
    go run ./examples/interferometer
    go run ./examples/quantum   # 量子光学：HOM / 相干 / 压缩 / EPR
    python3 examples/python_client.py http://localhost:1120

## 目录结构

    optics/          内核包（FFT、传播、介质、元件、模拟器、指标、目录、校验、量子光学、脚本元件与代码生成）
    server/          HTTP API 服务（提交/轮询、二进制与 PNG 输出、运行缓存、/api/quantum、/api/elements）
    cmd/wos/         wos 可执行文件（内嵌 web GUI，单二进制分发）
    elements/        元件定义文件（脚本元件，热重载；写新元件见 docs/KERNEL.md §4.1）
    examples/        Go 库用法示例、干涉仪示例、量子光学示例、Python 客户端
    docs/            物理模型 / 内核开发 / 量子光学 / 接入说明 / API / GUI / GPU 文档

## 文档索引

| 文档 | 内容 |
|---|---|
| docs/PHYSICS.md | 物理模型：约定、标量衍射、角谱法推导（含 asm_pad/asm_shift）、菲涅耳/夫琅禾费有效性条件、薄元件、琼斯计算、折返光路相位、指标定义 |
| docs/KERNEL.md | **内核开发**：架构、核心类型与 API、如何新增元件/传播算法/光源、量子内核、线程与内存、测试方法 |
| docs/QUANTUM.md | **量子光学**：Fock 基模型、态/门/测量、HOM/压缩/EPR、Go 与 HTTP API |
| docs/INTEGRATION.md | **接入说明**：作为 Go 库嵌入、HTTP 接入（curl/JS/Python）、量子接口、二进制格式、性能与精度调参 |
| docs/API.md | HTTP API 参考（端点、参数、错误） |
| docs/GUI.md | GUI 键盘/鼠标操作完整指南 |
| docs/GPU.md | **GPU 加速**：cuFFT 后端（`-tags cuda`）、启用方式、实测对比与限制 |

## 性能参考（8 核，标量模式）

| 网格 | 单次 ASM 传播 | 1024² 典型光路（含 5 个平面） |
|---|---|---|
| 512² | ~40 ms | ~0.3 s |
| 1024² | ~150 ms | ~1.5 s |
| 2048² | ~0.7 s | ~7 s（约 128 MB/平面） |

琼斯偏振开启时成本 ×2（两个分量）；全矢量模式（Ez）成本 ×3。

## GPU 加速（可选，需 `-tags cuda`）

内核自带一个可选的 CUDA / cuFFT 后端，把 `fft2D` / `fft1DAny` 分派给 GPU，因此**所有走 FFT 的路径**
（角谱 asm/asm_pad/asm_shift/asm_shift_pad、Fresnel、Fraunhofer、薄元件、Berreman、相干度、场指标等）
都自动受益。默认不启用，纯 Go 单二进制分发不受影响；无 CUDA 设备或未加标签时安全回退 CPU。

    # 构建（需 CUDA toolkit：nvcc + libcufft）
    go build -tags cuda -o wos ./cmd/wos

    # 启用：命令行开关，或环境变量（二选一）
    ./wos -gpu -addr :1120
    WOS_GPU=1 ./wos -addr :1120

启用成功时启动日志打印设备名；没有设备或未以 `-tags cuda` 构建时，`-gpu` 只会在日志提示后继续用 CPU。

实测（Tesla T4，单次复数 double 2-D FFT，含显存往返；`go test -tags cuda -bench FFT2D ./optics/`）：

| 网格 | CPU（8 核纯 Go） | GPU（cuFFT，含往返） | 加速比 |
|---|---|---|---|
| 1024² | ~14 ms | ~12 ms | ~1.1× |
| 2048² | ~57 ms | ~45 ms | ~1.3× |
| 4096² | ~331 ms | ~179 ms | ~1.85× |

限制与取舍：吞吐受 PCIe 往返与 T4 较弱的 FP64 算力限制，小网格反而得不偿失，故设了阈值
（2-D 边 ≥64、1-D 长度 ≥1024 才走 GPU）；GPU 调用串行化以保证 cuFFT 句柄与显存安全。
GPU 与 CPU 的一致性由 `go test -tags cuda ./optics/` 的 GPU 用例验证（2-D/1-D 往返以及 asm/asm_pad
整步传播，相对误差 < 1e-9）。详见 docs/GPU.md。
