# 内核开发指南（docs/KERNEL.md）

本文面向**内核开发者**：讲解 optics 包的结构、核心 API、并发/内存模型，以及如何扩展元件、传播算法与光源。接入（使用）层面的说明见 docs/INTEGRATION.md。

## 1. 包结构与分层

    optics/fft.go          并行 radix-2 FFT（plan 缓存 + sync.Pool 列缓冲）
    optics/field.go        场类型 Field、Context、Warnings、带限滤波
    optics/source.go       光源构造（含 LG/HG 多项式递推）
    optics/propagate.go    传播算法 ASM / ASMPad / ASMShift / Fresnel / Fraunhofer / Auto
    optics/elements.go     元件库与注册表（薄元件 + 琼斯元件）
    optics/expr.go         脚本元件的表达式语言（解析 + 闭包编译，含错误定位）
    optics/customelem.go   元件定义文件：加载/校验/注册/预览采样（见 §4.1）
    optics/exprgen.go      定义文件 → Go 原生元件源码（wos -gen-go）
    optics/exprgen_rt.go   生成元件用的运行时助手（genParamNum/genB2F/…）
    optics/elemgen_metalens.go 由 -gen-go 生成并入库的示例原生元件（metalens_native）
    optics/simulator.go    光路引擎：线性序列、反射臂、合束器、输出平面
    optics/metrics.go      指标（功率/峰值/质心/RMS/Strehl）与一维剖面
    optics/validate.go     配置校验（含分束臂静态遍历）
    optics/catalog.go      元件/光源/方法/示例/量子目录（GUI 由它驱动）
    optics/quantum.go      量子光学内核：QState、门、测量、SimulateQuantum
    optics/quantum_density.go 密度矩阵后端：混合态（热态）、损耗信道、酉共轭门
    optics/quantum_matrix.go 稠密小矩阵助手 + 分块矩阵指数（分束器精确构造）
    server/server.go       HTTP API（异步提交、轮询、LRU 运行缓存、/api/quantum）
    server/elements.go     /api/elements：脚本元件的列表/重载/掩膜预览
    server/render.go       PNG 渲染与色图
    cmd/wos/main.go        可执行文件（go:embed 内嵌 web GUI）

依赖为零（仅标准库）：FFT 自研、多项式递推自研、量子矩阵指数自研、PNG 用标准库 image/png。

## 2. 核心类型

    type Field struct { N int; DX float64; Polarized bool; Vectorial bool; Ex, Ey, Ez []complex128 }
    type Context struct { Wavelength float64; Evanescent string; EvanescentLimit float64; BackwardRegularize bool; TikhonovAlpha float64; Bandlimit *BandlimitOpts; RNG *rand.Rand; Warnings *Warnings }
    type Config struct { Grid GridSpec; Wavelength float64; Polarized *bool; Method, Evanescent string; EvanescentLimit float64; BackwardRegularize bool; TikhonovAlpha float64; Bandlimit *BandlimitOpts; Source SourceSpec; Elements []ElementSpec }
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
- 高精度变体：MethodASMPad（2N 零填充线性卷积）、MethodASMShift（离轴载频搬移）与 MethodASMShiftPad（载频搬移+零填充组合），见 PHYSICS.md §3.1。
- ApplyBandlimit 可对任意场做奈奎斯特带限（参数含义见 PHYSICS.md §3）。Propagate 本身不再自动带限——由模拟器 trainer 在每平面后施加一次，避免对 propagate 元件重复滤波；低层直接调用 Propagate 时如需带限请显式调用 ApplyBandlimit。
- Field.ApplyTilt 用精确方向余弦（非傍轴）；Field.ApplyJones 处理标量→矢量升维。
- 功率归一化：Field.NormalizePower(p)；功率读取 Field.Power()（W）。
- 1-D 横向场（x-z 剖面，柱面问题）：Propagate1D(a []complex128, dx, z, wl, ctx) 对任意长度一维场做角谱传播（复用 Bluestein FFT），衰逝波策略与 2-D 一致。
- 非均匀/复折射率介质：PropagateSplitStep(f, z, n IndexFunc, steps, ctx) 做对称 split-step（Strang）BPM；n(x,y,z) 可为复折射率（Im>0 吸收、Im<0 增益），支持分层/梯度介质；UniformIndex(n) 构造常数折射率。
- 宽带谱叠加：PropagatePolychromatic(f, z, samples []WavelengthSample, method, ctx) 对 λ 谱逐波长传播后非相干叠加强度 I=Σ w_k·|U_k|²。
- 全矢量 ASM（含 Ez，非傍轴）：PropagateVectorial(f, z, ctx) 由散度条件 Ez=-(kx Ex+ky Ey)/kz 重构纵分量并三分量传播；NewVectorialField(n, dx) 构造三分量场。
- 3-D 体传播：Propagate3D(f, zs, method, ctx) 把输入场一次传播到多个 z，返回平面栈（x,y,z 体积场）。
- 部分相干（Gaussian Schell 模型）：GenerateSchellRealizations(n, dx, SchellSourceParams{Width,Coherence,Seed}, m) 生成 m 个满足 I=exp(-2r²/w0²)、μ=exp(-|dr|²/(2σc²)) 的相干实现；AverageIntensity 做系综平均；PropagatePartiallyCoherent 生成+传播+平均（非相干叠加）。
- 各向异性/双折射介质：PropagateUniaxial(f, z, no, ne, ctx) 在光轴沿 x 的单轴晶体中传播；PropagateAnisotropic(f, z, eps [3][3]complex128, ctx) 用完整 Berreman 4×4 传播任意（双轴/复）介电张量。
- 传播介质元件（已注册，GUI 可用）：uniaxial{n_o,n_e,distance}、medium{index,absorption,steps,distance}（split-step）、biaxial{n_x,n_y,n_z,distance}（Berreman）。矢量角谱法 method=vectorial 会填充 Plane.Ez，GUI 视图 6/7 显示 |Ez|²/相位。

