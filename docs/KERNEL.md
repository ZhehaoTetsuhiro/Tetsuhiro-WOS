# 内核开发指南（docs/KERNEL.md）

本文面向**内核开发者**：讲解 optics 包的结构、核心 API、并发/内存模型，以及如何扩展元件、传播算法与光源。接入（使用）层面的说明见 docs/INTEGRATION.md。

## 1. 包结构与分层

    optics/fft.go          并行 radix-2 FFT（plan 缓存 + sync.Pool 列缓冲）
    optics/field.go        场类型 Field、Context、Warnings、带限滤波
    optics/source.go       光源构造（含 LG/HG 多项式递推）
    optics/propagate.go    传播算法 ASM / ASMPad / ASMShift / Fresnel / Fraunhofer / Auto
    optics/elements.go     元件库与注册表（薄元件 + 琼斯元件）
    optics/simulator.go    光路引擎：线性序列、反射臂、合束器、输出平面
    optics/metrics.go      指标（功率/峰值/质心/RMS/Strehl）与一维剖面
    optics/validate.go     配置校验（含分束臂静态遍历）
    optics/catalog.go      元件/光源/方法/示例/量子目录（GUI 由它驱动）
    optics/quantum.go      量子光学内核：QState、门、测量、SimulateQuantum
    optics/quantum_density.go 密度矩阵后端：混合态（热态）、损耗信道、酉共轭门
    optics/quantum_matrix.go 稠密小矩阵助手 + 分块矩阵指数（分束器精确构造）
    server/server.go       HTTP API（异步提交、轮询、LRU 运行缓存、/api/quantum）
    server/render.go       PNG 渲染与色图
    cmd/wos/main.go        可执行文件（go:embed 内嵌 web GUI）

依赖为零（仅标准库）：FFT 自研、多项式递推自研、量子矩阵指数自研、PNG 用标准库 image/png。

## 2. 核心类型

    type Field struct { N int; DX float64; Polarized bool; Ex, Ey []complex128 }
    type Context struct { Wavelength float64; Evanescent string; Bandlimit *BandlimitOpts; RNG *rand.Rand; Warnings *Warnings }
    type Config struct { Grid GridSpec; Wavelength float64; Polarized *bool; Method, Evanescent string; Bandlimit *BandlimitOpts; Source SourceSpec; Elements []ElementSpec }
    type Result struct { RunID string; Size, Width, DX, Wavelength, ElapsedMS float64...; Warnings []Warning; Planes []*Plane }
    type Plane  struct { ID, Label, Path string; Size int; DX float64; Ex, Ey []complex128; Stats PlaneStats }

三个抽象层次：

1. **低层**（数值原语）：fft2D / Propagate / Field 方法——可直接组合。
2. **元件层**：Element 接口 + 注册表，NewElement(spec) 构建。
3. **光路层**：Simulate(cfg) 处理 propagate/sensor/beamsplitter/combiner/mirror 的结构语义。

## 3. 数值原语用法

    f := optics.NewField(1024, 1e-5, false)          // 1024², dx=10µm, 标量
    // ... 填充 f.Ex ...
    ctx := &optics.Context{Wavelength: 632.8e-9, Evanescent: "decay", Warnings: &optics.Warnings{}}
    optics.Propagate(f, 0.1, optics.MethodASM, ctx)  // 传播 0.1 m

- Propagate 支持负 z（逆传播）；衰逝波在负 z 时自动置零并告警 backward_evanescent。
- 高精度变体：MethodASMPad（2N 零填充线性卷积）与 MethodASMShift（离轴载频搬移），见 PHYSICS.md §3.1。
- ApplyBandlimit 可对任意场做奈奎斯特带限（参数含义见 PHYSICS.md §3）。Propagate 本身不再自动带限——由模拟器 trainer 在每平面后施加一次，避免对 propagate 元件重复滤波；低层直接调用 Propagate 时如需带限请显式调用 ApplyBandlimit。
- Field.ApplyTilt 用精确方向余弦（非傍轴）；Field.ApplyJones 处理标量→矢量升维。
- 功率归一化：Field.NormalizePower(p)；功率读取 Field.Power()（W）。

## 4. 新增一种光学元件（完整配方）

以“柱面透镜”为例（只在一个方向聚焦）：

