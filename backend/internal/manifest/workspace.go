package manifest

import (
	"bufio"
	"bytes"
	"encoding/json"
	"path"
	"strings"
)

// Workspaces: a monorepo's packages declared by its tool, read like a
// Gradle include or a Maven module (Modules): npm's and Yarn's
// `workspaces` in package.json (a list, or `{"packages": [...]}`), pnpm's
// pnpm-workspace.yaml `packages:`, go.work's `use`, Cargo's `[workspace]
// members`, uv's `[tool.uv.workspace] members`. A member is a folder or a
// pattern (`packages/*`, `crates/**`): `*` one folder name, `**` any
// depth; a `!pattern` (npm, pnpm) or an `exclude` (Cargo, uv) leaves a
// folder out. A folder outside the repository, or under node_modules, is
// none.

// member is one folder a workspace declares, and by which tool (the
// ecosystem's prefix).
type member struct {
	dir, tool string
}

func workspaces(files map[string][]byte, dirs []string) []member {
	var out []member
	for p, body := range files {
		d := dirOf(p)
		var tool string
		var include, exclude []string
		switch path.Base(p) {
		case "package.json":
			tool = NPM
			include, exclude = npmWorkspaces(body)
		case "pnpm-workspace.yaml":
			tool = NPM
			include, exclude = pnpmPackages(body)
		case "go.work":
			tool = Go
			include = goWorkUses(body)
		case "Cargo.toml", "pyproject.toml":
			doc, err := parseTOML(body)
			if err != nil {
				continue
			}
			ws, _ := doc["workspace"].(map[string]any)
			tool = Cargo
			if path.Base(p) == "pyproject.toml" {
				t, _ := doc["tool"].(map[string]any)
				uv, _ := t["uv"].(map[string]any)
				ws, _ = uv["workspace"].(map[string]any)
				tool = Python
			}
			include, exclude = tomlStrings(ws["members"]), tomlStrings(ws["exclude"])
		default:
			continue
		}
		for _, dir := range expand(d, include, exclude, dirs) {
			out = append(out, member{dir: dir, tool: tool})
		}
	}
	return out
}