## 4. 新增一种光学元件（完整配方）

**不想写 Go 源码？** 见 §4.1：把元件的相位/振幅写成 elements/*.json 里的表达式，加载即用；定型后可以用 `wos -gen-go` 转成本节的这种原生实现。

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

## 4.1 脚本元件（元件定义文件）——不写 Go 就做出新元件

把元件的透过率写成 `elements/<名字>.json` 里的表达式，启动时加载即与内置元件同等可用：出现在 GUI 插入列表与参数面板里、能写进场景 JSON、能用于 Go API。改文件后重新加载（GUI「↻ 定义」按钮，或由服务端 ~2 秒的轮询自动完成）即可生效——**不需要重编译，也不需要重启服务**。

### 文件格式

    {
      "name": "metalens",              // 必填：类型名（小写标识符），即 scene 里的 "type"
      "label": "超表面透镜（双曲相位）",   // GUI 显示名
      "help": "…",                     // 悬停说明（写物理含义）
      "behavior": "transmit",          // transmit（默认）| mirror（反射时施加相位）
      "class": "lens",                 // 可选：lens|mirror|stop|other（GUI 配色；默认按 behavior）
      "params": [ /* ParamSpec，与 catalog 同格式：key/label/unit/kind/min/max/step/default/help */ ],
      "phase": "-sign*(2*pi/wl0)*(sqrt(r*r+f*f) - f)",  // 弧度（默认）
      "phase_unit": "rad",             // rad（默认）| waves（表达式值 × 2π）
      "amp": "1",                      // 可选：振幅透过率，缺省 1
      "jones": {                       // 可选：2×2 琼斯矩阵，每项 {re, im} 两个表达式
        "a": {"re": "1"}, "b": {"re": "0"}, "c": {"re": "0"}, "d": {"re": "-1"}
      }
    }

三个效果字段至少要有一个；`params` 里每个参数都必须给 `default`（GUI 插入元件时按默认值填）。元件轮廓（`shape`）仍是场景层的事：轮廓就是通光孔径，表达式在轮廓内逐点生效。

### 表达式语言（optics/expr.go）