**第 1 步**：在 optics/elements.go 定义实现（实现 Element 接口的 Apply）：

    type cylindricalLensEl struct{ f, rotation float64 }

    func newCylindricalLens(p map[string]any) (Element, error) {
        f, err := pf(p, "f", 0.1)          // 参数助手：pf/pfd/pi_/ps
        if err != nil || f == 0 {
            return nil, fmt.Errorf("cylindrical_lens: f must be non-zero")
        }
        return &cylindricalLensEl{f: f, rotation: pfd(p, "rotation", 0)}, nil
    }

    func (e *cylindricalLensEl) Apply(f *Field, ctx *Context) error {
        k := 2 * math.Pi / ctx.Wavelength
        c, s := math.Cos(e.rotation), math.Sin(e.rotation)
        n := f.N
        for j := 0; j < n; j++ {
            y := f.Y(j)
            for i := 0; i < n; i++ {
                x := f.X(i)
                u := c*x + s*y
                t := cexpI(-k*u*u/(2*e.f))
                idx := j*n + i
                f.Ex[idx] *= t
                if f.Polarized {
                    f.Ey[idx] *= t
                }
            }
        }
        return nil
    }

**第 2 步**：在 init() 中注册：

    RegisterElement("cylindrical_lens", newCylindricalLens)

**第 3 步**（可选，GUI 自动出参数面板）：在 optics/catalog.go 的 ElementDocs 追加：

    {Type: "cylindrical_lens", Label: "柱面透镜", Help: "仅沿一个方向聚焦 exp(-ik u²/2f)",
     Params: []ParamSpec{
         fp("f", "焦距", "m", -100, 100, 1e-3, 0.1, "沿 u 方向"),
         fp("rotation", "旋转角", "rad", -3.1416, 3.1416, 0.01, 0, ""),
     }},

完成——校验、模拟、HTTP、GUI 全部自动支持，无需其他改动。

**约定**：
- 纯标量透过率只乘 Ex/Ey；需要偏振耦合时用 f.ApplyJones(a,b,c,d)（自动处理升维）。
- 随机元件用确定性种子（如 diffuser），保证可复现。
- 错误返回带元件名的清晰消息；越界参数在构造器里报错。
- 结构性元件（改变光路拓扑，如分束）需同时改 simulator.go 的 runTrain 与 validate.go——普通薄元件不需要。

## 5. 新增一种传播算法

1. 在 propagate.go 定义 Method 常量与 ParseMethod 分支。
2. 实现 propXxx(f *Field, z float64, ctx *Context)：直接操作 f.Ex/f.Ey；ctx.transfer 可用于“FFT→乘 H→IFFT”的通用骨架。
3. 有效范围之外调用 ctx.Warnings.Add(code, msg, value) 告警（code 语义见 PHYSICS.md §4）。
4. 在 Propagate 的 switch 中接入；catalog.go 的 MethodDocs 加一条（GUI 自动出现）。
5. 若输出网格像素尺寸改变（如 Fraunhofer），必须同步更新 f.DX 并把布局按 N/2 平移对齐（见 propFraunhofer 的注释），否则后续元件坐标错位。

## 6. 新增一种光源

在 source.go 的 BuildSource switch 中加分支：填充 f.Ex（矢量源再填 f.Ey 或用偏振矢量），最后统一 ApplyTilt + NormalizePower。**必须调用 NormalizePower**，强度绝对标度依赖它。多项式模式（LG/HG）用 laguerre/hermite 递推助手。catalog.go 的 SourceDocs 加文档条目。

## 7. 光路引擎语义（结构元件）

- propagate：距离 = 光束方向路程（恒正），相位 +ik·s 累加；可选单步算法覆盖。
- mirror：应用相位/反射率后光束折返（不翻转传播距离符号，见 PHYSICS.md §6）。
- sensor：记录输出平面（克隆场 + 指标）。参数 strehl_aperture/strehl_distance 启用 Strehl。
- beamsplitter：先克隆反射臂场（i√R·e^{iφ}），再缩放透射主场（√(1−R)）；反射臂作为子光路**深度优先**执行（trainer.runTrain 递归，≤8 层），臂末场登记于 t.arms[armID]。
- combiner：终结元件；按权重 Σ w_ji·arm_i 相干叠加（"main" 指当前光路自身场），各臂 DX 必须一致。

