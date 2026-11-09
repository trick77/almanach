package endpoints

import (
	"path"
	"regexp"
	"strings"

	"github.com/trick77/almanach/internal/coderead"
)

// route is an HTTP route a repository's code declares: its method, its
// whole path, the path below the prefixes the application sets (a
// context path, a global prefix), its handler's name and file.
type route struct {
	method, path, local, handler, file string
}

// matches reports whether the route serves a spec's operation: the same
// method (or any) and path, the spec's base path and the application's
// prefix on either side or neither.
func (rt route) matches(o op, base string) bool {
	if rt.method != AnyMethod && rt.method != o.method {
		return false
	}
	spec := []string{Template(joinPath(base, o.path)), Template(o.path)}
	for _, c := range []string{Template(rt.path), Template(rt.local)} {
		for _, s := range spec {
			if c == s {
				return true
			}
		}
	}
	return false
}

// facts are what the routes of one file rest on that other files hold:
// JAX-RS's @ApplicationPath, NestJS's global prefix, each when the
// repository sets one value.
type facts struct {
	appPath, nestPrefix string
}

var (
	applicationPath = regexp.MustCompile(`@ApplicationPath\s*\(`)
	globalPrefix    = regexp.MustCompile(`\.setGlobalPrefix\s*\(`)
)

// repoFacts reads facts from every source, srcs without comments.
func repoFacts(srcs map[string]string, r coderead.Resolver) facts {
	one := func(re *regexp.Regexp) string {
		vals := map[string]bool{}
		for _, p := range coderead.SortedKeys(srcs) {
			coderead.Call(srcs[p], re, func(a string) {
				expr := coderead.SplitTop(a, ',')[0]
				if v, ok := r.In(p).Value(coderead.ModuleOf(p), expr, nil); ok {
					vals[v] = true
				}
			})
		}
		if len(vals) == 1 {
			return coderead.SortedKeys(vals)[0]
		}
		return ""
	}
	return facts{appPath: one(applicationPath), nestPrefix: one(globalPrefix)}
}

// codeRoutes is the routes a source declares.
func codeRoutes(p, src string, r coderead.Resolver, f facts) []route {
	switch strings.ToLower(path.Ext(p)) {
	case ".java", ".kt", ".scala", ".groovy":
		return jvmRoutes(p, p, src, r, f, false)
	case ".ts", ".js", ".mjs":
		if strings.Contains(src, "@nestjs/common") {
			return nestRoutes(p, src, r, f)
		}
		return expressRoutes(p, src, r)
	case ".go":
		return goRoutes(p, src, r)
	}
	return nil
}

// ann is an annotation or decorator and its arguments.
type ann struct{ name, args string }

// group is the annotations that stand together on one declaration, and
// the declaration's head up to its body.
type group struct {
	anns []ann
	decl string
	// kind is the declaration's: "class", "interface", or "" for a member.
	kind    string
	handler string
	// depth is how many braces enclose it: 0 for a top-level type.
	depth int
}

// braces tracks the brace depth of a source as a scan moves forward,
// string literals skipped.
type braces struct {
	src        string
	pos, depth int
	quote      byte
}

// at is the depth at offset i, i not before the last asked.
func (b *braces) at(i int) int {
	for ; b.pos < i && b.pos < len(b.src); b.pos++ {
		c := b.src[b.pos]
		if b.quote != 0 {
			if c == '\\' {
				b.pos++
			} else if c == b.quote || c == '\n' && b.quote != '`' {
				b.quote = 0
			}
			continue
		}
		switch c {
		case '"', '\'', '`':
			b.quote = c
		case '{':
			b.depth++
		case '}':
			b.depth--
		}
	}
	return b.depth
}

var (
	annStart  = regexp.MustCompile(`@([A-Za-z_][A-Za-z0-9_.]*)`)
	typeDecl  = regexp.MustCompile(`\b(class|interface|object|enum|record)\s+[A-Za-z_]`)
	ifaceDecl = regexp.MustCompile(`\binterface\s+[A-Za-z_]|@interface\b`)
	firstCall = regexp.MustCompile(`([A-Za-z_][A-Za-z0-9_]*)\s*(?:<[^()]*>)?\s*\(`)
)

