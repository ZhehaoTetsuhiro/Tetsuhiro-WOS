package optics

// ParamSpec describes one adjustable parameter of an element, source, or
// global setting. The web GUI renders a keyboard-operable control for every
// entry in this catalog, so new parameters appear automatically.
type ParamSpec struct {
	Key     string   `json:"key"`
	Label   string   `json:"label"`
	Unit    string   `json:"unit"`
	Kind    string   `json:"kind"` // float | int | choice | bool | text | nested
	Min     float64  `json:"min,omitempty"`
	Max     float64  `json:"max,omitempty"`
	Step    float64  `json:"step,omitempty"`
	Default any      `json:"default"`
	Choices []string `json:"choices,omitempty"`
	Help    string   `json:"help,omitempty"`
	// ShowIf is a simple visibility condition: a comma-separated AND list of
	// "key=value" terms where value may be a "|"-separated OR list, e.g.
	// "shape=circle|ring" or "shape=circle,kind=phase". The GUI hides the
	// parameter unless every term matches the element's current params.
	ShowIf string `json:"show_if,omitempty"`
}

// ElementDoc documents one element (or source) type for the catalog API.
type ElementDoc struct {
	Type   string      `json:"type"`
	Label  string      `json:"label"`
	Help   string      `json:"help"`
	Params []ParamSpec `json:"params"`
}

func fp(key, label, unit string, min, max, step, def float64, help string) ParamSpec {
	return ParamSpec{Key: key, Label: label, Unit: unit, Kind: "float", Min: min, Max: max, Step: step, Default: def, Help: help}
}

func ip(key, label, unit string, min, max, def int, help string) ParamSpec {
	return ParamSpec{Key: key, Label: label, Unit: unit, Kind: "int", Min: float64(min), Max: float64(max), Step: 1, Default: def, Help: help}
}

func cp(key, label string, choices []string, def string, help string) ParamSpec {
	return ParamSpec{Key: key, Label: label, Kind: "choice", Choices: choices, Default: def, Help: help}
}

func bp(key, label string, def bool, help string) ParamSpec {
	return ParamSpec{Key: key, Label: label, Kind: "bool", Default: def, Help: help}
}

func tp(key, label, def, help string) ParamSpec {
	return ParamSpec{Key: key, Label: label, Kind: "text", Default: def, Help: help}
}

// showIf attaches a visibility condition to a parameter (see ParamSpec.ShowIf).
func showIf(spec ParamSpec, cond string) ParamSpec {
	spec.ShowIf = cond
	return spec
}

// SourceDocs documents the built-in source types.
var SourceDocs = []ElementDoc{
	{Type: "plane", Label: "平面波", Help: "振幅均匀的平面波，填满整个网格；可加倾角。",
		Params: []ParamSpec{
			fp("power", "光功率", "W", 1e-9, 10, 1e-4, 1e-3, "光源总功率，用于把强度归一化为 W/m^2"),
			fp("tilt_x", "倾角 x", "rad", -0.5, 0.5, 1e-3, 0, "绕 y 轴倾斜角（精确方向余弦，非傍轴近似）"),
			fp("tilt_y", "倾角 y", "rad", -0.5, 0.5, 1e-3, 0, "绕 x 轴倾斜角"),
		}},
	{Type: "gaussian", Label: "高斯光束", Help: "基模高斯光束，1/e^2 振幅半径（束腰）为 waist。",
		Params: []ParamSpec{
			fp("waist", "束腰半径", "m", 1e-6, 0.05, 1e-5, 1e-3, "1/e^2 振幅半径；束腰越小发散越大"),
			fp("power", "光功率", "W", 1e-9, 10, 1e-4, 1e-3, ""),
			fp("x", "中心 x", "m", -0.05, 0.05, 1e-4, 0, ""),
			fp("y", "中心 y", "m", -0.05, 0.05, 1e-4, 0, ""),
			fp("tilt_x", "倾角 x", "rad", -0.5, 0.5, 1e-3, 0, ""),
			fp("tilt_y", "倾角 y", "rad", -0.5, 0.5, 1e-3, 0, ""),
		}},
	{Type: "laguerre_gaussian", Label: "拉盖尔-高斯光束", Help: "LG_p^l 模，携带轨道角动量 l*hbar。",
		Params: []ParamSpec{
			fp("waist", "束腰半径", "m", 1e-6, 0.05, 1e-5, 1e-3, ""),
			ip("p", "径向阶数 p", "", 0, 5, 0, ""),
			ip("l", "拓扑荷 l", "", -5, 5, 1, "相位螺旋 exp(i*l*theta)"),
			fp("power", "光功率", "W", 1e-9, 10, 1e-4, 1e-3, ""),
			fp("x", "中心 x", "m", -0.05, 0.05, 1e-4, 0, ""),
			fp("y", "中心 y", "m", -0.05, 0.05, 1e-4, 0, ""),
		}},
	{Type: "hermite_gaussian", Label: "厄米-高斯光束", Help: "HG_mn 模。",
		Params: []ParamSpec{
			fp("waist", "束腰半径", "m", 1e-6, 0.05, 1e-5, 1e-3, ""),
			ip("m", "阶数 m", "", 0, 5, 1, ""),
			ip("n", "阶数 n", "", 0, 5, 0, ""),
			fp("power", "光功率", "W", 1e-9, 10, 1e-4, 1e-3, ""),
			fp("x", "中心 x", "m", -0.05, 0.05, 1e-4, 0, ""),
			fp("y", "中心 y", "m", -0.05, 0.05, 1e-4, 0, ""),
		}},
	{Type: "bessel", Label: "贝塞尔光束", Help: "J0(kr sin(beta)) 截断贝塞尔光束（无衍射传播）。",
		Params: []ParamSpec{
			fp("beta", "锥角 beta", "rad", 1e-4, 0.3, 1e-3, 0.01, "决定中心亮斑尺度 ~2.405/(k sin beta)"),
			fp("radius", "截断半径", "m", 1e-4, 0.05, 1e-4, 0.004, "真实贝塞尔光束无限延伸，这里做硬边截断"),
			fp("power", "光功率", "W", 1e-9, 10, 1e-4, 1e-3, ""),
		}},
	{Type: "spherical", Label: "球面波（点光源）", Help: "发散或会聚球面波，等价于点光源位于光轴前方/后方 radius 处。",
		Params: []ParamSpec{
			fp("radius", "波面曲率半径", "m", 1e-4, 10, 1e-3, 0.1, "点源到网格平面的距离"),
			bp("converging", "会聚", false, "勾选后为会聚球面波"),
			fp("power", "光功率", "W", 1e-9, 10, 1e-4, 1e-3, ""),
			fp("x", "中心 x", "m", -0.05, 0.05, 1e-4, 0, ""),
			fp("y", "中心 y", "m", -0.05, 0.05, 1e-4, 0, ""),
		}},
}

