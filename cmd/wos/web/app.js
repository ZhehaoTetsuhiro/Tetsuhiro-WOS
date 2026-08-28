"use strict";
/* Tetsuhiro WOS 波动光学模拟器 — 鼠标与键盘双可用前端。
 * 全部控件为原生表单元素（Tab/方向键/Enter 原生可用），另提供全局快捷键。
 * 焦点位于表单控件内时键盘完全交还原生行为（数字框可输入科学计数法 e/E、
 * ↑/↓ 按步长步进），全局快捷键（f 定位、q/e 平面、n 新建、o 打开、s 保存等）
 * 在焦点离开控件后生效。Enter 在数字/文本框内确认并移出焦点。
 * 顶部可在「波动光学」与「量子光学」模式间切换（m 键或点击按钮）。
 */

// ---------------- state ----------------
const S = {
  catalog: null,
  config: null,
  runId: null,
  meta: null,
  busy: false,
  dirty: false,      // busy 期间的变更：完成后自动补算
  planeIdx: 0,
  view: "total",
  scale: "log",
  elIdx: 0,
  ctx: [],           // [{bsIndex, label}] 臂上下文栈
  autoRun: false,     // 默认关闭：修改参数后不自动重算，需手动「▶ 运行」
  profileAxis: null, // null | 'x' | 'y'
  hidePattern: false, // 是否隐藏中心输出图案
  cache: new Map(),
  timer: null,
  insertSel: 0,
  mode: "wave",      // "wave" | "quantum"
  qconfig: null,     // quantum optics config
  qresult: null,
  qtimer: null,
};

const $ = (sel) => document.querySelector(sel);

const clone = (o) => structuredClone(o);
const isPow2 = (n) => n > 0 && (n & (n - 1)) === 0;

function fmtNum(v, d) {
  d = d === undefined ? 4 : d;
  if (v === null || v === undefined || Number.isNaN(v)) return "\u2013";
  if (v === 0) return "0";
  const a = Math.abs(v);
  if (a >= 1e4 || a < 1e-3) return v.toExponential(2);
  return v.toFixed(d);
}

// ---------------- current train accessors ----------------
function curList() {
  let arr = S.config.elements;
  for (const c of S.ctx) arr = arr[c.bsIndex].params.reflected_arm.elements;
  return arr;
}
function curArmId() {
  return S.ctx.map((c) => c.label).join(".");
}
function bsChildrenIn(list) {
  const out = [];
  list.forEach((el, i) => { if (el.type === "beamsplitter") out.push("bs" + out.length); });
  return out;
}
// 进入当前选中分束器的反射臂子光路（与「编辑反射臂光路 →」按钮一致）。
function enterReflectedArm() {
  const list = curList();
  const i = S.elIdx;
  const el = list[i];
  if (!el || el.type !== "beamsplitter") return;
  const nBs = bsChildrenIn(list.slice(0, i)).length;
  S.ctx.push({ bsIndex: i, label: "bs" + nBs });
  S.elIdx = 0;
  renderAll();
}
function docFor(type) { return S.catalog.elements.find((d) => d.type === type); }
function srcDocFor(type) { return S.catalog.sources.find((d) => d.type === type); }

// ---------------- rendering ----------------
function renderBreadcrumb() {
  const el = $("#breadcrumb");
  el.innerHTML = "";
  const btn = document.createElement("button");
  btn.textContent = "主光路";
  btn.className = S.ctx.length === 0 ? "cur" : "";
  btn.addEventListener("click", () => { S.ctx = []; renderAll(); });
  el.appendChild(btn);
  S.ctx.forEach((c, i) => {
    el.appendChild(document.createTextNode(" / "));
    const b = document.createElement("button");
    b.textContent = "反射臂 " + c.label;
    b.addEventListener("click", () => { S.ctx = S.ctx.slice(0, i + 1); renderAll(); });
    el.appendChild(b);
  });
}

function elSummary(el) {
  const p = el.params || {};
  switch (el.type) {
    case "propagate": return "z=" + fmtNum(p.distance) + "m";
    case "lens": return "f=" + fmtNum(p.f) + "m";
    case "aperture": {
      const names = { circle: "圆孔", square: "方孔", rectangle: "矩孔", ellipse: "椭圆孔", triangle: "三角孔", ring: "环形孔", polygon: "多边形孔", double_slit: "双缝", cross: "十字孔", star: "星形孔", superellipse: "超椭圆孔", custom: "自定义孔" };
      return names[p.shape] || p.shape || "circle";
    }
    case "grating": return (p.kind || "") + " Λ=" + fmtNum(p.period) + "m";
    case "sensor": return p.label || "";
    case "beamsplitter": return "R=" + fmtNum(p.reflectivity);
    case "combiner": return ((p.outputs || []).length) + " 端口";
    case "mirror": return p.curvature ? "R=" + fmtNum(1 / p.curvature) + "m" : "平面";
    case "zernike": return "像差板";
    case "polarizer": return "θ=" + fmtNum(p.angle) + "rad";
    case "retarder": return "δ=" + fmtNum(p.retardance) + "rad";
    default: return "";
  }
}

function renderElList() {
  const list = $("#elList");
  list.innerHTML = "";
  curList().forEach((el, i) => {
    const b = document.createElement("button");
    b.className = "el" + (i === S.elIdx ? " sel" : "");
    b.setAttribute("role", "listitem");
    const doc = docFor(el.type);
    b.textContent = (doc ? doc.label : el.type) + " ";
    const s = elSummary(el);
    if (s) {
      const sp = document.createElement("span");
      sp.className = "tag";
      sp.textContent = s;
      b.appendChild(sp);
    }
    b.addEventListener("click", () => { S.elIdx = i; renderElList(); renderParams(); });
    list.appendChild(b);
  });
  if (S.elIdx >= curList().length) S.elIdx = Math.max(0, curList().length - 1);
  const sel = list.querySelector(".sel");
  if (sel) sel.scrollIntoView({ block: "nearest" });
}

