# GPU 加速设计（docs/GPU.md，可选/远期）

本文给出内核的 GPU 加速接口设计。当前实现为纯 Go 零依赖（仅标准库）；GPU 后端是可选加速，
必须保持 CPU 路径不变、通过构建标签（build tag）按需启用，并以 CPU 结果为基准验证一致性。

## 1. 加速目标

按热点排序（见 README 性能表，1024² 单次 ASM ~150ms）：

1. 2-D/1-D FFT（fft2D / fft1DAny，占 ASM 绝大部分耗时）。
2. 传递函数逐像素相乘（ctx.transfer、propASMPadCore 的频域循环）。
3. split-step 的介质相位屏（applyMediumPhase，逐像素复指数）。

## 2. 后端选择

- CUDA（cuFFT）经 cgo：吞吐最高，NVIDIA 硬件；用 //go:build cuda 标签隔离。
- OpenCL：跨厂商（NVIDIA/AMD/Intel/Apple），可动态发现设备。
- 混合精度：FFT 用 complex64 可再降一半带宽；相位敏感路径保留 complex128（或做误差回归测试）。

## 3. 接口抽象

在 fft.go 引入后端分派：

    // 内部：fft1DAny/fft2D 在 gpu 标签下改走 fftBackend.Do(...)
    type fftBackend interface {
        Plan1D(n int) FFTPlan
        Plan2D(n int) FFTPlan
        Exec(p FFTPlan, a []complex128, inverse bool)
    }

传播/介质层不感知后端：它们只调用 fft2D/fft1DAny 与逐像素循环；GPU 版把逐像素循环也下推为内核
（transfer、applyMediumPhase 的核函数化）。

## 4. 内存与数据搬运

- 1024² 复 128 场 ~16 MB/分量；PCIe 传输 ~ms 级，与单次 FFT 相当，需尽量“驻留显存”。
- 建议把整条光路的平面数组整体搬入显存，多平面传播在 GPU 内完成，仅最终传感器回传。
- server 的 LRU 预算（-max-run-mb）需计入显存池。

## 5. 正确性保障

- 复用现有精度测试作为“CPU 参照”：同一输入，GPU 与 CPU 相对 L2 误差须 < 1e-6（单精度则 < 1e-4）。
- Parseval / 冲激谱 / 往返三项 FFT 测试在 GPU 后端上重放。
- 非 2 的幂（Bluestein）在 GPU 上应退化为 CPU 或由 cuFFT 的任意长度支持，需单独验证。

## 6. 状态

当前仅落地本设计文档；未引入 cgo/CUDA 依赖，以维持零依赖单二进制分发。

## 7. 实现状态（已落地，2026-10）

第 1、3 节的设计已实现于 `optics/gpu.go`（`//go:build cuda`）与 `optics/gpu_stub.go`（默认构建）：

- 分派点：`fft2D` / `fft1DAny` 在启用时调用 `gpuFFT2D` / `gpuFFT1D`；纯 Go 实现保留为
  `fft2DCPU` / `fft1DAnyCPU`，任何 CUDA 调用失败即回退，所以无 GPU 机器结果不变。
- cuFFT Z2Z（double）单次 2-D 变换替代逐行/逐列；任意长度（含非 2 的幂）由 cuFFT 原生支持。
- 显存缓冲按字节大小做自由表复用（`gpuAlloc`/`gpuRelease`），避免每次 `cudaMalloc/cudaFree`。
- 线程安全：所有显存拷贝与 cuFFT 执行经单一互斥量串行化（句柄并发执行无保证）。
- 启用：`-gpu` 命令行开关或 `WOS_GPU=1`；`optics.SetGPU/GPUEnabled/GPUAvailable/GPUBackendInfo`。
- 阈值：`gpuMin2D=64`、`gpuMin1D=1024`（小于此的网格传输开销大于收益）。

实测（Tesla T4，`-bench FFT2D`，单次含往返）：1024² CPU 14 ms / GPU 12 ms（1.1×）；
2048² 57 ms / 45 ms（1.3×）；4096² 331 ms / 179 ms（1.85×）。

未做（后续可加）：真正“驻留显存”的多平面光路（把 `ctx.transfer`/`applyMediumPhase` 的逐像素循环
下推为 CUDA 内核，目标是每次传播只做一次 H2D/D2H）；本条需引入 nvcc 编译的 `.cu` 内核，
而当前后端只依赖运行库（cudart/cuFFT），保持 `go build -tags cuda` 无需 nvcc。

实测补充：页面锁定（pinned）暂存内存在本机反而更慢（4.1 GB/s vs 可分页 6.8 GB/s），
故未启用 pinned 路径；`gpuHostAlloc/gpuCopyH2D/gpuCopyD2H` 保留给基准与将来设备使用。
