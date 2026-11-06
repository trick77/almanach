package manifest

import (
	"path"
	"strings"
)

// Cargo: a Cargo.toml's `[package].name` is the crate it provides,
// cargo:<name>, compared as crates.io does (case and `-`/`_` alike). Its
// `[dependencies]`, and a target's (`[target.'cfg(unix)'.dependencies]`),
// are Main, `[dev-dependencies]` Test (tests, examples, benches),
// `[build-dependencies]` Dev, an optional one (a feature's) Dev, as a
// Python extra. A dependency renamed (`x = { package = "y" }`)
// is crate y; one taking its version from the workspace (`workspace =
// true`) gets the version the nearest workspace root's
// `[workspace.dependencies]` names, a table that itself requires nothing.

// cargoCoord is a crate's coordinate, its name normalised.
func cargoCoord(name string) string {
	return Cargo + ":" + strings.ToLower(strings.ReplaceAll(strings.TrimSpace(name), "_", "-"))
}

// cargoDep is a dependency entry: the crate it names (key or `package`),
// its version as written, whether it takes the workspace's, and whether
// it is optional (a feature's).
func cargoDep(key string, v any) (crate, version string, inherited, optional bool) {
	crate = key
	switch t := v.(type) {
	case string:
		version = t
	case map[string]any:
		if p, ok := t["package"].(string); ok && strings.TrimSpace(p) != "" {
			crate = p
		}
		version, _ = t["version"].(string)
		w, _ := t["workspace"].(string)
		o, _ := t["optional"].(string)
		inherited, optional = w == "true", o == "true"
	}
	return crate, strings.TrimSpace(version), inherited, optional
}

// cargoWS is each workspace root's `[workspace.dependencies]` by folder:
// the key a member names, the crate and version it stands for.
type cargoWS map[string]map[string]Dep

func cargoWorkspaces(files map[string][]byte) cargoWS {
	out := cargoWS{}
	for p, body := range files {
		if path.Base(p) != "Cargo.toml" {
			continue
		}
		doc, err := parseTOML(body)
		if err != nil {
			continue
		}
		ws, _ := doc["workspace"].(map[string]any)
		if ws == nil {
			continue
		}
		deps := map[string]Dep{}
		table, _ := ws["dependencies"].(map[string]any)
		for k, v := range table {
			crate, version, _, _ := cargoDep(k, v)
			deps[k] = Dep{Coord: cargoCoord(crate), Version: version}
		}
		out[dirOf(p)] = deps
	}
	return out
}

// of is the workspace dependencies of the nearest workspace root at or
// above folder d; nil for none.
func (w cargoWS) of(d string) map[string]Dep {
	for {
		if deps, ok := w[d]; ok {
			return deps
		}
		if d == "" {
			return nil
		}
		d = dirOf(d)
	}
}

// cargo is a Cargo.toml's crate and its dependencies.
func cargo(body []byte, ws map[string]Dep) ([]string, []Dep) {
	doc, err := parseTOML(body)
	if err != nil {
		return nil, nil
	}
	var prov []string
	pkg, _ := doc["package"].(map[string]any)
	if n, _ := pkg["name"].(string); strings.TrimSpace(n) != "" {
		prov = append(prov, cargoCoord(n))
	}
	var reqs []Dep
	read := func(t map[string]any) {
		for _, s := range []struct {
			key, scope string
		}{
			{"dependencies", ScopeMain}, {"dev-dependencies", ScopeTest}, {"dev_dependencies", ScopeTest},
			{"build-dependencies", ScopeDev}, {"build_dependencies", ScopeDev},
		} {
			table, _ := t[s.key].(map[string]any)
			for _, k := range sortedKeys(table) {
				crate, version, inherited, optional := cargoDep(k, table[k])
				coord := cargoCoord(crate)
				if inherited {
					if w, ok := ws[k]; ok {
						coord, version = w.Coord, w.Version
					}
				}
				if crate = strings.TrimSpace(crate); crate == "" {
					continue
				}
				scope := s.scope
				if optional && scope == ScopeMain {
					scope = ScopeDev
				}
				reqs = append(reqs, Dep{Coord: coord, Version: version, Scope: scope})
			}
		}
	}
	read(doc)
	targets, _ := doc["target"].(map[string]any)
	for _, k := range sortedKeys(targets) {
		if t, ok := targets[k].(map[string]any); ok {
			read(t)
		}
	}
	return prov, reqs
}