// ElementDocs documents the built-in element types (catalog order = GUI menu
// order). Structural elements (propagate/sensor/beamsplitter/combiner) are
// included so the GUI can insert them like any other element.
var ElementDocs = []ElementDoc{
	{Type: "propagate", Label: "自由传播", Help: "沿光束传播方向前进 distance 米；可单独指定传播算法。",
		Params: []ParamSpec{
			fp("distance", "距离", "m", 0, 100, 1e-3, 0.1, "沿光束方向的路程（反射后仍取正值，相位继续累加）"),
			cp("method", "传播算法", []string{"auto", "asm", "asm_pad", "asm_shift", "asm_shift_pad", "vectorial", "fresnel_tf", "fresnel_ir", "fraunhofer"}, "auto", "留空/auto 使用全局算法"),
		}},
	{Type: "uniaxial", Label: "单轴晶体（双折射）", Help: "在光轴沿 x 的单轴晶体中传播：异常光(Ex)按 n_e、寻常光(Ey)按 n_o 色散。", Params: []ParamSpec{
		fp("distance", "厚度", "m", 0, 1, 1e-4, 1e-3, ""),
		fp("n_o", "寻常折射率 n_o", "", 1, 4, 0.01, 1.5, ""),
		fp("n_e", "异常折射率 n_e", "", 1, 4, 0.01, 1.7, ""),
	}},
	{Type: "medium", Label: "均匀介质（吸收/增益）", Help: "在复折射率 n = index + i·absorption 的均匀介质中传播（split-step BPM）；absorption>0 吸收、<0 增益。", Params: []ParamSpec{
		fp("distance", "厚度", "m", 0, 1, 1e-4, 1e-3, ""),
		fp("index", "折射率", "", 1, 4, 0.01, 1.5, ""),
		fp("absorption", "吸收系数 Im(n)", "", -1, 1, 1e-4, 0, ">0 吸收、<0 增益"),
		ip("steps", "分步数", "", 1, 200, 20, "split-step 子步数（越大越准）"),
	}},
	{Type: "biaxial", Label: "双轴晶体（Berreman 4×4）", Help: "主轴折射率 n_x/n_y/n_z 的双轴晶体，完整 Berreman 4×4 各向异性传播（含 o/e 耦合，较慢）。", Params: []ParamSpec{
		fp("distance", "厚度", "m", 0, 1, 1e-4, 1e-3, ""),
		fp("n_x", "n_x", "", 1, 4, 0.01, 1.6, ""),
		fp("n_y", "n_y", "", 1, 4, 0.01, 1.5, ""),
		fp("n_z", "n_z", "", 1, 4, 0.01, 1.4, ""),
	}},
	{Type: "lens", Label: "薄透镜", Help: "相位 exp(-i k r^2/(2f))；f>0 会聚，f<0 发散，可带圆形孔径。",
		Params: []ParamSpec{
			fp("f", "焦距", "m", -100, 100, 1e-3, 0.1, "f>0 会聚"),
			fp("aperture", "孔径半径", "m", 0, 0.05, 1e-4, 0, "0 表示无孔径"),
			fp("x", "中心 x", "m", -0.05, 0.05, 1e-4, 0, ""),
			fp("y", "中心 y", "m", -0.05, 0.05, 1e-4, 0, ""),
		}},
	{Type: "aperture", Label: "孔径光阑", Help: "透射孔径：圆孔/方孔/矩孔/椭圆孔/三角孔/环形/多边形/双缝/十字/星形/超椭圆，亦可用顶点列表自定义；切换形状后仅显示该形状对应的参数。",
		Params: []ParamSpec{
			cp("shape", "形状", []string{"circle", "square", "rectangle", "ellipse", "triangle", "ring", "polygon", "double_slit", "cross", "star", "superellipse", "custom"}, "circle", "选择孔径形状；不同形状显示不同的参数"),
			// 圆孔 / 三角孔 / 多边形 / 星形共用外接圆半径
			showIf(fp("radius", "半径", "m", 1e-6, 0.05, 1e-4, 1e-3, "circle/triangle/polygon/star：外接圆半径"), "shape=circle|triangle|polygon|star"),
			// 方孔 / 矩孔 / 双缝 / 十字共用宽度
			showIf(fp("width", "宽度", "m", 1e-6, 0.05, 1e-4, 2e-3, "square=边长；rectangle/double_slit/cross=沿 u 方向的宽度"), "shape=square|rectangle|double_slit|cross"),
			showIf(fp("height", "高度", "m", 1e-6, 0.05, 1e-4, 2e-3, "rectangle/double_slit：沿 v 方向"), "shape=rectangle|double_slit"),
			// 椭圆 / 超椭圆共用半轴
			showIf(fp("a", "半轴 a", "m", 1e-6, 0.05, 1e-4, 1e-3, "ellipse/superellipse：沿 u 方向半轴"), "shape=ellipse|superellipse"),
			showIf(fp("b", "半轴 b", "m", 1e-6, 0.05, 1e-4, 2e-3, "ellipse/superellipse：沿 v 方向半轴"), "shape=ellipse|superellipse"),
			showIf(fp("order", "幂次 m", "", 0.2, 10, 0.1, 2, "superellipse：(|u|/a)^m+(|v|/b)^m<=1；m=2 为椭圆，m 增大趋近矩形"), "shape=superellipse"),
			// 环形
			showIf(fp("rin", "内半径", "m", 1e-6, 0.05, 1e-4, 5e-4, "ring"), "shape=ring"),
			showIf(fp("rout", "外半径", "m", 1e-6, 0.05, 1e-4, 1e-3, "ring"), "shape=ring"),
			// 双缝
			showIf(fp("separation", "缝间距", "m", 1e-6, 0.05, 1e-4, 1e-3, "double_slit：中心到中心"), "shape=double_slit"),
			// 多边形
			showIf(ip("sides", "边数", "", 3, 32, 6, "polygon：正多边形边数"), "shape=polygon"),
			// 十字
			showIf(fp("length", "臂长", "m", 1e-6, 0.05, 1e-4, 4e-3, "cross：十字臂全长（两臂端到端）"), "shape=cross"),
			// 星形
			showIf(fp("inner", "内半径", "m", 1e-6, 0.05, 1e-4, 5e-4, "star：尖角谷底到中心距离（须 < 半径）"), "shape=star"),
			showIf(ip("points", "尖角数", "", 3, 32, 5, "star：星形尖角数量"), "shape=star"),
			// 自定义多边形
			showIf(tp("vertices", "顶点列表", "1e-3,0;0,1e-3;-1e-3,0;0,-1e-3", "custom：分号分隔的 x,y 顶点（至少 3 个，自动闭合）"), "shape=custom"),
			// 公共参数
			fp("rotation", "旋转角", "rad", -3.1416, 3.1416, 0.01, 0, "绕中心旋转整个孔径"),
			fp("x", "中心 x", "m", -0.05, 0.05, 1e-4, 0, ""),
			fp("y", "中心 y", "m", -0.05, 0.05, 1e-4, 0, ""),
			fp("edge_sigma", "边缘平滑", "m", 0, 1e-3, 1e-6, 0, "0=理想硬边；>0 用误差函数软化边缘以抑制混叠"),
		}},
	{Type: "apodizer", Label: "高斯切趾器", Help: "振幅透过率 exp(-r^2/waist^2)，用于软化硬边。",
		Params: []ParamSpec{
			fp("waist", "切趾半径", "m", 1e-6, 0.05, 1e-5, 1e-3, ""),
			fp("amplitude", "透过率", "", 0, 1, 0.01, 1, ""),
			fp("x", "中心 x", "m", -0.05, 0.05, 1e-4, 0, ""),
			fp("y", "中心 y", "m", -0.05, 0.05, 1e-4, 0, ""),
		}},
	{Type: "grating", Label: "衍射光栅", Help: "正弦/二元振幅光栅、正弦/二元相位光栅与闪耀光栅（薄光栅标量模型）。",
		Params: []ParamSpec{
			cp("kind", "类型", []string{"amplitude_sin", "amplitude_binary", "phase_sin", "phase_binary", "blazed"}, "phase_sin", ""),
			fp("period", "周期", "m", 1e-6, 1e-2, 1e-6, 2e-4, ""),
			fp("modulation", "调制深度", "", 0, 2, 0.05, 1, "振幅光栅=对比度；相位光栅=峰峰值相位(rad)"),
			fp("duty", "占空比", "", 0, 1, 0.05, 0.5, "binary 类型"),
			fp("blaze_depth", "闪耀相位深度", "rad", 0, 6.2832, 0.05, 3.1416, "blazed 类型：锯齿相位峰峰值"),
			fp("rotation", "旋转角", "rad", -3.1416, 3.1416, 0.01, 0, ""),
			fp("offset", "相位偏移", "rad", 0, 6.2832, 0.05, 0, ""),
		}},
	{Type: "axicon", Label: "轴锥镜", Help: "锥形相位 exp(-i k (n-1) alpha r)，产生贝塞尔光束。",
		Params: []ParamSpec{
			fp("alpha", "锥角", "rad", 1e-4, 0.5, 1e-3, 0.02, "锥面与底面的夹角"),
			fp("index", "折射率", "", 1, 4, 0.01, 1.5, ""),
		}},
	{Type: "spiral_phase", Label: "螺旋相位板", Help: "exp(i*l*theta)，产生光学涡旋；可叠加焦距 f。",
		Params: []ParamSpec{
			ip("charge", "拓扑荷 l", "", -10, 10, 1, "整数"),
			fp("f", "叠加焦距", "m", -100, 100, 1e-3, 0, "0=不叠加透镜相位"),
			fp("x", "中心 x", "m", -0.05, 0.05, 1e-4, 0, ""),
			fp("y", "中心 y", "m", -0.05, 0.05, 1e-4, 0, ""),
		}},
	{Type: "wedge", Label: "楔形棱镜", Help: "线性相位 exp(-i k (n-1) alpha u)，使光束偏转。",
		Params: []ParamSpec{
			fp("alpha", "楔角", "rad", 1e-4, 0.5, 1e-3, 0.01, "偏转角 ~(n-1)*alpha"),
			fp("index", "折射率", "", 1, 4, 0.01, 1.5, ""),
			fp("rotation", "旋转角", "rad", -3.1416, 3.1416, 0.01, 0, "楔面方向"),
		}},
	{Type: "zone_plate", Label: "菲涅耳波带片", Help: "0/pi 交替相位或明暗交替的同心环，环半径 r_m^2 = m*lambda*f + (m*lambda/2)^2。",
		Params: []ParamSpec{
			fp("f", "焦距", "m", 1e-3, 10, 1e-3, 0.1, "一级焦点"),
			fp("radius", "半径", "m", 1e-4, 0.05, 1e-4, 0.01, "波带片外半径"),
			cp("kind", "类型", []string{"phase", "amplitude"}, "phase", ""),
		}},
	{Type: "diffuser", Label: "随机相位漫射体", Help: "高斯相关随机相位，相关长度 correlation，相位标准差 sigma。",
		Params: []ParamSpec{
			fp("sigma", "相位标准差", "rad", 0, 6.2832, 0.05, 3.1416, "pi=强漫射，产生完全散斑"),
			fp("correlation", "相关长度", "m", 0, 1e-2, 1e-6, 2e-5, "0=白噪声相位"),
			fp("amplitude", "透过率", "", 0, 1, 0.01, 1, ""),
			ip("seed", "随机种子", "", 0, 100000, 1, "相同种子得到相同相位屏"),
		}},
	{Type: "mirror", Label: "反射镜", Help: "振幅反射率与球面曲率相位；之后光束折返，路程相位继续累加（往返 2L 相位 2kL）。",
		Params: []ParamSpec{
			fp("reflectivity", "振幅反射率", "", 0, 1, 0.01, 1, ""),
			fp("curvature", "曲率 1/R", "1/m", -100, 100, 0.1, 0, "等效焦距 R/2；0=平面镜"),
			fp("tilt_x", "倾斜 x", "rad", -0.5, 0.5, 1e-3, 0, "光束偏转 2*tilt_x"),
			fp("tilt_y", "倾斜 y", "rad", -0.5, 0.5, 1e-3, 0, ""),
		}},
	{Type: "concave_mirror", Label: "凹面镜", Help: "会聚球面反射镜：等效焦距 f = R/2（半径 R>0）。",
		Params: []ParamSpec{
			fp("radius", "曲率半径 R", "m", 1e-3, 100, 1e-3, 0.5, "会聚，焦距 R/2"),
			fp("reflectivity", "振幅反射率", "", 0, 1, 0.01, 1, ""),
			fp("aperture", "孔径半径", "m", 0, 0.05, 1e-4, 0, "0 表示无孔径"),
			fp("x", "中心 x", "m", -0.05, 0.05, 1e-4, 0, ""),
			fp("y", "中心 y", "m", -0.05, 0.05, 1e-4, 0, ""),
		}},
	{Type: "convex_mirror", Label: "凸面镜", Help: "发散球面反射镜：等效焦距 f = -R/2（半径 R>0）。",
		Params: []ParamSpec{
			fp("radius", "曲率半径 R", "m", 1e-3, 100, 1e-3, 0.5, "发散，焦距 -R/2"),
			fp("reflectivity", "振幅反射率", "", 0, 1, 0.01, 1, ""),
			fp("aperture", "孔径半径", "m", 0, 0.05, 1e-4, 0, "0 表示无孔径"),
			fp("x", "中心 x", "m", -0.05, 0.05, 1e-4, 0, ""),
			fp("y", "中心 y", "m", -0.05, 0.05, 1e-4, 0, ""),
		}},
	{Type: "zernike", Label: "泽尼克像差板", Help: "Noll 序 1-21 项泽尼克波前像差（单位：波长），用于研究像差。",
		Params: zernikeParams()},
	{Type: "polarizer", Label: "线偏振片", Help: "琼斯投影矩阵，透振方向 angle。",
		Params: []ParamSpec{
			fp("angle", "透振角", "rad", -3.1416, 3.1416, 0.01, 0, ""),
			fp("transmission", "振幅透过率", "", 0, 1, 0.01, 1, ""),
		}},
	{Type: "retarder", Label: "相位延迟片", Help: "快轴 axis、相位延迟 retardance；pi/2=四分之一波片，pi=二分之一波片。",
		Params: []ParamSpec{
			fp("retardance", "相位延迟", "rad", 0, 6.2832, 0.01, 1.5708, "pi/2=QWP, pi=HWP"),
			fp("axis", "快轴方位角", "rad", -3.1416, 3.1416, 0.01, 0, ""),
		}},
	{Type: "rotator", Label: "旋光片", Help: "琼斯旋转矩阵（旋光角 angle）。",
		Params: []ParamSpec{
			fp("angle", "旋光角", "rad", -3.1416, 3.1416, 0.01, 0, ""),
		}},
	{Type: "custom_jones", Label: "自定义琼斯元件", Help: "任意 2x2 琼斯矩阵 [[a b] [c d]]（复元素）。",
		Params: []ParamSpec{
			fp("a_re", "a 实部", "", -10, 10, 0.1, 1, ""), fp("a_im", "a 虚部", "", -10, 10, 0.1, 0, ""),
			fp("b_re", "b 实部", "", -10, 10, 0.1, 0, ""), fp("b_im", "b 虚部", "", -10, 10, 0.1, 0, ""),
			fp("c_re", "c 实部", "", -10, 10, 0.1, 0, ""), fp("c_im", "c 虚部", "", -10, 10, 0.1, 0, ""),
			fp("d_re", "d 实部", "", -10, 10, 0.1, 1, ""), fp("d_im", "d 虚部", "", -10, 10, 0.1, 0, ""),
		}},
	{Type: "beamsplitter", Label: "分束器", Help: "对称分束器：透射臂继续主光路（t=sqrt(1-R)），反射臂（i*sqrt(R)）进入子光路，可在合束器中相干叠加。",
		Params: []ParamSpec{
			fp("reflectivity", "功率反射率 R", "", 0, 1, 0.01, 0.5, ""),
			fp("phase", "反射相位", "rad", 0, 6.2832, 0.05, 0, "附加到反射臂的常数相位"),
			ParamSpec{Key: "reflected_arm", Label: "反射臂光路", Kind: "nested", Default: map[string]any{}, Help: "反射臂内的元件列表（与主光路同格式）"},
		}},
	{Type: "combiner", Label: "合束器（干涉输出）", Help: "相干叠加各臂电场：out_j = sum_i w_ji * arm_i。必须位于光路末端。",
		Params: []ParamSpec{
			ParamSpec{Key: "outputs", Label: "输出端口", Kind: "nested", Default: []any{}, Help: "每端口：label + weights[{arm,re,im}]；arm 为 main 或 bs0/bs0.bs0 等臂标识"},
		}},
	{Type: "sensor", Label: "探测器（记录面）", Help: "记录该处光场：强度/相位/剖面与全部指标。",
		Params: []ParamSpec{
			tp("label", "名称", "sensor", ""),
			fp("strehl_aperture", "斯特列尔孔径", "m", 0, 0.05, 1e-4, 0, "计算 Strehl 用的参考光瞳半径，0=不计算"),
			fp("strehl_distance", "斯特列尔距离", "m", 0, 100, 1e-3, 0, "参考聚焦距离，0=不计算"),
		}},
}