- **变量**：`x y`（元件局部坐标，m）、`r = hypot(x,y)`、`th = atan2(y,x)`（rad）、`wl`（m）、`k = 2π/λ`；`pi`、`e`；以及该元件自己的每个参数（按 key 直接写，如 `f`、`Lambda`）。
- **运算**：`+ - * / % ^`（`^` 为幂、右结合）、比较 `> < >= <= == !=`、逻辑 `&& || !`；比较与逻辑的结果是 1/0，可直接参与算术（`if(a > b, x, y)` 里也用它）。
- **函数**：`sin cos tan asin acos atan atan2 sinh cosh tanh exp log log10 sqrt abs sign floor ceil round min max pow hypot mod clamp step rect smoothstep erf sinc rad deg`。
  - `step(x)`：x≥0 取 1；`rect(x)`：|x|≤0.5 取 1；`smoothstep(a,b,x)`、`clamp(v,lo,hi)` 同图形学约定。
  - `sinc(x) = sin(πx)/(πx)`（衍射约定，`sinc(0)=1`）。
  - `if(c, a, b)` **惰性**：只求值被选中的分支，可以拿它守住奇异点（`if(r > a, 1/r, 0)`）。
- **没有**循环、数组与 IO：表达式不会挂死，也不会读到自身以外的任何东西。除零/负数开方按 IEEE 754 给出 ±Inf/NaN，加载时会在探针网格（33×33）上试算并以 `第 N 列` + 坐标报出（`expression is not finite at (x, y) = …`）。
- 语法错误与未知变量/函数/参数都带**列号**报告；参数名不得与内置变量重名，也不得占用预览端点的查询键（`kind/size/width/wl/mask/scale/cmap/pmin/pmax/gamma/part`）。

### 加载、路径与优先级

候选目录按优先级从低到高：可执行文件旁的 `elements/` → 工作目录的 `elements/` → `~/.wos/elements/` → `-elements dir1,dir2`（命令行追加，最高）。目录**不递归**，只读 `*.json`；同名定义按上面顺序被后者覆盖，并在重载报告里给出一条 `skipped`（“overridden by …”）。与内置元件重名直接报错（不允许静默顶替）。

- `wos -check-elements`：只加载校验、打印每个文件的加载/跳过/错误，任何错误以退出码 1 结束（配 CI 或随手检查）。
- 服务端每 2 秒比对定义集合的指纹（每个 `.json` 的文件名、大小、修改时间，见 `optics/customelem.go` 的 `DefinitionDirsSignature`）：新增、改名、原地改写、删除都会触发重新加载，并把逐文件结果写进日志（`-no-elements-watch` 关闭）。只统计 `.json`，所以临时文件不会造成无谓重载。
- 场景引用了未加载的脚本元件时，报错信息会指明“要放 elements/ 下的定义文件”（`componentBehavior` 的 unknown 分支）。

### 预览与 GUI

- `GET /api/elements` 列出已加载定义（名字/标签/行为/类别/参数/表达式/来源文件）与版本号。
- `POST /api/elements/reload` 重新扫描，返回 `{dirs, loaded, skipped, errors, version}`。
- `GET /api/elements/{name}/preview.png?kind=amp|phase&size=&width=&wl=&<参数…>`：按当前参数把元件自身的掩膜画出来——`amp` 画 |t|（线性、0…峰值），`phase` 画包裹相位（|t|≈0 处按 `mask` 阈值置 NaN，渲染成空白，与平面视图同约定）。GUI 在参数面板里以「掩膜预览」两联图呈现，元素参数一改就刷新。
- GUI 插入列表给脚本元件加「· 脚本」后缀，悬停显示定义文件路径；「↻ 定义」按钮立即重载；页面每 5 秒比对 `/api/elements` 的版本号，服务端自动重载后会自己把目录拉过来（不打断正在编辑的输入框）。

### 定型为原生元件：`wos -gen-go`

    wos -gen-go elements/metalens.json -gen-name metalens_native > optics/elemgen_metalens.go

把定义打印成一份原生 Go 元件（内联表达式、注册元件工厂/目录条目/路由行为），丢进 `optics/` 编译即成为内核的一部分：不再每次运行解释表达式，也能随二进制分发。生成器保证语义一致：

- `if(...)` 被**提升为语句**，保持惰性；常量振幅/相位被折叠（`* (1)` 不出现）。
- 参数读取走 `genParamNum`（与 `paramNumericValue` 同一条转换规则），函数与 `^ % 比较/逻辑` 走 `math.*` 或 `exprgen_rt.go` 里的助手，因此数值与脚本版一致。
- `optics/elemgen_test.go` 锁定两件事：`elemgen_metalens.go` 必须等于当前生成器的输出（生成器一改、文件没重生成就红），以及 `metalens_native` 与脚本版 `metalens` 在元件面与传播 0.15 m 后逐像素一致（1e-12）。
- 生成文件里 `RegisterGeneratedElement` 会拒绝重名（init 期 panic），避免悄悄顶掉别的元件。

