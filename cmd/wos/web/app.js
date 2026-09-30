"use strict";
/* Tetsuhiro WOS 波动光学模拟器 — 鼠标与键盘双可用前端。
 *
 * 光路模型：场景（scene）＝带位置/朝向/形状尺寸的元件集合 + 若干光源。
 * 光路不再存储“依次经过的元件序列”，而是由元件位置自动路由得出，
 * 因此可以：在立体视图中查看整套光路、任意摆放/移动元件、添加多个光源。
 *
 * 中心视图（1-9、0 切换）：
 *   1 颜色   真实光色与明暗（按波长着色，多光源按各自颜色叠加）
 *   2 强度   总强度（可对数/线性）
 *   3 相位   去包裹后的波前相位（暗区被掩膜）
 *   4 偏振   S1/S2/S3、方位角、椭率；叠加偏振椭圆图，鼠标读取局部态
 *   5-7      |Ex|²、|Ey|²、|Ez|²
 *   8-9      相位 Ex、相位 Ey（环绕）
 *   0        立体视图（整套光路的三维渲染，可旋转/缩放/平移）
 *
 * 图像由服务端按视图直接渲染为 PNG（颜色/相位/偏振/强度），剖面与数值读取
 * 通过 JSON 接口获取，因此同一像素在图像、曲线与读取框中始终一致。
 */

// ---------------- state ----------------
const S = {
  catalog: null,
  config: null,
  runId: null,
  meta: null,
  busy: false,
  dirty: false,       // busy 期间的变更：完成后自动补算
  planeIdx: 0,
  view: "color",
  scale: "lin",
  scaleTouched: false, // 用户是否用 l 显式选择过标度
  compSel: 0,          // 当前选中的元件
  srcSel: 0,           // 当前选中的光源
  channel: -1,         // -1 = 全部光源（合成），>=0 = 只看第 N 个相干单元
  exposure: 0,         // 颜色视图曝光（档，1 档 = 2 倍）
  showPol: true,       // 偏振视图是否叠加椭圆
  pol: null,           // 偏振采样（方位角/椭率/偏振度，float32 数组）
  autoRun: false,
  inspect: null,       // 最近一次点击读取结果（仅点击后才有）
  inspectXY: null,
  profileAxis: null,   // null | 'x' | 'y'
  hidePattern: false,
  cache: new Map(),
  timer: null,
  inspectTimer: null,
  insertSel: 0,
  mode: "wave",        // "wave" | "quantum"
  qconfig: null,
  qresult: null,
  qtimer: null,
  cam: { yaw: -0.72, pitch: 0.52, zoom: 1, panX: 0, panY: 0 },
  camInvert: true,     // 立体视图拖动方向：默认“反向”（画面跟着指针走）
  camKeys: true,       // 是否启用键盘旋转（见 key3d）
  drag3d: null,
};

const $ = (sel) => document.querySelector(sel);
const $$ = (sel) => Array.from(document.querySelectorAll(sel));

const clone = (o) => structuredClone(o);
const isPow2 = (n) => n > 0 && (n & (n - 1)) === 0;

const DEG = 180 / Math.PI;

function fmtNum(v, d) {
  d = d === undefined ? 4 : d;
  if (v === null || v === undefined || Number.isNaN(v)) return "\u2013";
  if (v === 0) return "0";
  const a = Math.abs(v);
  if (a >= 1e4 || a < 1e-3) return v.toExponential(2);
  return v.toFixed(d);
}

// ---------------- config accessors ----------------
function sceneObj() {
  if (!S.config) return { components: [] };
  if (!S.config.scene) S.config.scene = { components: [] };
  if (!Array.isArray(S.config.scene.components)) S.config.scene.components = [];
  return S.config.scene;
}
function comps() { return sceneObj().components; }
function sources() {
  if (!S.config) return [];
  if (!Array.isArray(S.config.sources)) S.config.sources = [];
  return S.config.sources;
}
function curComp() { return comps()[S.compSel] || null; }
function curSrc() { return sources()[S.srcSel] || null; }
function docFor(type) { return (S.catalog.elements || []).find((d) => d.type === type); }
function srcDocFor(type) { return (S.catalog.sources || []).find((d) => d.type === type); }
function shapeDoc(kind) { return (S.catalog.shapes || []).find((d) => d.kind === kind); }
function viewOf(name) { return (S.catalog.views || []).includes(name); }

// Component parameters that the scene geometry itself supplies.
const GEOM_KEYS = ["x", "y", "aperture"];

function circleShape(r) { return { kind: "circle", params: { radius: r } }; }

function defaultShapeFor(type) {
  if (type === "sensor") return circleShape(5e-3);
  return circleShape(6e-3);
}
// Sensor default orientation: face the light coming down +z.
function defaultYawFor(type) { return type === "sensor" ? Math.PI : 0; }

function newComponent(type) {
  const doc = docFor(type);
  const c = {
    id: type + "_" + (comps().length + 1),
    type: type,
    label: doc ? doc.label : type,
    pos: { x: 0, y: 0, z: 0 },
    yaw: defaultYawFor(type), pitch: 0, roll: 0,
    shape: defaultShapeFor(type),
    params: {},
  };
  if (doc) {
    doc.params.forEach((pd) => {
      if (pd.kind === "nested" || GEOM_KEYS.includes(pd.key)) return;
      if (pd.key === "shape") return; // 形状由 shape 字段给出
      if (pd.default !== undefined) {
        const ins = paramVisible(pd, { shape: c.shape.kind }) || !pd.show_if;
        if (ins) c.params[pd.key] = clone(pd.default);
      }
    });
  }
  if (type === "sensor") c.params = {};
  return c;
}

function newSource(type, idx) {
  const doc = srcDocFor(type);
  const s = {
    id: "src" + (sources().length + 1),
    label: (doc ? doc.label : "光源") + " " + (sources().length + 1),
    type: type,
    pos: { x: 0, y: 0, z: -0.05 },
    dir: { x: 0, y: 0, z: 1 },
    group: "",
    params: {},
  };
  if (doc) doc.params.forEach((pd) => {
    if (pd.kind === "nested" || pd.default === undefined) return;
    s.params[pd.key] = clone(pd.default);
  });
  return s;
}

// 新元件的位置：插在“当前元件”与“沿 z 的下一个元件”之间（光序由位置决定），
// 找不到下一个就放到当前元件之后 100 mm。
function nextPosAlongAxis() {
  const list = comps();
  if (!list.length) return { x: 0, y: 0, z: 0 };
  const cur = list[S.compSel] || list[list.length - 1];
  const cz = cur.pos.z || 0;
  let nz = Infinity;
  for (const c of list) {
    const z = c.pos.z || 0;
    if (z > cz + 1e-9 && z < nz) nz = z;
  }
  const z = Number.isFinite(nz) ? (cz + nz) / 2 : cz + 0.1;
  return { x: cur.pos.x || 0, y: cur.pos.y || 0, z: z };
}

// ---------------- rendering ----------------
function renderSceneList() {
  const box = $("#sceneList");
  box.innerHTML = "";
  const list = comps();
  if (!list.length) {
    box.innerHTML = "<p class=hint>场景为空：按 i 插入元件，或从“模板”载入一套光路。</p>";
    return;
  }
  list.forEach((c, i) => {
    const b = document.createElement("button");
    b.className = "sc" + (i === S.compSel ? " sel" : "");
    b.setAttribute("role", "listitem");
    const dot = document.createElement("span");
    dot.className = "dot";
    dot.style.background = classColor(classOf(c.type));
    b.appendChild(dot);
    const doc = docFor(c.type);
    b.appendChild(document.createTextNode((c.label || (doc ? doc.label : c.type) || c.type) + " "));
    const tag = document.createElement("span");
    tag.className = "tag";
    tag.textContent = compSummary(c);
    b.appendChild(tag);
    b.title = "位置 (" + fmtNum(c.pos.x * 1e3) + ", " + fmtNum(c.pos.y * 1e3) + ", " +
      fmtNum(c.pos.z * 1e3) + ") mm";
    b.addEventListener("click", () => { S.compSel = i; renderSceneList(); renderParams(); });
    box.appendChild(b);
  });
  if (S.compSel >= list.length) S.compSel = Math.max(0, list.length - 1);
  const sel = box.querySelector(".sel");
  if (sel) sel.scrollIntoView({ block: "nearest" });
}

function classOf(type) {
  const c = S.catalog && S.catalog.classes ? S.catalog.classes[type] : null;
  if (c) return c;
  if (type === "sensor") return "detector";
  if (type === "mirror" || type === "concave_mirror" || type === "convex_mirror") return "mirror";
  if (type === "beamsplitter") return "splitter";
  if (type === "aperture") return "stop";
  if (type === "lens" || type === "axicon" || type === "zone_plate") return "lens";
  return "other";
}

function classColor(cls) {
  return {
    mirror: "#a9bccf", splitter: "#7fc4ee", lens: "#b3dcff",
    detector: "#3d4c5c", stop: "#c8ccd0", other: "#cfe3f7",
  }[cls] || "#cfe3f7";
}

function compSummary(c) {
  const p = c.params || {};
  switch (c.type) {
    case "lens": return "f=" + fmtNum(p.f) + "m";
    case "mirror": return p.curvature ? "R=" + fmtNum(1 / p.curvature) + "m" : "平面";
    case "beamsplitter": return "R=" + fmtNum(p.reflectivity);
    case "aperture": return shapeDoc(c.shape ? c.shape.kind : "") ? shapeDoc(c.shape.kind).label : "孔径";
    case "sensor": return "z=" + fmtNum((c.pos.z || 0) * 1e3, 1) + "mm";
    case "grating": return (p.kind || "") + " Λ=" + fmtNum(p.period) + "m";
    case "polarizer": return "θ=" + fmtNum(p.angle) + "rad";
    case "retarder": return "δ=" + fmtNum(p.retardance) + "rad";
    case "medium": return "n=" + fmtNum(p.index);
    default: return "z=" + fmtNum((c.pos.z || 0) * 1e3, 1) + "mm";
  }
}

function renderSourceList() {
  const box = $("#sourceList");
  box.innerHTML = "";
  const list = sources();
  if (!list.length) {
    box.innerHTML = "<p class=hint>没有光源：按“+ 光源”添加。</p>";
    return;
  }
  list.forEach((s, i) => {
    const b = document.createElement("button");
    b.className = "sc" + (i === S.srcSel ? " sel" : "");
    b.setAttribute("role", "listitem");
    const dot = document.createElement("span");
    dot.className = "dot";
    dot.style.background = wavelengthHex(s.wavelength || S.config.wavelength);
    b.appendChild(dot);
    b.appendChild(document.createTextNode((s.label || ("光源 " + (i + 1))) + " "));
    const tag = document.createElement("span");
    tag.className = "tag";
    const wl = (s.wavelength || S.config.wavelength) * 1e9;
    tag.textContent = (srcDocFor(s.type) ? srcDocFor(s.type).label : s.type) + " · " + fmtNum(wl, 1) + "nm" +
      (s.group ? " · 组" + s.group : "");
    b.appendChild(tag);
    b.addEventListener("click", () => { S.srcSel = i; renderSourceList(); renderSource(); });
    box.appendChild(b);
  });
  if (S.srcSel >= list.length) S.srcSel = Math.max(0, list.length - 1);
}

// 波长 → 显示颜色（与服务端 CIE 拟合一致的近似，仅用于列表色点与立体视图）。
function wavelengthHex(wl) {
  if (!wl || wl <= 0) return "#8fb8d8";
  const nm = wl * 1e9;
  let r = 0, g = 0, b = 0;
  if (nm >= 380 && nm < 440) { r = -(nm - 440) / 60; b = 1; }
  else if (nm < 490) { g = (nm - 440) / 50; b = 1; }
  else if (nm < 510) { g = 1; b = -(nm - 510) / 20; }
  else if (nm < 580) { r = (nm - 510) / 70; g = 1; }
  else if (nm < 645) { r = 1; g = -(nm - 645) / 65; }
  else if (nm <= 780) { r = 1; }
  else { r = 1; }
  const m = Math.max(r, g, b) || 1;
  const c = (v) => Math.round(255 * Math.pow(Math.max(v, 0) / m, 0.8));
  return "rgb(" + c(r) + "," + c(g) + "," + c(b) + ")";
}

// parameter control builders -------------------------------------------------
function bindNumInput(inp, get, set, onLive, onCommit) {
  const parse = () => {
    const raw = String(inp.value).trim();
    if (raw === "" || !Number.isFinite(Number(raw))) return null;
    return Number(raw);
  };
  inp.addEventListener("input", () => {
    const x = parse();
    if (x !== null) { set(x); onLive(); }
  });
  inp.addEventListener("change", () => {
    const x = parse();
    if (x === null) { inp.value = get(); return; }
    set(x);
    inp.value = String(get());
    onCommit();
  });
}

// show_if 条件：逗号分隔的多个条件需同时满足；每个条件为 key=value（value 用 | 分隔多个可选值）。
function paramVisible(pd, params) {
  const cond = pd.show_if;
  if (!cond) return true;
  return String(cond).split(",").every((c) => {
    const eq = c.indexOf("=");
    if (eq < 0) return true;
    const key = c.slice(0, eq).trim();
    const vals = c.slice(eq + 1).trim().split("|");
    const cur = params[key];
    return vals.some((v) => String(cur) === v);
  });
}

function showIfKeys(cond) {
  if (!cond) return [];
  return String(cond).split(",").map((c) => {
    const eq = c.indexOf("=");
    return eq < 0 ? "" : c.slice(0, eq).trim();
  }).filter(Boolean);
}

function hasDependent(list, key) {
  return list.some((pd) => pd.show_if && showIfKeys(pd.show_if).includes(key));
}