func tomlStrings(v any) []string {
	list, _ := v.([]any)
	var out []string
	for _, e := range list {
		if s, ok := e.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// npmWorkspaces is a package.json's workspace patterns, a `!` one an
// exclusion.
func npmWorkspaces(body []byte) (include, exclude []string) {
	var p struct {
		Workspaces json.RawMessage `json:"workspaces"`
	}
	if json.Unmarshal(body, &p) != nil || len(p.Workspaces) == 0 {
		return nil, nil
	}
	var list []string
	if json.Unmarshal(p.Workspaces, &list) != nil {
		var obj struct {
			Packages []string `json:"packages"`
		}
		if json.Unmarshal(p.Workspaces, &obj) != nil {
			return nil, nil
		}
		list = obj.Packages
	}
	return splitNegated(list)
}

func splitNegated(list []string) (include, exclude []string) {
	for _, s := range list {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(s), "!"); ok {
			exclude = append(exclude, rest)
		} else {
			include = append(include, s)
		}
	}
	return include, exclude
}

// pnpmPackages is pnpm-workspace.yaml's `packages:` list, read by hand:
// the top-level key, then its `- item` lines (quoted or bare, a `#`
// comment after) until the next top-level key. Flow style
// (`packages: [a, b]`) too.
func pnpmPackages(body []byte) (include, exclude []string) {
	var list []string
	in := false
	sc := bufio.NewScanner(bytes.NewReader(body))
	for sc.Scan() {
		line := sc.Text()
		if i := strings.Index(line, " #"); i >= 0 {
			line = line[:i]
		}
		if strings.HasPrefix(strings.TrimSpace(line), "#") || strings.TrimSpace(line) == "" {
			continue
		}
		top := line[0] != ' ' && line[0] != '\t' && line[0] != '-'
		switch {
		case top && strings.HasPrefix(line, "packages:"):
			in = true
			rest := strings.TrimSpace(strings.TrimPrefix(line, "packages:"))
			if strings.HasPrefix(rest, "[") && strings.HasSuffix(rest, "]") {
				for _, s := range strings.Split(rest[1:len(rest)-1], ",") {
					list = append(list, yamlScalar(s))
				}
				in = false
			}
		case top:
			in = false
		case in:
			if item, ok := strings.CutPrefix(strings.TrimSpace(line), "-"); ok {
				list = append(list, yamlScalar(item))
			}
		}
	}
	return splitNegated(list)
}

// yamlScalar is a plain or quoted YAML scalar's text.
func yamlScalar(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && (s[0] == '"' || s[0] == '\'') && s[len(s)-1] == s[0] {
		return s[1 : len(s)-1]
	}
	return s
}

// goWorkUses is a go.work's `use` folders, a line or a block.
func goWorkUses(body []byte) []string {
	var out []string
	inBlock := false
	sc := bufio.NewScanner(bytes.NewReader(body))
	for sc.Scan() {
		line := sc.Text()
		if i := strings.Index(line, "//"); i >= 0 {
			line = line[:i]
		}
		f := strings.Fields(line)
		switch {
		case len(f) == 0:
		case inBlock && f[0] == ")":
			inBlock = false
		case inBlock:
			out = append(out, unquote(f[0]))
		case f[0] == "use" && len(f) >= 2 && f[1] == "(":
			inBlock = true
		case f[0] == "use" && len(f) >= 2:
			out = append(out, unquote(f[1]))
		}
	}
	return out
}

// expand is the folders the patterns of a workspace in folder d name: a
// plain folder as it stands, a pattern each of dirs it matches; the
// excluded ones left out, the root never a member.
func expand(d string, include, exclude, dirs []string) []string {
	resolve := func(pats []string) (plain []string, globs [][]string) {
		for _, pat := range pats {
			pat = strings.TrimSuffix(strings.TrimSpace(pat), "/")
			full, ok := inRepo(d, pat)
			if !ok {
				continue
			}
			if strings.ContainsAny(full, "*?[") {
				globs = append(globs, strings.Split(full, "/"))
			} else {
				plain = append(plain, full)
			}
		}
		return plain, globs
	}
	inPlain, inGlobs := resolve(include)
	exPlain, exGlobs := resolve(exclude)
	excluded := func(dir string) bool {
		for _, e := range exPlain {
			if dir == e {
				return true
			}
		}
		segs := strings.Split(dir, "/")
		for _, g := range exGlobs {
			if globMatch(g, segs) {
				return true
			}
		}
		return false
	}
	seen := map[string]bool{}
	var out []string
	add := func(dir string) {
		if dir == "" || seen[dir] || excluded(dir) {
			return
		}
		for _, s := range strings.Split(dir, "/") {
			if s == "node_modules" {
				return
			}
		}
		seen[dir] = true
		out = append(out, dir)
	}
	for _, p := range inPlain {
		add(p)
	}
	if len(inGlobs) > 0 {
		for _, dir := range dirs {
			segs := strings.Split(dir, "/")
			for _, g := range inGlobs {
				if globMatch(g, segs) {
					add(dir)
					break
				}
			}
		}
	}
	return out
}

// globMatch reports whether a folder's segments match a pattern's: a `**`
// segment any number of them (none too), any other one segment by
// path.Match.
func globMatch(pat, segs []string) bool {
	if len(pat) == 0 {
		return len(segs) == 0
	}
	if pat[0] == "**" {
		for i := 0; i <= len(segs); i++ {
			if globMatch(pat[1:], segs[i:]) {
				return true
			}
		}
		return false
	}
	if len(segs) == 0 {
		return false
	}
	if ok, err := path.Match(pat[0], segs[0]); err != nil || !ok {
		return false
	}
	return globMatch(pat[1:], segs[1:])
}