### 性能

表达式**编译成闭包**（不是逐节点 switch），并按行在多核上并行求值。实测（1024² 网格、涡旋透镜级表达式）：AST 解释 144 ns/px → 闭包 80 ns/px → 闭包 + 复数乘 + 8 线程 **38 ns/px**，即约 **39 ms / 次施加**；同一次运行里每个相干单元的单步传播约 0.9 s，脚本元件的求值开销可忽略。

### 边界（有意不做）

- 只做**逐点（薄元件）透过率**；结构行为仅 `transmit` 与 `mirror`——分束器、探测器这类改变光路拓扑的元件仍需 §4 的原生配方。
- 不做需要全局信息的元件（按峰值归一化、卷积、迭代优化：RCWA/DOE 优化等）——表达式看不到场。
- 定义文件不随场景 JSON 走：把场景发给别人要一并给出 `elements/*.json`（缺定义时的报错会提示）。
- 与脚本元件相关的测试：`optics/expr_test.go`、`optics/customelem_test.go`、`optics/elemgen_test.go`、`optics/examples_scripted_test.go`（金属透镜焦斑/色散、正弦振幅光栅级次 (m/2)²、脚本镜 ≡ 凹面镜、JSON 场景路径）、`server/elements_test.go`（三个端点 + 重载后的目录）。

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
- **共面元件（同一平面、同一朝向）**：合并为**一个平面**只通过一次。默认**串联**——各元件的透过率相乘（物理上两片薄元件叠在同一平面）；元件参数 `parallel: true` 者改取**并集**（并排图案，如两条缝，各元件只在自己轮廓内作用后相加）。**同一 z 的两个元件不再被判成环路**：旧行为会丢弃整条光路（`planes: []` + 仅一条 `scene_cycle_dropped` 告警），现在正常通过，并补一条 `scene_geometry` 提示列出该平面上的元件。组内非头元件会先平移到自身位置再作用，再平移回来。
- sensor：记录输出平面（克隆场 + 指标）。参数 strehl_aperture/strehl_distance 启用 Strehl。参数 **`passthrough: true`（或 `monitor: true`）使其成为无损监视器**：记录该面后让光继续，于是一次运行可读多个端口（旧行为是探测器终结光路、一条路径只记第一个面）；不带该参数时仍吸收光并终结光路。

限制常量（simulator.go）：MinGridSize=2、MaxGridSize=65536×4（=262144）、MaxElements=256、MaxPlanes=64、MaxArmDepth=8。

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
- **全模式联合分布**：`QuantumResult.JointFull` 给出 `(cutoff+1)^modes` 长度的完整 P(n0,n1,…)（little-endian 下标），3+ 模符合/玻色采样可读；`joint_distributions` 仍只给两两边缘。
- **后选择**：`QuantumConfig.Postselect{Modes,Counts}` 投影到「指定模式光子数等于给定值」的子空间并归一化（纯态/密度矩阵后端都支持），结果含 `postselect_probability`。这是 KLM 这类 heralded 门的接口。
- **限制**：模式数 ≤16、截断 ≤64，且受状态空间 `(cutoff+1)^modes ≤ 2^20` 约束（密度矩阵后端为 `≤ 2^10`）。因此 5–8 模、截断 1–2 的电路（单光子/KLM）可用，而 4 模截断 20 仍照旧。
- 混合态（热态/损耗）由密度矩阵后端处理：门做酉共轭 ρ→UρU†（分块局部酉），损耗信道做 Kraus 分解 Σ E_l ρ E_l†。SimulateQuantum 自动选择后端（状态含 thermal 或门含 loss 时走密度矩阵）。密度矩阵内存为 Dim²，因此其后端另有 `≤ 2^10` 的状态空间上限。

物理测试（quantum_test.go）：HOM 聚束、相干态泊松统计、Fock g²、压缩真空正交分量（Heisenberg 极限 1/16）、双模压缩光子数关联、热态统计、损耗信道（迹守恒/二项分布）、单光子马赫-曾德尔。全部解析对比。
