// Package server exposes the optics kernel over HTTP.
//
// Endpoints (all JSON unless noted):
//
//	GET  /api/catalog                          element/source/method/examples docs
//	GET  /api/health                           liveness
//	GET  /api/elements                         loaded scripted elements (定义文件)
//	POST /api/elements/reload                  rescan the definition directories
//	GET  /api/elements/{name}/preview.png?kind=amp|phase   operator mask preview
//	POST /api/validate                         config validation issues
//	POST /api/simulate                         submit a config -> 202 {run_id}
//	GET  /api/runs/{id}                        run status + result metadata
//	GET  /api/runs/{id}/planes/{pid}?field=...&fmt=bin|png&scale=lin|log&cmap=...
//	GET  /api/runs/{id}/profiles/{pid}?axis=x|y&field=...&coord=...
//	GET  /api/runs/{id}/inspect/{pid}?x=&y=&part=    local Jones/Stokes/phase readout
//	GET  /api/runs/{id}/scene                        routed light path (3D layout view)
//
// Plane field views (?field=...) are total/amplitude/ex/ey/ez, phase_x/phase_y/
// phase_z (wrapped), phase_u (unwrapped wavefront), pol_azimuth/pol_ellip/
// pol_s1/pol_s2/pol_s3/pol_degree and color (real-wavelength RGB, PNG only).
// ?part=N selects one coherent unit (one source group) for the views that need
// a complex field.
package server

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"twos/optics"
)

// RunStatus values.
const (
	StatusRunning = "running"
	StatusDone    = "done"
	StatusError   = "error"
)

// Limits that keep the server bounded under load or adversarial input.
const (
	// maxQueuedRuns bounds how many simulations may be queued/running at once,
	// preventing a submit flood from spawning unbounded goroutines.
	maxQueuedRuns = 8
	// maxStoredRuns bounds the number of retained runs (finished or errored) so
	// s.runs/s.order cannot grow without bound when maxBytes is generous or
	// when many runs error.
	maxStoredRuns = 128
)

// runEntry is one submitted simulation.
type runEntry struct {
	status  string
	res     *optics.Result
	errMsg  string
	created time.Time
}

// Server holds an LRU store of finished runs and serializes simulations.
type Server struct {
	simSem   chan struct{} // at most one simulation at a time
	mu       sync.Mutex
	runs     map[string]*runEntry
	order    []string
	bytes    int64
	maxBytes int64
}

// New creates a server keeping at most maxBytes of plane data in memory.
func New(maxBytes int64) *Server {
	if maxBytes <= 0 {
		maxBytes = 512 << 20
	}
	return &Server{
		simSem:   make(chan struct{}, 1),
		runs:     map[string]*runEntry{},
		maxBytes: maxBytes,
	}
}

// writeJSON marshals the value *before* touching the response: NaN or ±Inf
// anywhere in the payload makes encoding/json fail, and writing the header
// first used to turn that into an empty 200 that the client could not even
// report.
func writeJSON(w http.ResponseWriter, code int, v any) {
	buf, err := json.Marshal(v)
	if err != nil {
		code = http.StatusInternalServerError
		buf = []byte(`{"error":"response contains non-finite numbers"}`)
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.WriteHeader(code)
	_, _ = w.Write(buf)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]any{"error": msg})
}