func zernikeParams() []ParamSpec {
	names := []string{"平移", "x 倾斜", "y 倾斜", "离焦", "斜像散", "竖像散", "竖彗差", "横彗差", "竖三叶", "斜三叶", "球差", "二级像散", "二级像散(45°)", "四叶", "四叶(45°)"}
	out := []ParamSpec{fp("radius", "归一化半径", "m", 1e-4, 0.05, 1e-4, 0.01, "泽尼克多项式定义域 |r|<=radius")}
	for i := 1; i <= 15; i++ {
		out = append(out, fp("c"+itoa(i), "Z"+itoa(i)+" "+names[i-1], "λ", -2, 2, 0.05, 0, ""))
	}
	for i := 16; i <= 21; i++ {
		out = append(out, fp("c"+itoa(i), "Z"+itoa(i), "λ", -2, 2, 0.05, 0, ""))
	}
	return out
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	b := [8]byte{}
	p := len(b)
	for i > 0 {
		p--
		b[p] = byte('0' + i%10)
		i /= 10
	}
	return string(b[p:])
}

// MethodDocs documents the propagation algorithms.
var MethodDocs = []struct {
	Key, Label, Help string
}{
	{"auto", "自动（=角谱法）", "始终选择精确的角谱法"},
	{"asm", "角谱法（精确）", "亥姆霍兹方程在均匀介质中的精确解；无傍轴近似，默认推荐"},
	{"asm_pad", "角谱法（零填充 2×，高精度）", "2N×2N 零填充做线性卷积，消除光束超出窗口时的环绕混叠；内存/耗时约 4×"},
	{"asm_shift", "角谱法（离轴频移）", "搬移倾斜光束的载频后再传播，抑制频谱贴边混叠；适合大倾角照明"},
	{"asm_shift_pad", "角谱法（离轴频移 + 零填充 2×）", "同时消除频谱贴边混叠与窗口环绕，适合大倾角且走离/发散的光束；内存/耗时约 4×"},
	{"vectorial", "矢量角谱法（含 Ez，非傍轴）", "由散度条件重构纵分量 Ez 并三分量传播；适合大 NA 聚焦（传感器可显示 |Ez|²）"},
	{"fresnel_tf", "菲涅尔（传递函数）", "傍轴近似；|z| <= N*dx^2/lambda 时有效"},
	{"fresnel_ir", "菲涅尔（冲激响应）", "傍轴近似；|z| >= N*dx^2/lambda 时有效"},
	{"fraunhofer", "夫琅禾费（远场）", "输出像素变为 lambda*|z|/(N*dx)；要求菲涅耳数 D^2/(lambda*z) << 1"},
}

