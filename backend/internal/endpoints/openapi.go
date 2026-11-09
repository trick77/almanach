package endpoints

import (
	"bytes"
	"encoding/json"
	"path"
	"regexp"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/trick77/almanach/internal/coderead"
)

// spec is an OpenAPI or Swagger file's API: its title and version, its
// base path, its operations.
type spec struct {
	file, name, version, basePath string
	ops                           []op
}

// op is one operation: a method, a path below the base path, a summary.
type op struct{ method, path, summary string }

var httpMethods = []string{"get", "put", "post", "delete", "options", "head", "patch", "trace"}

// decode reads a YAML or JSON document into loose maps; nil for neither.
func decode(p string, body []byte) map[string]any {
	var doc map[string]any
	if strings.EqualFold(path.Ext(p), ".json") {
		if json.Unmarshal(body, &doc) != nil {
			return nil
		}
		return doc
	}
	if yaml.NewDecoder(bytes.NewReader(body)).Decode(&doc) != nil {
		return nil
	}
	return doc
}

// docs is the tree's files read and each one decoded at most once: a
// split spec's path items point into one file hundreds of times.
type docs struct {
	files map[string][]byte
	cache map[string]map[string]any
}

// get is a file decoded, nil for one not read or neither YAML nor JSON.
func (d *docs) get(p string) map[string]any {
	if doc, ok := d.cache[p]; ok {
		return doc
	}
	var doc map[string]any
	if body, ok := d.files[p]; ok {
		doc = decode(p, body)
	}
	d.cache[p] = doc
	return doc
}

// openAPI reads a spec: a document with a top-level openapi or swagger key
// and paths. A path item that is a $ref to another file of the tree is
// followed once.
func openAPI(p string, d *docs) (spec, bool) {
	doc := d.get(p)
	if doc == nil {
		return spec{}, false
	}
	_, oas := doc["openapi"]
	_, swagger := doc["swagger"]
	paths, ok := doc["paths"].(map[string]any)
	if !oas && !swagger || !ok {
		return spec{}, false
	}
	s := spec{file: p}
	if info, ok := doc["info"].(map[string]any); ok {
		s.name, _ = info["title"].(string)
		s.version = scalar(info["version"])
	}
	s.name = oneLine(s.name)
	if s.name == "" {
		s.name = strings.TrimSuffix(path.Base(p), path.Ext(p))
	}
	if swagger {
		bp, _ := doc["basePath"].(string)
		s.basePath = basePath(bp)
	} else if servers, ok := doc["servers"].([]any); ok && len(servers) > 0 {
		if sv, ok := servers[0].(map[string]any); ok {
			u, _ := sv["url"].(string)
			s.basePath = basePath(serverPath(u, sv))
		}
	}
	for _, route := range coderead.SortedKeys(paths) {
		item, _ := paths[route].(map[string]any)
		if ref, ok := item["$ref"].(string); ok {
			item = refItem(p, ref, d)
		}
		for _, m := range httpMethods {
			o, ok := item[m].(map[string]any)
			if !ok {
				continue
			}
			sum, _ := o["summary"].(string)
			if sum == "" {
				sum, _ = o["operationId"].(string)
			}
			s.ops = append(s.ops, op{method: strings.ToUpper(m), path: Normalize(route), summary: oneLine(sum)})
		}
	}
	return s, true
}

// externalRef is a $ref to another file: one that does not start with #.
var externalRef = regexp.MustCompile(`\$ref"?\s*:\s*["']?[^#"'\s]`)

// refHops is how many $refs a path item is followed through.
const refHops = 4