// Handler returns the API handler (routes under /api).
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/catalog", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeErr(w, http.StatusMethodNotAllowed, "GET only")
			return
		}
		writeJSON(w, http.StatusOK, optics.BuildCatalog())
	})
	mux.HandleFunc("/api/elements", func(w http.ResponseWriter, r *http.Request) {
		// Exact path: the list of loaded scripted elements.
		s.handleElements(w, r)
	})
	mux.HandleFunc("/api/elements/", func(w http.ResponseWriter, r *http.Request) {
		// reload and {name}/preview.png live under the slash.
		s.handleElements(w, r)
	})
	mux.HandleFunc("/api/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "time": time.Now().Format(time.RFC3339)})
	})
	mux.HandleFunc("/api/validate", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeErr(w, http.StatusMethodNotAllowed, "POST only")
			return
		}
		cfg, err := decodeConfig(w, r)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		issues := optics.ValidateConfig(cfg)
		if len(issues) == 0 {
			writeJSON(w, http.StatusOK, map[string]any{"ok": true, "issues": []any{}})
			return
		}
		out := make([]map[string]string, 0, len(issues))
		for _, is := range issues {
			out = append(out, map[string]string{"path": is.Path, "message": is.Message})
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "issues": out})
	})
	mux.HandleFunc("/api/quantum", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeErr(w, http.StatusMethodNotAllowed, "POST only")
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
		if err != nil {
			writeErr(w, http.StatusBadRequest, "failed to read request body: "+err.Error())
			return
		}
		var cfg optics.QuantumConfig
		if err := json.Unmarshal(body, &cfg); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
			return
		}
		res, err := optics.SimulateQuantum(cfg)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		switch r.URL.Query().Get("fmt") {
		case "png":
			w.Header().Set("Content-Type", "image/png")
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.WriteHeader(http.StatusOK)
			_ = pngEncode(w, renderQuantumChart(res))
			return
		case "svg":
			w.Header().Set("Content-Type", "image/svg+xml")
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(renderQuantumSVG(res)))
			return
		}
		writeJSON(w, http.StatusOK, res)
	})
	mux.HandleFunc("/api/convert", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeErr(w, http.StatusMethodNotAllowed, "POST only")
			return
		}
		cfg, err := decodeConfig(w, r)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		if cfg.IsScene() {
			writeJSON(w, http.StatusOK, map[string]any{"scene": cfg.Scene, "sources": cfg.AllSources()})
			return
		}
		scene := optics.LayoutFromElements(cfg)
		writeJSON(w, http.StatusOK, map[string]any{"scene": scene, "sources": cfg.AllSources()})
	})
	mux.HandleFunc("/api/simulate", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeErr(w, http.StatusMethodNotAllowed, "POST only")
			return
		}
		cfg, err := decodeConfig(w, r)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		issues := optics.ValidateConfig(cfg)
		if len(issues) > 0 {
			first := issues[0]
			writeErr(w, http.StatusBadRequest, fmt.Sprintf("config validation failed: %s: %s", first.Path, first.Message))
			return
		}
		if err := optics.CheckGridMemory(cfg.Grid.Size); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		id, err := s.submit(cfg)
		if err != nil {
			writeErr(w, http.StatusTooManyRequests, err.Error())
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]any{"run_id": id, "status": StatusRunning})
	})
	mux.HandleFunc("/api/runs/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeErr(w, http.StatusMethodNotAllowed, "GET only")
			return
		}
		rest := strings.TrimPrefix(r.URL.Path, "/api/runs/")
		parts := strings.Split(rest, "/")
		if len(parts) < 1 || parts[0] == "" {
			writeErr(w, http.StatusBadRequest, "missing run id")
			return
		}
		id := parts[0]
		snap, ok := s.snapshot(id)
		if !ok {
			writeErr(w, http.StatusNotFound, "unknown run id")
			return
		}
		if len(parts) == 1 {
			s.writeRunMeta(w, id, snap)
			return
		}
		if snap.status != StatusDone {
			writeErr(w, http.StatusConflict, "run not finished")
			return
		}
		switch parts[1] {
		case "planes":
			if len(parts) < 3 {
				writeErr(w, http.StatusBadRequest, "missing plane id")
				return
			}
			pl := findPlane(snap.res, parts[2])
			if pl == nil {
				writeErr(w, http.StatusNotFound, "unknown plane id")
				return
			}
			// /planes/{pid}/inspect is accepted as an alias of /inspect/{pid}.
			if len(parts) >= 4 && parts[3] == "inspect" {
				s.serveInspect(w, r, pl)
				return
			}
			s.servePlaneData(w, r, pl)
		case "profiles":
			if len(parts) < 3 {
				writeErr(w, http.StatusBadRequest, "missing plane id")
				return
			}
			pl := findPlane(snap.res, parts[2])
			if pl == nil {
				writeErr(w, http.StatusNotFound, "unknown plane id")
				return
			}
			s.serveProfile(w, r, pl)
		case "inspect":
			if len(parts) < 3 {
				writeErr(w, http.StatusBadRequest, "missing plane id")
				return
			}
			pl := findPlane(snap.res, parts[2])
			if pl == nil {
				writeErr(w, http.StatusNotFound, "unknown plane id")
				return
			}
			s.serveInspect(w, r, pl)
		case "scene":
			if snap.res.Scene == nil {
				writeErr(w, http.StatusNotFound, "this run has no routed scene")
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"scene":   snap.res.Scene,
				"sources": sourceGeomWithColor(snap.res.Scene),
			})
		default:
			writeErr(w, http.StatusNotFound, "unknown sub-resource")
		}
	})
	return mux
}

func decodeConfig(w http.ResponseWriter, r *http.Request) (*optics.Config, error) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 2<<20))
	if err != nil {
		return nil, fmt.Errorf("failed to read request body: %v", err)
	}
	var cfg optics.Config
	if err := json.Unmarshal(body, &cfg); err != nil {
		return nil, fmt.Errorf("invalid JSON: %v", err)
	}
	return &cfg, nil
}