function mkParamRow(doc, get, set, onChoice) {
  const row = document.createElement("div");
  row.className = "prow";
  const lab = document.createElement("label");
  lab.textContent = doc.label + (doc.unit ? " [" + doc.unit + "]" : "");
  lab.title = doc.help || "";
  row.appendChild(lab);
  const val = get();
  if (doc.kind === "choice") {
    const sel = document.createElement("select");
    (doc.choices || []).forEach((c) => {
      const o = document.createElement("option");
      o.value = c; o.textContent = c;
      if (String(val) === String(c)) o.selected = true;
      sel.appendChild(o);
    });
    sel.dataset.key = doc.key;
    sel.addEventListener("change", () => { set(sel.value); scheduleRun(); if (onChoice) onChoice(); });
    row.appendChild(sel);
  } else if (doc.kind === "bool") {
    const cb = document.createElement("input");
    cb.type = "checkbox";
    cb.checked = !!val;
    cb.addEventListener("change", () => { set(cb.checked); scheduleRun(); });
    row.appendChild(cb);
  } else if (doc.kind === "text") {
    const inp = document.createElement("input");
    inp.type = "text";
    inp.value = val === undefined || val === null ? "" : String(val);
    inp.addEventListener("change", () => { set(inp.value); scheduleRun(); });
    row.appendChild(inp);
  } else { // float / int：纯键盘数值输入框（直接输入 / ↑↓ 步进 / Enter 确认，无滑杆）
    const num = document.createElement("input");
    num.type = "number";
    num.min = doc.min !== undefined ? doc.min : 0;
    num.max = doc.max !== undefined ? doc.max : 1;
    num.step = doc.step || 0.01;
    num.value = Number(val) || 0;
    num.title = "直接输入数值（支持科学计数法，如 1e-3）；↑/↓ 步进；Enter 确认";
    bindNumInput(num, () => get(), (x) => { set(x); }, () => scheduleRun(), () => scheduleRun());
    row.appendChild(num);
  }
  return row;
}

// A numeric row in millimetres / degrees (used for positions and angles).
function mkScaledRow(label, unit, factor, get, set, step, help) {
  const row = document.createElement("div");
  row.className = "prow";
  const lab = document.createElement("label");
  lab.textContent = label + " [" + unit + "]";
  lab.title = help || "";
  row.appendChild(lab);
  const num = document.createElement("input");
  num.type = "number";
  num.step = step;
  num.value = Number((get() * factor).toFixed(6));
  num.title = "直接输入数值；↑/↓ 步进；Enter 确认";
  bindNumInput(num, () => get() * factor, (x) => { set(x / factor); }, () => scheduleRun(), () => scheduleRun());
  row.appendChild(num);
  return row;
}

function renderParams() {
  const panel = $("#paramPanel");
  panel.innerHTML = "";
  const c = curComp();
  if (!c) { panel.innerHTML = "<p class=hint>选择或插入一个元件。</p>"; return; }
  const doc = docFor(c.type);
  const h = document.createElement("div");
  h.innerHTML = "<b>" + (c.label || (doc ? doc.label : c.type)) + "</b> <span class=hint>" +
    (doc ? (doc.help || "") : "未知元件类型") + "</span>";
  panel.appendChild(h);

  const sub1 = document.createElement("div");
  sub1.className = "subhead";
  sub1.textContent = "位置与朝向（场景坐标：z 为光轴，x 横向，y 竖直）";
  panel.appendChild(sub1);
  c.pos = c.pos || { x: 0, y: 0, z: 0 };
  panel.appendChild(mkScaledRow("位置 x", "mm", 1e3, () => c.pos.x || 0, (v) => { c.pos.x = v; }, 1, "元件中心在光学平台上的横向位置"));
  panel.appendChild(mkScaledRow("位置 y", "mm", 1e3, () => c.pos.y || 0, (v) => { c.pos.y = v; }, 1, "元件中心的高度（0 为光轴高度）"));
  panel.appendChild(mkScaledRow("位置 z", "mm", 1e3, () => c.pos.z || 0, (v) => { c.pos.z = v; }, 5, "沿光轴的位置：光路顺序由位置决定"));
  panel.appendChild(mkScaledRow("偏航 yaw", "°", DEG, () => c.yaw || 0, (v) => { c.yaw = v; }, 5, "绕 y 轴旋转（45° 折转镜 = 45°，探测器朝向来光）"));
  panel.appendChild(mkScaledRow("俯仰 pitch", "°", DEG, () => c.pitch || 0, (v) => { c.pitch = v; }, 5, "绕 x 轴旋转"));
  panel.appendChild(mkScaledRow("自转 roll", "°", DEG, () => c.roll || 0, (v) => { c.roll = v; }, 5, "绕自身法线旋转（决定孔径/偏振片方向）"));

  const sub2 = document.createElement("div");
  sub2.className = "subhead";
  sub2.textContent = "形状与尺寸（通光孔径/元件轮廓）";
  panel.appendChild(sub2);
  const srow = document.createElement("div");
  srow.className = "prow";
  srow.appendChild(Object.assign(document.createElement("label"), { textContent: "形状" }));
  const ssel = document.createElement("select");
  const noneOpt = document.createElement("option");
  noneOpt.value = "";
  noneOpt.textContent = "无（全尺寸）";
  ssel.appendChild(noneOpt);
  (S.catalog.shapes || []).forEach((sd) => {
    const o = document.createElement("option");
    o.value = sd.kind;
    o.textContent = sd.label;
    if (c.shape && c.shape.kind === sd.kind) o.selected = true;
    ssel.appendChild(o);
  });
  ssel.addEventListener("change", () => {
    if (!ssel.value) { c.shape = null; }
    else {
      const sd = shapeDoc(ssel.value);
      c.shape = { kind: ssel.value, params: {} };
      (sd ? sd.params : []).forEach((pd) => { if (pd.default !== undefined) c.shape.params[pd.key] = clone(pd.default); });
    }
    renderParams(); scheduleRun();
  });
  srow.appendChild(ssel);
  panel.appendChild(srow);
  if (c.shape) {
    const sd = shapeDoc(c.shape.kind);
    if (sd) {
      c.shape.params = c.shape.params || {};
      sd.params.forEach((pd) => {
        panel.appendChild(mkParamRow(pd, () => c.shape.params[pd.key], (v) => { c.shape.params[pd.key] = v; }));
      });
    }
  }

  if (doc) {
    const own = doc.params.filter((pd) => pd.key !== "shape" && !GEOM_KEYS.includes(pd.key));
    if (own.length) {
      const sub3 = document.createElement("div");
      sub3.className = "subhead";
      sub3.textContent = "元件参数";
      panel.appendChild(sub3);
      c.params = c.params || {};
      own.forEach((pd) => {
        if (pd.kind !== "nested" && !paramVisible(pd, Object.assign({ shape: c.shape && c.shape.kind }, c.params))) return;
        panel.appendChild(mkParamRow(pd, () => c.params[pd.key], (v) => { c.params[pd.key] = v; },
          () => { if (!hasDependent(own, pd.key)) return; renderParams(); }));
      });
    }
  }
}

function renderGlobals() {
  const panel = $("#globalPanel");
  panel.innerHTML = "";
  const g = S.config;
  const sizes = [128, 256, 512, 1024, 2048];
  const isCustom = !sizes.includes(g.grid.size);
  const row1 = document.createElement("div");
  row1.className = "prow";
  row1.appendChild(Object.assign(document.createElement("label"), { textContent: "网格大小" }));
  const ssel = document.createElement("select");
  sizes.forEach((n) => {
    const o = document.createElement("option");
    o.value = n; o.textContent = n + "×" + n;
    if (g.grid.size === n) o.selected = true;
    ssel.appendChild(o);
  });
  const custOpt = document.createElement("option");
  custOpt.value = "custom"; custOpt.textContent = "自定义";
  if (isCustom) custOpt.selected = true;
  ssel.appendChild(custOpt);
  row1.appendChild(ssel);
  panel.appendChild(row1);

  const row2 = document.createElement("div");
  row2.className = "prow";
  row2.appendChild(Object.assign(document.createElement("label"), { textContent: "边长 a [px]" }));
  const cinp = document.createElement("input");
  cinp.type = "number";
  cinp.min = 2; cinp.max = 65536 * 4; cinp.step = 2;
  cinp.value = g.grid.size;
  cinp.title = "自定义网格边长 a（网格为 a×a 像素，偶数，允许小于 64，不超过 65536×4）";
  bindNumInput(cinp, () => g.grid.size, (v) => { g.grid.size = clampGridSize(v); }, () => scheduleRun(), () => scheduleRun());
  row2.appendChild(cinp);
  row2.hidden = !isCustom;
  panel.appendChild(row2);

  ssel.addEventListener("change", () => {
    if (ssel.value === "custom") {
      row2.hidden = false;
      cinp.focus(); cinp.select();
    } else {
      row2.hidden = true;
      g.grid.size = Number(ssel.value);
      scheduleRun();
    }
  });

  const mk = (label, unit, get, set, opts) => {
    const row = document.createElement("div");
    row.className = "prow";
    row.appendChild(Object.assign(document.createElement("label"), { textContent: label + (unit ? " [" + unit + "]" : "") }));
    const num = document.createElement("input");
    num.type = "number";
    Object.assign(num, opts || {});
    num.value = get();
    num.title = "直接输入数值（支持科学计数法，如 632.8）；↑/↓ 步进；Enter 确认";
    bindNumInput(num, get, set, () => scheduleRun(), () => scheduleRun());
    row.appendChild(num);
    return row;
  };
  panel.appendChild(mk("网格宽度", "m", () => g.grid.width, (v) => { g.grid.width = v; }, { step: 0.001, min: 1e-5 }));
  panel.appendChild(mk("默认波长", "nm", () => g.wavelength * 1e9, (v) => { g.wavelength = v * 1e-9; }, { step: 0.1, min: 1 }));
  panel.appendChild(mk("默认光源功率", "W", () => defaultPower(), (v) => { setDefaultPower(v); }, { step: 1e-4, min: 1e-12 }));

  const brow = document.createElement("div");
  brow.className = "prow";
  brow.appendChild(Object.assign(document.createElement("label"), { textContent: "琼斯偏振" }));
  const cb = document.createElement("input");
  cb.type = "checkbox";
  cb.checked = g.polarized;
  cb.addEventListener("change", () => { g.polarized = cb.checked; renderSource(); scheduleRun(); });
  brow.appendChild(cb);
  panel.appendChild(brow);

  const mrow = document.createElement("div");
  mrow.className = "prow";
  mrow.appendChild(Object.assign(document.createElement("label"), { textContent: "传播算法" }));
  const msel = document.createElement("select");
  S.catalog.methods.forEach((m) => {
    const o = document.createElement("option");
    o.value = m.key; o.textContent = m.label; o.title = m.help;
    if (g.method === m.key) o.selected = true;
    msel.appendChild(o);
  });
  msel.addEventListener("change", () => { g.method = msel.value; scheduleRun(); });
  mrow.appendChild(msel);
  panel.appendChild(mrow);

  const erow = document.createElement("div");
  erow.className = "prow";
  erow.appendChild(Object.assign(document.createElement("label"), { textContent: "衰逝波处理" }));
  const esel = document.createElement("select");
  [["decay", "物理衰减"], ["zero", "直接置零"]].forEach(([k, l]) => {
    const o = document.createElement("option");
    o.value = k; o.textContent = l;
    if ((g.evanescent || "decay") === k) o.selected = true;
    esel.appendChild(o);
  });
  esel.addEventListener("change", () => { g.evanescent = esel.value; scheduleRun(); });
  erow.appendChild(esel);
  panel.appendChild(erow);

  const brow2 = document.createElement("div");
  brow2.className = "prow";
  brow2.appendChild(Object.assign(document.createElement("label"), { textContent: "奈奎斯特带限", title: "抑制硬边混叠的数值正则化" }));
  const bcb = document.createElement("input");
  bcb.type = "checkbox";
  bcb.checked = !!g.bandlimit;
  bcb.addEventListener("change", () => { g.bandlimit = bcb.checked ? { fraction: 0.9, sigma: 0.05 } : null; scheduleRun(); });
  brow2.appendChild(bcb);
  panel.appendChild(brow2);

  panel.appendChild(mk("衰逝波截断阈值", "nepers", () => g.evanescent_limit || 0, (v) => { g.evanescent_limit = v; }, { step: 0.1, min: 0 }));

  const breg = document.createElement("div");
  breg.className = "prow";
  breg.appendChild(Object.assign(document.createElement("label"), { textContent: "反向 Tikhonov 正则化", title: "负 z 传播时用阻尼逆 A/(1+(αA)²) 替代置零" }));
  const bcb2 = document.createElement("input");
  bcb2.type = "checkbox";
  bcb2.checked = !!g.backward_regularize;
  bcb2.addEventListener("change", () => { g.backward_regularize = bcb2.checked; scheduleRun(); });
  breg.appendChild(bcb2);
  panel.appendChild(breg);

  panel.appendChild(mk("Tikhonov α", "", () => g.tikhonov_alpha || 0, (v) => { g.tikhonov_alpha = v; }, { step: 0.001, min: 0 }));
}

function defaultPower() {
  const s = sources()[0];
  return (s && s.params && s.params.power) || 1e-3;
}
function setDefaultPower(v) {
  sources().forEach((s) => { s.params = s.params || {}; if (s.params.power === undefined) s.params.power = v; });
}

