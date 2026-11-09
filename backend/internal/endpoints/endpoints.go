// Package endpoints reads which interfaces a repository offers others:
// HTTP APIs (OpenAPI and Swagger specs, the routes its code declares:
// Spring, JAX-RS, NestJS, Express, Go), gRPC services (.proto) and SOAP
// services (WSDL). Pure: bytes in, facts out, every rule a table test. A
// route's path comes from a literal, a constant or a property code can
// resolve (coderead); one it cannot is left out, never guessed.
//
// A spec or WSDL a repository holds is often another service's, kept to
// generate a client from: it offers only where its own code bears it out
// (a server generator the build runs on it, a route of its code that
// matches, a class that implements the service); else it declares.
package endpoints

import (
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/trick77/almanach/internal/coderead"
)

// Version is this package's reading: raise it with any change to what
// Parse makes of a file, and every repository is read again at the next
// sync, not only those whose head moved.
const Version = 1

// Directions: what a repository does with an interface. Offers is one its
// code serves; Declares one it holds a contract of without code saying it
// serves it (another service's spec, kept for a client). Calls is kept for
// the callers' side, which no reader fills yet.
const (
	Offers   = "offers"
	Declares = "declares"
	Calls    = "calls"
)

// Kinds of interface.
const (
	HTTP = "http"
	GRPC = "grpc"
	SOAP = "soap"
)

// Methods of the endpoints that are no HTTP method: a gRPC call, a SOAP
// operation; and AnyMethod, a route its code serves for every method.
const (
	RPC       = "RPC"
	SOAPOp    = "SOAP"
	AnyMethod = "*"
)

// API is one interface: a spec, a gRPC service, a SOAP service, or the
// routes a repository's code declares with no spec ("" Name).
type API struct {
	Kind      string   `json:"kind"`
	Name      string   `json:"name"`
	Version   string   `json:"version"`
	BasePath  string   `json:"base_path"`
	Direction string   `json:"direction"`
	Files     []string `json:"files"`
}

// Endpoint is one operation of an API: an HTTP method and path, a gRPC
// method (RPC, "/pkg.Service/Method"), a SOAP operation (SOAPOp, its
// name). Path is whole, base path in front; Summary the spec's summary or
// operation id, or the handler's name in code.
type Endpoint struct {
	Kind      string   `json:"kind"`
	API       string   `json:"api"`
	Method    string   `json:"method"`
	Path      string   `json:"path"`
	Summary   string   `json:"summary"`
	Direction string   `json:"direction"`
	Files     []string `json:"files"`
}

// Read is what Parse found: sorted, each API and endpoint once.
type Read struct {
	APIs      []API      `json:"apis"`
	Endpoints []Endpoint `json:"endpoints"`
}

// GrepPattern is what a source file must contain, case aside, to be worth
// parsing (git grep -i -E): the rest of a tree is never read. Specs,
// protos, WSDLs, builds and application properties are read by their
// names (the poller's). POSIX ERE alone, no \s or \b: git grep -E takes
// the platform's regex, and a GNU extension compiles elsewhere to a
// literal that never matches. A file of path constants alone is read too:
// @GetMapping(ApiPaths.ORDER) names one whose file says nothing else.
const GrepPattern = `(static final string|const val|const) +[a-z0-9_]+ *(: *string *)?= *["'` + "`" + `]/|mapping|@path|@(get|post|put|delete|patch|head|options)([^a-z_]|$)|@nestjs/common|setglobalprefix|` +
	`require\(['"](express|fastify)|from ['"](express|fastify)|net/http|handlefunc|\.handle\("|` +
	`\.(get|post|put|delete|patch|options|head|any)\( *["'` + "`" + `]/|` +
	`gin-gonic|labstack/echo|go-chi|gorilla/mux|implbase|@grpcservice|@grpcmethod|register[a-z0-9_]+server|addservice|` +
	`@webservice|@payloadroot|wsdl|jaxws|endpointimpl|applicationpath|openapi|swagger|context-path|base-path`