func findPlane(res *optics.Result, id string) *optics.Plane {
	for _, p := range res.Planes {
		if p.ID == id {
			return p
		}
	}
	return nil
}

// submit registers the run and starts its computation in the background. It
// rejects new submissions once the queued+running count reaches maxQueuedRuns,
// bounding goroutine and store growth under a submit flood.
func (s *Server) submit(cfg *optics.Config) (string, error) {
	id := newRunID()
	s.mu.Lock()
	defer s.mu.Unlock()
	running := 0
	for _, e := range s.runs {
		if e.status == StatusRunning {
			running++
		}
	}
	if running >= maxQueuedRuns {
		return "", fmt.Errorf("too many queued simulations (limit %d); try again later", maxQueuedRuns)
	}
	s.runs[id] = &runEntry{status: StatusRunning, created: time.Now()}
	s.order = append(s.order, id)
	go func() {
		s.simSem <- struct{}{}
		defer func() { <-s.simSem }()
		s.runSimulation(id, cfg)
	}()
	return id, nil
}

// runSimulation executes one simulation and records its result. It always
// releases its slot (via the caller's defer) and converts a panic into an
// error entry so one bad run cannot wedge the server or leave a run stuck.
func (s *Server) runSimulation(id string, cfg *optics.Config) {
	defer func() {
		if r := recover(); r != nil {
			s.mu.Lock()
			if e := s.runs[id]; e != nil {
				e.status = StatusError
				e.errMsg = fmt.Sprintf("simulation panic: %v", r)
			}
			s.evict()
			s.mu.Unlock()
		}
	}()
	res, err := optics.Simulate(*cfg)
	s.mu.Lock()
	defer s.mu.Unlock()
	e := s.runs[id]
	if e == nil {
		return
	}
	if err != nil {
		e.status = StatusError
		e.errMsg = err.Error()
		s.evict()
		return
	}
	e.status = StatusDone
	e.res = res
	s.bytes += runBytes(e)
	s.evict()
}

// runSnapshot is an immutable copy of a run's mutable fields, safe to read
// without holding s.mu. res is immutable once a run finishes.
type runSnapshot struct {
	status string
	errMsg string
	res    *optics.Result
}

// snapshot returns a consistent view of a run's state under the lock. Reading
// status/errMsg/res through a snapshot (rather than a raw *runEntry) avoids
// the data race with the background simulation goroutine that mutates those
// fields.
func (s *Server) snapshot(id string) (*runSnapshot, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.runs[id]
	if !ok {
		return nil, false
	}
	return &runSnapshot{status: e.status, errMsg: e.errMsg, res: e.res}, true
}

// runBytes returns the approximate bytes retained for a finished run's plane
// data (16 bytes per complex128 element across Ex/Ey/Ez).
func runBytes(e *runEntry) int64 {
	var n int64
	for _, p := range e.res.Planes {
		n += int64(len(p.Ex)+len(p.Ey)+len(p.Ez)) * 16
		// A merged plane keeps one full grid per coherent unit in Parts.
		for i := range p.Parts {
			pt := &p.Parts[i]
			n += int64(len(pt.Ex)+len(pt.Ey)+len(pt.Ez)) * 16
		}
	}
	return n
}

// evict drops the oldest terminal (finished or errored) runs until both the
// memory budget and the maxStoredRuns cap are met. Caller must hold s.mu. At
// least one run is always kept so the most recent result stays servable; a
// single run larger than maxBytes therefore remains resident until a newer run
// lets it be reclaimed.
func (s *Server) evict() {
	for (s.bytes > s.maxBytes || len(s.runs) > maxStoredRuns) && len(s.order) > 1 {
		var victim string
		var victimIdx = -1
		for i, id := range s.order {
			e := s.runs[id]
			if e != nil && (e.status == StatusDone || e.status == StatusError) {
				victim, victimIdx = id, i
				break
			}
		}
		if victimIdx < 0 {
			break
		}
		e := s.runs[victim]
		if e.status == StatusDone {
			s.bytes -= runBytes(e)
		}
		delete(s.runs, victim)
		s.order = append(s.order[:victimIdx], s.order[victimIdx+1:]...)
	}
}