// 光源参数：类型 / 位置 / 发射方向 / 波长 / 相干组 / 功率与偏振等。
function renderSource() {
  const panel = $("#sourcePanel");
  panel.innerHTML = "";
  const s = curSrc();
  if (!s) { panel.innerHTML = "<p class=hint>没有光源：按“+ 光源”添加一个。</p>"; return; }
  const row = document.createElement("div");
  row.className = "prow";
  row.appendChild(Object.assign(document.createElement("label"), { textContent: "光源类型" }));
  const sel = document.createElement("select");
  S.catalog.sources.forEach((d) => {
    const o = document.createElement("option");
    o.value = d.type; o.textContent = d.label;
    if (s.type === d.type) o.selected = true;
    sel.appendChild(o);
  });
  sel.addEventListener("change", () => {
    s.type = sel.value;
    s.params = {};
    const doc = srcDocFor(s.type);
    if (doc) doc.params.forEach((pd) => { if (pd.default !== undefined) s.params[pd.key] = clone(pd.default); });
    renderSourceList(); renderSource(); scheduleRun();
    const again = panel.querySelector("select");
    if (again) again.focus();
  });
  row.appendChild(sel);
  panel.appendChild(row);

  const sub1 = document.createElement("div");
  sub1.className = "subhead";
  sub1.textContent = "光源位置与发射方向";
  panel.appendChild(sub1);
  s.pos = s.pos || { x: 0, y: 0, z: -0.05 };
  s.dir = s.dir || { x: 0, y: 0, z: 1 };
  panel.appendChild(mkScaledRow("位置 x", "mm", 1e3, () => s.pos.x || 0, (v) => { s.pos.x = v; }, 5, "光源在光学平台上的位置"));
  panel.appendChild(mkScaledRow("位置 y", "mm", 1e3, () => s.pos.y || 0, (v) => { s.pos.y = v; }, 5, ""));
  panel.appendChild(mkScaledRow("位置 z", "mm", 1e3, () => s.pos.z || 0, (v) => { s.pos.z = v; }, 10, "一般位于第一个元件之前"));
  panel.appendChild(mkScaledRow("方向 x", "", 1, () => s.dir.x || 0, (v) => { s.dir.x = v; }, 0.1, "发射方向向量（默认为 +z）"));
  panel.appendChild(mkScaledRow("方向 y", "", 1, () => s.dir.y || 0, (v) => { s.dir.y = v; }, 0.1, ""));
  panel.appendChild(mkScaledRow("方向 z", "", 1, () => s.dir.z || 0, (v) => { s.dir.z = v; }, 0.1, ""));

  const sub2 = document.createElement("div");
  sub2.className = "subhead";
  sub2.textContent = "波长与相干性";
  panel.appendChild(sub2);
  panel.appendChild(mkScaledRow("波长", "nm", 1e9, () => s.wavelength || S.config.wavelength, (v) => {
    s.wavelength = v > 0 ? v : 0;
  }, 0.1, "留空/0 表示使用全局默认波长；不同波长的光源互不相干"));

  const grow = document.createElement("div");
  grow.className = "prow";
  grow.appendChild(Object.assign(document.createElement("label"), { textContent: "相干组" }));
  const ginp = document.createElement("input");
  ginp.type = "text";
  ginp.value = s.group === "solo" ? "" : (s.group || "");
  ginp.placeholder = "留空 = 独立光源（强度叠加）";
  ginp.title = "同组同波长的光源按相干叠加（可产生干涉条纹），不同组/不同波长只做强度叠加";
  ginp.addEventListener("change", () => { s.group = ginp.value.trim(); scheduleRun(); });
  grow.appendChild(ginp);
  panel.appendChild(grow);

  const doc = srcDocFor(s.type);
  if (doc) {
    const sub3 = document.createElement("div");
    sub3.className = "subhead";
    sub3.textContent = "光源参数";
    panel.appendChild(sub3);
    const params = s.params || (s.params = {});
    doc.params.forEach((pd) => {
      if (!paramVisible(pd, params)) return;
      panel.appendChild(mkParamRow(pd, () => params[pd.key], (v) => { params[pd.key] = v; }));
    });
  }
  if (S.config.polarized) {
    const prow = document.createElement("div");
    prow.className = "prow";
    prow.appendChild(Object.assign(document.createElement("label"), { textContent: "偏振态" }));
    const psel = document.createElement("select");
    psel.id = "polSel";
    S.catalog.polarizations.forEach((p) => {
      const o = document.createElement("option");
      o.value = p.key; o.textContent = p.label;
      if ((s.params.polarization || "x") === p.key) o.selected = true;
      psel.appendChild(o);
    });
    psel.addEventListener("change", () => {
      s.params.polarization = psel.value;
      if (psel.value === "custom") { s.params.jx_re = 1; s.params.jx_im = 0; s.params.jy_re = 0; s.params.jy_im = 0; }
      renderSource(); scheduleRun();
      const again = $("#polSel");
      if (again) again.focus();
    });
    prow.appendChild(psel);
    panel.appendChild(prow);
    if (s.params.polarization === "custom") {
      [["jx_re", "Jx 实部"], ["jx_im", "Jx 虚部"], ["jy_re", "Jy 实部"], ["jy_im", "Jy 虚部"]].forEach(([k, l]) => {
        const r2 = document.createElement("div");
        r2.className = "prow";
        r2.appendChild(Object.assign(document.createElement("label"), { textContent: l }));
        const inp = document.createElement("input");
        inp.type = "number"; inp.step = 0.1;
        inp.value = s.params[k] !== undefined ? s.params[k] : (k.endsWith("re") ? 1 : 0);
        bindNumInput(inp, () => s.params[k], (v) => { s.params[k] = v; }, () => scheduleRun(), () => scheduleRun());
        r2.appendChild(inp);
        panel.appendChild(r2);
      });
    }
  }
}

function clampGridSize(v) {
  let n = Math.round(v);
  if (n % 2 !== 0) n += 1; // 内核校验要求偶数
  return Math.max(2, Math.min(65536 * 4, n));
}

// ---------------- output planes / stats ----------------
function curPlane() {
  if (!S.meta || !S.meta.planes || !S.meta.planes.length) return null;
  return S.meta.planes[Math.min(S.planeIdx, S.meta.planes.length - 1)];
}

function renderPlanes() {
  const lab = $("#planeLabel");
  if (!S.meta || !S.meta.planes.length) { if (lab) lab.textContent = "无输出平面"; return; }
  const p = S.meta.planes[S.planeIdx];
  lab.textContent = (p.path ? "[" + p.path + "] " : "") + (p.label || p.id) + " (" + (S.planeIdx + 1) + "/" + S.meta.planes.length + ")";
  renderChannelBar();
}

// 通道选择：全部合成，或某个相干单元（多光源时才能分开查看相位/偏振）。
function renderChannelBar() {
  const sel = $("#channelSel");
  const p = curPlane();
  const prev = S.channel;
  sel.innerHTML = "";
  const all = document.createElement("option");
  all.value = "-1";
  all.textContent = "全部光源（合成强度）";
  sel.appendChild(all);
  (p && p.parts ? p.parts : []).forEach((pt, i) => {
    const o = document.createElement("option");
    o.value = String(i);
    o.textContent = (pt.label || ("通道 " + (i + 1))) + " · " + fmtNum((pt.wavelength || 0) * 1e9, 1) + "nm";
    sel.appendChild(o);
  });
  const nParts = p && p.parts ? p.parts.length : 0;
  if (prev >= 0 && prev < nParts) sel.value = String(prev);
  else { S.channel = -1; sel.value = "-1"; }
  sel.disabled = nParts <= 1;
  const note = $("#viewNote");
  if (!note) return;
  const bits = [];
  if (p && p.merged_units) bits.push("该平面含多个互不相干的光源单元：相位/偏振请选具体通道");
  if (S.view === "color") bits.push("按各光源波长真实着色，亮度为真实明暗（可调曝光）");
  if (S.view === "phase_u") bits.push("相位已去包裹，暗区（强度<峰值 0.2%）掩膜");
  if (S.view === "pol") bits.push("颜色=方位角，可叠加椭圆；点击图像读取局部偏振态");
  note.textContent = bits.join(" · ");
}

function renderWarnings() {
  const box = $("#warnings");
  box.innerHTML = "";
  if (!S.meta) return;
  (S.meta.warnings || []).forEach((w) => {
    const d = document.createElement("div");
    d.className = "w";
    d.textContent = "⚠ " + w.message + (w.count > 1 ? "（×" + w.count + "）" : "");
    box.appendChild(d);
  });
  if (!(S.meta.warnings || []).length) box.innerHTML = "<p class=hint>无警告</p>";
}

function renderStats() {
  const box = $("#stats");
  if (!S.meta || !S.meta.planes.length) { box.innerHTML = ""; return; }
  const p = S.meta.planes[S.planeIdx];
  const st = p.stats;
  const parts = (p.parts || []).map((pt) =>
    "  · " + (pt.label || "单元") + " " + fmtNum((pt.wavelength || 0) * 1e9, 1) + "nm: " +
    fmtNum(pt.power) + " W" + (pt.peak ? "，峰值 " + fmtNum(pt.peak) + " W/m²" : "")).join("\n");
  box.innerHTML =
    "<b>" + (p.label || p.id) + "</b>" + (p.path ? " <span class=hint>[" + p.path + "]</span>" : "") + "\n" +
    "功率 " + fmtNum(st.power) + " W   峰值 " + fmtNum(st.peak) + " W/m²\n" +
    "质心 (" + fmtNum(st.centroid_x) + ", " + fmtNum(st.centroid_y) + ") m\n" +
    "RMS 半径 (" + fmtNum(st.rms_x) + ", " + fmtNum(st.rms_y) + ") m\n" +
    (st.strehl > 0 ? "斯特列尔比 " + st.strehl.toFixed(3) + "\n" : "") +
    "强度范围 [" + fmtNum(st.intensity_min) + ", " + fmtNum(st.intensity_max) + "] W/m²\n" +
    "相位范围 [" + st.phase_min.toFixed(2) + ", " + st.phase_max.toFixed(2) + "] rad（环绕）" +
    (parts ? "\n各光源通道：\n" + parts : "");
}

// ---------------- views ----------------
const VIEW_FIELD = {
  color: "color",
  total: "total",   // 仅用于剖面/曲线（颜色视图的曲线即总强度）；不再有独立“强度”标签页
  phase_u: "phase_u",
  pol: "pol_azimuth",
  ex: "ex", ey: "ey", ez: "ez",
  phase_x: "phase_x", phase_y: "phase_y",
};
const VIEW_LABEL = {
  color: "颜色", total: "总强度", phase_u: "相位（去包裹）", pol: "偏振",
  ex: "|Ex|²", ey: "|Ey|²", ez: "|Ez|²", phase_x: "相位 Ex", phase_y: "相位 Ey",
  scene3d: "立体视图",
};

function viewField(view) { return VIEW_FIELD[view] || "total"; }
// 剖面对应的数值通道：颜色/偏振是色调图（无数值），曲线改用总强度，保证“图 + 曲线”并存。
function profileField(view) {
  if (view === "color" || view === "pol") return "total";
  return viewField(view);
}
function effScale(view) {
  if (S.scaleTouched) return S.scale;
  if (view === "color" || view === "pol" || String(view).startsWith("phase")) return "lin";
  return "log";
}
function viewIsIntensity() {
  return ["color", "total", "ex", "ey", "ez", "amplitude"].includes(S.view);
}
function viewIsPhase() { return String(S.view).startsWith("phase"); }

function planeURL(pid, view) {
  const q = new URLSearchParams();
  if (view === "color") {
    q.set("field", "color");
    q.set("fmt", "png");
    q.set("scale", effScale(view));
    q.set("exposure", String(Math.pow(2, S.exposure)));
    q.set("gamma", "0.4545");
    if (S.channel >= 0) q.set("part", String(S.channel));
    return "/api/runs/" + S.runId + "/planes/" + pid + "?" + q.toString();
  }
  q.set("field", viewField(view));
  q.set("fmt", "png");
  q.set("scale", effScale(view));
  if (S.channel >= 0) q.set("part", String(S.channel));
  if (view === "pol") q.set("cmap", "phase");
  return "/api/runs/" + S.runId + "/planes/" + pid + "?" + q.toString();
}

let viewToken = 0;

let lastImg = null;

function renderView() {
  const cv = $("#view");
  const p = curPlane();
  if (!p) { cv.getContext("2d").clearRect(0, 0, cv.width, cv.height); return; }
  if (S.view === "scene3d") { return; }
  const n = p.size;
  const rid = S.runId;
  const token = ++viewToken;
  const img = new Image();
  img.onload = () => {
    if (token !== viewToken || S.runId !== rid) return; // 过期响应丢弃
    lastImg = img;
    paintView();
  };
  img.onerror = () => setStatus("视图加载失败（" + VIEW_LABEL[S.view] + "）", true);
  img.src = planeURL(p.id, S.view);
}

// 把最近一次载入的图像画到画布上，并按视图叠加辅助图形。
function paintView() {
  const cv = $("#view"), p = curPlane();
  if (!p || !lastImg) return;
  const n = p.size;
  cv.width = n; cv.height = n;
  const ctx = cv.getContext("2d");
  ctx.clearRect(0, 0, n, n);
  ctx.drawImage(lastImg, 0, 0);
  if (S.view === "pol" && S.showPol) drawPolEllipses(ctx, n);
  if (S.inspectXY) drawPickMarker(ctx, S.inspectXY[0], S.inspectXY[1], n);
  drawProfile();
}

// 偏振椭圆叠加：一次读入方位角/椭率/偏振度（float32 二进制），按网格画椭圆。
async function loadPolSamples() {
  const p = curPlane();
  if (!p || S.view !== "pol") { S.pol = null; return; }
  const rid = S.runId, n = p.size;
  try {
    const [az, el, deg, tot] = await Promise.all([
      fetchBin(p.id, "pol_azimuth", n), fetchBin(p.id, "pol_ellip", n),
      fetchBin(p.id, "pol_degree", n), fetchBin(p.id, "total", n),
    ]);
    if (S.runId !== rid) return;
    S.pol = { az: az, el: el, deg: deg, tot: tot, n: n };
    redrawView();
  } catch (e) { S.pol = null; }
}

async function fetchBin(pid, field, n) {
  const q = new URLSearchParams({ field: field, fmt: "bin" });
  if (S.channel >= 0) q.set("part", String(S.channel));
  const r = await fetch("/api/runs/" + S.runId + "/planes/" + pid + "?" + q.toString());
  if (!r.ok) throw new Error("bin " + field);
  const buf = await r.arrayBuffer();
  return new Float32Array(buf);
}