// PolarizationDocs documents the source polarization presets.
var PolarizationDocs = []struct {
	Key, Label string
}{
	{"x", "线偏振 x"}, {"y", "线偏振 y"}, {"d", "线偏振 45°"}, {"a", "线偏振 -45°"},
	{"r", "右旋圆偏振"}, {"l", "左旋圆偏振"}, {"custom", "自定义琼斯矢量"},
}

// Example is a ready-to-run preset shown in the GUI.
type Example struct {
	Name   string `json:"name"`
	Config Config `json:"config"`
}

// Examples returns the built-in preset configurations.
func Examples() []Example {
	bl := &BandlimitOpts{Fraction: 0.9, Sigma: 0.05}
	pf2 := func(b bool) *bool { return &b }
	ex := []Example{
		{Name: "高斯光束传播", Config: Config{
			Grid: GridSpec{Size: 1024, Width: 0.01}, Wavelength: 632.8e-9, Polarized: pf2(false),
			Method: "asm", Evanescent: "decay", Bandlimit: bl,
			Source: SourceSpec{Type: "gaussian", Params: map[string]any{"waist": 1e-3, "power": 1e-3}},
			Elements: []ElementSpec{
				{Type: "propagate", Params: map[string]any{"distance": 0.05}},
				{Type: "sensor", Params: map[string]any{"label": "z=0.05m"}},
				{Type: "propagate", Params: map[string]any{"distance": 0.25}},
				{Type: "sensor", Params: map[string]any{"label": "z=0.3m"}},
			},
		}},
		{Name: "透镜聚焦（艾里斑）", Config: Config{
			Grid: GridSpec{Size: 1024, Width: 0.01}, Wavelength: 632.8e-9, Polarized: pf2(false),
			Method: "asm", Evanescent: "decay", Bandlimit: bl,
			Source: SourceSpec{Type: "plane", Params: map[string]any{"power": 1e-3}},
			Elements: []ElementSpec{
				{Type: "lens", Params: map[string]any{"f": 0.5, "aperture": 0.0025}},
				{Type: "propagate", Params: map[string]any{"distance": 0.5}},
				{Type: "sensor", Params: map[string]any{"label": "焦面", "strehl_aperture": 0.0025, "strehl_distance": 0.5}},
			},
		}},
		{Name: "单缝夫琅禾费衍射", Config: Config{
			Grid: GridSpec{Size: 1024, Width: 0.02}, Wavelength: 632.8e-9, Polarized: pf2(false),
			Method: "asm", Evanescent: "decay", Bandlimit: bl,
			Source: SourceSpec{Type: "plane", Params: map[string]any{"power": 1e-3}},
			Elements: []ElementSpec{
				{Type: "aperture", Params: map[string]any{"shape": "rectangle", "width": 4e-4, "height": 0.02}},
				{Type: "propagate", Params: map[string]any{"distance": 1.0, "method": "fraunhofer"}},
				{Type: "sensor", Params: map[string]any{"label": "远场"}},
			},
		}},
		{Name: "双缝干涉", Config: Config{
			Grid: GridSpec{Size: 1024, Width: 0.02}, Wavelength: 632.8e-9, Polarized: pf2(false),
			Method: "asm", Evanescent: "decay", Bandlimit: bl,
			Source: SourceSpec{Type: "plane", Params: map[string]any{"power": 1e-3}},
			Elements: []ElementSpec{
				{Type: "aperture", Params: map[string]any{"shape": "double_slit", "width": 1e-4, "height": 0.02, "separation": 1e-3}},
				{Type: "propagate", Params: map[string]any{"distance": 1.0, "method": "fraunhofer"}},
				{Type: "sensor", Params: map[string]any{"label": "干涉条纹"}},
			},
		}},
		{Name: "衍射光栅光谱", Config: Config{
			Grid: GridSpec{Size: 1024, Width: 0.008}, Wavelength: 632.8e-9, Polarized: pf2(false),
			Method: "asm", Evanescent: "decay", Bandlimit: bl,
			Source: SourceSpec{Type: "plane", Params: map[string]any{"power": 1e-3}},
			Elements: []ElementSpec{
				{Type: "grating", Params: map[string]any{"kind": "phase_sin", "period": 2e-5, "modulation": 2.0}},
				{Type: "propagate", Params: map[string]any{"distance": 1.0, "method": "fraunhofer"}},
				{Type: "sensor", Params: map[string]any{"label": "衍射级"}},
			},
		}},
		{Name: "圆孔衍射（远场艾里斑）", Config: Config{
			Grid: GridSpec{Size: 1024, Width: 0.02}, Wavelength: 632.8e-9, Polarized: pf2(false),
			Method: "asm", Evanescent: "decay", Bandlimit: bl,
			Source: SourceSpec{Type: "plane", Params: map[string]any{"power": 1e-3}},
			Elements: []ElementSpec{
				{Type: "aperture", Params: map[string]any{"shape": "circle", "radius": 2e-3}},
				{Type: "propagate", Params: map[string]any{"distance": 2.0, "method": "fraunhofer"}},
				{Type: "sensor", Params: map[string]any{"label": "远场"}},
			},
		}},
		{Name: "波带片聚焦", Config: Config{
			Grid: GridSpec{Size: 1024, Width: 0.004}, Wavelength: 632.8e-9, Polarized: pf2(false),
			Method: "asm", Evanescent: "decay", Bandlimit: bl,
			Source: SourceSpec{Type: "plane", Params: map[string]any{"power": 1e-3}},
			Elements: []ElementSpec{
				{Type: "zone_plate", Params: map[string]any{"f": 0.05, "radius": 0.002, "kind": "phase"}},
				{Type: "propagate", Params: map[string]any{"distance": 0.05}},
				{Type: "sensor", Params: map[string]any{"label": "焦点"}},
			},
		}},
		{Name: "光学涡旋", Config: Config{
			Grid: GridSpec{Size: 512, Width: 0.01}, Wavelength: 632.8e-9, Polarized: pf2(false),
			Method: "asm", Evanescent: "decay", Bandlimit: bl,
			Source: SourceSpec{Type: "gaussian", Params: map[string]any{"waist": 2e-3, "power": 1e-3}},
			Elements: []ElementSpec{
				{Type: "spiral_phase", Params: map[string]any{"charge": 3}},
				{Type: "propagate", Params: map[string]any{"distance": 0.3}},
				{Type: "sensor", Params: map[string]any{"label": "涡旋光束"}},
			},
		}},
		{Name: "偏振片与波片", Config: Config{
			Grid: GridSpec{Size: 512, Width: 0.01}, Wavelength: 632.8e-9, Polarized: pf2(true),
			Method: "asm", Evanescent: "decay", Bandlimit: bl,
			Source: SourceSpec{Type: "gaussian", Params: map[string]any{"waist": 2e-3, "power": 1e-3, "polarization": "d"}},
			Elements: []ElementSpec{
				{Type: "retarder", Params: map[string]any{"retardance": 1.5708, "axis": 0}},
				{Type: "sensor", Params: map[string]any{"label": "经过 QWP（45°线偏振→圆偏振）"}},
			},
		}},
		{Name: "马赫-曾德尔干涉仪", Config: Config{
			Grid: GridSpec{Size: 512, Width: 0.01}, Wavelength: 632.8e-9, Polarized: pf2(false),
			Method: "asm", Evanescent: "decay", Bandlimit: bl,
			Source: SourceSpec{Type: "gaussian", Params: map[string]any{"waist": 2e-3, "power": 1e-3}},
			Elements: []ElementSpec{
				{Type: "propagate", Params: map[string]any{"distance": 0.02}},
				{Type: "beamsplitter", Params: map[string]any{"reflectivity": 0.5, "reflected_arm": map[string]any{
					"elements": []any{map[string]any{"type": "propagate", "params": map[string]any{"distance": 0.04}}},
				}}},
				{Type: "propagate", Params: map[string]any{"distance": 0.04}},
				{Type: "combiner", Params: map[string]any{"outputs": []any{
					map[string]any{"label": "端口1", "weights": []any{
						map[string]any{"arm": "main", "re": 0.70710678, "im": 0},
						map[string]any{"arm": "bs0", "re": 0, "im": 0.70710678}}},
					map[string]any{"label": "端口2", "weights": []any{
						map[string]any{"arm": "main", "re": 0, "im": 0.70710678},
						map[string]any{"arm": "bs0", "re": 0.70710678, "im": 0}}},
				}}},
			},
		}},
		{Name: "迈克尔逊干涉仪", Config: Config{
			Grid: GridSpec{Size: 512, Width: 0.01}, Wavelength: 632.8e-9, Polarized: pf2(false),
			Method: "asm", Evanescent: "decay", Bandlimit: bl,
			Source: SourceSpec{Type: "gaussian", Params: map[string]any{"waist": 2e-3, "power": 1e-3}},
			Elements: []ElementSpec{
				{Type: "propagate", Params: map[string]any{"distance": 0.01}},
				{Type: "beamsplitter", Params: map[string]any{"reflectivity": 0.5, "reflected_arm": map[string]any{
					"elements": []any{
						map[string]any{"type": "propagate", "params": map[string]any{"distance": 0.02}},
						map[string]any{"type": "mirror", "params": map[string]any{}},
						map[string]any{"type": "propagate", "params": map[string]any{"distance": 0.02}},
					},
				}}},
				{Type: "propagate", Params: map[string]any{"distance": 0.02}},
				{Type: "mirror", Params: map[string]any{}},
				{Type: "propagate", Params: map[string]any{"distance": 0.02}},
				{Type: "combiner", Params: map[string]any{"outputs": []any{
					map[string]any{"label": "探测器", "weights": []any{
						map[string]any{"arm": "main", "re": 0.70710678, "im": 0},
						map[string]any{"arm": "bs0", "re": 0, "im": 0.70710678}}},
					map[string]any{"label": "回光源端口", "weights": []any{
						map[string]any{"arm": "main", "re": 0, "im": 0.70710678},
						map[string]any{"arm": "bs0", "re": 0.70710678, "im": 0}}},
				}}},
			},
		}},
		{Name: "贝塞尔光束（轴锥镜）", Config: Config{
			Grid: GridSpec{Size: 512, Width: 0.01}, Wavelength: 632.8e-9, Polarized: pf2(false),
			Method: "asm", Evanescent: "decay", Bandlimit: bl,
			Source: SourceSpec{Type: "gaussian", Params: map[string]any{"waist": 3e-3, "power": 1e-3}},
			Elements: []ElementSpec{
				{Type: "axicon", Params: map[string]any{"alpha": 0.02, "index": 1.5}},
				{Type: "propagate", Params: map[string]any{"distance": 0.2}},
				{Type: "sensor", Params: map[string]any{"label": "贝塞尔区"}},
			},
		}},
		{Name: "像差研究（泽尼克球差+离焦）", Config: Config{
			Grid: GridSpec{Size: 1024, Width: 0.01}, Wavelength: 632.8e-9, Polarized: pf2(false),
			Method: "asm", Evanescent: "decay", Bandlimit: bl,
			Source: SourceSpec{Type: "plane", Params: map[string]any{"power": 1e-3}},
			Elements: []ElementSpec{
				{Type: "lens", Params: map[string]any{"f": 0.3, "aperture": 0.003}},
				{Type: "zernike", Params: map[string]any{"radius": 0.003, "c4": 0.5, "c11": 1.0}},
				{Type: "propagate", Params: map[string]any{"distance": 0.3}},
				{Type: "sensor", Params: map[string]any{"label": "像差焦斑", "strehl_aperture": 0.003, "strehl_distance": 0.3}},
			},
		}},
	}
	return ex
}

