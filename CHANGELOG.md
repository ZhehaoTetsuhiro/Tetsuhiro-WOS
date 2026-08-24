# 更新日志（Changelog）

本项目所有显著变更都会记录于此。格式遵循 [Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.0/)，版本号遵循 [语义化版本](https://semver.org/lang/zh-CN/)。

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

[Unreleased]: https://github.com/ZhehaoTetsuhiro/Tetsuhiro-WOS/compare/v0.1.1...HEAD
[v0.1]: https://github.com/ZhehaoTetsuhiro/Tetsuhiro-WOS/releases/tag/v0.1
[v0.1.1]: https://github.com/ZhehaoTetsuhiro/Tetsuhiro-WOS/releases/tag/v0.1.1