function maxOf(a) {
  let m = 0;
  for (let i = 0; i < a.length; i++) if (a[i] > m) m = a[i];
  return m;
}

function redrawView() { paintView(); }

// 画一个偏振椭圆：长轴方向 = 方位角 ψ，短轴比例 = tan(椭率角 χ)，附带旋向箭头。
// 在中心图像上标出最近一次点击读取的位置（细十字，带白描边以保证在亮暗背景上都可见）。
function drawPickMarker(ctx, i, j, n) {
  const arm = Math.max(5, Math.round(n / 60));
  ctx.save();
  ctx.lineWidth = 3;
  ctx.strokeStyle = "rgba(255,255,255,.85)";
  for (const pass of [0, 1]) {
    if (pass === 1) { ctx.lineWidth = 1.4; ctx.strokeStyle = "rgba(20,60,110,.95)"; }
    ctx.beginPath();
    ctx.moveTo(i - arm, j); ctx.lineTo(i + arm, j);
    ctx.moveTo(i, j - arm); ctx.lineTo(i, j + arm);
    ctx.stroke();
  }
  ctx.restore();
}

function drawEllipse(ctx, cx, cy, st, r, color) {
  const az = st.azimuth || 0;
  const chi = st.ellipticity || 0;
  const a = r;
  const b = Math.min(r, r * Math.abs(Math.tan(chi)));
  ctx.save();
  ctx.translate(cx, cy);
  ctx.rotate(az);
  ctx.beginPath();
  ctx.ellipse(0, 0, Math.max(a, 0.6), Math.max(b, 0.6), 0, 0, Math.PI * 2);
  ctx.strokeStyle = color;
  ctx.stroke();
  if (chi !== 0) {
    // 旋向小箭头：χ>0 左旋、χ<0 右旋
    ctx.beginPath();
    const sgn = chi > 0 ? 1 : -1;
    ctx.arc(0, 0, Math.max(a, 0.6) * 0.8, 0, sgn * Math.PI * 0.7, sgn < 0);
    ctx.stroke();
  }
  ctx.restore();
}

// 在已绘制的图像上叠加偏振椭圆（每个采样点一个椭圆，长轴=方位角，短轴=椭率）。
function drawPolEllipses(ctx, n) {
  const D = S.pol;
  if (!D || D.n !== n) return;
  const step = Math.max(12, Math.round(n / 14));
  const maxI = maxOf(D.tot);
  ctx.save();
  ctx.lineWidth = 1.4;
  for (let j = step; j < n - step; j += step) {
    for (let i = step; i < n - step; i += step) {
      const k = j * n + i;
      // 只在“有光且偏振明确”的采样点画椭圆：无光处偏振角无意义（数值噪声）。
      if (!(D.deg[k] > 0.5)) continue;
      if (maxI > 0 && D.tot[k] < 0.1 * maxI) continue;
      drawEllipse(ctx, i, j, { azimuth: D.az[k], ellipticity: D.el[k] }, 0.5 * step, "rgba(10,25,40,.85)");
    }
  }
  ctx.restore();
}

function inspectURL(i, j) {
  const p = curPlane();
  const q = [];
  if (S.channel >= 0) q.push("part=" + S.channel);
  if (i !== null && i !== undefined) q.push("x=" + i, "y=" + j);
  return "/api/runs/" + S.runId + "/inspect/" + p.id + (q.length ? "?" + q.join("&") : "");
}

async function inspectAt(i, j) {
  const p = curPlane();
  if (!p || !S.runId) return;
  if (S.view === "scene3d") return;
  const rid = S.runId;
  try {
    const r = await fetch(inspectURL(i, j));
    if (!r.ok) return;
    const js = await r.json();
    if (S.runId !== rid) return;
    S.inspect = js;
    S.inspectXY = [i, j];
    renderInspect();
  } catch (e) { /* 忽略 */ }
}

// 清空读数卡片（切换平面/视图/重新运行时调用：展开状态不跨上下文保留）。
function clearInspect() {
  clearTimeout(S.inspectTimer);
  S.inspect = null;
  S.inspectXY = null;
  renderInspect();
}

// 偏振与相位的读取卡片：琼斯矢量、Stokes、椭圆、相位（含波前 PV/RMS）。
function renderInspect() {
  const box = $("#inspectBox");
  if (!box) return;
  const js = S.inspect;
  if (!js) {
    box.innerHTML = "<p class=hint>点击中心图像上的任意位置，即可在此展开该点的偏振态、相位与琼斯矢量读数。</p>";
    return;
  }
  const st = js.stokes || {};
  const cards = [];

  const cv = document.createElement("canvas");
  cv.id = "ellipseCv";
  cv.width = 232; cv.height = 232;
  const cctx = cv.getContext("2d");
  cctx.clearRect(0, 0, 232, 232);
  cctx.save();
  cctx.translate(116, 116);
  cctx.strokeStyle = "rgba(20,40,60,.25)";
  cctx.beginPath(); cctx.moveTo(-100, 0); cctx.lineTo(100, 0);
  cctx.moveTo(0, -100); cctx.lineTo(0, 100); cctx.stroke();
  cctx.restore();
  drawEllipse(cctx, 116, 116, st, 92, "#1460c8");
  box.innerHTML = "";
  box.appendChild(cv);

  const wrap = document.createElement("div");
  wrap.className = "cards";

  const c1 = document.createElement("div");
  c1.className = "card";
  c1.innerHTML = "<b>像素与强度</b><span class='kv'>" +
    "位置 x=" + fmtNum(js.pos_x * 1e3, 3) + " mm, y=" + fmtNum(js.pos_y * 1e3, 3) + " mm\n" +
    "强度 " + fmtNum(js.intensity) + " W/m²\n" +
    "通道 " + (js.label || "—") + "（" + fmtNum((js.wavelength || 0) * 1e9, 1) + " nm）" +
    (js.parts > 1 ? "\n该平面共 " + js.parts + " 个相干单元" : "") + "</span>";
  wrap.appendChild(c1);

  const c2 = document.createElement("div");
  c2.className = "card";
  const az = (st.azimuth || 0) * DEG;
  const ch = (st.ellipticity || 0) * DEG;
  c2.innerHTML = "<b>偏振态</b><span class='kv'>" +
    "方位角 ψ = " + az.toFixed(1) + "°（相对 x 轴）\n" +
    "椭率角 χ = " + ch.toFixed(1) + "°（>0 左旋）\n" +
    "轴比 b/a = " + (st.axis_ratio !== undefined ? st.axis_ratio.toFixed(3) : "—") + "\n" +
    "手性：" + (st.handedness || "—") + "\n" +
    "偏振度 DOP = " + (st.degree !== undefined ? st.degree.toFixed(3) : "—") + "</span>";
  wrap.appendChild(c2);

  const c3 = document.createElement("div");
  c3.className = "card";
  c3.innerHTML = "<b>斯托克斯参数（归一化）</b><span class='kv'>" +
    "S0 = " + fmtNum(st.s0) + " W/m²\n" +
    "S1/S0 = " + (st.s0 ? (st.s1 / st.s0).toFixed(3) : "—") + "\n" +
    "S2/S0 = " + (st.s0 ? (st.s2 / st.s0).toFixed(3) : "—") + "\n" +
    "S3/S0 = " + (st.s0 ? (st.s3 / st.s0).toFixed(3) : "—") + "\n" +
    "|Ex| = " + fmtNum(st.amp_x) + "，" + "|Ey| = " + fmtNum(st.amp_y) + "</span>";
  wrap.appendChild(c3);

  const c4 = document.createElement("div");
  c4.className = "card";
  const ps = js.phase_stats || {};
  c4.innerHTML = "<b>相位</b><span class='kv'>" +
    "Δφ(Ey−Ex) = " + (st.delta / Math.PI).toFixed(3) + " π\n" +
    "φ(Ex) = " + (js.phase / Math.PI).toFixed(3) + " π\n" +
    (js.phase_unwrapped !== undefined ? "去包裹 φ = " + (js.phase_unwrapped / Math.PI).toFixed(3) + " π\n" : "") +
    "波前 PV = " + (ps.pv !== undefined ? (ps.pv / (2 * Math.PI)).toFixed(4) : "—") + " λ\n" +
    "波前 RMS = " + (ps.rms !== undefined ? (ps.rms / (2 * Math.PI)).toFixed(4) : "—") + " λ</span>";
  wrap.appendChild(c4);

  const c5 = document.createElement("div");
  c5.className = "card";
  c5.innerHTML = "<b>琼斯矢量</b><span class='kv'>" +
    "Ex = " + fmtNum(js.ex_re) + (js.ex_im >= 0 ? " + " : " − ") + fmtNum(Math.abs(js.ex_im)) + "i\n" +
    "Ey = " + fmtNum(js.ey_re) + (js.ey_im >= 0 ? " + " : " − ") + fmtNum(Math.abs(js.ey_im)) + "i</span>";
  wrap.appendChild(c5);

  box.appendChild(wrap);
}

// ---------------- 3D layout view ----------------
function camCtx() {
  const tr = S.meta && S.meta.scene;
  const cv = $("#view3d");
  const w = cv.clientWidth || 640, h = cv.clientHeight || 480;
  let cx = 0, cy = 0, cz = 0, size = 0.2;
  if (tr) {
    cx = (tr.min.x + tr.max.x) / 2;
    cy = (tr.min.y + tr.max.y) / 2;
    cz = (tr.min.z + tr.max.z) / 2;
    const sx = tr.max.x - tr.min.x, sy = tr.max.y - tr.min.y, sz = tr.max.z - tr.min.z;
    size = Math.max(sx, sy, sz, 1e-3);
  } else {
    // 没有场景轨迹时，用元件位置估计范围（几何未路由成功的情况）。
    const list = comps();
    if (list.length) {
      const zs = list.map((c) => c.pos.z || 0);
      cz = (Math.min(...zs) + Math.max(...zs)) / 2;
      size = Math.max(Math.max(...zs) - Math.min(...zs), 0.05);
    }
  }
  const base = Math.min(w, h) * 0.72 * S.cam.zoom / Math.max(size, 1e-6);
  const a = S.cam.yaw, b = S.cam.pitch;
  return {
    cx, cy, cz, size,
    cosA: Math.cos(a), sinA: Math.sin(a),
    cosB: Math.cos(b), sinB: Math.sin(b),
    base, scale: base, dist: size * 3.0, w, h,
    px: w / 2 + S.cam.panX, py: h / 2 + S.cam.panY,
  };
}

function project3(p, K) {
  const x = p.x - K.cx, y = p.y - K.cy, z = p.z - K.cz;
  const x1 = x * K.cosA + z * K.sinA;
  const z1 = -x * K.sinA + z * K.cosA;
  const y1 = y * K.cosB - z1 * K.sinB;
  const z2 = y * K.sinB + z1 * K.cosB;
  const zc = z2 + K.dist;
  const f = K.base * (K.dist / Math.max(zc, K.dist * 0.35));
  return { x: K.px + x1 * f, y: K.py - y1 * f, depth: zc };
}

