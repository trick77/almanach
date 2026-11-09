// Package coderead is what the readers of facts in code share (messaging,
// endpoints): a resolver that turns an expression into a string (a literal,
// a constant, a property, their concatenation), and the scanning of calls,
// annotations and config files it rests on. Line- and regex-based, no
// parser: a reader tolerates the odd case it misses, and one it cannot
// resolve is left out, never guessed.
package coderead

import (
	"bytes"
	"path"
	"regexp"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Resolver turns an expression in code into a string: a literal, a
// constant, a property, or their concatenation.
type Resolver struct {
	// config is each module's application properties, flattened; a module
	// is the path before src/main/ ("" at the root). keyFile is the file
	// each module's key came from, by module NUL key.
	config  map[string]map[string]string
	keyFile map[string]string
	// consts are the String constants by simple name; one named twice with
	// two values is none. fileConsts are each file's own, read first: a
	// per-class TOPIC is the commonest constant and would collide.
	consts     map[string]string
	fileConsts map[string]map[string]string
	// getters are what a method without parameters returns, by its name:
	// every override's (a base class sends to getTopic(), each subclass
	// names its own), all of them the repository's, each read in its file.
	getters map[string][]fileExpr
	// file is the file an expression is read in.
	file string
}

// fileExpr is an expression and the file it stands in.
type fileExpr struct{ file, expr string }

// NewResolver learns the constants, getters and application properties of
// files, path to body: YAML first, .properties over it, Spring's order for
// one location.
func NewResolver(files map[string][]byte) Resolver {
	r := Resolver{config: map[string]map[string]string{}, keyFile: map[string]string{}, consts: map[string]string{},
		fileConsts: map[string]map[string]string{}, getters: map[string][]fileExpr{}}
	order := SortedKeys(files)
	sort.SliceStable(order, func(i, j int) bool {
		return !strings.HasSuffix(order[i], ".properties") && strings.HasSuffix(order[j], ".properties")
	})
	for _, p := range order {
		r.learn(p, files[p])
	}
	return r
}

var (
	javaGetter   = regexp.MustCompile(`String\s+([A-Za-z_][A-Za-z0-9_]*)\s*\(\s*\)\s*\{\s*return\s+([^;{}]+);\s*\}`)
	kotlinGetter = regexp.MustCompile(`fun\s+([A-Za-z_][A-Za-z0-9_]*)\s*\(\s*\)\s*(?::\s*String\s*)?=\s*([^\n;]+)`)
	getterCall   = regexp.MustCompile(`^(?:this\.)?([A-Za-z_][A-Za-z0-9_]*)\s*\(\s*\)$`)
)

// ModuleOf is the module a path sits in: what comes before src/main/.
func ModuleOf(p string) string {
	if i := strings.Index(p, "src/main/"); i >= 0 {
		return p[:i]
	}
	return ""
}

// baseConfig is the application properties without a profile: a value a
// stage overrides is no one value.
var baseConfig = regexp.MustCompile(`^(application|bootstrap)\.(ya?ml|properties)$`)

// Config reports whether a path is application properties without a
// profile.
func Config(p string) bool { return baseConfig.MatchString(path.Base(p)) }

var (
	javaConst   = regexp.MustCompile(`(?:static\s+final|final\s+static)\s+String\s+([A-Za-z_][A-Za-z0-9_]*)\s*=\s*"([^"\\]*)"\s*;`)
	kotlinConst = regexp.MustCompile(`const\s+val\s+([A-Za-z_][A-Za-z0-9_]*)\s*(?::\s*String\s*)?=\s*"([^"\\$]*)"`)
	scriptConst = regexp.MustCompile(`(?:export\s+)?const\s+([A-Za-z_][A-Za-z0-9_]*)\s*(?::\s*string\s*)?=\s*['"]([^'"\\$]*)['"]`)
	// Go: a const line, or a line of a const block; never an assignment.
	goConst      = regexp.MustCompile(`(?m)^const\s+([A-Za-z_][A-Za-z0-9_]*)\s*(?:string\s*)?=\s*"([^"\\]*)"`)
	goConstBlock = regexp.MustCompile(`(?ms)^const\s*\((.*?)^\)`)
	goBlockLine  = regexp.MustCompile(`(?m)^\s*([A-Za-z_][A-Za-z0-9_]*)\s*(?:string\s*)?=\s*"([^"\\]*)"`)
)

// goConsts is a Go file's string constants, as the other languages'
// regexes find them: name, value.
func goConsts(src string) [][]string {
	out := goConst.FindAllStringSubmatch(src, -1)
	for _, b := range goConstBlock.FindAllStringSubmatch(src, -1) {
		out = append(out, goBlockLine.FindAllStringSubmatch(b[1], -1)...)
	}
	return out
}

func (r *Resolver) learn(p string, body []byte) {
	if Config(p) {
		mod := ModuleOf(p)
		if r.config[mod] == nil {
			r.config[mod] = map[string]string{}
		}
		one := map[string]string{}
		if strings.HasSuffix(p, ".properties") {
			Properties(body, one)
		} else {
			FlatYAML(body, one)
		}
		for k, v := range one {
			r.config[mod][k] = v
			r.keyFile[mod+"\x00"+k] = p
		}
		return
	}
	var re, getter *regexp.Regexp
	ext := strings.ToLower(path.Ext(p))
	switch ext {
	case ".java", ".groovy", ".scala":
		re, getter = javaConst, javaGetter
	case ".kt":
		re, getter = kotlinConst, kotlinGetter
	case ".ts", ".js", ".mjs":
		re = scriptConst
	case ".go":
	default:
		return
	}
	src := StripComments(string(body))
	var found [][]string
	if ext == ".go" {
		found = goConsts(src)
	} else {
		found = re.FindAllStringSubmatch(src, -1)
	}
	own := map[string]string{}
	for _, m := range found {
		own[m[1]] = m[2]
		if old, ok := r.consts[m[1]]; ok && old != m[2] {
			r.consts[m[1]] = ""
			continue
		}
		r.consts[m[1]] = m[2]
	}
	r.fileConsts[p] = own
	if getter == nil {
		return
	}
	for _, m := range getter.FindAllStringSubmatch(src, -1) {
		r.getters[m[1]] = append(r.getters[m[1]], fileExpr{file: p, expr: strings.TrimSpace(m[2])})
	}
}

// In is the resolver reading expressions in file p.
func (r Resolver) In(p string) Resolver {
	r.file = p
	return r
}

// Modules is the modules with application properties, sorted.
func (r Resolver) Modules() []string { return SortedKeys(r.config) }

// Settings is a module's application properties, flattened.
func (r Resolver) Settings(mod string) map[string]string { return r.config[mod] }

// KeyFile is the file a module's key came from.
func (r Resolver) KeyFile(mod, key string) string { return r.keyFile[mod+"\x00"+key] }

// Property is a key's value for the module: its own, else the one value
// every module that sets the key agrees on.
func (r Resolver) Property(mod, key string) (string, bool) {
	if v, ok := r.config[mod][key]; ok {
		return v, true
	}
	var found string
	seen := false
	for _, cfg := range r.config {
		v, ok := cfg[key]
		if !ok {
			continue
		}
		if seen && v != found {
			return "", false
		}
		found, seen = v, true
	}
	return found, found != ""
}

var placeholder = regexp.MustCompile(`\$\{([^}:]+)(?::([^}]*))?\}`)

// Fill replaces every ${key} or ${key:default} in s; false when one has
// no value. used, when not nil, collects the keys read.
func (r Resolver) Fill(mod, s string, used map[string]bool) (string, bool) {
	ok := true
	out := placeholder.ReplaceAllStringFunc(s, func(m string) string {
		sub := placeholder.FindStringSubmatch(m)
		if v, found := r.Property(mod, sub[1]); found {
			if used != nil {
				used[sub[1]] = true
			}
			return v
		}
		if strings.Contains(m, ":") {
			return sub[2]
		}
		ok = false
		return ""
	})
	return out, ok && !strings.Contains(out, "${")
}

// Value is an expression's string: string literals, constants (by their
// simple name) and properties in them, joined by +; false for anything
// else (a call, a parameter, a SpEL expression).
func (r Resolver) Value(mod, expr string, used map[string]bool) (string, bool) {
	var b strings.Builder
	for _, part := range SplitTop(expr, '+') {
		part = strings.TrimSpace(part)
		switch {
		case len(part) >= 2 && (part[0] == '"' || part[0] == '\'' || part[0] == '`') && part[len(part)-1] == part[0]:
			// Kotlin writes a Spring placeholder "\${key}".
			s, ok := r.Fill(mod, strings.ReplaceAll(part[1:len(part)-1], `\$`, "$"), used)
			if !ok || strings.Contains(s, "#{") {
				return "", false
			}
			b.WriteString(s)
		case identifier.MatchString(part):
			name := part[strings.LastIndex(part, ".")+1:]
			v, ok := r.fileConsts[r.file][name]
			if !ok || strings.Contains(part, ".") {
				v, ok = r.consts[name]
			}
			if !ok || v == "" {
				return "", false
			}
			s, ok := r.Fill(mod, v, used)
			if !ok {
				return "", false
			}
			b.WriteString(s)
		default:
			return "", false
		}
	}
	return b.String(), b.Len() > 0
}

var identifier = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*(\.[A-Za-z_][A-Za-z0-9_]*)*$`)

// Values is a list expression's strings: a single expression, an array
// literal ({a, b}, [a, b], arrayOf(a, b)) or a collection factory
// (List.of, Arrays.asList, listOf, ...). Each resolved on its own; one
// that does not resolve is left out.
func (r Resolver) Values(mod, expr string, used map[string]bool) []string {
	expr = strings.TrimSpace(expr)
	for _, f := range []string{"List.of", "Set.of", "Arrays.asList", "Collections.singletonList", "Collections.singleton",
		"listOf", "setOf", "arrayOf", "Pattern.compile"} {
		if strings.HasPrefix(expr, f+"(") && strings.HasSuffix(expr, ")") {
			if f == "Pattern.compile" {
				return nil
			}
			expr = expr[len(f)+1 : len(expr)-1]
			break
		}
	}
	if n := len(expr); n >= 2 && (expr[0] == '{' && expr[n-1] == '}' || expr[0] == '[' && expr[n-1] == ']') {
		expr = expr[1 : n-1]
	}
	var out []string
	for _, e := range SplitTop(expr, ',') {
		if m := getterCall.FindStringSubmatch(strings.TrimSpace(e)); m != nil {
			for _, ret := range r.getters[m[1]] {
				if v, ok := r.In(ret.file).Value(mod, ret.expr, used); ok {
					out = append(out, v)
				}
			}
			continue
		}
		if v, ok := r.Value(mod, e, used); ok {
			out = append(out, v)
		}
	}
	return out
}

// inString steps a scan through string literals: true while s[*i] is in
// one (or opens or closes it), the index moved past an escape.
func inString(s string, i *int, quote *byte) bool {
	c := s[*i]
	if *quote != 0 {
		switch {
		case c == '\\':
			*i++
		case c == *quote:
			*quote = 0
		case c == '\n' && *quote != '`':
			// A quote left open (a Scala symbol, a stray apostrophe)
			// ends with its line, never swallows the file.
			*quote = 0
			return false
		}
		return true
	}
	if c == '"' || c == '\'' || c == '`' {
		*quote = c
		return true
	}
	return false
}