// parameter control builders -------------------------------------------------
// 数值输入框绑定：合法数字在 input 时实时提交（配合 350ms 防抖自动重算）；
// change（Enter/Tab/失焦）时提交并规范化显示，空/非法输入回退为当前参数值，
// 避免把空值静默当作 0 写入配置。
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

function renderParams() {
  const panel = $("#paramPanel");
  panel.innerHTML = "";
  const list = curList();
  const el = list[S.elIdx];
  if (!el) { panel.innerHTML = "<p class=hint>光路为空：按 i 插入元件。</p>"; return; }
  const doc = docFor(el.type);
  if (!doc) { panel.innerHTML = "<p class=hint>未知元件类型 " + el.type + "。</p>"; return; }
  const h = document.createElement("div");
  h.innerHTML = "<b>" + doc.label + "</b> <span class=hint>" + (doc.help || "") + "</span>";
  panel.appendChild(h);
  const params = el.params || (el.params = {});
  doc.params.forEach((pd) => {
    if (pd.kind !== "nested" && !paramVisible(pd, params)) return;
    if (pd.kind === "nested") {
      const row = document.createElement("div");
      row.className = "prow";
      if (pd.key === "reflected_arm") {
        const b = document.createElement("button");
        b.textContent = "编辑反射臂光路 →";
        b.title = "进入该分束器的反射臂子光路（Enter 直接进入，Esc 返回）";
        b.addEventListener("click", () => { enterReflectedArm(); });
        row.appendChild(b);
      } else if (pd.key === "outputs") {
        const b = document.createElement("button");
        b.textContent = "编辑合束端口 ↓";
        b.addEventListener("click", () => { renderCombiner(el, panel); });
        row.appendChild(b);
      }
      panel.appendChild(row);
      return;
    }
    let onChoice;
    if (pd.kind === "choice" && hasDependent(doc.params, pd.key)) {
      const key = pd.key;
      onChoice = () => { renderParams(); const s = panel.querySelector('select[data-key="' + key + '"]'); if (s) s.focus(); };
    }
    panel.appendChild(mkParamRow(pd, () => params[pd.key], (v) => { params[pd.key] = v; }, onChoice));
  });
  if (el.type === "combiner") renderCombiner(el, panel);
}

function armsForCurrent() {
  const list = curList();
  return ["main"].concat(bsChildrenIn(list));
}

function renderCombiner(el, panel) {
  const box = document.createElement("div");
  const outs = el.params.outputs || (el.params.outputs = []);
  const arms = armsForCurrent();
  const draw = () => {
    box.innerHTML = "";
    outs.forEach((o, oi) => {
      const orow = document.createElement("div");
      orow.className = "prow";
      const lab = document.createElement("input");
      lab.type = "text";
      lab.value = o.label || "";
      lab.title = "端口名称";
      lab.addEventListener("change", () => { o.label = lab.value; scheduleRun(); });
      orow.appendChild(lab);
      const del = document.createElement("button");
      del.textContent = "删除端口";
      del.addEventListener("click", () => { outs.splice(oi, 1); renderParams(); });
      orow.appendChild(del);
      box.appendChild(orow);
      (o.weights || []).forEach((w, wi) => {
        const wrow = document.createElement("div");
        wrow.className = "prow";
        const asel = document.createElement("select");
        arms.forEach((a) => {
          const op = document.createElement("option");
          op.value = a; op.textContent = a;
          if (w.arm === a) op.selected = true;
          asel.appendChild(op);
        });
        asel.addEventListener("change", () => { w.arm = asel.value; scheduleRun(); });
        wrow.appendChild(asel);
        const re = document.createElement("input");
        re.type = "number"; re.step = 0.01; re.value = w.re !== undefined ? w.re : 1;
        re.title = "权重实部（支持科学计数法，Enter 确认）";
        bindNumInput(re, () => (w.re !== undefined ? w.re : 1), (v) => { w.re = v; }, () => scheduleRun(), () => scheduleRun());
        wrow.appendChild(re);
        const im = document.createElement("input");
        im.type = "number"; im.step = 0.01; im.value = w.im || 0;
        im.title = "权重虚部（支持科学计数法，Enter 确认）";
        bindNumInput(im, () => (w.im || 0), (v) => { w.im = v; }, () => scheduleRun(), () => scheduleRun());
        wrow.appendChild(im);
        const wdel = document.createElement("button");
        wdel.textContent = "×";
        wdel.addEventListener("click", () => { o.weights.splice(wi, 1); renderParams(); });
        wrow.appendChild(wdel);
        box.appendChild(wrow);
      });
      const addW = document.createElement("button");
      addW.textContent = "+ 权重项";
      addW.addEventListener("click", () => { o.weights.push({ arm: "main", re: 1, im: 0 }); renderParams(); });
      box.appendChild(addW);
    });
    const addO = document.createElement("button");
    addO.textContent = "+ 输出端口";
    addO.addEventListener("click", () => { outs.push({ label: "out" + outs.length, weights: [] }); renderParams(); });
    box.appendChild(addO);
  };
  draw();
  panel.appendChild(box);
}