function render3D() {
  const cv = $("#view3d");
  const wrap = $("#canvasWrap");
  cv.width = Math.max(320, wrap.clientWidth);
  cv.height = Math.max(240, wrap.clientHeight);
  const ctx = cv.getContext("2d");
  ctx.clearRect(0, 0, cv.width, cv.height);
  const K = camCtx();
  const tr = S.meta && S.meta.scene;

  // 光学平台网格（y = 0 平面）
  const zSpan = tr ? Math.max(tr.max.z - tr.min.z, 0.05) : 0.3;
  const xSpan = tr ? Math.max(tr.max.x - tr.min.x, 0.05) : 0.1;
  const gridStep = niceStep(Math.max(zSpan, xSpan) / 8);
  const x0 = (tr ? tr.min.x : -0.05) - gridStep, x1 = (tr ? tr.max.x : 0.05) + gridStep;
  const z0 = (tr ? tr.min.z : -0.05) - gridStep, z1 = (tr ? tr.max.z : 0.25) + gridStep;
  ctx.lineWidth = 1;
  ctx.strokeStyle = "rgba(120,160,195,.35)";
  for (let x = Math.ceil(x0 / gridStep) * gridStep; x <= x1; x += gridStep) {
    const a = project3({ x: x, y: 0, z: z0 }, K), b = project3({ x: x, y: 0, z: z1 }, K);
    ctx.beginPath(); ctx.moveTo(a.x, a.y); ctx.lineTo(b.x, b.y); ctx.stroke();
  }
  for (let z = Math.ceil(z0 / gridStep) * gridStep; z <= z1; z += gridStep) {
    const a = project3({ x: x0, y: 0, z: z }, K), b = project3({ x: x1, y: 0, z: z }, K);
    ctx.beginPath(); ctx.moveTo(a.x, a.y); ctx.lineTo(b.x, b.y); ctx.stroke();
  }
  // 光轴
  const az0 = project3({ x: 0, y: 0, z: z0 }, K), az1 = project3({ x: 0, y: 0, z: z1 }, K);
  ctx.strokeStyle = "rgba(90,125,158,.8)";
  ctx.setLineDash([5, 4]);
  ctx.beginPath(); ctx.moveTo(az0.x, az0.y); ctx.lineTo(az1.x, az1.y); ctx.stroke();
  ctx.setLineDash([]);

  const items = [];

  // 光路（光束线段）
  if (tr && tr.segments) {
    tr.segments.forEach((seg) => {
      const a = project3(seg.from, K), b = project3(seg.to, K);
      items.push({
        depth: (a.depth + b.depth) / 2,
        draw: () => {
          ctx.beginPath();
          ctx.moveTo(a.x, a.y);
          ctx.lineTo(b.x, b.y);
          const col = sourceColor(seg.source);
          const pw = Math.max(0, Math.min(1, seg.power || 0));
          const blocked = seg.kind === "blocked";
          ctx.strokeStyle = blocked ? "rgba(150,160,170,.75)" : col.stroke;
          if (blocked) ctx.setLineDash([3, 5]);
          ctx.lineWidth = blocked ? 1 : 1.4 + 3.2 * Math.sqrt(pw);
          ctx.stroke();
          ctx.setLineDash([]);
        },
      });
    });
  }

  // 元件（方形/圆形的板）
  if (tr && tr.components) {
    tr.components.forEach((c) => {
      const corners = quadCorners(c);
      const proj = corners.map((p) => project3(p, K));
      const depth = proj.reduce((s, p) => s + p.depth, 0) / proj.length;
      items.push({
        depth: depth,
        draw: () => {
          ctx.beginPath();
          ctx.moveTo(proj[0].x, proj[0].y);
          for (let i = 1; i < proj.length; i++) ctx.lineTo(proj[i].x, proj[i].y);
          ctx.closePath();
          const cls = c.class || "other";
          ctx.fillStyle = classFill(cls);
          ctx.fill();
          ctx.strokeStyle = c.hits > 0 ? "#1460c8" : "rgba(60,90,120,.8)";
          ctx.lineWidth = (c.hits > 0 ? 2 : 1.2);
          ctx.stroke();
          // 法线指示
          const p0 = project3(c.pos, K);
          const pn = project3({ x: c.pos.x + c.normal.x * K.size * 0.06, y: c.pos.y + c.normal.y * K.size * 0.06, z: c.pos.z + c.normal.z * K.size * 0.06 }, K);
          ctx.strokeStyle = "rgba(40,70,100,.7)";
          ctx.beginPath(); ctx.moveTo(p0.x, p0.y); ctx.lineTo(pn.x, pn.y); ctx.stroke();
          if (c.label) {
            ctx.fillStyle = "#14324a";
            ctx.font = "11px system-ui, sans-serif";
            ctx.fillText(c.label, p0.x + 8, p0.y - 8);
          }
        },
      });
      // 焦距提示：透镜用一个小标记表示光焦度方向
    });
  }

  // 光源
  const srcs = (S.meta && S.meta.sources) || [];
  srcs.forEach((s) => {
    const p0 = project3(s.pos, K);
    const p1 = project3({ x: s.pos.x + s.dir.x * K.size * 0.05, y: s.pos.y + s.dir.y * K.size * 0.05, z: s.pos.z + s.dir.z * K.size * 0.05 }, K);
    items.push({
      depth: p0.depth - 1e-6,
      draw: () => {
        const col = sourceColor(s.index);
        ctx.strokeStyle = col.stroke;
        ctx.lineWidth = 2.4;
        ctx.beginPath(); ctx.moveTo(p0.x, p0.y); ctx.lineTo(p1.x, p1.y); ctx.stroke();
        ctx.beginPath();
        ctx.arc(p0.x, p0.y, 6, 0, Math.PI * 2);
        ctx.fillStyle = col.stroke;
        ctx.fill();
        ctx.strokeStyle = "rgba(255,255,255,.9)";
        ctx.lineWidth = 1.5;
        ctx.stroke();
        ctx.fillStyle = "#14324a";
        ctx.font = "11px system-ui, sans-serif";
        ctx.fillText((s.label || "光源") + " " + fmtNum(s.wavelength * 1e9, 1) + "nm", p0.x + 9, p0.y + 4);
      },
    });
  });

  items.sort((a, b) => b.depth - a.depth);
  items.forEach((it) => it.draw());

  // 坐标轴指示
  drawAxisTriad(ctx);
  // 空场景提示
  if (!tr) {
    ctx.fillStyle = "#3d6285";
    ctx.font = "13px system-ui, sans-serif";
    ctx.fillText("尚未路由出光路：运行一次模拟，或检查光源与元件位置。", 16, 24);
  }
}

function quadCorners(c) {
  const hu = c.hu || 3e-3, hv = c.hv || 3e-3;
  const u = c.u || { x: 1, y: 0, z: 0 }, v = c.v || { x: 0, y: 1, z: 0 };
  const pts = [];
  [[-1, -1], [1, -1], [1, 1], [-1, 1]].forEach(([su, sv]) => {
    pts.push({
      x: c.pos.x + u.x * hu * su + v.x * hv * sv,
      y: c.pos.y + u.y * hu * su + v.y * hv * sv,
      z: c.pos.z + u.z * hu * su + v.z * hv * sv,
    });
  });
  return pts;
}

function classFill(cls) {
  return {
    mirror: "rgba(175,195,215,.92)", splitter: "rgba(140,205,245,.85)",
    lens: "rgba(190,225,255,.8)", detector: "rgba(70,90,110,.92)",
    stop: "rgba(205,210,215,.9)", other: "rgba(205,225,245,.85)",
  }[cls] || "rgba(205,225,245,.85)";
}

function sourceColor(idx) {
  const srcs = (S.meta && S.meta.sources) || [];
  const s = srcs.find((x) => x.index === idx) || srcs[idx];
  const hex = s && s.hex ? s.hex : wavelengthHex(s ? s.wavelength : S.config.wavelength);
  return { stroke: hex, raw: s };
}

function niceStep(v) {
  const p = Math.pow(10, Math.floor(Math.log10(Math.max(v, 1e-9))));
  const m = v / p;
  if (m <= 1) return p;
  if (m <= 2) return 2 * p;
  if (m <= 5) return 5 * p;
  return 10 * p;
}

function drawAxisTriad(ctx) {
  const ox = 52, oy = ctx.canvas.height - 46, L = 30;
  const axes = [
    { v: { x: 1, y: 0, z: 0 }, c: "#c0392b", l: "x" },
    { v: { x: 0, y: 1, z: 0 }, c: "#27ae60", l: "y" },
    { v: { x: 0, y: 0, z: 1 }, c: "#1460c8", l: "z" },
  ];
  ctx.save();
  ctx.lineWidth = 2;
  ctx.font = "11px system-ui, sans-serif";
  const K = { cx: 0, cy: 0, cz: 0, cosA: Math.cos(S.cam.yaw), sinA: Math.sin(S.cam.yaw),
    cosB: Math.cos(S.cam.pitch), sinB: Math.sin(S.cam.pitch), base: L, scale: L, dist: 1e6, w: 0, h: 0, px: 0, py: 0 };
  axes.forEach((a) => {
    const p = project3(a.v, K);
    ctx.strokeStyle = a.c;
    ctx.beginPath(); ctx.moveTo(ox, oy); ctx.lineTo(ox + p.x, oy - p.y); ctx.stroke();
    ctx.fillStyle = a.c;
    ctx.fillText(a.l, ox + p.x * 1.15 - 3, oy - p.y * 1.15 + 4);
  });
  ctx.restore();
}

// ---------------- profile ----------------
function drawProfile() {
  const pc = $("#prof");
  const wrap = $("#canvasWrap");
  pc.width = wrap.clientWidth; pc.height = wrap.clientHeight;
  const ctx = pc.getContext("2d");
  ctx.clearRect(0, 0, pc.width, pc.height);
  if (!S.profileAxis || !S.meta || !S.meta.planes.length || S.view === "scene3d") return;
  const p = curPlane();
  const q = new URLSearchParams({ axis: S.profileAxis, field: profileField(S.view) });
  if (S.channel >= 0) q.set("part", String(S.channel));
  fetch("/api/runs/" + S.runId + "/profiles/" + p.id + "?" + q.toString())
    .then((r) => r.json())
    .then((prof) => {
      if (!S.profileAxis) return;
      const x = prof.x, v = prof.v;
      let vmin = Infinity, vmax = -Infinity;
      for (const y of v) { if (!Number.isFinite(y)) continue; if (y < vmin) vmin = y; if (y > vmax) vmax = y; }
      if (!Number.isFinite(vmin) || vmax - vmin < 1e-30) return;
      const pad = 30, w = pc.width - 2 * pad, h = pc.height - 2 * pad;
      ctx.strokeStyle = "rgba(20,40,60,.25)";
      ctx.beginPath(); ctx.moveTo(pad, pc.height - pad); ctx.lineTo(pc.width - pad, pc.height - pad); ctx.stroke();
      const isPhase = viewIsPhase();
      ctx.strokeStyle = isPhase ? "#c96f00" : "#1460c8";
      ctx.lineWidth = 1.5;
      ctx.beginPath();
      let started = false;
      for (let i = 0; i < x.length; i++) {
        const val = v[i];
        if (!Number.isFinite(val)) { started = false; continue; }
        const px = pad + (i / (x.length - 1)) * w;
        const t = (val - vmin) / (vmax - vmin);
        const py = pc.height - pad - t * h;
        if (!started) { ctx.moveTo(px, py); started = true; } else ctx.lineTo(px, py);
      }
      ctx.stroke();
      ctx.fillStyle = "#33475c";
      ctx.font = "11px system-ui, sans-serif";
      ctx.fillText((prof.unit ? prof.unit + "  " : "") + "范围 " + fmtNum(vmin) + " ~ " + fmtNum(vmax) +
        "   切割位置 " + fmtNum(prof.coord) + " m", pad, 14);
    }).catch(() => {});
}

function setView(v) {
  S.view = v;
  $$("#viewTabs button[data-view]").forEach((b) => b.classList.toggle("active", b.dataset.view === v));
  const is3d = v === "scene3d";
  $("#view").hidden = is3d;
  $("#view3d").hidden = !is3d;
  $("#view3dHint").hidden = !is3d;
  $("#prof").hidden = is3d;
  $("#channelBar").hidden = is3d;
  if (is3d) {
    $("#prof").hidden = true;
    S.profileAxis = null;
    render3D();
    return;
  }
  const scaleBtn = $("#scaleBtn");
  if (scaleBtn) scaleBtn.textContent = "l " + (effScale(v) === "log" ? "对数" : "线性");
  renderChannelBar();
  renderView();
  if (S.view === "pol") loadPolSamples();
  if (S.profileAxis) drawProfile();
  clearInspect();
}

function stepPlane(d) {
  const n = S.meta ? S.meta.planes.length : 0;
  if (!n) return;
  S.planeIdx = Math.min(n - 1, Math.max(0, S.planeIdx + d));
  S.inspect = null;
  renderPlanes(); renderStats(); renderView();
  updatePlaneNav();
  if (S.view === "pol") loadPolSamples();
  clearInspect();
}

function togglePattern() {
  S.hidePattern = !S.hidePattern;
  renderPatternVisibility();
}

function renderPatternVisibility() {
  const hidden = !!S.hidePattern;
  $("#view").hidden = hidden || S.view === "scene3d";
  const btn = $("#hidePatternBtn");
  if (btn) {
    btn.textContent = hidden ? "显示图案" : "隐藏图案";
    btn.classList.toggle("active", hidden);
  }
}

function updatePlaneNav() {
  const nav = $("#planeNav");
  if (nav) nav.hidden = S.view === "scene3d" || !(S.meta && S.meta.planes.length);
  const colors = $("#channelBar");
  if (colors) colors.hidden = S.view === "scene3d";
}

function hideOverlay(ov) {
  if (!ov || ov.hidden) return;
  ov.hidden = true;
  const ae = document.activeElement;
  if (ae && ov.contains(ae)) ae.blur();
}

// ---------------- simulation ----------------
function setStatus(msg, isErr) {
  const el = $("#status");
  el.textContent = msg;
  el.style.color = isErr ? "var(--err)" : "";
}

async function run() {
  if (S.busy) { S.dirty = true; return; }  // 计算中先标记，完成后补算，避免配置变更被吞掉
  if (!S.config) return;
  S.busy = true;
  setStatus("计算中…");
  try {
    const r = await fetch("/api/simulate", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(S.config),
    });
    if (!r.ok) {
      const j = await r.json().catch(() => ({}));
      setStatus("配置错误: " + (j.error || r.status), true);
      renderWarnings();
      return;
    }
    const j = await r.json();
    const myRunId = j.run_id;   // 局部捕获：载入新配置会重置 S.runId，轮询不能依赖全局状态
    S.runId = myRunId;
    for (;;) {
      const m = await fetch("/api/runs/" + myRunId).then((x) => x.json());
      if (m.status === "done") {
        if (S.runId !== myRunId) return; // 期间状态已被重置：丢弃过期结果，由 dirty 补算
        S.meta = m;
        S.cache.clear();
        S.polSamples = null;
        S.planeIdx = Math.min(S.planeIdx, Math.max(0, m.planes.length - 1));
        if (S.channel >= 0) {
          const pl = curPlane();
          if (!pl || !pl.parts || S.channel >= pl.parts.length) S.channel = -1;
        }
        renderPlanes(); renderWarnings(); renderStats(); renderView();
        updatePlaneNav();
        if (S.view === "scene3d") render3D();
        if (S.view === "pol") loadPolSamples();
        clearInspect();
        setStatus("完成 " + m.elapsed_ms.toFixed(0) + " ms · 网格 " + m.grid.size + "² · 输出 " + m.planes.length + " 平面" +
          (m.scene ? " · 元件 " + m.scene.components.length + " · 光束段 " + m.scene.segments.length : "") +
          (m.warnings && m.warnings.length ? " · " + m.warnings.length + " 条警告" : ""));
        return;
      }
      if (m.status === "error") {
        setStatus("计算错误: " + (m.error || "?"), true);
        return;
      }
      if (S.runId !== myRunId) return; // 状态已重置，停止轮询过期任务
      await new Promise((res) => setTimeout(res, 250));
    }
  } catch (e) {
    setStatus("请求失败: " + e.message, true);
  } finally {
    S.busy = false;
    if (S.dirty) { S.dirty = false; run(); }
  }
}

function scheduleRun() {
  if (!S.autoRun) return;
  clearTimeout(S.timer);
  S.timer = setTimeout(run, 350);
}

