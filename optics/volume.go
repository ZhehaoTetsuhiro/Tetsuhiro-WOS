package optics

// Propagate3D propagates the input field to every distance in zs and returns
// a slice of cloned fields (one per distance, in order), yielding the 3-D
// x,y,z volume of the evolving beam. Each output is an independent
// propagation from the input field; the input is not modified. For a
// multi-slice thick-medium walk, chain the outputs or use PropagateSplitStep.
func Propagate3D(f *Field, zs []float64, method Method, ctx *Context) ([]*Field, error) {
	out := make([]*Field, len(zs))
	for i, z := range zs {
		g := f.Clone()
		if err := Propagate(g, z, method, ctx); err != nil {
			return nil, err
		}
		out[i] = g
	}
	return out, nil
}