// Refs is the files the specs among files point their path items at with
// a relative $ref, through a chain of them, and that files lacks: what a
// caller reads next, then asks again (the chain goes on in what it read),
// then parses. A spec with no $ref to another file is not decoded.
func Refs(files map[string][]byte) []string {
	d := &docs{files: files, cache: map[string]map[string]any{}}
	want := map[string]bool{}
	for _, p := range coderead.SortedKeys(files) {
		if !SpecCandidate(p) || !externalRef.Match(files[p]) {
			continue
		}
		doc := d.get(p)
		_, oas := doc["openapi"]
		_, swagger := doc["swagger"]
		paths, ok := doc["paths"].(map[string]any)
		if !oas && !swagger || !ok {
			continue
		}
		for _, v := range paths {
			item, _ := v.(map[string]any)
			from := p
			for hop := 0; hop < refHops; hop++ {
				ref, ok := item["$ref"].(string)
				if !ok {
					break
				}
				f := refFile(from, ref)
				if f == "" {
					break
				}
				if _, have := files[f]; !have {
					want[f] = true
					break
				}
				item, from = fragment(d.get(f), ref), f
			}
		}
	}
	out := make([]string, 0, len(want))
	for f := range want {
		out = append(out, f)
	}
	sort.Strings(out)
	return out
}

// refFile is the tree path a relative $ref names, "" for a ref inside the
// file, a URL, or one that leaves the tree.
func refFile(from, ref string) string {
	f, _, _ := strings.Cut(ref, "#")
	if f == "" || strings.Contains(f, "://") || strings.HasPrefix(f, "/") {
		return ""
	}
	p := path.Clean(path.Join(path.Dir(from), f))
	if p == ".." || strings.HasPrefix(p, "../") {
		return ""
	}
	return p
}

// refItem is the path item a $ref names in another file, through a chain
// of them: the file whole, or the entry its fragment points at
// (#/paths/~1orders or #/orders).
func refItem(from, ref string, d *docs) map[string]any {
	var item map[string]any
	for hop := 0; hop < refHops; hop++ {
		f := refFile(from, ref)
		if f == "" {
			return nil
		}
		item = fragment(d.get(f), ref)
		next, ok := item["$ref"].(string)
		if !ok {
			return item
		}
		from, ref = f, next
	}
	return nil
}

// fragment is the entry a $ref's fragment points at in a decoded file,
// the file whole for none; nil for a file not read.
func fragment(doc map[string]any, ref string) map[string]any {
	if doc == nil {
		return nil
	}
	_, frag, _ := strings.Cut(ref, "#")
	var cur any = doc
	for _, seg := range strings.Split(strings.Trim(frag, "/"), "/") {
		if seg == "" {
			continue
		}
		seg = strings.ReplaceAll(strings.ReplaceAll(seg, "~1", "/"), "~0", "~")
		m, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = m[seg]
	}
	item, _ := cur.(map[string]any)
	return item
}

// serverPath is an OpenAPI server URL's path, its variables filled with
// their defaults; "" when one has none.
func serverPath(u string, sv map[string]any) string {
	vars, _ := sv["variables"].(map[string]any)
	for name, v := range vars {
		def := ""
		if m, ok := v.(map[string]any); ok {
			def = scalar(m["default"])
		}
		u = strings.ReplaceAll(u, "{"+name+"}", def)
	}
	if strings.Contains(u, "{") {
		return ""
	}
	if i := strings.Index(u, "://"); i >= 0 {
		rest := u[i+3:]
		j := strings.Index(rest, "/")
		if j < 0 {
			return ""
		}
		u = rest[j:]
	}
	return u
}

// basePath is a base path normalized, "" for the root.
func basePath(p string) string {
	if n := Normalize(p); n != "/" {
		return n
	}
	return ""
}

func scalar(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case int, int64, float64, bool:
		b, _ := json.Marshal(t)
		return string(b)
	}
	return ""
}

