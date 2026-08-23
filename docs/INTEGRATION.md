# 接入说明（docs/INTEGRATION.md）

WOS 提供两种接入方式：**Go 库直接嵌入**（推荐给内核开发者/高吞吐场景）与 **HTTP API**（任意语言）。以下均给出最小可运行示例。

## 1. Go 库嵌入（内核优先）

模块名 wos，零第三方依赖：

    require wos v0.0.0
    replace wos => ../WOS        // 或直接把源码放入你的工程

最小程序（透镜聚焦 + 指标 + PNG 输出）：

    package main

    import (
        "fmt"
        "log"
        "wos/optics"
        "wos/server" // 可选：PNG 可视化助手
    )

    func main() {
        cfg := optics.Config{
            Grid:       optics.GridSpec{Size: 1024, Width: 0.01},
            Wavelength: 632.8e-9,
            Method:     "asm",
            Evanescent: "decay",
            Bandlimit:  &optics.BandlimitOpts{Fraction: 0.9, Sigma: 0.05},
            Source:     optics.SourceSpec{Type: "plane", Params: map[string]any{"power": 1e-3}},
            Elements: []optics.ElementSpec{
                {Type: "lens", Params: map[string]any{"f": 0.5, "aperture": 0.0025}},
                {Type: "propagate", Params: map[string]any{"distance": 0.5}},
                {Type: "sensor", Params: map[string]any{"label": "focus",
                    "strehl_aperture": 0.0025, "strehl_distance": 0.5}},
            },
        }
        res, err := optics.Simulate(cfg)
        if err != nil { log.Fatal(err) }
        p := res.Planes[0]
        fmt.Printf("峰值 %.4g W/m², Strehl %.3f, 功率 %.4g W\n", p.Stats.Peak, p.Stats.Strehl, p.Stats.Power)
        // 直接访问复振幅场（内核级数据通路）：
        c := p.Ex[p.Size/2*p.Size+p.Size/2]     // 中心像素复振幅
        fmt.Printf("中心场 %v\n", c)
        // 剖面
        prof, _ := p.ProfileOf("x", optics.KindIntensity, nil)
        fmt.Printf("剖面 %d 点, x[0]=%g m\n", len(prof.X), prof.X[0])
        // PNG 输出（可选，仅可视化用）
        _ = server.RenderPlanePNG("focus.png", p, "total", "log", "inferno")
    }

完整示例：examples/demo（聚焦）、examples/interferometer（马赫-曾德尔扫描）。

### 1.1 配置 JSON 模式（与 HTTP/文件通用）

Config 结构体直接就是 JSON 协议（字段名见 Config 注释）：

    {
      "grid": {"size": 1024, "width": 0.01},
      "wavelength": 632.8e-9,
      "polarized": false,
      "method": "asm",
      "evanescent": "decay",
      "bandlimit": {"fraction": 0.9, "sigma": 0.05},
      "source": {"type": "gaussian", "params": {"waist": 0.001}},
      "elements": [
        {"type": "propagate", "params": {"distance": 0.1}},
        {"type": "lens", "params": {"f": 0.2}},
        {"type": "sensor", "params": {"label": "focus"}}
      ]
    }

json.Unmarshal 到 optics.Config 即完成解析；ValidateConfig 给出逐项错误。

### 1.2 干涉仪配置（分束臂 + 合束）

分束器把反射臂定义为嵌套光路，合束器以复权重相干叠加（物理约定见 PHYSICS.md §6）：

    "elements": [
      {"type": "propagate", "params": {"distance": 0.02}},
      {"type": "beamsplitter", "params": {"reflectivity": 0.5, "reflected_arm": {
        "elements": [
          {"type": "propagate", "params": {"distance": 0.04}},
          {"type": "mirror", "params": {}},
          {"type": "propagate", "params": {"distance": 0.04}}
        ]}}},
      {"type": "propagate", "params": {"distance": 0.04}},
      {"type": "combiner", "params": {"outputs": [
        {"label": "det", "weights": [
          {"arm": "main", "re": 0.7071, "im": 0},
          {"arm": "bs0",  "re": 0,      "im": 0.7071}]},
        {"label": "src", "weights": [
          {"arm": "main", "re": 0,      "im": 0.7071},
          {"arm": "bs0",  "re": 0.7071, "im": 0}]}
      ]}}
    ]