// ---------------- scene editing ----------------
function selectComp(delta) {
  const n = comps().length;
  if (!n) return;
  S.compSel = (S.compSel + delta + n) % n;
  renderSceneList(); renderParams();
}
function nudgeComp(delta) {
  // 用 Shift+↑/↓ 微调当前元件在光轴上的位置（步长 5 mm）
  const c = curComp();
  if (!c) return;
  c.pos.z = (c.pos.z || 0) + delta * 5e-3;
  renderSceneList(); renderParams();
  run();
}
function delComp() {
  const list = comps();
  if (!list.length) return;
  list.splice(S.compSel, 1);
  if (S.compSel >= list.length) S.compSel = Math.max(0, list.length - 1);
  renderAll();
  run();
}
function dupComp() {
  const list = comps();
  const c = curComp();
  if (!c) return;
  const cp = clone(c);
  cp.id = (c.type || "c") + "_" + (list.length + 1);
  cp.label = (c.label || c.type) + " 副本";
  cp.pos = { x: c.pos.x, y: c.pos.y, z: c.pos.z + 0.05 };
  list.splice(S.compSel + 1, 0, cp);
  S.compSel = Math.min(S.compSel + 1, list.length - 1);
  renderAll();
  run();
}
function addSource() {
  const list = sources();
  const type = list.length ? list[list.length - 1].type : "gaussian";
  const s = newSource(type, list.length);
  // 新光源放在已有光源旁边，避免完全重叠
  if (list.length) {
    s.pos = { x: (list[list.length - 1].pos.x || 0) + 3e-3, y: 0, z: (list[list.length - 1].pos.z || -0.05) };
    s.group = ""; // 默认独立（不相干）
  }
  list.push(s);
  S.srcSel = list.length - 1;
  renderAll();
  run();
}
function dupSource() {
  const list = sources();
  const s = curSrc();
  if (!s) return;
  const cp = clone(s);
  cp.id = "src" + (list.length + 1);
  cp.label = (s.label || "光源") + " 副本";
  cp.pos = { x: (s.pos.x || 0) + 3e-3, y: s.pos.y || 0, z: s.pos.z || -0.05 };
  list.splice(S.srcSel + 1, 0, cp);
  S.srcSel = Math.min(S.srcSel + 1, list.length - 1);
  renderAll();
  run();
}
function delSource() {
  const list = sources();
  if (!list.length) return;
  list.splice(S.srcSel, 1);
  if (S.srcSel >= list.length) S.srcSel = Math.max(0, list.length - 1);
  renderAll();
  run();
}
function openInsert() {
  $("#insertOverlay").hidden = false;
  S.insertSel = 0;
  const inp = $("#insFilter");
  inp.value = "";
  renderInsertList("");
  inp.focus();
}
function renderInsertList(filter) {
  const box = $("#insList");
  box.innerHTML = "";
  const f = filter.toLowerCase();
  let idx = 0;
  S.catalog.elements.forEach((d) => {
    if (f && !(d.label + d.type).toLowerCase().includes(f)) return;
    const b = document.createElement("button");
    b.textContent = d.label + " (" + d.type + ")";
    b.title = d.help || "";
    const myIdx = idx++;
    b.addEventListener("click", () => { insertComponent(d.type); });
    if (myIdx === S.insertSel) { b.classList.add("sel"); b.scrollIntoView({ block: "nearest" }); }
    box.appendChild(b);
  });
}
function insertComponent(type) {
  const c = newComponent(type);
  const next = nextPosAlongAxis();
  // 位置可随后自由修改：光路顺序完全由位置决定，与列表顺序无关。
  c.pos = type === "sensor" ? { x: next.x, y: next.y, z: next.z + 0.05 } : next;
  const list = comps();
  list.splice(S.compSel + 1, 0, c);
  S.compSel = Math.min(S.compSel + 1, list.length - 1);
  hideOverlay($("#insertOverlay"));
  renderAll();
  run();
}

// ---------------- presets ----------------
function fillPresetSelect() {
  const sel = $("#presetSel");
  if (!sel) return;
  while (sel.options.length > 1) sel.remove(1);
  (S.catalog.examples || []).forEach((ex, i) => {
    const o = document.createElement("option");
    o.value = String(i);
    o.textContent = ex.name;
    sel.appendChild(o);
  });
}

async function applyPreset(idx) {
  const ex = (S.catalog.examples || [])[idx];
  if (!ex) return;
  await adoptConfig(clone(ex.config), "已载入模板：" + ex.name);
}

async function adoptConfig(cfg, msg) {
  cfg = normalizeConfig(cfg);
  if (!cfg.scene || !Array.isArray(cfg.scene.components) || !cfg.scene.components.length) {
    if (Array.isArray(cfg.elements) && cfg.elements.length) {
      // 旧版元件序列配置：向内核请求转换成定位场景（沿 +z 按累计距离摆放）。
      try {
        const r = await fetch("/api/convert", {
          method: "POST", headers: { "Content-Type": "application/json" },
          body: JSON.stringify(cfg),
        });
        if (r.ok) {
          const j = await r.json();
          cfg.scene = j.scene;
          if (!cfg.sources || !cfg.sources.length) cfg.sources = j.sources || [];
          delete cfg.elements;
          msg = (msg ? msg + " · " : "") + "元件序列已转换为定位场景";
        }
      } catch (e) { /* 转换失败则保留原配置 */ }
    }
  }
  if (!cfg.scene) cfg.scene = { components: [] };
  if (!Array.isArray(cfg.sources) || !cfg.sources.length) {
    cfg.sources = [{ id: "src1", label: "光源 1", type: "gaussian",
      pos: { x: 0, y: 0, z: -0.05 }, dir: { x: 0, y: 0, z: 1 }, params: { waist: 1e-3, power: 1e-3 } }];
  }
  S.config = cfg;
  resetViewState();
  renderAll();
  setStatus(msg || "已载入配置");
  run();
}

// ---------------- overlays ----------------
const HELP_ROWS = [
  ["Tab / Shift+Tab", "在控件间移动焦点（全部操作均可用 Tab 完成）"],
  ["f", "逐个定位参数：元件参数 → 光源参数 → 全局参数 → 元件列表（循环）"],
  ["输入框内", "原生输入：数字框支持科学计数法（如 1e-3）、↑/↓ 按步长步进；Enter 确认并移出焦点"],
  ["↑ / ↓", "选择上一个/下一个元件"],
  ["Shift+↑ / Shift+↓", "把当前元件沿光轴前后移动 5 mm（顺序由位置决定）"],
  ["i", "插入新元件（输入文字过滤，Enter 插入）"],
  ["d / Delete", "删除当前元件"],
  ["▶ 运行 / 空格 / Ctrl+Enter", "运行模拟（修改参数后不自动运行）"],
  ["q / e", "上一个 / 下一个输出平面"],
  ["1 - 8 / 0", "视图：1 颜色 / 2 相位 / 3 偏振 / 4 |Ex|² / 5 |Ey|² / 6 |Ez|² / 7 相位Ex / 8 相位Ey / 0 立体视图"],
  ["c", "切换通道（全部光源 ↔ 各光源单元）"],
  ["p", "显示/隐藏一维剖面"],
  ["x / y", "剖面方向：横向 / 纵向"],
  ["l", "对数/线性标度切换"],
  ["h", "隐藏/显示中心图案"],
  ["鼠标（图像上）", "点击读取该点的偏振态、相位、琼斯矢量与斯托克斯参数（点击处标出十字）"],
  ["鼠标（立体视图）", "拖动旋转、滚轮缩放、右键/Shift 拖动平移、双击复位"],
  ["立体视图拖动方向", "默认“反向”（画面跟着指针走）。若不习惯，点立体视图左下角「拖动方向」按钮切换（浏览器本地记忆）"],
  ["a", "自动运行开关（默认关闭）"],
  ["j", "高级：直接编辑配置 JSON"],
  ["n", "新建空白场景"],
  ["o", "打开 JSON 配置文件（内置模板见左侧“模板”下拉）"],
  ["s", "把当前配置保存为 JSON 文件"],
  ["Esc", "关闭对话框"],
  ["m", "波动光学 / 量子光学 模式切换"],
];

function openHelp() {
  const box = $("#helpTable");
  box.innerHTML = "";
  HELP_ROWS.forEach(([k, d]) => {
    const a = document.createElement("div");
    a.className = "help-item";
    const kk = document.createElement("div");
    kk.className = "help-key";
    kk.textContent = k;
    const dd = document.createElement("div");
    dd.className = "help-desc";
    dd.textContent = d;
    a.appendChild(kk); a.appendChild(dd);
    box.appendChild(a);
  });
  $("#helpOverlay").hidden = false;
  $("#helpClose").focus();
}
function closeHelp() { hideOverlay($("#helpOverlay")); }
function openJson() {
  $("#jsonOverlay").hidden = false;
  $("#jsonText").value = JSON.stringify(S.config, null, 2);
  $("#jsonMsg").textContent = "";
  $("#jsonText").focus();
}
async function applyJson() {
  try {
    const cfg = JSON.parse($("#jsonText").value);
    hideOverlay($("#jsonOverlay"));
    await adoptConfig(cfg, "已应用 JSON 配置");
  } catch (e) {
    $("#jsonMsg").textContent = "JSON 解析失败: " + e.message;
  }
}
function closeJson() { hideOverlay($("#jsonOverlay")); }

// ---------------- files (新建 / 打开 / 保存) ----------------
// 兼容旧版本导出的配置（ElementSpec/SourceSpec 曾以大写 Type/Params 序列化，
// 旧配置可能只有 elements 而没有 scene）。
function normalizeConfig(cfg) {
  if (!cfg || typeof cfg !== "object") return cfg;
  if (cfg.source && !cfg.sources) {
    cfg.sources = [cfg.source];
    delete cfg.source;
  }
  (cfg.sources || []).forEach((s) => {
    if (s.type === undefined && s.Type !== undefined) s.type = s.Type;
    if (s.params === undefined && s.Params !== undefined) s.params = s.Params;
  });
  if (cfg.scene && Array.isArray(cfg.scene.components)) {
    cfg.scene.components.forEach((c) => {
      if (c.type === undefined && c.Type !== undefined) c.type = c.Type;
      if (c.params === undefined && c.Params !== undefined) c.params = c.Params;
    });
  }
  if (Array.isArray(cfg.elements)) {
    cfg.elements = cfg.elements.map((el) => {
      if (el.type === undefined && el.Type !== undefined) el.type = el.Type;
      if (el.params === undefined && el.Params !== undefined) el.params = el.Params;
      return el;
    });
  }
  return cfg;
}

function blankConfig() {
  return {
    grid: { size: 512, width: 0.01 },
    wavelength: 632.8e-9,
    polarized: false,
    method: "asm",
    evanescent: "decay",
    evanescent_limit: 0,
    backward_regularize: false,
    tikhonov_alpha: 0,
    bandlimit: { fraction: 0.9, sigma: 0.05 },
    sources: [{
      id: "src1", label: "高斯光源", type: "gaussian",
      pos: { x: 0, y: 0, z: -0.05 }, dir: { x: 0, y: 0, z: 1 },
      params: { waist: 1e-3, power: 1e-3 },
    }],
    scene: {
      components: [
        { id: "lens_1", type: "lens", label: "透镜", pos: { x: 0, y: 0, z: 0 },
          yaw: 0, pitch: 0, roll: 0, shape: circleShape(4e-3), params: { f: 0.5 } },
        { id: "sensor_1", type: "sensor", label: "焦面", pos: { x: 0, y: 0, z: 0.5 },
          yaw: Math.PI, pitch: 0, roll: 0, shape: circleShape(5e-3), params: {} },
      ],
    },
  };
}
function resetViewState() {
  S.compSel = 0; S.srcSel = 0; S.planeIdx = 0;
  S.meta = null; S.runId = null; S.channel = -1;
  S.inspect = null; S.polSamples = null;
  S.cache.clear();
}
function newFile() {
  S.config = blankConfig();
  resetViewState();
  renderAll();
  setStatus("已新建空白场景（按 i 插入元件，或从“模板”载入）");
  run();
}
function saveFile() {
  if (!S.config) return;
  const blob = new Blob([JSON.stringify(S.config, null, 2)], { type: "application/json" });
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = "wos-config.json";
  document.body.appendChild(a);
  a.click();
  a.remove();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
  setStatus("已保存配置 wos-config.json");
}
function openFile(file) {
  const rd = new FileReader();
  rd.onload = async () => {
    try {
      await adoptConfig(JSON.parse(String(rd.result)), "已载入 " + file.name);
    } catch (err) {
      setStatus("打开文件失败: " + err.message, true);
    }
  };
  rd.onerror = () => setStatus("读取文件失败", true);
  rd.readAsText(file);
}
function openFilePicker() {
  $("#fileOpen").click();
}

// ---------------- global render ----------------
function renderAll() {
  if (S.mode === "quantum") { renderQuantum(); return; }
  renderSceneList();
  renderParams();
  renderSourceList();
  renderSource();
  renderGlobals();
  renderPlanes();
  renderWarnings();
  renderStats();
  updatePlaneNav();
}

// ---------------- keyboard ----------------
function focusableCycle() {
  const out = [];
  ["#paramPanel", "#sourcePanel", "#globalPanel"].forEach((z) => {
    $$(z + " input, " + z + " select, " + z + " button").forEach((el) => out.push(el));
  });
  const sc = $("#sceneList button.sel") || $("#sceneList button") || $("#insertBtn");
  if (sc) out.push(sc);
  const sr = $("#sourceList button.sel") || $("#sourceList button");
  if (sr) out.push(sr);
  return out;
}
function jumpFocus() {
  const list = focusableCycle();
  if (!list.length) return;
  const i = list.indexOf(document.activeElement);
  list[(i < 0 ? 0 : i + 1) % list.length].focus();
}

