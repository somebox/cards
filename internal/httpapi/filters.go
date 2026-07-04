package httpapi

// resolveMeFilter deep-copies a saved-filter map, replacing the literal "me"
// token with the viewing actor under identity keys (owner, created_by). The
// substitution is a UI-only convenience: the filter DSL compiler and the API
// stay presentation-free (they never know what "me" means), so this happens
// here, at the board handler, against the injected UI actor. An empty actor
// leaves "me" untouched (nothing to resolve to).
func resolveMeFilter(f map[string]any, actor string) map[string]any {
	if f == nil {
		return nil
	}
	out := make(map[string]any, len(f))
	for k, v := range f {
		if actor != "" && (k == "owner" || k == "created_by") {
			out[k] = replaceMe(v, actor)
		} else {
			out[k] = v
		}
	}
	return out
}

// replaceMe recursively replaces the string "me" with actor inside a filter
// value — handling a bare string, an op object ({"$eq":"me"}), and a list
// ({"$in":["me","alice"]}).
func replaceMe(v any, actor string) any {
	switch t := v.(type) {
	case string:
		if t == "me" {
			return actor
		}
		return t
	case map[string]any:
		m := make(map[string]any, len(t))
		for k, vv := range t {
			m[k] = replaceMe(vv, actor)
		}
		return m
	case []any:
		s := make([]any, len(t))
		for i, vv := range t {
			s[i] = replaceMe(vv, actor)
		}
		return s
	default:
		return v
	}
}
