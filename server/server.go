// Package server exposes the optics kernel over HTTP.
//
// Endpoints (all JSON unless noted):
//
//	GET  /api/catalog                          element/source/method/examples docs
//	GET  /api/health                           liveness
//	POST /api/validate                         config validation issues
//	POST /api/simulate                         submit a config -> 202 {run_id}
//	GET  /api/runs/{id}                        run status + result metadata
//	GET  /api/runs/{id}/planes/{pid}?field=...&fmt=bin|png&scale=lin|log&cmap=...
//	GET  /api/runs/{id}/profiles/{pid}?axis=x|y&field=...&coord=...
package server

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
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

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
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
			writeErr(w, http.StatusBadRequest, fmt.Sprintf("配置校验失败：%s: %s", first.Path, first.Message))
			return
		}
		id := s.submit(cfg)
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
		e := s.get(id)
		if e == nil {
			writeErr(w, http.StatusNotFound, "unknown run id")
			return
		}
		if len(parts) == 1 {
			s.writeRunMeta(w, id, e)
			return
		}
		if e.status != StatusDone {
			writeErr(w, http.StatusConflict, "run not finished")
			return
		}
		switch parts[1] {
		case "planes":
			if len(parts) < 3 {
				writeErr(w, http.StatusBadRequest, "missing plane id")
				return
			}
			pl := findPlane(e.res, parts[2])
			if pl == nil {
				writeErr(w, http.StatusNotFound, "unknown plane id")
				return
			}
			s.servePlaneData(w, r, pl)
		case "profiles":
			if len(parts) < 3 {
				writeErr(w, http.StatusBadRequest, "missing plane id")
				return
			}
			pl := findPlane(e.res, parts[2])
			if pl == nil {
				writeErr(w, http.StatusNotFound, "unknown plane id")
				return
			}
			s.serveProfile(w, r, pl)
		default:
			writeErr(w, http.StatusNotFound, "unknown sub-resource")
		}
	})
	return mux
}

func decodeConfig(w http.ResponseWriter, r *http.Request) (*optics.Config, error) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 2<<20))
	if err != nil {
		return nil, fmt.Errorf("读取请求体失败: %v", err)
	}
	var cfg optics.Config
	if err := json.Unmarshal(body, &cfg); err != nil {
		return nil, fmt.Errorf("JSON 解析失败: %v", err)
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

// submit registers the run and starts its computation in the background.
func (s *Server) submit(cfg *optics.Config) string {
	id := newRunID()
	s.mu.Lock()
	s.runs[id] = &runEntry{status: StatusRunning, created: time.Now()}
	s.order = append(s.order, id)
	s.mu.Unlock()
	go func() {
		s.simSem <- struct{}{}
		res, err := optics.Simulate(*cfg)
		<-s.simSem
		s.mu.Lock()
		defer s.mu.Unlock()
		e := s.runs[id]
		if e == nil {
			return
		}
		if err != nil {
			e.status = StatusError
			e.errMsg = err.Error()
			return
		}
		e.status = StatusDone
		e.res = res
		for _, p := range res.Planes {
			s.bytes += int64(len(p.Ex)+len(p.Ey)) * 16
		}
		s.evict()
	}()
	return id
}

func (s *Server) get(id string) *runEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.runs[id]
}

// evict drops the oldest finished runs until the memory budget is met.
func (s *Server) evict() {
	for s.bytes > s.maxBytes && len(s.order) > 1 {
		var victim string
		var victimIdx = -1
		for i, id := range s.order {
			e := s.runs[id]
			if e != nil && e.status == StatusDone {
				victim, victimIdx = id, i
				break
			}
		}
		if victimIdx < 0 {
			break
		}
		e := s.runs[victim]
		for _, p := range e.res.Planes {
			s.bytes -= int64(len(p.Ex)+len(p.Ey)) * 16
		}
		delete(s.runs, victim)
		s.order = append(s.order[:victimIdx], s.order[victimIdx+1:]...)
	}
}

// writeRunMeta serializes status + result metadata (no field data).
func (s *Server) writeRunMeta(w http.ResponseWriter, id string, e *runEntry) {
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
	get := fieldGetter(pl, field)
	if get == nil {
		writeErr(w, http.StatusBadRequest, fmt.Sprintf("unknown field %q", field))
		return
	}
	n := pl.Size
	switch fmtStr {
	case "bin":
		buf := make([]byte, n*n*4)
		for i := 0; i < n*n; i++ {
			bits := math.Float32bits(float32(get(i)))
			buf[4*i] = byte(bits)
			buf[4*i+1] = byte(bits >> 8)
			buf[4*i+2] = byte(bits >> 16)
			buf[4*i+3] = byte(bits >> 24)
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("X-Grid-Size", fmt.Sprint(n))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(buf)
	case "png":
		img, err := renderPlane(pl, get, r.URL.Query())
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Access-Control-Allow-Origin", "*")
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
	prof, err := pl.ProfileOf(axis, field, coord)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, prof)
}

// fieldGetter returns the pixel value function for a named field view.
func fieldGetter(pl *optics.Plane, field string) func(i int) float64 {
	ex := pl.Ex
	ey := pl.Ey
	switch field {
	case "total":
		return func(i int) float64 {
			return norm2(ex[i]) + norm2(ey[i])
		}
	case "ex":
		return func(i int) float64 { return norm2(ex[i]) }
	case "ey":
		return func(i int) float64 { return norm2(ey[i]) }
	case "phase_x":
		return func(i int) float64 { return math.Atan2(imag(ex[i]), real(ex[i])) }
	case "phase_y":
		return func(i int) float64 { return math.Atan2(imag(ey[i]), real(ey[i])) }
	}
	return nil
}

func norm2(z complex128) float64 {
	return real(z)*real(z) + imag(z)*imag(z)
}

func parseFloat(s string) (float64, error) {
	var v float64
	_, err := fmt.Sscanf(s, "%g", &v)
	return v, err
}

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