document.addEventListener("keydown", (e) => {
  const t = e.target;
  const tag = t.tagName;
  const inField = tag === "INPUT" || tag === "SELECT" || tag === "TEXTAREA";
  if (!$("#helpOverlay").hidden) {
    if (e.key === "Escape") closeHelp();
    return;
  }
  if (!$("#insertOverlay").hidden) {
    if (e.key === "Escape") { hideOverlay($("#insertOverlay")); return; }
    if (e.key === "Enter") { const b = $("#insList button.sel"); if (b) b.click(); return; }
    if (e.key === "ArrowDown") { S.insertSel++; renderInsertList($("#insFilter").value); e.preventDefault(); return; }
    if (e.key === "ArrowUp") { S.insertSel = Math.max(0, S.insertSel - 1); renderInsertList($("#insFilter").value); e.preventDefault(); return; }
    if (tag === "INPUT") { setTimeout(() => renderInsertList($("#insFilter").value), 0); }
    return;
  }
  if (!$("#jsonOverlay").hidden) {
    if (e.key === "Escape") { closeJson(); return; }
    if (e.key === "Enter" && (e.ctrlKey || e.metaKey)) { applyJson(); return; }
    return;
  }
  if (inField) {
    if (e.key === "f" && S.mode === "wave") { e.preventDefault(); jumpFocus(); return; }
    if (e.key === "Enter" && tag === "INPUT" && (t.type === "number" || t.type === "text")) t.blur();
    return;
  }
  if (S.mode === "quantum") {
    if (e.key === " ") { e.preventDefault(); qRun(); }
    else if (e.key === "m") { setMode("wave"); }
    return;
  }
  switch (e.key) {
    case " ": e.preventDefault(); run(); break;
    case "m": setMode("quantum"); break;
    case "Enter": if (e.ctrlKey) run(); break;
    case "1": setView("color"); break;
    case "2": setView("phase_u"); break;
    case "3": setView("pol"); break;
    case "4": setView("ex"); break;
    case "5": setView("ey"); break;
    case "6": setView("ez"); break;
    case "7": setView("phase_x"); break;
    case "8": setView("phase_y"); break;
    case "0": setView("scene3d"); break;
    case "f": jumpFocus(); break;
    case "q": stepPlane(-1); break;
    case "e": if (e.ctrlKey) break; stepPlane(1); break;
    case "c": {
      const p = curPlane();
      const n = p && p.parts ? p.parts.length : 0;
      if (n > 1) { S.channel = S.channel >= n - 1 ? -1 : S.channel + 1; S.inspect = null; renderChannelBar(); renderView(); if (S.view === "pol") loadPolSamples(); }
      break;
    }
    case "[" : selectComp(-1); break;
    case "]" : selectComp(1); break;
    case "ArrowUp": if (e.shiftKey) nudgeComp(-1); else selectComp(-1); e.preventDefault(); break;
    case "ArrowDown": if (e.shiftKey) nudgeComp(1); else selectComp(1); e.preventDefault(); break;
    case "i": e.preventDefault(); openInsert(); break;
    case "d": case "Delete": delComp(); break;
    case "p": S.profileAxis = S.profileAxis ? null : "x"; drawProfile(); break;
    case "h": togglePattern(); break;
    case "x": if (S.profileAxis) { S.profileAxis = "x"; drawProfile(); } break;
    case "y": if (S.profileAxis) { S.profileAxis = "y"; drawProfile(); } break;
    case "l": {
      S.scaleTouched = true;
      const cur = effScale(S.view);
      S.scale = cur === "log" ? "lin" : "log";
      const b = $("#scaleBtn");
      if (b) b.textContent = "l " + (S.scale === "log" ? "对数" : "线性");
      renderView();
      break;
    }
    case "a": S.autoRun = !S.autoRun; setStatus("自动运行 " + (S.autoRun ? "开" : "关"), false); break;
    case "j": openJson(); break;
    case "n": newFile(); break;
    case "s": saveFile(); break;
    case "o": openFilePicker(); break;
    case "Escape": break;
    case "?": case "F1": openHelp(); e.preventDefault(); break;
  }
});
// ---------------- quantum optics mode ----------------
function blankQuantumConfig() {
  return {
    modes: 2,
    cutoff: 4,
    state: { type: "fock", params: { occupation: "1,1" } },
    gates: [{ type: "beam_splitter", params: { mode0: 0, mode1: 1, reflectivity: 0.5 } }],
  };
}

function qDoc(type) { return S.catalog.quantum.states.find((d) => d.type === type); }
function qGateDoc(type) { return S.catalog.quantum.gates.find((d) => d.type === type); }

function setMode(m) {
  S.mode = m;
  $("#waveModeBtn").classList.toggle("active", m === "wave");
  $("#quantumModeBtn").classList.toggle("active", m === "quantum");
  $("#waveEditor").hidden = m !== "wave";
  $("#quantumEditor").hidden = m !== "quantum";
  $("#waveOutput").hidden = m !== "wave";
  $("#quantumOutput").hidden = m !== "quantum";
  $("#waveGlobals").hidden = m !== "wave";
  if (m === "quantum") { renderQuantum(); qRun(); }
  else { renderAll(); }
}

// A generic parameter row for the quantum editor.
function qRow(doc, get, set, onCommit) {
  const row = document.createElement("div");
  row.className = "prow";
  const lab = document.createElement("label");
  lab.textContent = doc.label + (doc.unit ? " [" + doc.unit + "]" : "");
  lab.title = doc.help || "";
  row.appendChild(lab);
  const val = get();
  if (doc.kind === "choice") {
    const sel = document.createElement("select");
    (doc.choices || []).forEach((c) => {
      const o = document.createElement("option");
      o.value = c; o.textContent = c;
      if (String(val) === String(c)) o.selected = true;
      sel.appendChild(o);
    });
    sel.addEventListener("change", () => { set(sel.value); onCommit(); });
    row.appendChild(sel);
  } else if (doc.kind === "bool") {
    const cb = document.createElement("input");
    cb.type = "checkbox";
    cb.checked = !!val;
    cb.addEventListener("change", () => { set(cb.checked); onCommit(); });
    row.appendChild(cb);
  } else if (doc.kind === "text") {
    const inp = document.createElement("input");
    inp.type = "text";
    inp.value = val === undefined || val === null ? "" : String(val);
    inp.addEventListener("change", () => { set(inp.value); onCommit(); });
    row.appendChild(inp);
  } else { // float / int
    const num = document.createElement("input");
    num.type = "number";
    num.min = doc.min !== undefined ? doc.min : 0;
    num.max = doc.max !== undefined ? doc.max : 1;
    num.step = doc.step || 0.01;
    num.value = Number(val) || 0;
    bindNumInput(num, () => get(), (x) => { set(x); }, () => onCommit(), () => onCommit());
    row.appendChild(num);
  }
  return row;
}

function renderQConfigPanel() {
  const panel = $("#qConfigPanel");
  panel.innerHTML = "";
  const q = S.qconfig;
  const mkInt = (label, get, set, min, max) => {
    const row = document.createElement("div");
    row.className = "prow";
    row.appendChild(Object.assign(document.createElement("label"), { textContent: label }));
    const num = document.createElement("input");
    num.type = "number"; num.min = min; num.max = max; num.step = 1; num.value = get();
    bindNumInput(num, get, set, () => {}, () => {});
    row.appendChild(num);
    return row;
  };
  panel.appendChild(mkInt("模式数 modes", () => q.modes, (v) => { q.modes = Math.max(1, Math.round(v)); }, 1, 4));
  panel.appendChild(mkInt("截断 cutoff", () => q.cutoff, (v) => { q.cutoff = Math.max(1, Math.round(v)); }, 1, 20));

  const st = q.state;
  const srow = document.createElement("div");
  srow.className = "prow";
  srow.appendChild(Object.assign(document.createElement("label"), { textContent: "初态" }));
  const ssel = document.createElement("select");
  S.catalog.quantum.states.forEach((d) => {
    const o = document.createElement("option");
    o.value = d.type; o.textContent = d.label;
    if (st.type === d.type) o.selected = true;
    ssel.appendChild(o);
  });
  ssel.addEventListener("change", () => {
    st.type = ssel.value;
    st.params = {};
    const doc = qDoc(st.type);
    if (doc) doc.params.forEach((pd) => { if (pd.default !== undefined) st.params[pd.key] = clone(pd.default); });
    renderQuantum();
  });
  srow.appendChild(ssel);
  panel.appendChild(srow);

  const doc = qDoc(st.type);
  if (!doc) return;
  const params = st.params || (st.params = {});
  doc.params.forEach((pd) => {
    panel.appendChild(qRow(pd, () => params[pd.key], (v) => { params[pd.key] = v; }, () => {}));
  });
}

function renderQGates() {
  const box = $("#qGatesPanel");
  box.innerHTML = "";
  (S.qconfig.gates || []).forEach((g, i) => {
    const row = document.createElement("div");
    row.className = "gateRow";
    const head = document.createElement("div");
    head.className = "ghead";
    const sel = document.createElement("select");
    S.catalog.quantum.gates.forEach((d) => {
      const o = document.createElement("option");
      o.value = d.type; o.textContent = d.label;
      if (g.type === d.type) o.selected = true;
      sel.appendChild(o);
    });
    sel.addEventListener("change", () => {
      g.type = sel.value;
      g.params = {};
      const doc = qGateDoc(g.type);
      if (doc) doc.params.forEach((pd) => { if (pd.default !== undefined) g.params[pd.key] = clone(pd.default); });
      renderQuantum();
    });
    head.appendChild(sel);
    const del = document.createElement("button");
    del.textContent = "删除";
    del.addEventListener("click", () => { S.qconfig.gates.splice(i, 1); renderQuantum(); qRun(); });
    head.appendChild(del);
    row.appendChild(head);
    const doc = qGateDoc(g.type);
    if (doc) {
      const params = g.params || (g.params = {});
      doc.params.forEach((pd) => {
        row.appendChild(qRow(pd, () => params[pd.key], (v) => { params[pd.key] = v; }, () => {}));
      });
    }
    box.appendChild(row);
  });
}

function renderQuantum() {
  renderQConfigPanel();
  renderQGates();
}

function scheduleQuantum() {
  clearTimeout(S.qtimer);
  S.qtimer = setTimeout(qRun, 350);
}

// Build the JSON payload for the /api/quantum endpoint (occupation converted
// from a comma string to an int array).
function buildQuantumPayload() {
  const cfg = {
    modes: S.qconfig.modes,
    cutoff: S.qconfig.cutoff,
    state: S.qconfig.state,
    gates: S.qconfig.gates || [],
  };
  if (cfg.state.type === "fock" && typeof cfg.state.params.occupation === "string") {
    const occ = cfg.state.params.occupation.split(",").map((s) => parseInt(s.trim(), 10)).filter((n) => !Number.isNaN(n));
    cfg.state = { type: cfg.state.type, params: Object.assign({}, cfg.state.params, { occupation: occ }) };
  }
  return cfg;
}

async function qRun() {
  if (!S.qconfig) return;
  try {
    const r = await fetch("/api/quantum", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(buildQuantumPayload()),
    });
    const j = await r.json();
    if (!r.ok) { setStatus("量子模拟错误: " + (j.error || r.status), true); return; }
    S.qresult = j;
    renderQResults(j);
    setStatus("量子模拟完成（" + j.modes + " 模式，cutoff " + j.cutoff + "）");
  } catch (e) {
    setStatus("量子请求失败: " + e.message, true);
  }
}

async function qExportPng() {
  if (!S.qconfig) return;
  try {
    const r = await fetch("/api/quantum?fmt=png", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(buildQuantumPayload()),
    });
    if (!r.ok) { setStatus("量子 PNG 导出失败: " + r.status, true); return; }
    const blob = await r.blob();
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = "quantum-chart.png";
    document.body.appendChild(a);
    a.click();
    a.remove();
    setTimeout(() => URL.revokeObjectURL(url), 1000);
    setStatus("已导出 quantum-chart.png");
  } catch (e) {
    setStatus("量子 PNG 导出失败: " + e.message, true);
  }
}

async function qExportSvg() {
  if (!S.qconfig) return;
  try {
    const r = await fetch("/api/quantum?fmt=svg", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(buildQuantumPayload()),
    });
    if (!r.ok) { setStatus("量子 SVG 导出失败: " + r.status, true); return; }
    const blob = await r.blob();
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = "quantum-chart.svg";
    document.body.appendChild(a);
    a.click();
    a.remove();
    setTimeout(() => URL.revokeObjectURL(url), 1000);
    setStatus("已导出 quantum-chart.svg");
  } catch (e) {
    setStatus("量子 SVG 导出失败: " + e.message, true);
  }
}

function renderQResults(res) {
  const box = $("#qResults");
  box.innerHTML = "";
  // per-mode photon statistics
  const statGrid = document.createElement("div");
  statGrid.className = "qgrid";
  for (let m = 0; m < res.modes; m++) {
    const card = document.createElement("div");
    card.className = "qcard";
    const h = document.createElement("h3");
    h.textContent = "模式 " + m + "：⟨n⟩=" + fmtNum(res.mean_photons[m]) + "  g²(0)=" + fmtNum(res.g2[m]);
    card.appendChild(h);
    const dist = res.photon_distributions[m];
    let mx = 0;
    for (const p of dist) if (p > mx) mx = p;
    for (let n = 0; n < dist.length; n++) {
      const r = document.createElement("div");
      r.className = "qbarRow";
      const lab = document.createElement("span");
      lab.className = "n"; lab.textContent = n;
      r.appendChild(lab);
      const bar = document.createElement("span");
      bar.className = "qbar";
      bar.style.width = (mx > 0 ? Math.max(1, (dist[n] / mx) * 100) : 0) + "%";
      r.appendChild(bar);
      const p = document.createElement("span");
      p.className = "p"; p.textContent = fmtNum(dist[n]);
      r.appendChild(p);
      card.appendChild(r);
    }
    const q = res.quadratures[m];
    const qline = document.createElement("div");
    qline.className = "hint";
    qline.textContent = "x：⟨x⟩=" + fmtNum(q.mean_x) + " Var=" + fmtNum(q.var_x) + "  p：⟨p⟩=" + fmtNum(q.mean_p) + " Var=" + fmtNum(q.var_p);
    card.appendChild(qline);
    statGrid.appendChild(card);
  }
  box.appendChild(statGrid);

  // joint distributions
  for (const key of Object.keys(res.joint_distributions || {})) {
    const [m0, m1] = key.split(",").map(Number);
    const base = res.cutoff + 1;
    const flat = res.joint_distributions[key];
    const nShow = Math.min(base, 6);
    const card = document.createElement("div");
    card.className = "qcard";
    const h = document.createElement("h3");
    h.textContent = "联合分布 P(n" + m0 + ", n" + m1 + ")";
    card.appendChild(h);
    const table = document.createElement("table");
    table.className = "qtable";
    let html = "<tr><th>n" + m0 + "\\n" + m1 + "</th>";
    for (let b = 0; b < nShow; b++) html += "<th>" + b + "</th>";
    html += "</tr>";
    for (let a = 0; a < nShow; a++) {
      html += "<tr><th>" + a + "</th>";
      for (let b = 0; b < nShow; b++) {
        html += "<td>" + fmtNum(flat[a * base + b]) + "</td>";
      }
      html += "</tr>";
    }
    table.innerHTML = html;
    card.appendChild(table);
    box.appendChild(card);
  }
}