// Source reports whether a path is a file whose code Parse reads for
// routes and service implementations, when GrepPattern finds it.
func Source(p string) bool {
	switch strings.ToLower(path.Ext(p)) {
	case ".java", ".kt", ".scala", ".groovy", ".ts", ".js", ".mjs", ".go", ".xml":
		return true
	}
	return false
}

// SpecPattern is what a YAML or JSON file must contain, case aside, to be
// read as a spec candidate (git grep -i -E): a spec is read whole, so the
// source pattern's words (mapping, context-path) would read i18n files and
// Helm values for nothing.
const SpecPattern = `openapi|swagger`

// notSpecs are JSON files that name swagger as a dependency and are never
// a spec.
var notSpecs = map[string]bool{"package.json": true, "package-lock.json": true, "npm-shrinkwrap.json": true,
	"composer.json": true, "tsconfig.json": true}

// SpecCandidate reports whether a path may be an OpenAPI or Swagger spec:
// YAML or JSON, read when SpecPattern finds it; Parse knows it by its keys.
func SpecCandidate(p string) bool {
	if notSpecs[strings.ToLower(path.Base(p))] {
		return false
	}
	switch strings.ToLower(path.Ext(p)) {
	case ".yaml", ".yml", ".json":
		return true
	}
	return false
}

// Build reports whether a path is a build file Parse reads for generators:
// Maven, Gradle, npm, openapi-generator-cli.
func Build(p string) bool {
	switch path.Base(p) {
	case "pom.xml", "build.gradle", "build.gradle.kts", "package.json", "openapitools.json":
		return true
	}
	return false
}

// Config reports whether a path is application properties without a
// profile: a context path and a route's ${key} are resolved against it.
func Config(p string) bool { return coderead.Config(p) }

// WSDLNamed holds WSDLs to those a text names by their base name, any
// case (a build that generates from one, code that serves one): a vendor
// delivers hundreds and a repository uses a few.
func WSDLNamed(wsdls []string, texts map[string][]byte) []string {
	var all strings.Builder
	for _, p := range coderead.SortedKeys(texts) {
		all.WriteString(strings.ToLower(string(texts[p])))
		all.WriteByte('\n')
	}
	hay := all.String()
	var out []string
	for _, w := range wsdls {
		if namedIn(hay, strings.ToLower(path.Base(w))) {
			out = append(out, w)
		}
	}
	return out
}

// namedIn reports whether text names a file by its base name, standing on
// its own: CustomerOrder.wsdl names no Order.wsdl.
func namedIn(text, name string) bool {
	for i := 0; ; {
		j := strings.Index(text[i:], name)
		if j < 0 {
			return false
		}
		at := i + j
		if at == 0 || !nameByte(text[at-1]) {
			return true
		}
		i = at + 1
	}
}

func nameByte(b byte) bool { return isWord(b) || b == '-' || b == '.' }

// Parse reads the facts out of files, path to body: specs, protos, WSDLs,
// builds, application properties and sources together.
func Parse(files map[string][]byte) Read {
	r := coderead.NewResolver(files)
	// Every source without its comments, once.
	srcs := map[string]string{}
	for p, body := range files {
		if Source(p) {
			srcs[p] = coderead.StripComments(string(body))
		}
	}
	f := repoFacts(srcs, r)
	gens := generators(files)
	d := &docs{files: files, cache: map[string]map[string]any{}}
	var specs []spec
	var routes []route
	var services []service
	impls := implementations{}
	for _, p := range coderead.SortedKeys(files) {
		body := files[p]
		switch ext := strings.ToLower(path.Ext(p)); {
		case ext == ".proto":
			services = append(services, protoServices(p, body)...)
		case ext == ".wsdl":
			impls.wsdls++
			services = append(services, wsdlServices(p, body)...)
		case SpecCandidate(p):
			if s, ok := openAPI(p, d); ok {
				specs = append(specs, s)
			}
		}
		if src, ok := srcs[p]; ok {
			routes = append(routes, codeRoutes(p, src, r, f)...)
			impls.learn(p, src)
		}
	}
	routes = append(routes, interfaceRoutes(srcs, r, f)...)
	return assemble(specs, routes, services, gens, impls)
}