// Catalog is the full documentation payload served to the GUI.
type Catalog struct {
	Sources       []ElementDoc   `json:"sources"`
	Elements      []ElementDoc   `json:"elements"`
	Methods       []any          `json:"methods"`
	Polarizations []any          `json:"polarizations"`
	Quantum       QuantumCatalog `json:"quantum"`
	Examples      []Example      `json:"examples"`
}

// QuantumCatalog documents the quantum-optics states and gates.
type QuantumCatalog struct {
	States []ElementDoc `json:"states"`
	Gates  []ElementDoc `json:"gates"`
}

// QuantumStateDocs documents the built-in quantum initial states.
var QuantumStateDocs = []ElementDoc{
	{Type: "vacuum", Label: "真空态", Help: "全模式 |0…0⟩ 真空态。",
		Params: []ParamSpec{}},
	{Type: "fock", Label: "Fock 光子数态", Help: "多模光子数态 |n0,n1,…⟩；occupation 用逗号分隔（如 1,1）。",
		Params: []ParamSpec{
			tp("occupation", "光子数（逗号分隔）", "1,1", "每模光子数，如 1,1 表示 |1,1⟩"),
		}},
	{Type: "coherent", Label: "相干态", Help: "单模相干态 |α⟩（其余模式为真空）。",
		Params: []ParamSpec{
			ip("mode", "模式", "", 0, MaxQuantumModes-1, 0, "施加到哪个模式"),
			fp("alpha_re", "α 实部", "", -10, 10, 0.1, 1, ""),
			fp("alpha_im", "α 虚部", "", -10, 10, 0.1, 0, ""),
		}},
	{Type: "squeezed_vacuum", Label: "压缩真空态", Help: "单模压缩真空 S(z)|0⟩，z = r·e^{iθ}。",
		Params: []ParamSpec{
			ip("mode", "模式", "", 0, MaxQuantumModes-1, 0, ""),
			fp("r", "压缩参数 r", "", 0, 3, 0.1, 0.5, "越大压缩越强"),
			fp("phase", "压缩角 θ", "rad", 0, 6.2832, 0.1, 0, ""),
		}},
	{Type: "two_mode_squeezed", Label: "双模压缩真空（EPR）", Help: "模式 0、1 的纠缠双模压缩真空态。",
		Params: []ParamSpec{
			fp("r", "压缩参数 r", "", 0, 3, 0.1, 0.5, ""),
		}},
	{Type: "thermal", Label: "热态", Help: "每模热态（几何光子数分布），mean_n 用逗号分隔（如 1,0.5）。混合态，走密度矩阵后端。",
		Params: []ParamSpec{
			tp("mean_n", "平均光子数（逗号分隔）", "1", "每模平均光子数，如 1 或 1,0.5"),
		}},
}

