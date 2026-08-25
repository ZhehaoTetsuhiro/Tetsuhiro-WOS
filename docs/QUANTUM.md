# 量子光学模拟（docs/QUANTUM.md）

本文件定义 Tetsuhiro WOS 量子光学内核的物理模型、数值方法与 API。它与波动光学内核（角谱法等）独立：波动光学演算经典复振幅场，量子内核演算多模 **Fock 基线性光学** 中的量子态。纯态用态矢量表示，混合态（热态、损耗信道）用密度矩阵表示。

## 1. 模型与约定

- **纯态**：`M` 个光学模式，每模光子数截断到 `0..cutoff`。态矢量为

      |ψ⟩ = Σ_{n0,n1,…,n_{M−1}} c_{n0 n1 …} |n0,n1,…,n_{M−1}⟩

  编码为 little-endian 下标：`idx = n0 + (cutoff+1)·n1 + (cutoff+1)²·n2 + …`（模式 0 变化最快）。
- **混合态**：密度矩阵 `ρ`（Dim×Dim，行主序）；门做酉共轭 `ρ → U ρ U†`，损耗信道做 Kraus 分解 `ρ → Σ_l E_l ρ E_l†`。
- **产生/湮灭算符**：`a|n⟩ = √n |n−1⟩`，`[a, a†] = 1`（各模式独立，不同模式对易）。
- **正交分量**：`x_θ = (a e^{−iθ} + a† e^{iθ}) / 2`。真空方差 `Var(x_θ) = 1/4`（散粒噪声基线）。
- 平均光子数 `⟨n⟩ = Tr(ρ a†a)`；功率/模数的物理含义映射为 `⟨n⟩`。

## 2. 初态（构造器）

| 类型 | 态 | 说明 |
|---|---|---|
| vacuum | \|0…0⟩ | 全真空 |
| fock | \|n0,n1,…⟩ | 光子数态（可多模、可多光子） |
| coherent | ⊗\|α_m⟩ | 相干态，泊松统计 |
| squeezed_vacuum | ⊗ S(z_m)\|0⟩ | 压缩真空，z = r e^{iθ} |
| two_mode_squeezed | sech(r) Σ tanh(r)^n \|n,n⟩ | 双模压缩真空（EPR 纠缠态，模式 0、1） |
| thermal | ⊗ Σ P(n)\|n⟩⟨n\| | 热态（混合态），几何分布 P(n)，g²(0)=2 |

## 3. 门（线性光学幺正，原地作用）

| 门 | 算符 | 参数 |
|---|---|---|
| PhaseShift | exp(i φ n) | mode, phase |
| BeamSplitter | exp(iθ (a_m0†a_m1 + a_m0a_m1†))，θ = asin(√R) | mode0, mode1, reflectivity R |
| Displace | exp(α a† − α* a) | mode, α (实/虚部) |
| Squeeze | exp(½(z* a² − z a†²)) | mode, r, phase (z=r e^{iθ}) |
| Loss | Σ_l E_l ρ E_l†，E_l=√((1−T)^l/l!) T^{n/2} a^l | mode, transmittance T（损耗信道，产生混合态） |

**分束器约定**（与经典内核一致）：对称、无损，海森堡变换为

    a0 → cosθ a0 + i sinθ a1
    a1 → i sinθ a0 + cosθ a1

即透射 `√(1−R)`、反射 `i√R`；`R = 0.5` 是平衡分束器。矩阵指数通过按总光子数分块、逐块对角化精确计算（无截断误差的经典极限）。

## 4. 测量（观测量）

- **平均光子数** `⟨n⟩`；**光子数分布** `P(n)`（每模）。
- **二阶相干度** `g²(0) = ⟨n(n−1)⟩ / ⟨n⟩²`：相干态 = 1，热态/双模压缩约化态 = 2，单光子 = 0，Fock \|n⟩ = 1−1/n。
- **正交分量** `⟨x_θ⟩` 与 `Var(x_θ)`：压缩真空低于 1/4（低于散粒噪声），乘积 ≥ 1/16（海森堡极限，纯态取等号）。
- **联合分布** `P(na, nb)`（任意模式对）：用于 Hong-Ou-Mandel 符合计数、EPR 关联。
- **保真度** `F = |⟨ψ|φ⟩|²`；**范数**（幺正性检验）。

## 5. 标志性效应（测试覆盖）

| 效应 | 结果 |
|---|---|
| **Hong-Ou-Mandel**：两个不可分辨光子 \|1,1⟩ → 50:50 分束器 | P(1,1)=0（聚束），P(2,0)=P(0,2)=1/2 |
| 相干态泊松统计 | 均值 = 方差 = \|α\|²，g²(0)=1 |
| 压缩真空正交分量 | Var(x)=e^{−2r}/4 < 1/4，Var(p)=e^{2r}/4 > 1/4，乘积=1/16 |
| 双模压缩真空 | ⟨n⟩=sinh²(r)，完美光子数关联 P(n,m≠n)=0，约化态 g²=2 |
| 热态统计 | 几何分布，⟨n⟩=n̄，g²(0)=2 |
| 损耗信道 | Fock \|1⟩ → 二项分布；相干态仍相干（⟨n⟩=T\|α\|²，g²=1）；迹守恒 |
| **单光子马赫-曾德尔** | P(1,0)=(1−cos φ)/2，P(0,1)=(1+cos φ)/2（单光子干涉） |

## 6. API

### Go 库

    import "twos/optics"

    q, _ := optics.FockState(2, 4, []int{1, 1})   // |1,1⟩
    q.BeamSplitter(0, 1, 0.5)                     // 50:50 分束器
    fmt.Println(q.JointProb(1, 1))                // HOM：≈0

完整示例见 `examples/quantum`（`go run ./examples/quantum`），含单光子马赫-曾德尔、热态、损耗信道与 PNG 导出。

### HTTP

`POST /api/quantum` 接收 `QuantumConfig`，同步返回 `QuantumResult`（量子模拟为微秒级，无需异步轮询）：

    {
      "modes": 2, "cutoff": 4,
      "state": {"type": "fock", "params": {"occupation": [1,1]}},
      "gates": [{"type": "beam_splitter", "params": {"mode0":0, "mode1":1, "reflectivity":0.5}}]
    }

返回 `mean_photons`、`g2`、`photon_distributions`（每模 P(n)）、`quadratures`（mean_x/var_x/mean_p/var_p）、`joint_distributions`（"m0,m1" 拍平为 `(cutoff+1)²` 数组，下标 `a*(cutoff+1)+b`）。

`POST /api/quantum?fmt=png` 返回同一结果的 PNG 图表（上方每模光子数分布柱状图，下方第一对模式联合分布热图，对数标度）。Go 库等价：`server.RenderQuantumPNG(path, res)`。

## 7. 限制

- 光子数截断 `cutoff ≤ 20`、模式数 `≤ 4`（`SimulateQuantum` 校验）。截断误差随 `cutoff` 增大按 `tanh(r)^{2·cutoff}` 量级衰减；密度矩阵后端（热态/损耗）内存为 Dim²，模式多、截断大时成本更高。
- 线性光学：不含 Kerr 非线性、腔、光-物质相互作用等（留待扩展）。密度矩阵保真度（Uhlmann）未实现，可用迹/可观测统计代替。
