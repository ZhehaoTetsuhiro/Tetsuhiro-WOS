# HTTP API 参考（docs/API.md）

基址：http://localhost:1120（-addr 可改）。全部响应 JSON（二进制/PNG 端点除外），CORS 全开。错误统一为 {"error":"..."}。

## 端点一览

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | /api/catalog | 元件/光源/算法/偏振/示例目录（GUI 由它驱动；含中文标签、单位、范围、默认值；脚本元件以 `custom:true` + `source` 标记，`classes` 给出各类型的绘图类别） |
| GET | /api/health | 存活检查 |
| GET | /api/elements | 已加载的脚本元件（元件定义文件）列表：名字/标签/行为/类别/参数/表达式/来源文件 + `version` |
| POST | /api/elements/reload | 重新扫描元件定义目录，返回 `{dirs, loaded, skipped, errors, version}` |
| GET | /api/elements/{name}/preview.png | 脚本元件自身的掩膜图：`?kind=amp|phase&size=&width=&wl=&mask=&<参数…>`（PNG） |
| POST | /api/validate | 校验配置，返回 {"ok":bool,"issues":[{path,message}]} |
| POST | /api/quantum | 量子光学模拟，同步返回 QuantumResult（微秒级，无需轮询） |
| POST | /api/simulate | 提交配置，202 返回 {"run_id","status":"running"}；后台计算（串行队列） |
| GET | /api/runs/{id} | 状态 + 结果元数据；status = running / done / error |
| GET | /api/runs/{id}/planes/{pid} | 平面场数据：fmt=bin（float32）或 fmt=png |
| GET | /api/runs/{id}/profiles/{pid} | 一维剖面 JSON |
| GET | /api/runs/{id}/inspect/{pid} | 局部读数：该像素的琼斯矢量、斯托克斯参数、偏振椭圆、相位（含波前 PV/RMS）与强度 |
| GET | /api/runs/{id}/scene | 路由出的光路几何（元件、光束段、包围盒）与各光源颜色，供立体视图使用 |
| POST | /api/convert | 把旧的 elements 元件序列配置转换为定位场景（scene + sources），不计算 |

静态资源（/ 与 /app.js、/style.css）为内嵌的键盘操作 GUI。

## POST /api/simulate

请求体 = Config JSON（结构见 docs/INTEGRATION.md §1.1；完整字段与示例见 /api/catalog 的 examples 与 optics.Config 注释）。校验失败返回 400 并给出第一个错误（全部错误用 /api/validate 获取）。

限制：网格 2–262144（偶数，支持非 2 的幂）、元件 ≤256、输出平面 ≤64、分束臂嵌套 ≤8、请求体 ≤2 MB。

## POST /api/quantum

请求体 = QuantumConfig（Fock 基线性光学；详见 docs/QUANTUM.md §6）：

    {
      "modes": 2, "cutoff": 4,
      "state": {"type": "fock", "params": {"occupation": [1,1]}},
      "gates": [{"type": "beam_splitter", "params": {"mode0":0, "mode1":1, "reflectivity":0.5}}]
    }

同步返回 QuantumResult：

    {
      "modes": 2, "cutoff": 4, "norm": 1.0,
      "mean_photons": [0.5, 0.5], "g2": [0, 0],
      "photon_distributions": [[0.5, 0.5, ...], [...]],
      "quadratures": [{"mode":0,"mean_x":0,"var_x":0.25,"mean_p":0,"var_p":0.25}, ...],
      "joint_distributions": {"0,1": [ ... ]}   // 拍平 (cutoff+1)^2，下标 a*(cutoff+1)+b
    }

state.type：vacuum / fock / coherent / squeezed_vacuum / two_mode_squeezed / thermal；gate.type：phase_shift / beam_splitter / displacement / squeeze / loss（参数见 /api/catalog 的 quantum 段）。热态（thermal）与损耗门（loss）产生混合态，自动走密度矩阵后端。

`POST /api/quantum?fmt=png` 返回同一结果的 PNG 图表（每模光子数分布柱状图 + 第一对模式联合分布热图）；`fmt=svg` 返回等价的矢量 SVG 图表。

