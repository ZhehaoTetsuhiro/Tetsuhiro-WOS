# HTTP API 参考（docs/API.md）

基址：http://localhost:8080（-addr 可改）。全部响应 JSON（二进制/PNG 端点除外），CORS 全开。错误统一为 {"error":"..."}。

## 端点一览

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | /api/catalog | 元件/光源/算法/偏振/示例目录（GUI 由它驱动；含中文标签、单位、范围、默认值） |
| GET | /api/health | 存活检查 |
| POST | /api/validate | 校验配置，返回 {"ok":bool,"issues":[{path,message}]} |
| POST | /api/simulate | 提交配置，202 返回 {"run_id","status":"running"}；后台计算（串行队列） |
| GET | /api/runs/{id} | 状态 + 结果元数据；status = running / done / error |
| GET | /api/runs/{id}/planes/{pid} | 平面场数据：fmt=bin（float32）或 fmt=png |
| GET | /api/runs/{id}/profiles/{pid} | 一维剖面 JSON |

静态资源（/ 与 /app.js、/style.css）为内嵌的键盘操作 GUI。

## POST /api/simulate

请求体 = Config JSON（结构见 docs/INTEGRATION.md §1.1；完整字段与示例见 /api/catalog 的 examples 与 optics.Config 注释）。校验失败返回 400 并给出第一个错误（全部错误用 /api/validate 获取）。

限制：网格 64–2048（2 的幂）、元件 ≤256、输出平面 ≤64、分束臂嵌套 ≤8、请求体 ≤2 MB。

## GET /api/runs/{id}

    {
      "run_id": "bc7e8fdea164d00a",
      "status": "done",                       // running | done | error
      "elapsed_ms": 340.0,
      "grid": {"size": 1024, "width": 0.01, "dx": 9.765625e-6},
      "wavelength": 6.328e-7,
      "warnings": [{"code":"...","message":"...","count":1,"value":0}],
      "planes": [
        {"id":"sensor_0","label":"focus","path":"","size":1024,"dx":9.77e-6,
         "stats":{"power":0.000196,"peak":38409,"centroid_x":-3.4e-8,"centroid_y":-3.4e-8,
                  "rms_x":0.00023,"rms_y":0.00023,"strehl":0.998,
                  "intensity_min":9.6e-8,"intensity_max":38409,
                  "phase_min":-3.14,"phase_max":3.14}}
      ]
    }

- path 为臂路径（"" = 主光路，bs0 = 第 1 分束臂……）。
- stats 单位为 SI：power W、peak/intensity W/m²、质心/RMS m、strehl 无量纲（未启用时为 0）。
- warnings 常见码：fresnel_tf_alias、fresnel_ir_alias、fraunhofer_nearfield、evanescent_filtered、backward_evanescent（含义见 docs/PHYSICS.md）。

## GET /api/runs/{id}/planes/{pid}

查询参数：

| 参数 | 取值 | 默认 |
|---|---|---|
| field | total / ex / ey / phase_x / phase_y | total |
| fmt | bin / png | bin |
| scale | lin / log（仅强度视图） | 强度 log，相位 lin |
| cmap | inferno / phase / gray | 按视图 |
| pmin, pmax | 手动数据范围（物理单位） | 自动（stats 或 ±π） |

fmt=bin：float32 小端裸数组 N×N（行主序），无头，长度 4·N² 字节。
fmt=png：RGBA PNG（大小 = 网格尺寸）。

## GET /api/runs/{id}/profiles/{pid}

查询参数：axis = x | y（默认 x）；field 同上；coord = 固定坐标（m，缺省用强度质心）。

    {"axis":"x","coord":0,"x":[-0.005,...],"v":[9.6e-8,...]}

x 为位置数组（m），v 为对应场量（剖面为 3 像素厚切片的平均）。

## 运行缓存

完成的运行保留在内存 LRU 中（-max-run-mb，默认 512 MB 预算，按平面复数数据字节数计），被驱逐后返回 404。仿真串行执行：并发提交按顺序排队（状态保持 running）。