// groups reads a source's annotations in order, those standing together
// as one group with the declaration they annotate.
func groups(src string) []group {
	var out []group
	var cur []ann
	end, depth := 0, 0
	scan := &braces{src: src}
	flush := func(from int) {
		if len(cur) == 0 {
			return
		}
		rest := src[from:]
		if i := strings.IndexAny(rest, "{;="); i >= 0 {
			rest = rest[:i]
		}
		g := group{anns: cur, decl: rest, depth: depth}
		switch {
		case ifaceDecl.MatchString(rest):
			g.kind = "interface"
		case typeDecl.MatchString(rest):
			g.kind = "class"
		default:
			if m := firstCall.FindStringSubmatch(rest); m != nil {
				g.handler = m[1]
			}
		}
		out = append(out, g)
		cur = nil
	}
	for _, m := range annStart.FindAllStringSubmatchIndex(src, -1) {
		if m[0] < end {
			continue
		}
		if m[0] > 0 && (src[m[0]-1] == '.' || isWord(src[m[0]-1])) {
			continue
		}
		if len(cur) > 0 && strings.TrimSpace(src[end:m[0]]) != "" {
			flush(end)
		}
		if len(cur) == 0 {
			depth = scan.at(m[0])
		}
		a := ann{name: src[m[2]:m[3]]}
		end = m[1]
		i := end
		for i < len(src) && (src[i] == ' ' || src[i] == '\t' || src[i] == '\n' || src[i] == '\r') {
			i++
		}
		if i < len(src) && src[i] == '(' {
			if args, ok := coderead.Args(src, i); ok {
				a.args = args
				end = i + len(args) + 2
			}
		}
		cur = append(cur, a)
	}
	flush(end)
	return out
}

func isWord(b byte) bool {
	return b == '_' || b >= '0' && b <= '9' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z'
}

// short is an annotation's simple name: Mapping of org.x.Mapping.
func (a ann) short() string { return a.name[strings.LastIndex(a.name, ".")+1:] }

// find is the group's first annotation of one of the names.
func (g group) find(names ...string) (ann, bool) {
	for _, a := range g.anns {
		for _, n := range names {
			if a.short() == n {
				return a, true
			}
		}
	}
	return ann{}, false
}

var identifierKey = regexp.MustCompile(`^\s*[A-Za-z_][A-Za-z0-9_]*\s*$`)

// annValue is the expression an annotation's arguments give one of names
// (value=, path=), or their first positional one; false when they give
// none: the annotation's default.
func annValue(args string, names ...string) (string, bool) {
	if strings.TrimSpace(args) == "" {
		return "", false
	}
	for _, part := range coderead.SplitTop(args, ',') {
		k, v, ok := strings.Cut(part, "=")
		if ok && identifierKey.MatchString(k) {
			for _, n := range names {
				if strings.TrimSpace(k) == n {
					return strings.TrimSpace(v), true
				}
			}
			continue
		}
		if strings.TrimSpace(part) != "" {
			return strings.TrimSpace(part), true
		}
	}
	return "", false
}

// paths is the paths an annotation's argument names: [""] for none, nil
// when it names one code cannot resolve.
func paths(r coderead.Resolver, mod, args string, names ...string) []string {
	expr, ok := annValue(args, names...)
	if !ok {
		return []string{""}
	}
	return pathValues(r, mod, expr)
}

// pathValues is a path expression's values, a list's each; an empty
// literal is the empty path, which coderead resolves to nothing. nil when
// none resolves.
func pathValues(r coderead.Resolver, mod, expr string) []string {
	expr = strings.TrimSpace(expr)
	if n := len(expr); n >= 2 && (expr[0] == '{' && expr[n-1] == '}' || expr[0] == '[' && expr[n-1] == ']') {
		expr = expr[1 : n-1]
	}
	var out []string
	for _, e := range coderead.SplitTop(expr, ',') {
		switch e = strings.TrimSpace(e); e {
		case `""`, `''`, "``":
			out = append(out, "")
		case "":
		default:
			out = append(out, r.Values(mod, e, nil)...)
		}
	}
	return out
}

