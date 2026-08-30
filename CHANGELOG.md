# 更新日志（Changelog）

本项目所有显著变更都会记录于此。格式遵循 [Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.0/)，版本号遵循 [语义化版本](https://semver.org/lang/zh-CN/)。

## [v0.3.3] - 2026-08-30

### 修复（内核）

- 修复 bandlimit 在分束器后应用导致暗口出现数值条纹的缺陷。原实现每个元素后都施加奈奎斯特带限，分束器后主臂（继续在主循环传播）和子臂（进入子训练传播）经历的 FFT 次数不同，产生不对称的浮点舍入误差；等臂时两臂残余相位差约 3e-8 rad，在暗口形成 ~1e-15 相对可见度的周期性条纹图样。现改为仅在 propagate 元素后施加 bandlimit（分束器/反射镜本身不产生超出带限的频率分量），修复后暗口功率精确为 0。

### 新增（测试）

- 新增等臂 Michelson 回归测试 `TestMichelsonBalancedArms`：验证两臂完全等光程时探测器为暗口、回光源端口为亮口，总功率守恒。

### 变更（示例）

- Michelson preset 折返臂反射镜增加 `tilt_x = 0.5 mrad` 倾斜，使两臂返回时存在横向波前倾角，探测器端口产生周期约 0.5 mm 的等厚干涉条纹——这是 Michelson 干涉仪最经典的可视化模式。等臂（无倾斜）时暗口精确为零的行为由新增回归测试覆盖；新增 `examples/presets/michelson.svg` 光路示意图。

## [v0.3.2] - 2026-08-29

### 修复（传播内核复场级审查）

对传播内核做了一次复场（振幅+相位）级数值审查——将 `asm` / `asm_pad` / `asm_shift` / `asm_shift_pad` / `fresnel_tf` / `fresnel_ir` / `vectorial` / 一维传播与解析解（倾斜平面波、Fresnel–Gaussian 闭式解、夫琅禾费远场闭式解）逐像素对比。标量/矢量 ASM 核心、pad/shift 变体、一维传播与往返逆传播均验证精确（1e-8–1e-16）；审查发现并修复以下缺陷（原有测试均为强度级，无法察觉相位类错误）：

- **夫琅禾费远场（fraunhofer）复场存在奈奎斯特棋盘相位错误**：中心化输入布局下原始 DFT 与采样连续傅里叶变换相差因子 e^{iπ(p+q)}，原实现仅做 fftshift，输出复场逐像素相差 (−1)^{i+j}（相邻像素 π 相位翻转）。强度分布不受影响（现有强度级测试全部通过），但相位、剖面相位图与任何对远场结果的后续相干操作（继续传播、干涉合束）都被破坏。已在 fftshift 前补偿该因子，修复后与高斯远场闭式解的复场相对 L2 误差从 √2 降至 5.8e-16（新增回归测试 `TestFraunhoferComplexFieldExact`）。
- **fraunhofer_nearfield 告警失效**：菲涅耳数 F = D²/(λ|z|) 在传播完成之后、以过期输入像素尺寸对远场图样计算，实际上从不触发。现改为在传播前测量输入光斑支撑半径 D（与 docs/PHYSICS.md §4.3 定义一致），F > 0.5 时正确告警（新增回归测试 `TestFraunhoferNearfieldWarningMeasuresInput`）。
- **球面波光源的"会聚"选项产生发散波**：会聚分支仅把振幅乘以 −1（等价于整体加 π 常数相位），并未共轭相位——勾选"会聚"得到的是发散球面波。现改为相位取反 e^{−ikd}/d（发散波 e^{+ikd}/d 的相位共轭），会聚球面波在 z = +radius 处聚焦（新增回归测试 `TestSphericalSourceConvergingPhase` / `TestSphericalSourceConvergingFocuses`）。
- **泽尼克像差板 m=0 模式缺少 Noll 归一化**：m≠0 模式带 √(2(n+1)) 归一化而 m=0 模式（离焦 c4、球差 c11）为裸多项式，同元素内归一化不一致，与目录宣称的"Noll 序"不符。现补齐 √(n+1) 因子，所有系数含义统一为 RMS 归一化（新增回归测试 `TestZernikeNollNormalization`）。**行为变更**：与旧版本相比 c4 需乘 1/√3、c11 需乘 1/√5 才能复现旧的波前幅值。
- 新增复场级回归测试集 `optics/complex_field_test.go`（9 项）：单频倾斜平面波正/反向精确性（asm/asm_shift，含近带边强倾斜）、Fresnel–Gaussian 解析闭式解对比（asm/asm_pad/fresnel_tf）、夫琅禾费复场精确性、远场告警方向、pad/shift 变体复场一致性、矢量 ASM 单频平面波 Ez 重构精确性、球面波会聚相位与聚焦行为、泽尼克 Noll 归一化。

## [v0.3.1] - 2026-08-28

### 新增

- **孔径光阑按形状分参数 + 更多形状**：孔径光阑（aperture）切换形状后，GUI 仅显示该形状对应的参数（show_if 条件可见性）；新增方孔、三角孔、十字孔、星形孔、超椭圆孔，并支持用顶点列表自定义任意多边形孔（shape=custom）。三角/多边形/星形/自定义形状现也支持边缘平滑（edge_sigma）。

### 修复

- 全局参数栏「传播算法」「光源类型」下拉列表超出面板外框：`select` 增加 `min-width:0` 并按最长可伸缩空间收缩（超宽选项截断显示省略号），量子门表格内的下拉框同步修正。
- Fresnel 冲激响应法在裸网格上做圆周卷积会产生环绕伪影：改为零填充 2N×2N 网格上的线性卷积（输出窗口边缘的虚假 stationary point 伪影消除），并在 |z| < N·dx²/λ 时给出混叠告警。
- Berreman 4×4 前向本征模出现重复/退化根时 2×2 求解奇异产生 NaN：增加防御性回退，跳过已选根并取剩余根中实部最大者，保证两个本征向量互异。
- 量子图表渲染器对畸形结果（nil、切片过短、异常大的 Cutoff）可能 panic 或耗尽内存：统一经 `quantumChartDims` 清洗光子数范围与模式数。
- 仿真 goroutine 内的 panic 现被 recover 并转为错误信息返回给前端，不再导致进程崩溃；`optics.Warnings.Add` 对 nil 接收者静默丢弃诊断而非 panic。

## [v0.3.0] - 2026-08-26

### 新增

- **全矢量角谱法**：`method=vectorial` 由散度条件 kx·Ex + ky·Ey + kz·Ez = 0 重构纵分量 Ez，并三分量传播（非傍轴），适合大 NA 聚焦；传感器可显示 |Ez|²。
- **非均匀/复折射率介质**：`medium` 元件 + `PropagateSplitStep`（对称 split-step / Strang BPM），支持分层/梯度折射率与吸收/增益介质（复折射率）。
- **各向异性/双折射介质**：`uniaxial` 元件（光轴沿 x 的单轴晶体，n_o/n_e）与 `biaxial` 元件（完整 Berreman 4×4 矩阵法，任意双轴/复介电张量，含 o/e 耦合）。
- **宽带谱叠加**：`PropagatePolychromatic` 对波长谱逐波长传播后非相干叠加强度 I = Σ w_k·|U_k|²。
- **部分相干光**：Gaussian Schell 模型（`GenerateSchellRealizations` / `PropagatePartiallyCoherent`），可调束腰与横向相干宽度，系综平均。
- **3-D 体传播**：`Propagate3D` 把输入场一次传播到多个 z，输出 x,y,z 体积场（平面栈）。
- **GUI 视觉升级**：毛玻璃质感（半透明面板 + 背景模糊 + 柔和渐变背景）、左上角标题后显示版本号（v0.3.0）、深蓝色填充「▶ 运行」按钮单独凸显。
- **隐藏中心方形图案**：输出区新增「隐藏图案」按钮（h 键），仅隐去中心方形输出图样、保留剖面曲线，且隐藏后中心不再显示占位文字。
- **网格大小自定义**：网格大小下拉框新增「自定义」，输入边长 a 即得 a×a 网格；允许小于 64、超过 2048，上限 65536×4（内核 MinGridSize=2、MaxGridSize=262144）。
- **帮助多列布局**：? 帮助面板由单列表格改为多列排版，更紧凑易读。

### 修复

- 超大网格（如 65536）会触发进程级 OOM 崩溃：新增内存预算校验 `optics.CheckGridMemory`，估算峰值内存并与可用内存预算对比，超预算时返回干净错误而非致命崩溃。

## [v0.2.0] - 2026-08-26

### 新增

- **角谱法高精度变体**：`asm_pad`（2N×2N 零填充线性卷积，消除光束超出窗口时的环绕混叠）与 `asm_shift`（离轴载频搬移 + 平移传递函数 H(f+fc)，抑制大倾角光束频谱贴边混叠），均可按全局或单步选择，并配有解析对比测试。
- **量子光学内核**：Fock 基线性光学（optics/quantum.go）——Fock/相干/压缩真空/双模压缩（EPR）态，相移/分束器/位移/压缩门，光子数分布、g²(0)、正交分量、联合分布与保真度测量；物理测试覆盖 Hong-Ou-Mandel、泊松统计、压缩（Heisenberg 极限）、EPR 关联。
- **混合态（密度矩阵）**：optics/quantum_density.go——热态（几何分布、g²=2）、损耗信道（Kraus 算符、迹守恒）、单光子马赫-曾德尔干涉；SimulateQuantum 自动在纯态/密度矩阵后端间切换。
- **量子结果 PNG 导出**：`POST /api/quantum?fmt=png` 与 `server.RenderQuantumPNG`，输出光子数分布柱状图 + 联合分布热图；GUI 量子面板「导出 PNG」按钮。
- **量子接口**：HTTP `POST /api/quantum`（同步返回 QuantumResult）、`/api/catalog` 增加 quantum 目录段、`examples/quantum` 示例（含单光子 MZ、热态、损耗）、docs/QUANTUM.md。
- **GUI 量子面板**：顶部「波动光学 / 量子光学」模式切换（m 键），量子编辑器 + 光子数分布/联合分布可视化。
- **GUI 支持鼠标**：移除全局 `pointer-events:none`，鼠标与键盘双可用（键盘快捷键保留）。
- **凹面镜 / 凸面镜元件**：可调曲率半径 R 的球面反射镜——凹面镜会聚（等效焦距 R/2）、凸面镜发散（−R/2），带振幅反射率与可选孔径。
- **GUI 布局统一**：改为两栏——左侧「光路编辑器」（波动模式另含全局参数/光源/警告）+ 右侧**放大的输出区**；波动/量子模式**共用同一输出区**且同时只显示一种；「▶ 运行」按钮移至右上角（? 帮助旁）；改为天蓝色浅色主题。
- **禁用自动运行**：修改参数后不再自动重算（默认关闭，`a` 键可重新开启），需手动点「▶ 运行」/空格。
- **SVG 导出**：量子结果 `POST /api/quantum?fmt=svg`（矢量图表）与 `server.RenderQuantumSVG`；波动光学输出框新增「导出 SVG」——把 **X、Y 两个剖面**同时导出为**纯矢量 SVG（1D polyline + 坐标轴/标签，上下两张子图）**。

### 变更

- 修复角谱传播中奈奎斯特带限被重复施加的问题（propagate 元件不再在 Propagate 内与模拟器各滤波一次）。

### 修复

- 修复波动光学 SVG 剖面导出空白的问题（`<polyline>` 的 `points` 误用 `M/L` 路径命令，已改为纯坐标对）。

## [v0.1.1] - 2026-08-24

### 变更

- **按键优化**：选中分束器时按 Enter 进入反射臂子光路（Esc 返回），与「编辑反射臂光路 →」按钮一致；帮助面板、页脚快捷键提示与 GUI 文档同步更新。

## [v0.1] - 2026-08-24

首个公开发布版本。

### 新增

- **数值内核**：自研并行 FFT、角谱法（ASM，亥姆霍兹方程精确解）、菲涅耳（两种形式）、夫琅禾费远场传播。
- **数值稳健性**：衰逝波处理、奈奎斯特带限正则化、菲涅耳数 / 采样有效性自动告警。
- **光学元件**：20+ 种可调参数元件（透镜、光阑、光栅、轴锥镜、波带片、涡旋相位板、楔形棱镜、漫射体、反射镜、泽尼克像差板、偏振片、波片、旋光片、自定义琼斯矩阵、分束器、合束器等）。
- **物理完备性**：琼斯矢量偏振（2 分量）、折返光路（反射镜 / 迈克尔逊）、分束臂与相干合束（马赫-曾德尔等干涉仪）、功率归一化（SI 单位）、质心 / RMS / Strehl 指标、一维剖面。
- **光源**：平面波、高斯、拉盖尔-高斯、厄米-高斯、贝塞尔、球面波、自定义。
- **GUI**：纯键盘操作 Web 页面（Tab / 方向键 / 快捷键，鼠标已禁用），单二进制分发（`wos`）。
- **接入**：内核即库（`import "twos/optics"`）与 HTTP API。
- **精度验证**：内建 18 项物理与数值测试。

[Unreleased]: https://github.com/ZhehaoTetsuhiro/Tetsuhiro-WOS/compare/v0.3.3...HEAD
[v0.3.3]: https://github.com/ZhehaoTetsuhiro/Tetsuhiro-WOS/compare/v0.3.2...v0.3.3
[v0.3.2]: https://github.com/ZhehaoTetsuhiro/Tetsuhiro-WOS/compare/v0.3.1...v0.3.2
[v0.3.1]: https://github.com/ZhehaoTetsuhiro/Tetsuhiro-WOS/compare/v0.3.0...v0.3.1
[v0.3.0]: https://github.com/ZhehaoTetsuhiro/Tetsuhiro-WOS/compare/v0.2.0...v0.3.0
[v0.2.0]: https://github.com/ZhehaoTetsuhiro/Tetsuhiro-WOS/releases/tag/v0.2.0
[v0.1]: https://github.com/ZhehaoTetsuhiro/Tetsuhiro-WOS/releases/tag/v0.1
[v0.1.1]: https://github.com/ZhehaoTetsuhiro/Tetsuhiro-WOS/releases/tag/v0.1.1