// QuantumGateDocs documents the built-in linear-optical gates.
var QuantumGateDocs = []ElementDoc{
	{Type: "phase_shift", Label: "相移", Help: "exp(i·φ·n) 作用到单模。",
		Params: []ParamSpec{
			ip("mode", "模式", "", 0, MaxQuantumModes-1, 0, ""),
			fp("phase", "相位 φ", "rad", 0, 6.2832, 0.1, 0, ""),
		}},
	{Type: "beam_splitter", Label: "分束器", Help: "对称分束器 U=exp(iθ(a0†a1+a0a1†))，θ=asin(√R)。",
		Params: []ParamSpec{
			ip("mode0", "模式 0", "", 0, MaxQuantumModes-1, 0, ""),
			ip("mode1", "模式 1", "", 0, MaxQuantumModes-1, 1, ""),
			fp("reflectivity", "反射率 R", "", 0, 1, 0.05, 0.5, ""),
		}},
	{Type: "displacement", Label: "位移", Help: "位移算符 D(α)=exp(αa†−α*a)。",
		Params: []ParamSpec{
			ip("mode", "模式", "", 0, MaxQuantumModes-1, 0, ""),
			fp("alpha_re", "α 实部", "", -10, 10, 0.1, 1, ""),
			fp("alpha_im", "α 虚部", "", -10, 10, 0.1, 0, ""),
		}},
	{Type: "squeeze", Label: "压缩", Help: "单模压缩 S(z)=exp(½(z*a²−za†²))。",
		Params: []ParamSpec{
			ip("mode", "模式", "", 0, MaxQuantumModes-1, 0, ""),
			fp("r", "压缩参数 r", "", 0, 3, 0.1, 0.5, ""),
			fp("phase", "压缩角 θ", "rad", 0, 6.2832, 0.1, 0, ""),
		}},
	{Type: "loss", Label: "损耗信道", Help: "透射率 T 的损耗信道（振幅阻尼 Kraus 算符），产生混合态。",
		Params: []ParamSpec{
			ip("mode", "模式", "", 0, MaxQuantumModes-1, 0, ""),
			fp("transmittance", "透射率 T", "", 0, 1, 0.05, 0.5, "存活概率；Fock |n⟩ 经损耗变为二项分布"),
		}},
}

// BuildQuantumCatalog assembles the quantum catalog document.
func BuildQuantumCatalog() QuantumCatalog {
	return QuantumCatalog{States: QuantumStateDocs, Gates: QuantumGateDocs}
}

// BuildCatalog assembles the catalog document.
func BuildCatalog() Catalog {
	cat := Catalog{Sources: SourceDocs, Elements: ElementDocs, Quantum: BuildQuantumCatalog(), Examples: Examples()}
	for _, m := range MethodDocs {
		cat.Methods = append(cat.Methods, map[string]string{"key": m.Key, "label": m.Label, "help": m.Help})
	}
	for _, p := range PolarizationDocs {
		cat.Polarizations = append(cat.Polarizations, map[string]string{"key": p.Key, "label": p.Label})
	}
	return cat
}