// SplitTop splits s at sep outside brackets and string literals.
func SplitTop(s string, sep byte) []string {
	var out []string
	depth, start := 0, 0
	var quote byte
	for i := 0; i < len(s); i++ {
		if inString(s, &i, &quote) {
			continue
		}
		switch c := s[i]; c {
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			depth--
		default:
			if c == sep && depth == 0 {
				out = append(out, s[start:i])
				start = i + 1
			}
		}
	}
	return append(out, s[start:])
}

// Args is the text between the parenthesis at open and its match.
func Args(s string, open int) (string, bool) {
	depth := 0
	var quote byte
	for i := open; i < len(s); i++ {
		if inString(s, &i, &quote) {
			continue
		}
		switch s[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return s[open+1 : i], true
			}
		}
	}
	return "", false
}

// Call finds every call matching re (its match ends at the opening
// parenthesis) and hands its argument list on.
func Call(src string, re *regexp.Regexp, each func(args string)) {
	for _, m := range re.FindAllStringIndex(src, -1) {
		if a, ok := Args(src, m[1]-1); ok {
			each(a)
		}
	}
}

// Named is the value of the argument name= in an annotation's arguments.
func Named(a, name string) (string, bool) {
	for _, part := range SplitTop(a, ',') {
		k, v, ok := strings.Cut(part, "=")
		if ok && strings.TrimSpace(k) == name {
			return strings.TrimSpace(v), true
		}
	}
	return "", false
}