// serverGenerators are openapi-generator's and swagger-codegen's
// generators whose output serves the spec; every other one is a client,
// docs or a schema. A generator named so ("go-server", "kotlin-server")
// serves too (serverGenerator).
var serverGenerators = map[string]bool{
	"spring": true, "kotlin-spring": true, "jaxrs-spec": true, "jaxrs-cxf": true, "jaxrs-cxf-cdi": true,
	"jaxrs-cxf-extended": true, "jaxrs-resteasy": true, "jaxrs-resteasy-eap": true, "jaxrs-jersey": true,
	"java-msf4j": true, "java-pkmst": true, "java-play-framework": true, "java-vertx-web": true, "java-camel": true,
	"java-helidon-server": true, "java-micronaut-server": true, "java-undertow-server": true, "java-inflector": true,
	"python-flask": true, "python-fastapi": true, "python-aiohttp": true, "python-blueplanet": true,
	"aspnetcore": true, "php-laravel": true, "php-symfony": true, "php-slim4": true, "php-mezzio-ph": true,
	"ruby-on-rails": true, "ruby-sinatra": true, "rust-axum": true, "scala-akka-http-server": true,
	"scala-play-server": true, "scalatra": true, "nodejs-express-server": true, "go-gin-server": true,
	"go-echo-server": true, "haskell": true, "erlang-server": true, "ada-server": true, "cpp-pistache-server": true,
	"cpp-restbed-server": true, "cpp-qt-qhttpengine-server": true, "graphql-nodejs-express-server": true,
	"typescript-nestjs-server": true, "jaxrs": true, "inflector": true, "nodejs-server": true,
}

// clientLibraries are the libraries of a server generator that make a
// client of it: spring with spring-cloud is a Feign client.
var clientLibraries = map[string]bool{"spring-cloud": true, "spring-http-interface": true, "feign": true}

// serverGenerator reports whether a generator and its library serve the
// spec.
func serverGenerator(gen, lib string) bool {
	gen, lib = strings.ToLower(strings.TrimSpace(gen)), strings.ToLower(strings.TrimSpace(lib))
	if clientLibraries[lib] {
		return false
	}
	return serverGenerators[gen] || strings.HasSuffix(gen, "-server")
}

var (
	xmlTag = func(name string) *regexp.Regexp {
		return regexp.MustCompile(`<` + name + `>\s*([^<]+?)\s*</` + name + `>`)
	}
	pomExecution = regexp.MustCompile(`(?s)<execution>.*?</execution>`)
	pomPlugin    = regexp.MustCompile(`(?s)<plugin>.*?</plugin>`)
	pomInput     = xmlTag("inputSpec")
	pomGen       = xmlTag("generatorName")
	pomLang      = xmlTag("language")
	pomLib       = xmlTag("library")
	gradleInput  = regexp.MustCompile(`inputSpec\s*(?:\.set\s*\(|=)\s*(?:file\s*\(\s*)?["']([^"']+)["']`)
	gradleGen    = regexp.MustCompile(`generatorName\s*(?:\.set\s*\(|=)\s*["']([^"']+)["']`)
	gradleLib    = regexp.MustCompile(`library\s*(?:\.set\s*\(|=)\s*["']([^"']+)["']`)
	cliInput     = regexp.MustCompile(`(?:-i|--input-spec)[ =]+["']?([^\s"']+)`)
	cliGen       = regexp.MustCompile(`(?:-g|--generator-name)[ =]+["']?([^\s"']+)`)
	cliLib       = regexp.MustCompile(`--library[ =]+["']?([^\s"']+)`)
)

// gen is a generator a build runs: the build file, the spec as it names
// it, whether its output serves the spec.
type gen struct {
	build, input string
	serves       bool
}