function clampGridSize(v) {
  let n = Math.round(v);
  if (n % 2 !== 0) n += 1; // 内核校验要求偶数
  return Math.max(2, Math.min(65536 * 4, n)); // 上限 65536×4 = 262144
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

  // 自定义边长行（仅当选择“自定义”时显示）
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
  panel.appendChild(mk("波长", "nm", () => g.wavelength * 1e9, (v) => { g.wavelength = v * 1e-9; }, { step: 0.1, min: 1 }));

  const brow = document.createElement("div");
  brow.className = "prow";
  brow.appendChild(Object.assign(document.createElement("label"), { textContent: "琼斯偏振" }));
  const cb = document.createElement("input");
  cb.type = "checkbox";
  cb.checked = g.polarized;
  cb.addEventListener("change", () => { g.polarized = cb.checked; scheduleRun(); });
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

function renderSource() {
  const panel = $("#sourcePanel");
  panel.innerHTML = "";
  const src = S.config.source;
  const row = document.createElement("div");
  row.className = "prow";
  row.appendChild(Object.assign(document.createElement("label"), { textContent: "光源类型" }));
  const sel = document.createElement("select");
  S.catalog.sources.forEach((d) => {
    const o = document.createElement("option");
    o.value = d.type; o.textContent = d.label;
    if (src.type === d.type) o.selected = true;
    sel.appendChild(o);
  });
  sel.addEventListener("change", () => {
    src.type = sel.value; src.params = {};
    renderSource(); scheduleRun();
    // 面板重绘会销毁旧控件，把焦点交还给新的类型下拉，便于连续键盘调整
    const again = panel.querySelector("select");
    if (again) again.focus();
  });
  row.appendChild(sel);
  panel.appendChild(row);
  const doc = srcDocFor(src.type);
  if (!doc) return;
  const params = src.params || (src.params = {});
  doc.params.forEach((pd) => {
    panel.appendChild(mkParamRow(pd, () => params[pd.key], (v) => { params[pd.key] = v; }));
  });
  if (S.config.polarized) {
    const prow = document.createElement("div");
    prow.className = "prow";
    prow.appendChild(Object.assign(document.createElement("label"), { textContent: "偏振态" }));
    const psel = document.createElement("select");
    psel.id = "polSel";
    S.catalog.polarizations.forEach((p) => {
      const o = document.createElement("option");
      o.value = p.key; o.textContent = p.label;
      if ((params.polarization || "x") === p.key) o.selected = true;
      psel.appendChild(o);
    });
    psel.addEventListener("change", () => {
      params.polarization = psel.value;
      if (psel.value === "custom") { params.jx_re = 1; params.jx_im = 0; params.jy_re = 0; params.jy_im = 0; }
      renderSource(); scheduleRun();
      const again = $("#polSel");
      if (again) again.focus();
    });
    prow.appendChild(psel);
    panel.appendChild(prow);
    if (params.polarization === "custom") {
      [["jx_re", "Jx 实部"], ["jx_im", "Jx 虚部"], ["jy_re", "Jy 实部"], ["jy_im", "Jy 虚部"]].forEach(([k, l]) => {
        const r2 = document.createElement("div");
        r2.className = "prow";
        r2.appendChild(Object.assign(document.createElement("label"), { textContent: l }));
        const inp = document.createElement("input");
        inp.type = "number"; inp.step = 0.1; inp.value = params[k] !== undefined ? params[k] : (k.endsWith("re") ? 1 : 0);
        bindNumInput(inp, () => params[k], (v) => { params[k] = v; }, () => scheduleRun(), () => scheduleRun());
        r2.appendChild(inp);
        panel.appendChild(r2);
      });
    }
  }
}

function renderPlanes() {
  // 输出平面切换只用 q/e 与画布左上角的 ◀/▶（已无平面列表）
  const lab = $("#planeLabel");
  if (!S.meta || !S.meta.planes.length) { if (lab) lab.textContent = "无输出平面"; return; }
  const p = S.meta.planes[S.planeIdx];
  lab.textContent = (p.path ? "[" + p.path + "] " : "") + (p.label || p.id) + " (" + (S.planeIdx + 1) + "/" + S.meta.planes.length + ")";
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
  box.innerHTML =
    "<b>" + (p.label || p.id) + "</b>" + (p.path ? " <span class=hint>[" + p.path + "]</span>" : "") + "\n" +
    "功率 " + fmtNum(st.power) + " W   峰值 " + fmtNum(st.peak) + " W/m²\n" +
    "质心 (" + fmtNum(st.centroid_x) + ", " + fmtNum(st.centroid_y) + ") m\n" +
    "RMS 半径 (" + fmtNum(st.rms_x) + ", " + fmtNum(st.rms_y) + ") m\n" +
    (st.strehl > 0 ? "斯特列尔比 " + st.strehl.toFixed(3) + "\n" : "") +
    "强度范围 [" + fmtNum(st.intensity_min) + ", " + fmtNum(st.intensity_max) + "] W/m²\n" +
    "相位范围 [" + st.phase_min.toFixed(2) + ", " + st.phase_max.toFixed(2) + "] rad";
}

// ---------------- colormaps ----------------
function makeLUT(stops) {
  const lut = new Uint8Array(256 * 3);
  const n = stops.length - 1;
  for (let i = 0; i < 256; i++) {
    const x = (i / 255) * n;
    const k = Math.min(Math.floor(x), n - 1);
    const t = x - k;
    for (let c = 0; c < 3; c++) {
      lut[i * 3 + c] = Math.round(stops[k][c] + (stops[k + 1][c] - stops[k][c]) * t);
    }
  }
  return lut;
}
const INFERNO = makeLUT([[0, 0, 4], [55, 20, 115], [139, 25, 98], [203, 55, 74], [236, 101, 44], [249, 156, 34], [250, 200, 9], [252, 255, 164]]);
function phaseRGB(t) {
  const h = ((240 - 300 * t) % 360 + 360) % 360;
  const s = 0.85, v = 0.95;
  const c = v * s, x = c * (1 - Math.abs(((h / 60) % 2) - 1)), m = v - c;
  let r = 0, g = 0, b = 0;
  if (h < 60) { r = c; g = x; } else if (h < 120) { r = x; g = c; }
  else if (h < 180) { g = c; b = x; } else if (h < 240) { g = x; b = c; }
  else if (h < 300) { r = x; b = c; } else { r = c; b = x; }
  return [Math.round((r + m) * 255), Math.round((g + m) * 255), Math.round((b + m) * 255)];
}

// ---------------- data + rendering ----------------
async function fetchPlane(pid, view) {
  const key = S.runId + "/" + pid + "/" + view;
  if (S.cache.has(key)) return S.cache.get(key);
  const r = await fetch("/api/runs/" + S.runId + "/planes/" + pid + "?field=" + view + "&fmt=bin");
  if (!r.ok) throw new Error("plane fetch " + r.status);
  const buf = await r.arrayBuffer();
  const arr = new Float32Array(buf);
  S.cache.set(key, arr);
  if (S.cache.size > 64) S.cache.delete(S.cache.keys().next().value);
  return arr;
}

function renderView() {
  const cv = $("#view");
  if (!S.meta || !S.meta.planes.length) { cv.getContext("2d").clearRect(0, 0, cv.width, cv.height); return; }
  const p = S.meta.planes[S.planeIdx];
  const n = p.size;
  const isPhase = S.view === "phase_x" || S.view === "phase_y" || S.view === "phase_z";
  const rid = S.runId;
  fetchPlane(p.id, S.view).then((arr) => {
    if (S.runId !== rid) return; // 过期响应（新的运行已开始）丢弃
    cv.width = n; cv.height = n;
    const ctx = cv.getContext("2d");
    const img = ctx.createImageData(n, n);
    const d = img.data;
    let vmin, vmax;
    if (isPhase) { vmin = -Math.PI; vmax = Math.PI; }
    else { vmin = p.stats.intensity_min; vmax = p.stats.intensity_max; }
    const dyn = 1e4;
    for (let i = 0; i < n * n; i++) {
      const v = arr[i];
      let t;
      if (isPhase) t = (v - vmin) / (vmax - vmin);
      else if (S.scale === "log") t = Math.log10(1 + Math.max(v - vmin, 0) / (vmax - vmin) * (dyn - 1)) / Math.log10(dyn);
      else t = (v - vmin) / (vmax - vmin);
      if (t < 0) t = 0; if (t > 1) t = 1;
      let r, g, b;
      if (isPhase) [r, g, b] = phaseRGB(t);
      else {
        const li = (t * 255) | 0;
        r = INFERNO[li * 3]; g = INFERNO[li * 3 + 1]; b = INFERNO[li * 3 + 2];
      }
      d[i * 4] = r; d[i * 4 + 1] = g; d[i * 4 + 2] = b; d[i * 4 + 3] = 255;
    }
    ctx.putImageData(img, 0, 0);
    drawProfile();
  }).catch((e) => setStatus("视图加载失败: " + e.message, true));
}

function drawProfile() {
  const pc = $("#prof");
  const ctx = pc.getContext("2d");
  const wrap = $("#canvasWrap");
  pc.width = wrap.clientWidth; pc.height = wrap.clientHeight;
  ctx.clearRect(0, 0, pc.width, pc.height);
  if (!S.profileAxis || !S.meta || !S.meta.planes.length) return;
  const p = S.meta.planes[S.planeIdx];
  fetch("/api/runs/" + S.runId + "/profiles/" + p.id + "?axis=" + S.profileAxis + "&field=" + S.view)
    .then((r) => r.json())
    .then((prof) => {
      if (!S.profileAxis) return;
      const x = prof.x, v = prof.v;
      let vmin = Infinity, vmax = -Infinity;
      for (const y of v) { if (y < vmin) vmin = y; if (y > vmax) vmax = y; }
      if (vmax - vmin < 1e-30) return;
      const isPhase = S.view === "phase_x" || S.view === "phase_y" || S.view === "phase_z";
      const pad = 30, w = pc.width - 2 * pad, h = pc.height - 2 * pad;
      ctx.strokeStyle = "rgba(20,40,60,.25)";
      ctx.beginPath(); ctx.moveTo(pad, pc.height - pad); ctx.lineTo(pc.width - pad, pc.height - pad); ctx.stroke();
      ctx.strokeStyle = isPhase ? "#c96f00" : "#1460c8";
      ctx.lineWidth = 1.5;
      ctx.beginPath();
      for (let i = 0; i < x.length; i++) {
        const px = pad + (i / (x.length - 1)) * w;
        const t = (v[i] - vmin) / (vmax - vmin);
        const py = pc.height - pad - t * h;
        if (i === 0) ctx.moveTo(px, py); else ctx.lineTo(px, py);
      }
      ctx.stroke();
      ctx.fillStyle = "#33475c";
      ctx.font = "11px sans-serif";
      ctx.fillText((isPhase ? "相位 rad  范围 " : "强度 W/m²  范围 ") + fmtNum(vmin) + " ~ " + fmtNum(vmax) + "   切割位置 " + fmtNum(prof.coord) + "m", pad, 14);
    }).catch(() => {});
}

function setView(v) {
  S.view = v;
  document.querySelectorAll("#viewTabs button[data-view]").forEach((b) => b.classList.toggle("active", b.dataset.view === v));
  renderView();
}

// 切换输出平面（q 上一个 / e 下一个）；clamped，无平面时无操作。
function stepPlane(d) {
  const n = S.meta ? S.meta.planes.length : 0;
  if (!n) return;
  S.planeIdx = Math.min(n - 1, Math.max(0, S.planeIdx + d));
  renderPlanes(); renderStats(); renderView();
}

// 隐藏/显示中心输出图案（h 键或「隐藏图案 / 显示图案」按钮）。
function togglePattern() {
  S.hidePattern = !S.hidePattern;
  renderPatternVisibility();
}
function renderPatternVisibility() {
  const hidden = !!S.hidePattern;
  // 仅隐藏中心方形图样（#view），剖面曲线（#prof）保持显示，不显示任何占位文字。
  $("#view").hidden = hidden;
  const btn = $("#hidePatternBtn");
  if (btn) {
    btn.textContent = hidden ? "显示图案" : "隐藏图案";
    btn.classList.toggle("active", hidden);
  }
}

// 隐藏对话框并把焦点交还给页面主体，避免焦点残留在隐藏控件里导致全局快捷键失效。
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
        S.planeIdx = Math.min(S.planeIdx, Math.max(0, m.planes.length - 1));
        renderPlanes(); renderWarnings(); renderStats(); renderView();
        setStatus("完成 " + m.elapsed_ms.toFixed(0) + " ms · 网格 " + m.grid.size + "² · 输出 " + m.planes.length + " 平面" +
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

// ---------------- element editing ----------------
function selectEl(delta) {
  const n = curList().length;
  if (!n) return;
  S.elIdx = (S.elIdx + delta + n) % n;
  renderElList(); renderParams();
}
function moveEl(delta) {
  const list = curList();
  const j = S.elIdx + delta;
  if (j < 0 || j >= list.length) return;
  [list[S.elIdx], list[j]] = [list[j], list[S.elIdx]];
  S.elIdx = j;
  renderElList();
  run();
}
function delEl() {
  const list = curList();
  if (!list.length) return;
  const el = list[S.elIdx];
  list.splice(S.elIdx, 1);
  if (el.type === "beamsplitter") {
    // If the deleted BS is on the context stack, pop the context.
    while (S.ctx.length && S.ctx[S.ctx.length - 1].bsIndex >= S.elIdx) S.ctx.pop();
  }
  if (S.elIdx >= list.length) S.elIdx = Math.max(0, list.length - 1);
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
    b.addEventListener("click", () => { insertElement(d.type); });
    if (myIdx === S.insertSel) { b.classList.add("sel"); b.scrollIntoView({ block: "nearest" }); }
    box.appendChild(b);
  });
}
function insertElement(type) {
  const list = curList();
  const el = { type: type, params: {} };
  if (type === "beamsplitter") el.params = { reflectivity: 0.5, phase: 0, reflected_arm: { elements: [] } };
  if (type === "combiner") el.params = { outputs: [{ label: "out", weights: [] }] };
  if (type === "sensor") el.params = { label: "sensor_" + list.length };
  const doc = docFor(type);
  if (doc) doc.params.forEach((pd) => {
    if (pd.kind !== "nested" && pd.default !== undefined) el.params[pd.key] = clone(pd.default);
  });
  list.splice(S.elIdx + 1, 0, el);
  S.elIdx = Math.min(S.elIdx + 1, list.length - 1);
  hideOverlay($("#insertOverlay"));
  renderAll();
  run();
}

