# 更新日志（Changelog）

本项目所有显著变更都会记录于此。格式遵循 [Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.0/)，版本号遵循 [语义化版本](https://semver.org/lang/zh-CN/)。

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

[Unreleased]: https://github.com/ZhehaoTetsuhiro/Tetsuhiro-WOS/compare/v0.3.0...HEAD
[v0.3.0]: https://github.com/ZhehaoTetsuhiro/Tetsuhiro-WOS/compare/v0.2.0...v0.3.0
[v0.2.0]: https://github.com/ZhehaoTetsuhiro/Tetsuhiro-WOS/releases/tag/v0.2.0
[v0.1]: https://github.com/ZhehaoTetsuhiro/Tetsuhiro-WOS/releases/tag/v0.1
[v0.1.1]: https://github.com/ZhehaoTetsuhiro/Tetsuhiro-WOS/releases/tag/v0.1.1