// FlatYAML flattens a YAML file's scalars into dotted keys: its documents
// without a profile, as Config its files; one for a profile
// (spring.config.activate.on-profile, spring.profiles) is a stage's.
func FlatYAML(body []byte, out map[string]string) {
	dec := yaml.NewDecoder(bytes.NewReader(body))
	for {
		var doc any
		if err := dec.Decode(&doc); err != nil {
			return
		}
		one := map[string]string{}
		flatten("", doc, one)
		if _, ok := one["spring.config.activate.on-profile"]; ok {
			continue
		}
		if _, ok := one["spring.profiles"]; ok {
			continue
		}
		for k, v := range one {
			out[k] = v
		}
	}
}

// StripComments blanks // and /* */ comments outside string literals,
// keeping every offset: an apostrophe in a comment would otherwise open a
// literal for the scan of a call's arguments.
func StripComments(src string) string {
	b := []byte(src)
	var quote byte
	for i := 0; i < len(b); i++ {
		c := b[i]
		switch {
		case quote != 0:
			if c == '\\' {
				i++
			} else if c == quote || c == '\n' && quote != '`' {
				quote = 0
			}
		case c == '"' || c == '\'' || c == '`':
			quote = c
		case c == '/' && i+1 < len(b) && b[i+1] == '/':
			for ; i < len(b) && b[i] != '\n'; i++ {
				b[i] = ' '
			}
		case c == '/' && i+1 < len(b) && b[i+1] == '*':
			for ; i < len(b) && (b[i] != '*' || i+1 >= len(b) || b[i+1] != '/'); i++ {
				if b[i] != '\n' {
					b[i] = ' '
				}
			}
			if i+1 < len(b) {
				b[i], b[i+1] = ' ', ' '
				i++
			}
		}
	}
	return string(b)
}

func flatten(prefix string, v any, out map[string]string) {
	switch t := v.(type) {
	case map[string]any:
		for k, x := range t {
			key := k
			if prefix != "" {
				key = prefix + "." + k
			}
			flatten(key, x, out)
		}
	case string:
		out[prefix] = t
	case []any:
		var parts []string
		for _, x := range t {
			if s, ok := x.(string); ok {
				parts = append(parts, s)
			}
		}
		if len(parts) > 0 {
			out[prefix] = strings.Join(parts, ",")
		}
	}
}

// Properties reads key=value lines of a .properties file.
func Properties(body []byte, out map[string]string) {
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line[0] == '#' || line[0] == '!' {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			k, v, ok = strings.Cut(line, ":")
		}
		if ok {
			out[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
}

// SortedKeys is a map's keys, sorted.
func SortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