// ---------------- overlays ----------------
const HELP_ROWS = [
  ["Tab / Shift+Tab", "在控件间移动焦点（全部操作均可用 Tab 完成）"],
  ["f", "逐个定位参数：当前元件各参数 → 光源各参数 → 全局各参数 → 元件列表（循环；输入框内按 f 同样生效，不输入字符）"],
  ["输入框内", "原生输入：数字框支持科学计数法（如 1e-3）、↑/↓ 按步长步进；Enter 确认并移出焦点"],
  ["↑ / ↓", "选择上一个/下一个元件"],
  ["Shift+↑ / Shift+↓", "移动元件在光路中的顺序"],
  ["i", "在当前元件后插入新元件（输入文字过滤，Enter 插入）"],
  ["d / Delete", "删除当前元件"],
  ["▶ 运行 / 空格 / Ctrl+Enter", "运行模拟（修改参数后不自动运行）"],
  ["Enter", "进入当前分束器的反射臂（选中分束器时；按钮上触发按钮，输入框内确认参数）"],
  ["q / e", "上一个 / 下一个输出平面（焦点不在输入框内时）"],
  ["1 - 7", "视图：总强度 / |Ex|² / |Ey|² / 相位 Ex / 相位 Ey / |Ez|² / 相位 Ez"],
  ["p", "显示/隐藏一维剖面"],
  ["x / y", "剖面方向：横向 / 纵向"],
  ["l", "强度视图 对数/线性 标度切换"],
  ["h", "隐藏/显示中心输出图案"],
  ["a", "自动运行开关（默认关闭）"],
  ["j", "高级：直接编辑配置 JSON"],
  ["n", "新建空白光路"],
  ["o", "打开 JSON 配置文件（内置示例在 examples/presets/ 目录）"],
  ["s", "把当前配置保存为 JSON 文件"],
  ["Esc", "关闭对话框 / 返回上一层光路"],
  ["m", "波动光学 / 量子光学 模式切换"],
  ["? / F1", "打开本帮助（鼠标可直接点击任意按钮操作）"],
];
function openHelp() {
  $("#helpOverlay").hidden = false;
  const t = $("#helpTable");
  t.innerHTML = "";
  HELP_ROWS.forEach(([k, v]) => {
    const item = document.createElement("div");
    item.className = "help-item";
    const key = document.createElement("div");
    key.className = "help-key";
    key.textContent = k;
    const desc = document.createElement("div");
    desc.className = "help-desc";
    desc.textContent = v;
    item.append(key, desc);
    t.appendChild(item);
  });
  $("#helpClose").focus();
}
function closeHelp() { hideOverlay($("#helpOverlay")); }
function openJson() {
  $("#jsonOverlay").hidden = false;
  $("#jsonText").value = JSON.stringify(S.config, null, 2);
  $("#jsonMsg").textContent = "";
  $("#jsonText").focus();
}
function applyJson() {
  try {
    const cfg = normalizeConfig(JSON.parse($("#jsonText").value));
    if (!cfg || typeof cfg !== "object" || !Array.isArray(cfg.elements)) throw new Error("缺少 elements 数组，不是有效的 Tetsuhiro WOS 配置");
    S.config = cfg;
    S.ctx = []; S.elIdx = 0; S.planeIdx = 0; S.meta = null; S.runId = null;
    S.cache.clear();
    hideOverlay($("#jsonOverlay"));
    renderAll();
    run();
  } catch (e) {
    $("#jsonMsg").textContent = "JSON 解析失败: " + e.message;
  }
}
function closeJson() { hideOverlay($("#jsonOverlay")); }