臂标识：main 恒为当前光路；bs0 为当前光路第 1 个分束器的反射臂，bs1 第 2 个……嵌套臂为 bs0.bs0 等。合束器必须为光路末端。

## 2. HTTP 接入

启动服务：

    go build -o wos ./cmd/wos && ./wos -addr :8080

端点总览（详见 docs/API.md）：POST /api/simulate（202，返回 run_id）→ 轮询 GET /api/runs/{id} → GET 平面二进制/PNG 与剖面。

### 2.1 curl 全流程

    curl -s -X POST -H 'Content-Type: application/json' --data @system.json \
         http://localhost:8080/api/simulate          # {"run_id":"...","status":"running"}
    curl -s http://localhost:8080/api/runs/<RUN>     # 轮询到 status=done，取 planes[].id
    curl -s -o plane.bin "http://localhost:8080/api/runs/<RUN>/planes/sensor_0?field=total&fmt=bin"
    curl -s -o plane.png "http://localhost:8080/api/runs/<RUN>/planes/sensor_0?field=total&fmt=png&scale=log&cmap=inferno"
    curl -s "http://localhost:8080/api/runs/<RUN>/profiles/sensor_0?axis=x&field=total"

### 2.2 二进制平面格式

- fmt=bin：**float32 小端**裸数组，长度 N×N×4 字节，行主序（y 行 × x 列），无头。
- 网格尺寸/物理像素 dx 从 GET /api/runs/{id} 的 grid 与 planes[].dx 获取。
- field 取值：total（|Ex|²+|Ey|²）、ex、ey（分量强度，W/m²）、phase_x、phase_y（弧度，−π…π）。
- 一行 Python：data = struct.unpack('<%df' % (nbytes//4), raw)（完整客户端见 examples/python_client.py）。

### 2.3 Python 客户端

    python3 examples/python_client.py http://localhost:8080

输出指标、剖面，并将平面保存为 wos_plane.npy（自动实测艾里斑暗环与理论对比）。

### 2.4 JavaScript 浏览器端

    const r = await fetch('/api/simulate', {method:'POST', headers:{'Content-Type':'application/json'}, body: JSON.stringify(cfg)});
    const {run_id} = await r.json();
    // 轮询 /api/runs/{run_id} 至 status=done
    const buf = await fetch('/api/runs/'+run_id+'/planes/'+pid+'?field=total&fmt=bin').then(x=>x.arrayBuffer());
    const img = new Float32Array(buf);   // 直接进 canvas ImageData

（GUI 本身即按此协议实现，见 cmd/wos/web/app.js。）

## 3. 精度与性能调参清单

| 目标 | 操作 |
|---|---|
| 最高精度 | method=asm（默认）、bandlimit 适度（0.9/0.05）、网格 1024–2048、edge_sigma=0（理想硬边） |
| 抑制硬边混叠伪影 | bandlimit 开启；或孔径 edge_sigma≈1 像素 |
| 远场衍射 | 元件后单步 method=fraunhofer（注意输出像素尺寸变化，且其后不能直接合束） |
| 长距离/大光束 | Fresnel 二法按 z 与 N·dx²/λ 关系选择（越界自动告警） |
| 速度 | polarized=false 省一半；512 网格快速试算；auto-run 关闭后手动空格运行 |
| 内存 | 服务端 -max-run-mb 控制 LRU；内核直接调用时注意 1024² 矢量平面 ≈32 MB |

## 4. 常见问题

- **聚焦光斑只有几个像素？** 均匀网格分辨聚焦点的能力受 W/N 限制：暗环直径 ≈ 2.44λf/D，需要 2.44λf/(D·dx) 个像素；增大 N、缩小 W 或增大 f/D。这是网格法的固有约束，见 PHYSICS.md §2。
- **Fraunhofer 之后又传播了？** 输出像素已变为 λz/(N·dx)，后续元件坐标随之改变——物理正确，但注意合束限制。
- **干涉条纹对比度低？** 检查合束权重是否复现了第二个分束器矩阵；臂长差是否以“光束方向路程”计算（折返 = 2L）。
- **端口功率和 ≠ 源功率？** 检查反射臂是否含吸收元件（属物理吸收），以及 evanescent 告警（衰逝波损失）。