// ---------------- 中心画布的鼠标交互（读取偏振/相位、3D 旋转缩放） ----------------
function pixelAt(cv, e) {
  const p = curPlane();
  if (!p) return null;
  const r = cv.getBoundingClientRect();
  if (!r.width || !r.height) return null;
  const i = Math.floor((e.clientX - r.left) / r.width * p.size);
  const j = Math.floor((e.clientY - r.top) / r.height * p.size);
  if (i < 0 || j < 0 || i >= p.size || j >= p.size) return null;
  return [i, j];
}

// 拖动方向偏好（记忆在浏览器本地；隐私模式下不可用时静默忽略）。
function loadCamPref() {
  try {
    const v = localStorage.getItem("wos.camInvert");
    if (v !== null) S.camInvert = v === "1";
  } catch (e) { /* 忽略 */ }
}
function saveCamPref() {
  try { localStorage.setItem("wos.camInvert", S.camInvert ? "1" : "0"); } catch (e) { /* 忽略 */ }
}
function renderInvertBtn() {
  const b = $("#invRotBtn");
  if (!b) return;
  b.textContent = "拖动方向：" + (S.camInvert ? "反向" : "正向");
  b.setAttribute("aria-pressed", S.camInvert ? "true" : "false");
  b.classList.toggle("on", S.camInvert);
  b.title = S.camInvert
    ? "当前“反向”：画面跟着指针走。点击切回正向。"
    : "当前“正向”：画面与指针方向相反。点击切回反向（默认）。";
}
function toggleCamInvert() {
  S.camInvert = !S.camInvert;
  saveCamPref();
  renderInvertBtn();
  render3D();
}

function wireCanvasMouse() {
  const cv = $("#view");
  // 读数按需展开：只有在中心图像上点击，才会显示下方该点的偏振/相位/琼斯读数卡片。
  cv.addEventListener("click", (e) => {
    if (S.view === "scene3d" || S.hidePattern) return;
    const pt = pixelAt(cv, e);
    if (!pt) return;
    S.inspectXY = pt;
    inspectAt(pt[0], pt[1]);
  });

  const c3 = $("#view3d");
  c3.addEventListener("mousedown", (e) => {
    const pan = e.button === 2 || e.shiftKey;
    S.drag3d = { x: e.clientX, y: e.clientY, pan: pan, sx: S.cam.panX, sy: S.cam.panY, yaw: S.cam.yaw, pitch: S.cam.pitch };
    e.preventDefault();
  });
  window.addEventListener("mousemove", (e) => {
    const d = S.drag3d;
    if (!d) return;
    const dx = e.clientX - d.x, dy = e.clientY - d.y;
    if (d.pan) { S.cam.panX = d.sx + dx; S.cam.panY = d.sy + dy; }
    else {
      // 默认反向（camInvert）：画面跟着指针走，与多数三维查看器的“抓住转”一致；
      // 左下角「拖动方向」按钮可切回正向。
      const sgn = S.camInvert ? -1 : 1;
      S.cam.yaw = d.yaw + sgn * dx * 0.008;
      S.cam.pitch = Math.max(-1.45, Math.min(1.45, d.pitch + sgn * dy * 0.006));
    }
    render3D();
  });
  window.addEventListener("mouseup", () => { S.drag3d = null; });
  c3.addEventListener("contextmenu", (e) => e.preventDefault());
  c3.addEventListener("wheel", (e) => {
    e.preventDefault();
    const f = e.deltaY > 0 ? 0.9 : 1.111;
    S.cam.zoom = Math.max(0.2, Math.min(8, S.cam.zoom * f));
    render3D();
  }, { passive: false });
  c3.addEventListener("dblclick", () => {
    S.cam = { yaw: -0.72, pitch: 0.52, zoom: 1, panX: 0, panY: 0 };
    render3D();
  });
}

// ---------------- 剖面曲线导出为 SVG ----------------
function svgEsc(s) {
  return String(s).replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;").replace(/"/g, "&quot;");
}

async function exportProfileSvg() {
  const p = curPlane();
  if (!p) { setStatus("没有可导出的输出平面", true); return; }
  const field = profileField(S.view);
  const axes = ["x", "y"];
  const panels = [];
  for (const ax of axes) {
    const q = new URLSearchParams({ axis: ax, field: field });
    if (S.channel >= 0) q.set("part", String(S.channel));
    const r = await fetch("/api/runs/" + S.runId + "/profiles/" + p.id + "?" + q.toString());
    if (!r.ok) continue;
    panels.push(await r.json());
  }
  if (!panels.length) { setStatus("剖面导出失败：无数据", true); return; }
  const W = 720, H = 260, PAD = 46;
  let svg = '<?xml version="1.0" encoding="UTF-8"?>\n';
  svg += '<svg xmlns="http://www.w3.org/2000/svg" width="' + W + '" height="' + (H * panels.length + 30) + '" viewBox="0 0 ' + W + ' ' + (H * panels.length + 30) + '">\n';
  svg += '<rect width="100%" height="100%" fill="#fbfdff"/>\n';
  svg += '<text x="' + PAD + '" y="20" font-family="sans-serif" font-size="13" fill="#14324a">' +
    svgEsc((p.label || p.id) + " · " + (VIEW_LABEL[S.view] || field) + (S.channel >= 0 ? " · 通道 " + S.channel : "") + " · " + fmtNum(p.stats.power) + " W") + '</text>\n';
  panels.forEach((prof, pi) => {
    const y0 = 30 + pi * H;
    let vmin = Infinity, vmax = -Infinity;
    prof.v.forEach((v) => { if (Number.isFinite(v)) { if (v < vmin) vmin = v; if (v > vmax) vmax = v; } });
    if (!Number.isFinite(vmin)) { vmin = 0; vmax = 1; }
    if (vmax - vmin < 1e-30) vmax = vmin + 1e-30;
    svg += '<rect x="' + PAD + '" y="' + (y0 + 26) + '" width="' + (W - PAD * 2) + '" height="' + (H - 70) + '" fill="#fff" stroke="#cddbe8"/>\n';
    svg += '<text x="' + PAD + '" y="' + (y0 + 46) + '" font-family="sans-serif" font-size="12" fill="#33475c">' +
      svgEsc((prof.axis === "y" ? "纵向 (y)" : "横向 (x)") + " 切割位置 " + fmtNum(prof.coord) + " m · " + (prof.unit || "") +
        " 范围 " + fmtNum(vmin) + " ~ " + fmtNum(vmax)) + '</text>\n';
    let d = "", started = false;
    prof.v.forEach((v, i) => {
      if (!Number.isFinite(v)) { started = false; return; }
      const px = PAD + (i / (prof.v.length - 1)) * (W - PAD * 2);
      const py = y0 + 26 + (H - 70) * (1 - (v - vmin) / (vmax - vmin));
      d += (started ? " L " : " M ") + px.toFixed(2) + " " + py.toFixed(2);
      started = true;
    });
    svg += '<path d="' + d + '" fill="none" stroke="' + (viewIsPhase() ? "#c96f00" : "#1460c8") + '" stroke-width="1.5"/>\n';
  });
  svg += "</svg>\n";
  const blob = new Blob([svg], { type: "image/svg+xml" });
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = "wos-profile.svg";
  document.body.appendChild(a);
  a.click();
  a.remove();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
  setStatus("已导出剖面曲线 wos-profile.svg");
}

// ---------------- 按钮与输入控件接线 ----------------
function wireUI() {
  $("#runBtn").addEventListener("click", () => { if (S.mode === "quantum") qRun(); else run(); });
  $("#newBtn").addEventListener("click", newFile);
  $("#saveBtn").addEventListener("click", saveFile);
  $("#openBtn").addEventListener("click", openFilePicker);
  $("#fileOpen").addEventListener("change", (ev) => {
    const f = ev.target.files && ev.target.files[0];
    if (f) openFile(f);
    ev.target.value = ""; // 允许重复打开同一文件
  });
  $("#helpBtn").addEventListener("click", openHelp);
  $("#helpClose").addEventListener("click", closeHelp);
  $("#insertBtn").addEventListener("click", openInsert);
  $("#insClose").addEventListener("click", () => hideOverlay($("#insertOverlay")));
  $("#insFilter").addEventListener("input", () => { S.insertSel = 0; renderInsertList($("#insFilter").value); });
  $("#dupBtn").addEventListener("click", dupComp);
  $("#deleteBtn").addEventListener("click", delComp);
  $("#addSrcBtn").addEventListener("click", addSource);
  $("#dupSrcBtn").addEventListener("click", dupSource);
  $("#delSrcBtn").addEventListener("click", delSource);
  $("#prevPlane").addEventListener("click", () => stepPlane(-1));
  $("#nextPlane").addEventListener("click", () => stepPlane(1));
  $("#hidePatternBtn").addEventListener("click", togglePattern);
  $("#exportSvgBtn").addEventListener("click", () => { exportProfileSvg().catch((e) => setStatus("导出失败: " + e.message, true)); });
  $("#profBtn").addEventListener("click", () => {
    S.profileAxis = S.profileAxis ? null : "x";
    renderProfileBtn();
    drawProfile();
    if (S.profileAxis) drawProfile();
  });
  $("#scaleBtn").addEventListener("click", () => {
    S.scaleTouched = true;
    S.scale = effScale(S.view) === "log" ? "lin" : "log";
    $("#scaleBtn").textContent = "l " + (S.scale === "log" ? "对数" : "线性");
    renderView();
  });
  $("#presetSel").addEventListener("change", (ev) => {
    const v = ev.target.value;
    if (v === "") return;
    applyPreset(Number(v)).catch((e) => setStatus("载入模板失败: " + e.message, true));
    ev.target.value = "";
  });
  $("#exposure").addEventListener("input", (ev) => {
    S.exposure = Number(ev.target.value) || 0;
    clearTimeout(S.expTimer);
    S.expTimer = setTimeout(() => { if (S.view === "color") renderView(); }, 120);
  });
  $$("#viewTabs button[data-view]").forEach((b) => {
    b.addEventListener("click", () => setView(b.dataset.view));
  });
  $("#waveModeBtn").addEventListener("click", () => setMode("wave"));
  $("#quantumModeBtn").addEventListener("click", () => setMode("quantum"));
  $("#qAddGate").addEventListener("click", () => {
    S.qconfig.gates.push({ type: "beam_splitter", params: { mode0: 0, mode1: 1, reflectivity: 0.5 } });
    renderQuantum(); qRun();
  });
  $("#invRotBtn").addEventListener("click", toggleCamInvert);
  $("#qExportPng").addEventListener("click", qExportPng);
  $("#qExportSvg").addEventListener("click", qExportSvg);
  $("#jsonApply").addEventListener("click", applyJson);
  $("#jsonClose").addEventListener("click", closeJson);
  window.addEventListener("resize", () => {
    if (S.view === "scene3d") render3D();
    else { drawProfile(); if (S.view === "pol") redrawView(); }
  });
}

function renderProfileBtn() {
  const b = $("#profBtn");
  if (!b) return;
  b.classList.toggle("active", !!S.profileAxis);
  b.textContent = S.profileAxis ? "p 剖面 " + (S.profileAxis === "x" ? "横向" : "纵向") : "p 剖面";
}

// ---------------- init ----------------
async function init() {
  let cat = null;
  try {
    const r = await fetch("/api/catalog");
    if (!r.ok) throw new Error("catalog " + r.status);
    cat = await r.json();
  } catch (e) {
    setStatus("目录加载失败: " + e.message, true);
    return;
  }
  S.catalog = cat;
  S.catalog.classes = S.catalog.classes || {};
  S.catalog.polarizations = S.catalog.polarizations || [{ key: "x", label: "线偏振 (x)" }];
  S.qconfig = typeof blankQuantumConfig === "function" ? blankQuantumConfig() : null;

  // 默认载入“迈克尔逊干涉仪”模板（若目录里有），否则用空白场景。
  const exs = S.catalog.examples || [];
  let cfg = blankConfig();
  const defIdx = exs.findIndex((x) => x.name && x.name.indexOf("迈克尔逊") >= 0);
  if (defIdx >= 0) cfg = clone(exs[defIdx].config);
  S.config = normalizeConfig(cfg);
  if (!S.config.scene) S.config.scene = { components: [] };
  if (!Array.isArray(S.config.sources) || !S.config.sources.length) S.config.sources = blankConfig().sources;
  resetViewState();

  fillPresetSelect();
  loadCamPref();
  wireUI();
  renderInvertBtn();
  wireCanvasMouse();
  renderProfileBtn();
  $("#exposure").value = "0";
  setMode("wave");          // 设定界面可见性并渲染全部面板
  setView(S.view);
  renderPatternVisibility();
  clearInspect();
  run();
}

init();
