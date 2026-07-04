package tools

func (r *Registry) lookup(name string) (Tool, bool) {
	tool, ok := r.tools[name]
	return tool, ok
}