// assemble decides each spec's and service's direction and merges the
// routes code declares into the spec they match. An API is one by kind
// and name; specs of one title and two versions are two, the version in
// the name. An endpoint is one within its API: two APIs may share an
// operation (a SOAP operation's name, a /health).
func assemble(specs []spec, routes []route, services []service, gens []gen, impls implementations) Read {
	out := Read{APIs: []API{}, Endpoints: []Endpoint{}}
	apis := map[[2]string]*API{}
	eps := map[string]*Endpoint{}
	versions := map[string]map[string]bool{}
	files := make([]string, 0, len(specs))
	for _, s := range specs {
		if versions[s.name] == nil {
			versions[s.name] = map[string]bool{}
		}
		versions[s.name][s.version] = true
		files = append(files, s.file)
	}
	for i, s := range specs {
		if len(versions[s.name]) > 1 && s.version != "" {
			specs[i].name = s.name + " " + s.version
		}
	}
	addAPI := func(a API) {
		k := [2]string{a.Kind, a.Name}
		if old := apis[k]; old != nil {
			old.Files = union(old.Files, a.Files)
			if a.Direction == Offers {
				old.Direction = Offers
			}
			if old.Version == "" {
				old.Version = a.Version
			}
			if old.BasePath == "" {
				old.BasePath = a.BasePath
			}
			return
		}
		a.Files = union(nil, a.Files)
		apis[k] = &a
	}
	addEndpoint := func(e Endpoint) {
		k := e.API + "\x00" + Key(e.Kind, e.Method, e.Path)
		if old := eps[k]; old != nil {
			old.Files = union(old.Files, e.Files)
			if e.Direction == Offers {
				old.Direction = Offers
			}
			if old.Summary == "" {
				old.Summary = e.Summary
			}
			return
		}
		e.Files = union(nil, e.Files)
		eps[k] = &e
	}
	matched := make([]bool, len(routes))
	for _, s := range specs {
		// Offered when a server generator the build runs reads it, or the
		// code serves at least half its operations: one shared /health
		// makes no partner's API this repository's.
		dir := Declares
		if serverGenerated(gens, s.file, files) {
			dir = Offers
		}
		hits := 0
		for _, op := range s.ops {
			for _, rt := range routes {
				if rt.matches(op, s.basePath) {
					hits++
					break
				}
			}
		}
		if len(s.ops) > 0 && 2*hits >= len(s.ops) {
			dir = Offers
		}
		if dir == Offers {
			for _, op := range s.ops {
				for i, rt := range routes {
					if rt.matches(op, s.basePath) {
						matched[i] = true
					}
				}
			}
		}
		addAPI(API{Kind: HTTP, Name: s.name, Version: s.version, BasePath: s.basePath, Direction: dir, Files: []string{s.file}})
		for _, op := range s.ops {
			files := []string{s.file}
			if dir == Offers {
				for _, rt := range routes {
					if rt.matches(op, s.basePath) {
						files = append(files, rt.file)
					}
				}
			}
			addEndpoint(Endpoint{Kind: HTTP, API: s.name, Method: op.method, Path: joinPath(s.basePath, op.path),
				Summary: op.summary, Direction: dir, Files: files})
		}
	}
	for i, rt := range routes {
		if matched[i] {
			continue
		}
		addAPI(API{Kind: HTTP, Name: "", BasePath: "", Direction: Offers, Files: []string{rt.file}})
		addEndpoint(Endpoint{Kind: HTTP, API: "", Method: rt.method, Path: rt.path, Summary: rt.handler, Direction: Offers,
			Files: []string{rt.file}})
	}
	for _, s := range services {
		dir, by := impls.direction(s)
		files := append([]string{s.file}, by...)
		addAPI(API{Kind: s.kind, Name: s.name, BasePath: s.basePath, Direction: dir, Files: files})
		for _, op := range s.ops {
			addEndpoint(Endpoint{Kind: s.kind, API: s.name, Method: op.method, Path: op.path, Direction: dir, Files: files})
		}
	}
	for _, a := range apis {
		out.APIs = append(out.APIs, *a)
	}
	for _, e := range eps {
		out.Endpoints = append(out.Endpoints, *e)
	}
	// By kind, then name; within a kind the routes code declares without a
	// spec ("") last: the named interface is what a reader looks for.
	sort.Slice(out.APIs, func(i, j int) bool {
		a, b := out.APIs[i], out.APIs[j]
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		if (a.Name == "") != (b.Name == "") {
			return b.Name == ""
		}
		return a.Name < b.Name
	})
	sort.Slice(out.Endpoints, func(i, j int) bool {
		a, b := out.Endpoints[i], out.Endpoints[j]
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		if a.API != b.API {
			if (a.API == "") != (b.API == "") {
				return b.API == ""
			}
			return a.API < b.API
		}
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		return a.Method < b.Method
	})
	return out
}