// springVerbs are Spring's shortcut mappings by their method.
var springVerbs = map[string]string{"GetMapping": "GET", "PostMapping": "POST", "PutMapping": "PUT",
	"DeleteMapping": "DELETE", "PatchMapping": "PATCH"}

var (
	requestMethod = regexp.MustCompile(`\b(GET|POST|PUT|DELETE|PATCH|HEAD|OPTIONS|TRACE)\b`)
	jaxrsVerbs    = []string{"GET", "POST", "PUT", "DELETE", "PATCH", "HEAD", "OPTIONS"}
	// clientMarks are a file's marks of an HTTP client declared by
	// annotations, which are no routes it serves: the callers' side.
	clientMarks = regexp.MustCompile(`@(FeignClient|HttpExchange|RegisterRestClient|RegisterClient)\b`)
)

// contextPath is the prefix a module's application puts before every
// route: a servlet context path, a WebFlux base path, Quarkus's root path;
// false when one is set to a value code cannot resolve. The module's own
// alone: another module's is another application's.
func contextPath(r coderead.Resolver, mod string) (string, bool) {
	for _, k := range []string{"server.servlet.context-path", "spring.webflux.base-path", "quarkus.http.root-path"} {
		if v, ok := r.Settings(mod)[k]; ok {
			s, ok := r.Fill(mod, v, nil)
			return s, ok
		}
	}
	return "", true
}

// jvmRoutes reads Spring MVC/WebFlux and JAX-RS routes: a class's prefix,
// its methods' mappings. iface reads the routes an interface declares
// (interfaceRoutes), else a class's. home is the file that serves them,
// whose module's context path they sit under: p, or the class that
// implements the interface.
func jvmRoutes(p, home, src string, r coderead.Resolver, f facts, iface bool) []route {
	if clientMarks.MatchString(src) {
		return nil
	}
	mod := coderead.ModuleOf(p)
	r = r.In(p)
	ctx, ok := contextPath(r, coderead.ModuleOf(home))
	if !ok {
		return nil
	}
	var out []route
	var prefixes []string
	var spring, jaxrs, inType bool
	for _, g := range groups(src) {
		if g.kind != "" {
			// A type inside a type (a DTO record, a request class) leaves
			// its controller's routes as they are.
			if g.depth > 0 && inType {
				continue
			}
			inType = (g.kind == "interface") == iface
			prefixes, spring, jaxrs = []string{""}, false, false
			if _, ok := g.find("RestController", "Controller"); ok {
				spring = true
			}
			if a, ok := g.find("RequestMapping"); ok {
				spring = true
				prefixes = paths(r, mod, a.args, "value", "path")
			}
			if a, ok := g.find("Path"); ok {
				jaxrs = true
				prefixes = paths(r, mod, a.args, "value")
			}
			if iface && !spring && !jaxrs {
				// An interface is a controller's only by its own annotations.
				inType = false
			}
			continue
		}
		if !inType || prefixes == nil {
			continue
		}
		var methods, subs []string
		for _, a := range g.anns {
			switch n := a.short(); {
			case springVerbs[n] != "" && (spring || iface):
				methods = []string{springVerbs[n]}
				subs = paths(r, mod, a.args, "value", "path")
			case n == "RequestMapping" && (spring || iface):
				subs = paths(r, mod, a.args, "value", "path")
				methods = nil
				if m, ok := coderead.Named(a.args, "method"); ok {
					methods = requestMethod.FindAllString(m, -1)
				}
				if len(methods) == 0 {
					methods = []string{AnyMethod}
				}
			}
		}
		if jaxrs {
			for _, v := range jaxrsVerbs {
				if _, ok := g.find(v); ok {
					methods = append(methods, v)
				}
			}
			if len(methods) > 0 {
				subs = []string{""}
				if a, ok := g.find("Path"); ok {
					subs = paths(r, mod, a.args, "value")
				}
			}
		}
		if len(methods) == 0 || subs == nil {
			continue
		}
		app := ctx
		if jaxrs && !spring {
			app = joinPath(ctx, f.appPath)
		}
		for _, pre := range prefixes {
			for _, sub := range subs {
				local := joinPath(pre, sub)
				for _, m := range methods {
					out = append(out, route{method: m, path: joinPath(app, local), local: local, handler: g.handler, file: p})
				}
			}
		}
	}
	return out
}

