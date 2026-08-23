#!/usr/bin/env python3
"""WOS 内核的 Python 客户端示例（仅标准库 + 可选 numpy）。

用法:
    python3 examples/python_client.py http://localhost:8080

流程: POST /api/simulate -> 轮询 /api/runs/{id} -> 拉取 float32 二进制平面
     -> 打印指标；有 numpy 时保存 .npy 并打印光斑尺寸。
"""
import json
import struct
import sys
import time
import urllib.request

BASE = sys.argv[1] if len(sys.argv) > 1 else "http://localhost:8080"

CONFIG = {
    "grid": {"size": 1024, "width": 0.01},
    "wavelength": 632.8e-9,
    "polarized": False,
    "method": "asm",
    "evanescent": "decay",
    "bandlimit": {"fraction": 0.9, "sigma": 0.05},
    "source": {"type": "plane", "params": {"power": 1e-3}},
    "elements": [
        {"type": "lens", "params": {"f": 0.5, "aperture": 0.0025}},
        {"type": "propagate", "params": {"distance": 0.5}},
        {"type": "sensor", "params": {"label": "focus",
                                      "strehl_aperture": 0.0025,
                                      "strehl_distance": 0.5}},
    ],
}


def call(method, path, body=None):
    req = urllib.request.Request(BASE + path, method=method)
    data = None
    if body is not None:
        data = json.dumps(body).encode()
        req.add_header("Content-Type", "application/json")
    with urllib.request.urlopen(req, data) as r:
        return r.status, r.read()


def main():
    status, raw = call("POST", "/api/simulate", CONFIG)
    assert status == 202, raw
    run_id = json.loads(raw)["run_id"]
    print(f"run_id = {run_id}")

    for _ in range(200):
        time.sleep(0.25)
        _, raw = call("GET", f"/api/runs/{run_id}")
        meta = json.loads(raw)
        if meta["status"] == "done":
            break
        if meta["status"] == "error":
            raise RuntimeError(meta.get("error"))
    else:
        raise RuntimeError("timeout")

    print(f"耗时 {meta['elapsed_ms']:.0f} ms, 输出平面 {len(meta['planes'])} 个")
    for p in meta["planes"]:
        s = p["stats"]
        print(f"  {p['id']:>10}  功率={s['power']:.4g} W  峰值={s['peak']:.4g} W/m²  Strehl={s['strehl']:.3f}")

    # 拉取第一个平面的强度（float32 little-endian 二进制）
    pid = meta["planes"][0]["id"]
    _, raw = call("GET", f"/api/runs/{run_id}/planes/{pid}?field=total&fmt=bin")
    vals = struct.unpack(f"<{len(raw)//4}f", raw)

    # 剖面
    _, raw = call("GET", f"/api/runs/{run_id}/profiles/{pid}?axis=x&field=total")
    prof = json.loads(raw)

    try:
        import numpy as np
        n = meta["grid"]["size"]
        img = np.asarray(vals, dtype=np.float32).reshape(n, n)
        np.save("wos_plane.npy", img)
        # 一阶暗环半径: 峰值右侧首个低于 2% 峰值的暗环，再做局部极小细化
        y, x = np.unravel_index(np.argmax(img), img.shape)
        row = img[y, x:]
        peak = row[0]
        first = next((i for i in range(3, 30) if row[i] < 0.02 * peak), None)
        if first is None:
            raise RuntimeError("未找到暗环")
        lo, hi = max(3, first - 3), first + 4
        i0 = lo + int(np.argmin(row[lo:hi]))
        dx = meta["grid"]["dx"]
        r_first_null = i0 * dx
        r_airy = 1.22 * CONFIG["wavelength"] * 0.5 / 0.005
        print(f"实测第一暗环 r={r_first_null*1e6:.1f} µm, 理论 1.22λf/D={r_airy*1e6:.1f} µm")
        print(f"剖面点数 {len(prof['x'])}, 已保存 wos_plane.npy ({n}×{n})")
    except ImportError:
        print("未安装 numpy：二进制数据已按 float32 解析（len=%d）" % len(vals))


if __name__ == "__main__":
    main()