限制常量（validate.go）：MaxGridSize=2048、MaxElements=256、MaxPlanes=64、MaxArmDepth=8。

## 8. 并发与内存

- FFT 行/列按 GOMAXPROCS 并行（parFor）；列变换每 worker 独立 scratch（sync.Pool，避免数据竞争）。
- fftPlan 按尺寸缓存（sync.Map，只读共享，安全）。
- 1024² 矢量场约 32 MB/平面；server 以字节预算做 LRU 驱逐（-max-run-mb，默认 512 MB），并串行化并发模拟（semaphore）。
- 内核本身并发安全：Simulate 每次运行独立分配，可多 goroutine 并行调用（注意内存）。

## 9. 测试方法（物理回归）

    go test ./optics/ -v          # 全部
    go test ./optics/ -run Airy   # 单项

测试即精度文档（accuracy_test.go）：每条测试都是“可解析物理量 vs 解析公式”的对比（艾里斑峰值/暗环、sinc²、Raman-Nath 级数比、干涉端口功率、Gouy 相位……）。**新增元件后请按同样风格补一条解析对比测试**：整数像素的孔径（Dirichlet=精确 sinc）、明确的有效性窗口、宽裕但物理的容差。

注意事项（踩过的坑，写测试时务必规避）：
- Fraunhofer 输出剖面以 N/2 为中心（ci = round(centroid/dx + N/2)）。
- 硬边 + 混叠噪声会污染一阶暗环——用带限或首个低于 2% 峰值的暗环定位法。
- 往返/干涉的相位是 k·(总路程)（含 Gouy 按总距离计算），不是各段相位相加。
- 倾角/相位梯度不得超出 π/像素（奈奎斯特）。
- 离轴频移法（asm_shift）的关键是搬移载频后必须用**平移后的传递函数 H(f+fc)**，否则传播结果与普通 ASM 无异（shift 只是纯相位，必须连同传递函数一起搬）。

## 10. 量子光学内核

量子内核与波动内核解耦，纯态态矢量 + 混合态密度矩阵（Fock 基）：

    type QState struct { Modes int; Cutoff int; Amps []complex128 }          // 纯态
    type DensityMatrix struct { Modes int; Cutoff int; Rho []complex128 }    // 混合态
    FockState / CoherentState / SqueezedVacuumState / TwoModeSqueezedVacuum / ThermalState
    PhaseShift / BeamSplitter / Displace / Squeeze / Loss
    MeanPhotonNumber / PhotonNumberDistribution / G2 / JointProb /
    QuadratureStats / Fidelity / Norm / Normalize
    SimulateQuantum(cfg QuantumConfig) (*QuantumResult, error)                // JSON 入口

实现要点：
- 下标 little-endian 混合进制：`idx = n0 + base·n1 + base²·n2 + …`。
- 单模门：构建 (cutoff+1)² 的局部幺正矩阵，按「旁观模式」分块散射到态矢量；双模门同理（(cutoff+1)² 的局部矩阵）。
- 分束器矩阵按总光子数分块、逐块对易哈密顿 exp(iθ(a0†a1+a0a1†)) 求矩阵指数（见 quantum_matrix.go 的 beamSplitterMatrix），精确且与经典对称分束器约定一致。
- 位移/压缩门对反厄米生成元 expm（缩放平方法 + 泰勒级数）。
- 混合态（热态/损耗）由密度矩阵后端处理：门做酉共轭 ρ→UρU†（分块局部酉），损耗信道做 Kraus 分解 Σ E_l ρ E_l†。SimulateQuantum 自动选择后端（状态含 thermal 或门含 loss 时走密度矩阵）。
- 限制：MaxQuantumModes=4、MaxQuantumCutoff=20（SimulateQuantum 校验）。密度矩阵内存为 Dim²，模式多、截断大时成本更高。

物理测试（quantum_test.go）：HOM 聚束、相干态泊松统计、Fock g²、压缩真空正交分量（Heisenberg 极限 1/16）、双模压缩光子数关联、热态统计、损耗信道（迹守恒/二项分布）、单光子马赫-曾德尔。全部解析对比。