// writeRunMeta serializes status + result metadata (no field data).
func (s *Server) writeRunMeta(w http.ResponseWriter, id string, e *runSnapshot) {
	out := map[string]any{"run_id": id, "status": e.status}
	if e.status == StatusError {
		out["error"] = e.errMsg
		writeJSON(w, http.StatusOK, out)
		return
	}
	if e.res != nil {
		out["grid"] = map[string]any{"size": e.res.Size, "width": e.res.Width, "dx": e.res.DX}
		out["wavelength"] = e.res.Wavelength
		out["elapsed_ms"] = e.res.ElapsedMS
		out["warnings"] = e.res.Warnings
		planes := make([]optics.PlaneInfo, 0, len(e.res.Planes))
		for _, p := range e.res.Planes {
			planes = append(planes, p.Info())
		}
		out["planes"] = planes
		out["views"] = viewNames()
		if e.res.Scene != nil {
			out["scene"] = e.res.Scene
			out["sources"] = sourceGeomWithColor(e.res.Scene)
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// sourceColor is a source plus the colour it should be drawn in.
type sourceColor struct {
	optics.SourceGeom
	Color   [3]float64 `json:"color"` // linear sRGB, unit peak
	Visible bool       `json:"visible"`
	Hex     string     `json:"hex"`
}

// sourceGeomWithColor decorates the traced sources with their display colour so
// the GUI can label channels and draw beams the way they look.
func sourceGeomWithColor(tr *optics.SceneTrace) []sourceColor {
	out := make([]sourceColor, 0, len(tr.Sources))
	for _, s := range tr.Sources {
		r, g, b, vis := optics.WavelengthRGB(s.Wavelength)
		out = append(out, sourceColor{
			SourceGeom: s,
			Color:      [3]float64{r, g, b},
			Visible:    vis,
			Hex:        fmt.Sprintf("#%02x%02x%02x", int(r*255+0.5), int(g*255+0.5), int(b*255+0.5)),
		})
	}
	return out
}

// serveInspect returns the full local state at one point of a plane: the Jones
// vector, the Stokes parameters, the polarization ellipse and the phase. This
// is what makes the polarization and phase readouts convenient — the GUI asks
// for the pixel under the cursor.
func (s *Server) serveInspect(w http.ResponseWriter, r *http.Request, pl *optics.Plane) {
	q := r.URL.Query()
	part := -1
	if ps := q.Get("part"); ps != "" {
		if v, err := parseFloat(ps); err == nil {
			part = int(v)
		}
	}
	idx := -1
	if xs, ys := q.Get("x"), q.Get("y"); xs != "" && ys != "" {
		xi, err1 := parseFloat(xs)
		yi, err2 := parseFloat(ys)
		if err1 != nil || err2 != nil {
			writeErr(w, http.StatusBadRequest, "x and y must be numbers")
			return
		}
		// NaN, ±Inf and huge values convert to a negative int (MinInt64), so a
		// plain range check would let them through and silently fall back to the
		// centroid readout: reject them as numbers first.
		if !finiteNum(xi) || !finiteNum(yi) || xi < 0 || yi < 0 ||
			xi >= float64(pl.Size) || yi >= float64(pl.Size) {
			writeErr(w, http.StatusBadRequest, "x/y out of range")
			return
		}
		idx = int(yi)*pl.Size + int(xi)
	}
	pp, pidx := partField(pl, part)
	// Default to the intensity centroid so a readout is available immediately.
	if idx < 0 {
		dx := pl.DX
		cx := int(math.Round(pl.Stats.CentroidX/dx + float64(pl.Size)/2))
		cy := int(math.Round(pl.Stats.CentroidY/dx + float64(pl.Size)/2))
		cx = clampInt(cx, 0, pl.Size-1)
		cy = clampInt(cy, 0, pl.Size-1)
		idx = cy*pl.Size + cx
	}
	st := pp.StokesAtPixel(idx)
	unwrap := pp.UnwrapPhase(phaseMaskCut)
	pv, rms, waves := optics.PhaseStats(unwrap, pp.Wavelength)
	var ex, ey complex128
	if idx < len(pp.Ex) {
		ex = pp.Ex[idx]
	}
	if pp.Ey != nil && idx < len(pp.Ey) {
		ey = pp.Ey[idx]
	}
	out := map[string]any{
		"part":         pidx,
		"label":        pp.Label,
		"wavelength":   pp.Wavelength,
		"x":            idx % pl.Size,
		"y":            idx / pl.Size,
		"pos_x":        (float64(idx%pl.Size) - float64(pl.Size)/2) * pl.DX,
		"pos_y":        (float64(idx/pl.Size) - float64(pl.Size)/2) * pl.DX,
		"ex_re":        real(ex),
		"ex_im":        imag(ex),
		"ey_re":        real(ey),
		"ey_im":        imag(ey),
		"intensity":    st.S0,
		"phase":        pp.PhaseAt(idx),
		"stokes":       st,
		"phase_stats":  map[string]float64{"pv": pv, "rms": rms, "waves": waves},
		"parts":        len(pl.Parts),
		"merged_units": pl.Merged,
	}
	if unwrap != nil && idx < len(unwrap) && finiteNum(unwrap[idx]) {
		out["phase_unwrapped"] = unwrap[idx]
	}
	writeJSON(w, http.StatusOK, out)
}

// servePlaneData streams one field view of a plane as float32 binary or PNG.
func (s *Server) servePlaneData(w http.ResponseWriter, r *http.Request, pl *optics.Plane) {
	q := r.URL.Query()
	field := q.Get("field")
	if field == "" {
		field = "total"
	}
	fmtStr := q.Get("fmt")
	if fmtStr == "" {
		fmtStr = "bin"
	}
	part := -1
	if ps := q.Get("part"); ps != "" {
		if v, err := parseFloat(ps); err == nil {
			part = int(v)
		}
	}
	if field == "color" {
		if fmtStr != "png" {
			writeErr(w, http.StatusBadRequest, "field=color is only available as fmt=png")
			return
		}
		img, err := renderColor(pl, q)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.WriteHeader(http.StatusOK)
		_ = pngEncode(w, img)
		return
	}
	mask := 0.0
	if s := q.Get("mask"); s != "" {
		if v, err := parseFloat(s); err == nil && v > 0 {
			mask = v
		}
	}
	vals, info, partIdx, err := planeValues(pl, field, part, mask)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	n := pl.Size
	switch fmtStr {
	case "bin":
		buf := make([]byte, n*n*4)
		for i := 0; i < n*n && i < len(vals); i++ {
			bits := math.Float32bits(float32(vals[i]))
			buf[4*i] = byte(bits)
			buf[4*i+1] = byte(bits >> 8)
			buf[4*i+2] = byte(bits >> 16)
			buf[4*i+3] = byte(bits >> 24)
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("X-Grid-Size", fmt.Sprint(n))
		w.Header().Set("X-View-Unit", info.unit)
		w.Header().Set("X-View-Part", fmt.Sprint(partIdx))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(buf)
	case "png":
		img, err := renderValues(vals, n, info, q)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("X-View-Unit", info.unit)
		w.WriteHeader(http.StatusOK)
		_ = pngEncode(w, img)
	default:
		writeErr(w, http.StatusBadRequest, "fmt must be bin or png")
	}
}

// serveProfile returns a 1-D cut as JSON.
func (s *Server) serveProfile(w http.ResponseWriter, r *http.Request, pl *optics.Plane) {
	q := r.URL.Query()
	axis := q.Get("axis")
	if axis == "" {
		axis = "x"
	}
	field := q.Get("field")
	if field == "" {
		field = "total"
	}
	var coord *float64
	if cs := q.Get("coord"); cs != "" {
		if v, err := parseFloat(cs); err == nil {
			coord = &v
		}
	}
	part := -1
	if ps := q.Get("part"); ps != "" {
		if v, err := parseFloat(ps); err == nil {
			part = int(v)
		}
	}
	vals, info, _, err := planeValues(pl, field, part, 0)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	prof, err := profileOfValues(vals, pl.Size, pl.DX, axis, coord, pl.Stats.CentroidX, pl.Stats.CentroidY)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	// Masked samples (no light ⇒ no phase/polarization) cannot be encoded as
	// JSON numbers; sending them as null marks an honest gap.
	safe := make([]any, len(prof.V))
	for i, v := range prof.V {
		if finiteNum(v) {
			safe[i] = v
		}
	}
	out := map[string]any{"axis": prof.Axis, "coord": prof.Coord, "x": prof.X, "v": safe, "unit": info.unit}
	writeJSON(w, http.StatusOK, out)
}

func norm2(z complex128) float64 {
	return real(z)*real(z) + imag(z)*imag(z)
}

func parseFloat(s string) (float64, error) {
	return strconv.ParseFloat(strings.TrimSpace(s), 64)
}

// finiteNum reports whether v is a usable finite number.
func finiteNum(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

func newRunID() string {
	var b [8]byte
	now := time.Now().UnixNano()
	for i := range b {
		now = now*6364136223846793005 + 1442695040888963407
		b[i] = byte(now >> 40)
	}
	const hex = "0123456789abcdef"
	out := make([]byte, 16)
	for i, c := range b {
		out[2*i] = hex[c>>4]
		out[2*i+1] = hex[c&15]
	}
	return string(out)
}