// union is a and b's files, sorted, each once.
func union(a, b []string) []string {
	set := map[string]bool{}
	for _, f := range a {
		set[f] = true
	}
	for _, f := range b {
		if f != "" {
			set[f] = true
		}
	}
	return coderead.SortedKeys(set)
}

// Key is an endpoint's identity within a repository: its kind, its method
// and its path, parameters by position, not by name.
func Key(kind, method, p string) string {
	return kind + " " + method + " " + Template(p)
}

// param is a path parameter as the conventions write it: {id}, {id:\d+},
// :id, <id>, <int:id>.
var param = regexp.MustCompile(`^(\{[^/{}]+\}|:[A-Za-z_][A-Za-z0-9_]*\??|<[^/<>]+>)$`)

// Normalize is a path as Almanach stores it: one leading slash, no
// trailing or doubled one, every parameter written {name}.
func Normalize(p string) string {
	p = strings.TrimSpace(p)
	if i := strings.IndexAny(p, "?#"); i >= 0 {
		p = p[:i]
	}
	var segs []string
	for _, s := range strings.Split(p, "/") {
		if s == "" || s == "." {
			continue
		}
		if param.MatchString(s) {
			name := strings.Trim(s, "{}:<>?")
			if i := strings.LastIndex(name, ":"); i >= 0 && strings.HasPrefix(s, "<") {
				name = name[i+1:]
			} else if i := strings.Index(name, ":"); i >= 0 {
				name = name[:i]
			}
			s = "{" + name + "}"
		}
		segs = append(segs, s)
	}
	return "/" + strings.Join(segs, "/")
}

// Template is a path with every parameter {}: two paths that differ only
// in their parameters' names are one route.
func Template(p string) string {
	segs := strings.Split(Normalize(p), "/")
	for i, s := range segs {
		if strings.HasPrefix(s, "{") {
			segs[i] = "{}"
		}
	}
	return strings.Join(segs, "/")
}

// Match reports whether a concrete path (/orders/42) is served by a
// route's path (/orders/{id}): segment by segment, a parameter matching
// any one segment that is not empty.
func Match(route, concrete string) bool {
	rs := strings.Split(Template(route), "/")
	cs := strings.Split(Normalize(concrete), "/")
	if len(rs) != len(cs) {
		return false
	}
	for i := range rs {
		if rs[i] != "{}" && rs[i] != cs[i] {
			return false
		}
	}
	return true
}

// joinPath joins a base path and a path, normalized.
func joinPath(base, p string) string {
	return Normalize(strings.TrimSuffix(base, "/") + "/" + strings.TrimPrefix(p, "/"))
}

// oneLine is a summary on one line, cut at 200 bytes on a rune.
func oneLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= 200 {
		return s
	}
	cut := 200
	for cut > 0 && s[cut]&0xC0 == 0x80 {
		cut--
	}
	return s[:cut] + "…"
}
