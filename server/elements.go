package server

import (
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"twos/optics"
)

// Scripted-element endpoints (元件定义文件):
//
//	GET  /api/elements                       loaded definitions + directories
//	POST /api/elements/reload                rescan the definition directories
//	GET  /api/elements/{name}/preview.png    the operator's own mask (PNG)
//
// The preview answers "what did I just write?": kind=amp draws |t|, kind=phase
// draws the wrapped phase masked where the amplitude is (near) zero. width, wl
// and the element's own parameters come from the query string, so the GUI
// previews the component with the parameters it currently has.
func (s *Server) handleElements(w http.ResponseWriter, r *http.Request) {
	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/elements"), "/")
	if rest == "" {
		if r.Method != http.MethodGet {
			writeErr(w, http.StatusMethodNotAllowed, "GET only")
			return
		}
		s.writeElementList(w)
		return
	}
	if rest == "reload" {
		if r.Method != http.MethodPost {
			writeErr(w, http.StatusMethodNotAllowed, "POST only")
			return
		}
		writeJSON(w, http.StatusOK, optics.ReloadElementDefinitions(nil))
		return
	}
	parts := strings.SplitN(rest, "/", 2)
	if len(parts) == 2 && parts[1] == "preview.png" {
		if r.Method != http.MethodGet {
			writeErr(w, http.StatusMethodNotAllowed, "GET only")
			return
		}
		s.serveElementPreview(w, r, parts[0])
		return
	}
	writeErr(w, http.StatusNotFound, "unknown elements sub-resource")
}

// elementListEntry describes one loaded definition, including its expressions,
// so a client can show what the element actually does.
type elementListEntry struct {
	Name      string             `json:"name"`
	Label     string             `json:"label"`
	Behavior  string             `json:"behavior"`
	Class     string             `json:"class"`
	Source    string             `json:"source"`
	Phase     string             `json:"phase,omitempty"`
	PhaseUnit string             `json:"phase_unit,omitempty"`
	Amp       string             `json:"amp,omitempty"`
	Jones     bool               `json:"jones,omitempty"`
	Params    []optics.ParamSpec `json:"params"`
}

func (s *Server) writeElementList(w http.ResponseWriter) {
	docs := optics.ScriptedElementDocs()
	classes := optics.ComponentClasses()
	out := make([]elementListEntry, 0, len(docs))
	for _, d := range docs {
		def, ok := optics.ScriptedDefinition(d.Type)
		if !ok {
			continue
		}
		out = append(out, elementListEntry{
			Name: def.Name, Label: d.Label, Behavior: def.Behavior, Class: classes[def.Name],
			Source: def.Source, Phase: def.Phase, PhaseUnit: def.PhaseUnit,
			Amp: def.Amp, Jones: def.Jones != nil, Params: def.Params,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"dirs": optics.ElementDirs(), "elements": out,
		"version": optics.ScriptedElementsVersion(),
	})
}

func (s *Server) serveElementPreview(w http.ResponseWriter, r *http.Request, name string) {
	q := r.URL.Query()
	if !optics.IsScriptedElement(name) {
		writeErr(w, http.StatusNotFound, "no scripted element "+name+" is loaded")
		return
	}
	kind := q.Get("kind")
	if kind == "" {
		kind = "amp"
	}
	if kind != "amp" && kind != "phase" {
		writeErr(w, http.StatusBadRequest, "kind must be amp or phase")
		return
	}
	size := intQuery(q, "size", 192, 32, 512)
	width := floatQuery(q, "width", 8e-3)
	if !(width > 0) {
		writeErr(w, http.StatusBadRequest, "width must be > 0")
		return
	}
	wl := floatQuery(q, "wl", 632.8e-9)
	if !(wl > 0) {
		writeErr(w, http.StatusBadRequest, "wl must be > 0")
		return
	}
	mask := floatQuery(q, "mask", 2e-3)
	raw := map[string]string{}
	for k, v := range q {
		if len(v) > 0 {
			raw[k] = v[0]
		}
	}
	params, err := optics.ParamsFromStrings(name, raw)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	amp, phase, err := optics.SampleScriptedMask(name, params, size, width, wl)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	vals := make([]float64, size*size)
	info := viewInfo{kind: kindAmplitude, unit: "|t|"}
	rq := url.Values{}
	for k, v := range q {
		rq[k] = append([]string(nil), v...)
	}
	switch kind {
	case "phase":
		info = viewInfo{kind: kindPhaseWrapped, unit: "rad"}
		peak := 0.0
		for _, a := range amp {
			if math.Abs(a) > peak {
				peak = math.Abs(a)
			}
		}
		cut := mask * peak
		for i := range vals {
			// Phase is meaningless where the element transmits nothing:
			// render those points as masked (NaN) instead of a fake 0 rad.
			if math.Abs(amp[i]) <= cut {
				vals[i] = math.NaN()
			} else {
				vals[i] = phase[i]
			}
		}
		if rq.Get("scale") == "" {
			rq.Set("scale", "lin")
		}
	default:
		copy(vals, amp)
		if rq.Get("scale") == "" {
			// A modulation smaller than 1 would be a black square on a log
			// scale; the mask is a transmission, so draw it linearly from 0.
			rq.Set("scale", "lin")
		}
		if rq.Get("pmin") == "" {
			rq.Set("pmin", "0")
		}
	}
	img, err := renderValues(vals, size, info, rq)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-store")
	if err := pngEncode(w, img); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
	}
}

func intQuery(q url.Values, key string, def, min, max int) int {
	v, err := strconv.Atoi(q.Get(key))
	if err != nil {
		return def
	}
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

func floatQuery(q url.Values, key string, def float64) float64 {
	s := strings.TrimSpace(q.Get(key))
	if s == "" {
		return def
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return def
	}
	return v
}