限制：模式数 ≤4、截断 ≤20（越界返回 400）。

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
- warnings 常见码：fresnel_tf_alias、fresnel_ir_alias、fraunhofer_nearfield、asm_alias_wrap、evanescent_filtered、backward_evanescent、scene_cycle_dropped（含义见 docs/PHYSICS.md）。

## GET /api/runs/{id}/planes/{pid}

查询参数：

| 参数 | 取值 | 默认 |
|---|---|---|
| field | total / amplitude / ex / ey / ez / phase_x / phase_y / phase_z / phase_u / pol_azimuth / pol_ellip / pol_s1 / pol_s2 / pol_s3 / pol_degree / pol_intensity / color | total |
| fmt | bin / png | bin |
| part | 相干单元序号（光源分组后的编号），缺省为全部单元的合成 | -1 |
| scale | lin / log（强度类） | 强度 log，相位/偏振 lin |
| cmap | inferno / phase / gray / diverging | 按视图（相位与偏振方位角用 phase，斯托克斯/椭率用 diverging） |
| mask | 相位/偏振视图的强度掩膜阈值（相对峰值） | 相位 2e-3，偏振 1e-6 |
| exposure, gamma | 图像视图的曝光倍数与 γ（仅 field=color&fmt=png） | 1，1 |
| pmin, pmax | 手动数据范围（物理单位） | 自动（stats 或 ±π） |

`field=color` 只支持 `fmt=png`：按各光源波长把强度映射为真实颜色的 sRGB 图像（亮度为真实强度，曝光按 2 的幂缩放）。

部分视图需要复振幅，当平面含多个互不相干的光源单元时请用 `part=N` 指定其一（`GET /api/runs/{id}` 的 planes[].parts 列出各单元）。

fmt=bin：float32 小端裸数组 N×N（行主序），无头，长度 4·N² 字节。
fmt=png：RGBA PNG（大小 = 网格尺寸）。

## GET /api/runs/{id}/profiles/{pid}

查询参数：axis = x | y（默认 x）；field 同上（`color` 除外，图像是色调图，用 `total` 取曲线）；coord = 固定坐标（m，缺省用强度质心）；part = 相干单元序号。被掩膜的相位点返回 `null`（GUI 曲线会自动跳过）。

    {"axis":"x","coord":0,"x":[-0.005,...],"v":[9.6e-8,...]}

x 为位置数组（m），v 为对应场量（剖面为 3 像素厚切片的平均）。

## 运行缓存

完成的运行保留在内存 LRU 中（-max-run-mb，默认 512 MB 预算，按平面复数数据字节数计），被驱逐后返回 404。仿真串行执行：并发提交按顺序排队（状态保持 running）。

## GET /api/runs/{id}/inspect/{pid}

查询参数：`x`、`y` 为像素索引（行主序，0 起点），缺省用强度质心；`part` 选择相干单元。返回：

    {"part":0,"label":"…","wavelength":6.328e-7,
     "x":256,"y":219,"pos_x":0,"pos_y":-0.000645,
     "ex_re":3.79,"ex_im":1.56,"ey_re":0,"ey_im":0,
     "intensity":16.78,"phase":0.39,"phase_unwrapped":1.19,
     "stokes":{"s0":16.78,"s1":16.78,"s2":0,"s3":0,"azimuth":0,"ellipticity":0,
               "axis_ratio":0,"handedness":"线偏振","degree":1},
     "phase_stats":{"pv":3.46,"rms":0.71,"waves":0.55},
     "parts":1,"merged_units":false}

GUI 用它在鼠标位置实时显示偏振态与相位（含以 λ 为单位的波前 PV/RMS）。

## POST /api/convert

请求体为任意配置；若其中只有 `elements`（旧格式），返回 `{"scene":{...},"sources":[...]}`——沿 +z 按累计传播距离摆放元件、探测器正对来光。若配置已经是场景，则原样返回。GUI 打开旧预设文件时自动调用。

## GET /api/runs/{id}/scene

返回路由出的光路几何（`{scene:{components,segments,min,max},sources:[{index,pos,dir,wavelength,label,hex}]}`）。`hex` 是该波长的显示颜色，立体视图直接使用。