var (
	implementsOf = regexp.MustCompile(`(?s)\bclass\s+[A-Za-z_][A-Za-z0-9_]*[^{]*`)
	ifaceName    = regexp.MustCompile(`\binterface\s+([A-Za-z_][A-Za-z0-9_]*)`)
	identWord    = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*`)
)

// interfaceRoutes reads the routes an interface declares for a class that
// implements it (an API interface written by hand, a controller or JAX-RS
// resource implementing it): the interface's file and the class's, the
// context path the class's module sets. A client's interface
// (clientMarks) declares none. srcs are the sources without comments.
func interfaceRoutes(srcs map[string]string, r coderead.Resolver, f facts) []route {
	// The first class, by path, whose header names each type after its own
	// name: what it extends or implements.
	impl := map[string]string{}
	for _, p := range coderead.SortedKeys(srcs) {
		if !isJVM(p) {
			continue
		}
		for _, h := range implementsOf.FindAllString(srcs[p], -1) {
			words := identWord.FindAllString(h, -1)
			for _, w := range words[min(2, len(words)):] {
				if _, ok := impl[w]; !ok {
					impl[w] = p
				}
			}
		}
	}
	var out []route
	for _, p := range coderead.SortedKeys(srcs) {
		if !isJVM(p) {
			continue
		}
		m := ifaceName.FindStringSubmatch(srcs[p])
		if m == nil {
			continue
		}
		c, ok := impl[m[1]]
		if !ok || c == p {
			continue
		}
		for _, rt := range jvmRoutes(p, c, srcs[p], r, f, true) {
			out = append(out, rt, route{method: rt.method, path: rt.path, local: rt.local, handler: rt.handler, file: c})
		}
	}
	return out
}

func isJVM(p string) bool {
	switch strings.ToLower(path.Ext(p)) {
	case ".java", ".kt", ".scala", ".groovy":
		return true
	}
	return false
}

// nestVerbs are NestJS's route decorators by their method.
var nestVerbs = map[string]string{"Get": "GET", "Post": "POST", "Put": "PUT", "Delete": "DELETE", "Patch": "PATCH",
	"Options": "OPTIONS", "Head": "HEAD", "All": AnyMethod}

// nestRoutes reads a NestJS controller: @Controller's path, each
// handler's decorator, the global prefix in front.
func nestRoutes(p, src string, r coderead.Resolver, f facts) []route {
	r = r.In(p)
	var out []route
	var prefixes []string
	for _, g := range groups(src) {
		if g.kind != "" {
			if g.depth > 0 && prefixes != nil {
				continue
			}
			prefixes = nil
			if a, ok := g.find("Controller"); ok {
				prefixes = nestPaths(r, a.args)
			}
			continue
		}
		if prefixes == nil {
			continue
		}
		for _, a := range g.anns {
			m, ok := nestVerbs[a.short()]
			if !ok {
				continue
			}
			subs := nestPaths(r, a.args)
			for _, pre := range prefixes {
				for _, sub := range subs {
					local := joinPath(pre, sub)
					out = append(out, route{method: m, path: joinPath(f.nestPrefix, local), local: local, handler: g.handler, file: p})
				}
			}
		}
	}
	return out
}

// nestPaths is a NestJS decorator's paths: a string, an array, or an
// options object's path; [""] for none, nil for one code cannot resolve.
func nestPaths(r coderead.Resolver, args string) []string {
	a := strings.TrimSpace(args)
	if a == "" {
		return []string{""}
	}
	if strings.HasPrefix(a, "{") {
		inner := strings.TrimSuffix(strings.TrimPrefix(a, "{"), "}")
		for _, part := range coderead.SplitTop(inner, ',') {
			if k, v, ok := strings.Cut(part, ":"); ok && strings.TrimSpace(k) == "path" {
				a = strings.TrimSpace(v)
				break
			}
			a = ""
		}
		if a == "" {
			return []string{""}
		}
	}
	vals := pathValues(r, "", a)
	if len(vals) == 0 {
		return nil
	}
	return vals
}

var (
	// An app: express(), fastify(), or straight off the require.
	expressApp    = regexp.MustCompile(`([A-Za-z_$][A-Za-z0-9_$]*)\s*=\s*(?:express|fastify|Fastify|require\s*\(\s*['"](?:express|fastify)['"]\s*\))\s*\(`)
	expressRouter = regexp.MustCompile(`([A-Za-z_$][A-Za-z0-9_$]*)\s*=\s*(?:(?:express|require\s*\(\s*['"]express['"]\s*\))\s*\.\s*)?Router\s*\(`)
	expressUse    = regexp.MustCompile(`([A-Za-z_$][A-Za-z0-9_$]*)\s*\.\s*use\s*\(`)
	expressRoute  = regexp.MustCompile(`([A-Za-z_$][A-Za-z0-9_$]*)\s*\.\s*(get|post|put|delete|patch|options|head|all)\s*\(`)
)

// expressRoutes reads Express and Fastify routes on an application, or on
// a router the same file mounts on one; a router mounted elsewhere is left
// out: its prefix is another file's.
func expressRoutes(p, src string, r coderead.Resolver) []route {
	if !strings.Contains(src, "express") && !strings.Contains(src, "fastify") {
		return nil
	}
	r = r.In(p)
	prefix := map[string]string{}
	for _, m := range expressApp.FindAllStringSubmatch(src, -1) {
		prefix[m[1]] = ""
	}
	routers := map[string]bool{}
	for _, m := range expressRouter.FindAllStringSubmatch(src, -1) {
		routers[m[1]] = true
	}
	for _, m := range expressUse.FindAllStringSubmatchIndex(src, -1) {
		base, isApp := prefix[src[m[2]:m[3]]]
		a, ok := coderead.Args(src, m[1]-1)
		if !isApp || !ok {
			continue
		}
		parts := coderead.SplitTop(a, ',')
		if len(parts) != 2 || !routers[strings.TrimSpace(parts[1])] {
			continue
		}
		if v, ok := r.Value("", parts[0], nil); ok {
			prefix[strings.TrimSpace(parts[1])] = joinPath(base, v)
		}
	}
	var out []route
	for _, m := range expressRoute.FindAllStringSubmatchIndex(src, -1) {
		recv := src[m[2]:m[3]]
		pre, ok := prefix[recv]
		if !ok {
			continue
		}
		a, ok := coderead.Args(src, m[1]-1)
		if !ok {
			continue
		}
		v, ok := r.Value("", coderead.SplitTop(a, ',')[0], nil)
		if !ok || !strings.HasPrefix(v, "/") {
			continue
		}
		method := strings.ToUpper(src[m[4]:m[5]])
		if method == "ALL" {
			method = AnyMethod
		}
		local := joinPath(pre, v)
		out = append(out, route{method: method, path: local, local: local, file: p})
	}
	return out
}

var (
	// goRoot is a router made where the file makes it, or a parameter or
	// field of a root router's type: its routes are its own paths.
	goRoot      = regexp.MustCompile(`([A-Za-z_][A-Za-z0-9_]*)\s*:?=\s*(?:gin\.(?:New|Default)|echo\.New|chi\.(?:NewRouter|NewMux)|mux\.NewRouter|http\.NewServeMux)\s*\(`)
	goRootParam = regexp.MustCompile(`([A-Za-z_][A-Za-z0-9_]*)\s+\*?(?:gin\.Engine|echo\.Echo|chi\.Mux|mux\.Router|http\.ServeMux)\b`)
	goGroup     = regexp.MustCompile(`([A-Za-z_][A-Za-z0-9_]*)\s*:?=\s*([A-Za-z_][A-Za-z0-9_]*)\s*\.\s*Group\s*\(`)
	goSub       = regexp.MustCompile(`([A-Za-z_][A-Za-z0-9_]*)\s*:?=\s*([A-Za-z_][A-Za-z0-9_]*)\s*\.\s*PathPrefix\s*\(`)
	goHandle    = regexp.MustCompile(`([A-Za-z_][A-Za-z0-9_]*)\s*\.\s*(HandleFunc|Handle|GET|POST|PUT|DELETE|PATCH|OPTIONS|HEAD|Any|Get|Post|Put|Delete|Patch|Options|Head)\s*\(`)
	goMethods   = regexp.MustCompile(`^\s*\.\s*Methods\s*\(`)
	goPattern   = regexp.MustCompile(`^(?:([A-Z]+)\s+)?(/.*)$`)
	// goNested is chi's nesting by closure: a route inside one has the
	// closure's prefix, which a line-based read cannot follow.
	goNested = regexp.MustCompile(`\.\s*(Route|Mount)\s*\(`)
	// goServes is a file that serves what it routes: a router made
	// elsewhere is often mounted under a prefix (chi's Mount, a gorilla
	// subrouter), which a file that only builds it does not show.
	goServes = regexp.MustCompile(`\bListenAndServe(TLS)?\s*\(|\.Serve(TLS)?\s*\(|\.Run(TLS)?\s*\(|\.Start(TLS)?\s*\(|&?http\.Server\s*\{`)
)

// goRoutes reads net/http, gorilla/mux, chi, gin and echo routes on a
// router whose prefix the file shows, in a file that serves it; a router
// built or handed in elsewhere is left out: its prefix is another file's.
func goRoutes(p, src string, r coderead.Resolver) []route {
	if !goServes.MatchString(src) {
		return nil
	}
	r = r.In(p)
	prefix := map[string]string{"http": ""}
	for _, m := range goRoot.FindAllStringSubmatch(src, -1) {
		prefix[m[1]] = ""
	}
	for _, m := range goRootParam.FindAllStringSubmatch(src, -1) {
		prefix[m[1]] = ""
	}
	for _, re := range []*regexp.Regexp{goGroup, goSub} {
		for _, m := range re.FindAllStringSubmatchIndex(src, -1) {
			parent, ok := prefix[src[m[4]:m[5]]]
			a, okArgs := coderead.Args(src, m[1]-1)
			if !ok || !okArgs {
				continue
			}
			if v, ok := r.Value("", coderead.SplitTop(a, ',')[0], nil); ok {
				prefix[src[m[2]:m[3]]] = joinPath(parent, v)
			}
		}
	}
	nested := goNested.MatchString(src)
	var out []route
	for _, m := range goHandle.FindAllStringSubmatchIndex(src, -1) {
		recv, verb := src[m[2]:m[3]], src[m[4]:m[5]]
		pre, ok := prefix[recv]
		if !ok || recv == "http" && verb != "HandleFunc" && verb != "Handle" {
			continue
		}
		// Nested by closure, a router's routes may sit under any prefix: the
		// default mux's alone are known (a closure's router shadows the
		// root's name).
		if nested && recv != "http" {
			continue
		}
		a, ok := coderead.Args(src, m[1]-1)
		if !ok {
			continue
		}
		v, ok := r.Value("", coderead.SplitTop(a, ',')[0], nil)
		if !ok {
			continue
		}
		method := AnyMethod
		if verb != "HandleFunc" && verb != "Handle" {
			method = strings.ToUpper(verb)
			if method == "ANY" {
				method = AnyMethod
			}
		}
		pm := goPattern.FindStringSubmatch(v)
		if pm == nil {
			continue
		}
		if pm[1] != "" {
			method = pm[1]
		}
		methods := []string{method}
		// gorilla/mux: .Methods("GET", "POST") after the call.
		after := src[m[1]-1+len(a)+2:]
		if mm := goMethods.FindStringIndex(after); mm != nil {
			if ma, ok := coderead.Args(after, mm[1]-1); ok {
				methods = nil
				for _, e := range coderead.SplitTop(ma, ',') {
					if s, ok := r.Value("", e, nil); ok {
						methods = append(methods, strings.ToUpper(s))
					}
				}
			}
		}
		local := pm[2]
		for _, me := range methods {
			out = append(out, route{method: me, path: joinPath(pre, local), local: joinPath(pre, local), file: p})
		}
	}
	return out
}