// generators is every openapi-generator and swagger-codegen run the
// builds declare.
func generators(files map[string][]byte) []gen {
	var out []gen
	var p string
	put := func(input, g, lib string) {
		if input = strings.TrimSpace(input); input != "" && g != "" {
			out = append(out, gen{build: p, input: input, serves: serverGenerator(g, lib)})
		}
	}
	first := func(re *regexp.Regexp, s string) string {
		if m := re.FindStringSubmatch(s); m != nil {
			return m[1]
		}
		return ""
	}
	for _, p = range coderead.SortedKeys(files) {
		body := files[p]
		src := string(body)
		switch path.Base(p) {
		case "pom.xml":
			for _, plugin := range pomPlugin.FindAllString(src, -1) {
				if !strings.Contains(plugin, "openapi-generator") && !strings.Contains(plugin, "swagger-codegen") {
					continue
				}
				// An execution's own configuration over the plugin's: a
				// setting either leaves out comes from the other.
				level := pomExecution.ReplaceAllString(plugin, "")
				blocks := pomExecution.FindAllString(plugin, -1)
				if len(blocks) == 0 {
					blocks = []string{""}
				}
				either := func(re *regexp.Regexp, b string) string {
					if v := first(re, b); v != "" {
						return v
					}
					return first(re, level)
				}
				for _, b := range blocks {
					gen := either(pomGen, b)
					if gen == "" {
						gen = either(pomLang, b)
					}
					put(either(pomInput, b), gen, either(pomLib, b))
				}
			}
		case "build.gradle", "build.gradle.kts":
			for _, m := range gradleInput.FindAllStringSubmatchIndex(src, -1) {
				block := enclosing(src, m[0])
				put(src[m[2]:m[3]], first(gradleGen, block), first(gradleLib, block))
			}
		case "package.json":
			for _, line := range strings.Split(src, "\n") {
				if strings.Contains(line, "openapi-generator") || strings.Contains(line, "swagger-codegen") {
					put(first(cliInput, line), first(cliGen, line), first(cliLib, line))
				}
			}
		case "openapitools.json":
			var cfg struct {
				GeneratorCLI struct {
					Generators map[string]struct {
						GeneratorName string `json:"generatorName"`
						InputSpec     string `json:"inputSpec"`
						Glob          string `json:"glob"`
						Library       string `json:"library"`
					} `json:"generators"`
				} `json:"generator-cli"`
			}
			if json.Unmarshal(body, &cfg) == nil {
				for _, g := range cfg.GeneratorCLI.Generators {
					in := g.InputSpec
					if in == "" {
						in = g.Glob
					}
					put(in, g.GeneratorName, g.Library)
				}
			}
		}
	}
	return out
}

// specTail is the tree path a generator's input names, its leading
// variables (${project.basedir}/, $rootDir/) and ./ taken off.
func specTail(input string) string {
	segs := strings.Split(strings.ReplaceAll(input, "\\", "/"), "/")
	for len(segs) > 0 && (segs[0] == "" || segs[0] == "." || strings.ContainsAny(segs[0], "${}")) {
		segs = segs[1:]
	}
	return path.Clean(strings.Join(segs, "/"))
}

// serverGenerated reports whether a server generator reads the spec at p,
// one of specs: the input below its build file's folder is p, or the
// input's folders and name end p; an input of a name alone names the one
// spec of that name, never one of two.
func serverGenerated(gens []gen, p string, specs []string) bool {
	for _, g := range gens {
		if !g.serves {
			continue
		}
		tail := specTail(g.input)
		if tail == "." || tail == "" {
			continue
		}
		if path.Join(path.Dir(g.build), tail) == p {
			return true
		}
		if strings.Contains(tail, "/") {
			if p == tail || strings.HasSuffix(p, "/"+tail) {
				return true
			}
			continue
		}
		n := 0
		for _, s := range specs {
			if strings.EqualFold(path.Base(s), tail) {
				n++
			}
		}
		if n == 1 && strings.EqualFold(path.Base(p), tail) {
			return true
		}
	}
	return false
}

// enclosing is the brace block around offset i: from its { to its }; the
// whole text when there is none.
func enclosing(s string, i int) string {
	start, depth := -1, 0
	for j := i; j >= 0; j-- {
		switch s[j] {
		case '}':
			depth++
		case '{':
			if depth == 0 {
				start = j
			} else {
				depth--
			}
		}
		if start >= 0 {
			break
		}
	}
	if start < 0 {
		return s
	}
	depth = 0
	for j := start; j < len(s); j++ {
		switch s[j] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return s[start : j+1]
			}
		}
	}
	return s[start:]
}