// ---------------- files (新建 / 打开 / 保存) ----------------
// 兼容旧版本导出的配置（ElementSpec/SourceSpec 曾以大写 Type/Params 序列化）。
function normalizeConfig(cfg) {
  cfg.elements = (cfg.elements || []).map((el) => {
    if (el.type === undefined && el.Type !== undefined) el.type = el.Type;
    if (el.params === undefined && el.Params !== undefined) el.params = el.Params;
    return el;
  });
  if (cfg.source) {
    if (cfg.source.type === undefined && cfg.source.Type !== undefined) cfg.source.type = cfg.source.Type;
    if (cfg.source.params === undefined && cfg.source.Params !== undefined) cfg.source.params = cfg.source.Params;
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
    source: { type: "plane", params: { power: 1e-3 } },
    elements: [],
  };
}
function resetViewState() {
  S.ctx = []; S.elIdx = 0; S.planeIdx = 0; S.meta = null; S.runId = null;
  S.cache.clear();
}
function newFile() {
  S.config = blankConfig();
  resetViewState();
  renderAll();
  setStatus("已新建空白光路（按 i 插入元件）");
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
  rd.onload = () => {
    try {
      const cfg = normalizeConfig(JSON.parse(String(rd.result)));
      if (!cfg || typeof cfg !== "object" || !Array.isArray(cfg.elements)) throw new Error("缺少 elements 数组，不是有效的 Tetsuhiro WOS 配置");
      S.config = cfg;
      resetViewState();
      renderAll();
      setStatus("已载入 " + file.name);
      run();
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
  renderBreadcrumb();
  renderElList();
  renderParams();
  renderGlobals();
  renderSource();
  renderPlanes();
  renderWarnings();
  renderStats();
}

// ---------------- keyboard ----------------
// f 定位：按“当前元件参数 → 光源参数 → 全局参数 → 元件列表”的控制顺序逐个循环移动
// 焦点，因此同一个元件/光源的多个参数只需连按 f 即可逐个到达。
// 焦点不在任何控件上时按 f 跳到当前元件的第一个参数；f 被全局拦截、不会输入字符
// （标签/文本里需要字母 f 时请用 j JSON 编辑器）。
function focusableCycle() {
  const out = [];
  ["#paramPanel", "#sourcePanel", "#globalPanel"].forEach((z) => {
    document.querySelectorAll(z + " input, " + z + " select, " + z + " button").forEach((el) => out.push(el));
  });
  const elSel = $("#elList button.sel") || $("#elList button") || $("#insertBtn");
  if (elSel) out.push(elSel);
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
    // 表单控件内：键盘交还原生行为。此前 q/e/n/s/o 在数字框/下拉框内仍被全局
    // 拦截，导致科学计数法的 "e" 无法输入（并被切换平面）、误按 "n" 会清空整条
    // 光路。全局快捷键统一在焦点离开控件后生效。
    // 例外：f 定位在所有输入控件内都生效且不输入字符（文本框内的 f 字符请用 j
    // JSON 编辑器输入），因此连按 f 可在同一元件/光源的多个参数间切换。
    if (e.key === "f" && S.mode === "wave") { e.preventDefault(); jumpFocus(); return; }
    // Enter：数字/文本框确认并移出焦点（change 事件随之提交，之后可直接用全局键）。
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
    case "Enter":
      if (e.ctrlKey) { run(); break; }
      // 焦点不在按钮上时，Enter 进入当前分束器的反射臂（按钮上仍走原生触发，避免双重进入）
      if (tag !== "BUTTON") enterReflectedArm();
      break;
    case "1": setView("total"); break;
    case "2": setView("ex"); break;
    case "3": setView("ey"); break;
    case "4": setView("phase_x"); break;
    case "5": setView("phase_y"); break;
    case "6": setView("ez"); break;
    case "7": setView("phase_z"); break;
    case "f": jumpFocus(); break;
    case "q": stepPlane(-1); break;
    case "e": if (e.ctrlKey) break; stepPlane(1); break;
    case "[": selectEl(-1); break;
    case "]": selectEl(1); break;
    case "ArrowUp": if (e.shiftKey) moveEl(-1); else selectEl(-1); e.preventDefault(); break;
    case "ArrowDown": if (e.shiftKey) moveEl(1); else selectEl(1); e.preventDefault(); break;
    case "i": e.preventDefault(); openInsert(); break;  // 阻止默认动作：避免 "i" 被敲进过滤框
    case "d": case "Delete": delEl(); break;
    case "p": S.profileAxis = S.profileAxis ? null : "x"; drawProfile(); break;
    case "h": togglePattern(); break;
    case "x": if (S.profileAxis) { S.profileAxis = "x"; drawProfile(); } break;
    case "y": if (S.profileAxis) { S.profileAxis = "y"; drawProfile(); } break;
    case "l": S.scale = S.scale === "log" ? "lin" : "log";
      $("#scaleBtn").textContent = "l " + (S.scale === "log" ? "对数" : "线性");
      renderView(); break;
    case "a": S.autoRun = !S.autoRun; setStatus("自动运行 " + (S.autoRun ? "开" : "关"), false); break;
    case "j": openJson(); break;
    case "n": newFile(); break;
    case "s": saveFile(); break;
    case "o": openFilePicker(); break;
    case "Escape": if (S.ctx.length) { S.ctx.pop(); S.elIdx = 0; renderAll(); } break;
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

function svgEscape(s) {
  return String(s).replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;").replace(/"/g, "&quot;");
}

// Render one 1-D profile plot (axes + grid + polyline + labels) into `out`,
// vertically offset by `oy`.
function plotProfileBlock(out, prof, field, axis, oy) {
  const W = 820, H = 350;
  const ml = 78, mr = 22, mt = 44, mb = 48;
  const pw = W - ml - mr, ph = H - mt - mb;
  const x = prof.x, v = prof.v;
  const fieldLabel = { total: "总强度", ex: "|Ex|²", ey: "|Ey|²", ez: "|Ez|²", phase_x: "相位 Ex", phase_y: "相位 Ey", phase_z: "相位 Ez" }[field] || field;
  const unit = (field === "phase_x" || field === "phase_y" || field === "phase_z") ? "rad" : "W/m²";
  const ty = (yy) => (yy + oy).toFixed(1);
  if (!x || !x.length || x.length < 2) {
    out.push('<text x="' + (W / 2) + '" y="' + ty(H / 2) + '" text-anchor="middle" font-size="13" fill="#33475c">无剖面数据（' + axis.toUpperCase() + '）</text>');
    return;
  }
  let vmin = Infinity, vmax = -Infinity;
  for (const y of v) { if (y < vmin) vmin = y; if (y > vmax) vmax = y; }
  if (!(vmax - vmin > 1e-30)) vmax = vmin + 1;
  const xmin = x[0], xmax = x[x.length - 1];
  const X = (xi) => ml + (xi - xmin) / (xmax - xmin) * pw;
  const Y = (vi) => mt + (1 - (vi - vmin) / (vmax - vmin)) * ph;

  let pts = "";
  for (let i = 0; i < x.length; i++) {
    pts += (i ? " " : "") + X(x[i]).toFixed(2) + "," + (Y(v[i]) + oy).toFixed(2);
  }
  // grid
  for (let i = 0; i <= 4; i++) {
    out.push('<line x1="' + ml + '" y1="' + ty(mt + ph * i / 4) + '" x2="' + (W - mr) + '" y2="' + ty(mt + ph * i / 4) + '" stroke="#e3e9ee"/>');
    out.push('<line x1="' + (ml + pw * i / 4).toFixed(1) + '" y1="' + ty(mt) + '" x2="' + (ml + pw * i / 4).toFixed(1) + '" y2="' + ty(H - mb) + '" stroke="#e3e9ee"/>');
  }
  // plot frame
  out.push('<rect x="' + ml + '" y="' + ty(mt) + '" width="' + pw + '" height="' + ph + '" fill="none" stroke="#33475c"/>');
  // curve
  out.push('<polyline points="' + pts + '" fill="none" stroke="#1460c8" stroke-width="1.8"/>');
  // y-axis labels (top = max, bottom = min)
  out.push('<text x="' + (ml - 8) + '" y="' + ty(mt + 4) + '" text-anchor="end" font-size="11" fill="#33475c">' + fmtNum(vmax) + '</text>');
  out.push('<text x="' + (ml - 8) + '" y="' + ty(H - mb + 4) + '" text-anchor="end" font-size="11" fill="#33475c">' + fmtNum(vmin) + '</text>');
  // x-axis labels
  out.push('<text x="' + ml + '" y="' + ty(H - mb + 18) + '" text-anchor="middle" font-size="11" fill="#33475c">' + fmtNum(xmin) + '</text>');
  out.push('<text x="' + (W - mr) + '" y="' + ty(H - mb + 18) + '" text-anchor="middle" font-size="11" fill="#33475c">' + fmtNum(xmax) + '</text>');
  // title
  out.push('<text x="' + (W / 2) + '" y="' + ty(16) + '" text-anchor="middle" font-size="14" font-weight="bold" fill="#14324a">' + svgEscape(fieldLabel) + ' 剖面（' + axis.toUpperCase() + '）</text>');
  out.push('<text x="' + (W / 2) + '" y="' + ty(H - 6) + '" text-anchor="middle" font-size="11" fill="#5a7d9e">位置 (m) · 固定坐标 ' + fmtNum(prof.coord) + ' m · ' + unit + ' 范围 ' + fmtNum(vmin) + ' ~ ' + fmtNum(vmax) + '</text>');
}

// Build a pure-vector SVG containing BOTH the X and Y profiles (stacked).
function buildProfileSVG(profX, profY, field) {
  const W = 820, plotH = 350, gap = 8;
  const H = plotH * 2 + gap;
  const out = [];
  out.push('<svg xmlns="http://www.w3.org/2000/svg" width="' + W + '" height="' + H + '" viewBox="0 0 ' + W + ' ' + H + '" font-family="sans-serif">');
  out.push('<rect width="100%" height="100%" fill="#ffffff"/>');
  plotProfileBlock(out, profX, field, "x", 0);
  plotProfileBlock(out, profY, field, "y", plotH + gap);
  out.push('</svg>');
  return out.join("");
}

// Export the current wave-optics X and Y profiles as a pure-vector SVG.
async function exportWaveSvg() {
  if (!S.meta || !S.meta.planes.length) { setStatus("无输出平面可导出", true); return; }
  const p = S.meta.planes[S.planeIdx];
  const field = S.view;
  try {
    const [profX, profY] = await Promise.all([
      fetch("/api/runs/" + S.runId + "/profiles/" + p.id + "?axis=x&field=" + field).then((r) => r.json()),
      fetch("/api/runs/" + S.runId + "/profiles/" + p.id + "?axis=y&field=" + field).then((r) => r.json()),
    ]);
    const svg = buildProfileSVG(profX, profY, field);
    const blob = new Blob([svg], { type: "image/svg+xml" });
    const a = document.createElement("a");
    a.href = URL.createObjectURL(blob);
    a.download = "wos-profile.svg";
    document.body.appendChild(a);
    a.click();
    a.remove();
    setTimeout(() => URL.revokeObjectURL(a.href), 1000);
    setStatus("已导出 wos-profile.svg（" + field + " · X/Y 两个剖面）");
  } catch (e) {
    setStatus("导出 SVG 失败: " + e.message, true);
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

// ---------------- init ----------------
async function init() {
  try {
    const r = await fetch("/api/catalog");
    if (!r.ok) throw new Error("catalog " + r.status);
    S.catalog = await r.json();
  } catch (e) {
    setStatus("无法连接内核 API（" + e.message + "）。请先启动服务：./wos -addr :8080", true);
    return;
  }
  $("#scaleBtn").textContent = "l 对数";
  document.querySelectorAll("#viewTabs button[data-view]").forEach((b) => {
    b.classList.toggle("active", b.dataset.view === S.view);
    b.addEventListener("click", () => setView(b.dataset.view));
  });
  $("#profBtn").addEventListener("click", () => { S.profileAxis = S.profileAxis ? null : "x"; drawProfile(); });
  $("#scaleBtn").addEventListener("click", () => { S.scale = S.scale === "log" ? "lin" : "log"; $("#scaleBtn").textContent = "l " + (S.scale === "log" ? "对数" : "线性"); renderView(); });
  $("#prevPlane").addEventListener("click", () => stepPlane(-1));
  $("#nextPlane").addEventListener("click", () => stepPlane(1));
  $("#insertBtn").addEventListener("click", openInsert);
  $("#deleteBtn").addEventListener("click", delEl);
  $("#helpBtn").addEventListener("click", openHelp);
  $("#helpClose").addEventListener("click", closeHelp);
  $("#insClose").addEventListener("click", () => hideOverlay($("#insertOverlay")));
  $("#insFilter").addEventListener("input", () => { S.insertSel = 0; renderInsertList($("#insFilter").value); });
  $("#jsonApply").addEventListener("click", applyJson);
  $("#jsonClose").addEventListener("click", closeJson);
  $("#newBtn").addEventListener("click", newFile);
  $("#saveBtn").addEventListener("click", saveFile);
  $("#openBtn").addEventListener("click", openFilePicker);
  $("#waveModeBtn").addEventListener("click", () => setMode("wave"));
  $("#quantumModeBtn").addEventListener("click", () => setMode("quantum"));
  $("#qAddGate").addEventListener("click", () => {
    S.qconfig.gates.push({ type: "beam_splitter", params: { mode0: 0, mode1: 1, reflectivity: 0.5 } });
    renderQuantum(); qRun();
  });
  $("#runBtn").addEventListener("click", () => { if (S.mode === "quantum") qRun(); else run(); });
  $("#qExportPng").addEventListener("click", qExportPng);
  $("#qExportSvg").addEventListener("click", qExportSvg);
  $("#exportSvgBtn").addEventListener("click", exportWaveSvg);
  $("#hidePatternBtn").addEventListener("click", togglePattern);
  $("#fileOpen").addEventListener("change", (ev) => {
    const f = ev.target.files && ev.target.files[0];
    if (f) openFile(f);
    ev.target.value = ""; // 允许重复打开同一文件
  });
  window.addEventListener("resize", () => { drawProfile(); });
  // 默认加载“透镜聚焦（艾里斑）”；示例下拉框已删除，内置预设均为
  // examples/presets/*.json 文件，按 o 打开载入。
  let cfg = null;
  const exs = S.catalog.examples || [];
  if (exs.length) {
    const defIdx = Math.max(0, exs.findIndex((x) => x.name.includes("透镜聚焦")));
    cfg = exs[defIdx] ? clone(exs[defIdx].config) : null;
  }
  S.config = cfg || blankConfig();
  S.qconfig = blankQuantumConfig();
  renderAll();
  run();
}
init();
