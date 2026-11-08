package baseline

import (
	"bufio"
	"bytes"
	"encoding/json"
	"path"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

// A skeleton is what a router reads of a file it may not read whole: the
// lines that say what the file is and offers (its package, imports, types,
// signatures, annotations, first doc sentence; a config's keys, a
// schema's tables), every file of the tree still named by one. Line-based,
// our own code, no parser (user, 2026-11-08: a parser is a cgo dependency
// per grammar; a router tolerates the odd line a regex misses). Built from
// the body ReadFiles hands on, redacted already; a config keeps its keys,
// never a value.

// SkeletonMaxBytes caps one file's skeleton: a god class's signatures are
// its skeleton, and a router reads hundreds of them.
const SkeletonMaxBytes = 4096

// skeletonLineMax cuts one kept line: a signature, not a body on one line.
const skeletonLineMax = 160

var (
	// No "default" among the modifiers: a switch arm ("default: run(x);")
	// would pass for a signature; an interface's default method is
	// reJavaMethod's, the modifier taken off first.
	reJavaKeep = regexp.MustCompile(`^\s*(package|import)\s|^\s*@[A-Za-z]|\b(class|interface|enum|record|object)\s+[A-Z]\w*|^\s*(public|protected|private|internal|override|static|abstract|suspend|fun|open)\b[^=;]*\(`)
	// reDefault is "${key:default}" in an annotation: the default may be a
	// credential, the key is what the skeleton needs.
	reDefault = regexp.MustCompile(`\$\{([^}:]+):[^}]*\}`)
	// reJavaMethod is a signature with no modifier: an interface's methods
	// (a Spring Data repository, a Feign client), a package-private one. A
	// return type, a lower-case name, its parameters, an end of ; or {.
	reJavaMethod = regexp.MustCompile(`^\s*([\w.?]+(?:<[^=;]*>)?(?:\[\])*)\s+([a-z]\w*)\s*\([^=;{]*\)\s*(?:throws\s+[\w.,\s]+)?\s*[;{]?\s*$`)
	reTSKeep     = regexp.MustCompile(`^\s*(import|export)\s|^\s*@[A-Za-z]\w*\(|\bpath:\s*['"]`)
	reGoKeep     = regexp.MustCompile(`^package\s|^import\s|^func\s+[A-Z]|^func\s+\([^)]*\)\s+[A-Z]|^type\s+[A-Z]|^(var|const)\s+[A-Z]|^\t[A-Z]\w*(\s|$)`)
	rePyKeep     = regexp.MustCompile(`^\s*(import|from)\s|^\s*(class|def|async\s+def)\s|^\s*@[A-Za-z]`)
	reYAMLKey    = regexp.MustCompile(`^( {0,2})(- )?["']?([A-Za-z0-9_.$/{}\-]+)["']?\s*:`)
	rePropKey    = regexp.MustCompile(`^\s*([A-Za-z0-9_.\-\[\]"]+)\s*[=:]`)
	reSection    = regexp.MustCompile(`^\[\[?[A-Za-z0-9_.\-" ]+\]\]?$`)
	reSQLKeep    = regexp.MustCompile(`(?i)^\s*(create|alter|drop)\s+(or\s+replace\s+)?((global\s+|local\s+)?(temporary|temp)\s+|unlogged\s+)?(table|view|materialized\s+view|index|unique\s+index|sequence|schema|type|function|procedure|trigger)\b`)
	reSQLColumn  = regexp.MustCompile(`^\s*"?([A-Za-z_][A-Za-z0-9_]*)"?\s+[A-Za-z]`)
	rePomKeep    = regexp.MustCompile(`<(groupId|artifactId|version|packaging|module|parent)>`)
	reBPMNKeep   = regexp.MustCompile(`<(\w+:)?(process|\w*[Tt]ask|startEvent|endEvent|\w*Gateway|subProcess|callActivity|boundaryEvent|intermediate\w*Event|decision)\b[^>]*`)
	reXMLAttr    = regexp.MustCompile(`\b(id|name)="[^"]*"`)
	reProtoKeep  = regexp.MustCompile(`^\s*(package|import|message|service|rpc|enum)\s`)
	reJSONSchema = regexp.MustCompile(`"(name|namespace|type)"\s*:\s*"[^"]*"`)
	reContainer  = regexp.MustCompile(`(?i)^\s*(FROM|EXPOSE|ENTRYPOINT|CMD|USER|WORKDIR|ARG|ENV)\b`)
	rePipeline   = regexp.MustCompile(`\bstage\s*\(|^\s*stages\s*\{|^\s*pipeline\s*\{|^\s*agent\b`)
	reMDHeading  = regexp.MustCompile(`^#{1,3}\s`)
)

// Skeleton is the skeleton of the file at p with body: its kept lines in
// file order, each cut at skeletonLineMax, the whole at SkeletonMaxBytes,
// "" for an empty body. A kind it knows nothing of keeps its first three
// lines that are not blank.
func Skeleton(p string, body []byte) string {
	if len(bytes.TrimSpace(body)) == 0 {
		return ""
	}
	base := strings.ToLower(path.Base(p))
	ext := strings.ToLower(path.Ext(p))
	var keep func(line string) (string, bool)
	switch {
	case ext == ".java" || ext == ".kt" || ext == ".kts" || ext == ".scala" || ext == ".groovy" && !strings.HasPrefix(base, "jenkinsfile"):
		keep = docAnd(javaKeep)
	case ext == ".ts" || ext == ".tsx" || ext == ".js" || ext == ".jsx" || ext == ".mjs" || ext == ".cjs" || ext == ".vue":
		keep = tsKeep
	case ext == ".go":
		keep = goKeep()
	case ext == ".py":
		keep = matching(rePyKeep)
	case ext == ".yaml" || ext == ".yml":
		keep = yamlKey
	case configFile(base, ext):
		keep = propKeys()
	case ext == ".sql":
		keep = sqlKeep()
	case ext == ".xml" || ext == ".bpmn" || ext == ".dmn":
		keep = xmlKeep(body)
	case ext == ".proto":
		keep = matching(reProtoKeep)
	case ext == ".avsc":
		keep = avroKeep
	case ext == ".json":
		return jsonKeys(body)
	case base == "containerfile" || base == "dockerfile" || strings.HasPrefix(base, "dockerfile.") || strings.HasSuffix(base, ".containerfile"):
		keep = containerKeep
	case strings.HasPrefix(base, "jenkinsfile"):
		keep = matching(rePipeline)
	case ext == ".md" || ext == ".adoc":
		keep = matching(reMDHeading)
	default:
		return headLines(body)
	}
	return collect(body, keep)
}

// collect is the lines keep keeps, as keep rewrites them, within the caps:
// each line of what keep returns cut on its own (a doc line never pushes
// its declaration out), a line the same as the one before it once. A line
// repeated further on stays: the second table's id column, the annotation
// on the second method.
func collect(body []byte, keep func(string) (string, bool)) string {
	var b strings.Builder
	sc := bufio.NewScanner(bytes.NewReader(body))
	sc.Buffer(make([]byte, 0, 64*1024), len(body)+1)
	last := ""
	for sc.Scan() {
		kept, ok := keep(sc.Text())
		if !ok {
			continue
		}
		for _, line := range strings.Split(kept, "\n") {
			line = cutLine(strings.TrimRight(line, " \t\r{"))
			if line == "" || line == last {
				continue
			}
			if b.Len()+len(line)+1 > SkeletonMaxBytes {
				return b.String()
			}
			last = line
			b.WriteString(line)
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// javaKeep keeps a JVM file's package, imports, annotations, types and
// signatures, with a modifier or without one (reJavaMethod), never a
// statement that only looks like one ("return x(y);", "new A(b);").
func javaKeep(line string) (string, bool) {
	if reJavaKeep.MatchString(line) {
		return reDefault.ReplaceAllString(line, "$${$1}"), true
	}
	// An interface's default method: the modifier off, then a signature.
	sig := line
	if t := strings.TrimSpace(line); strings.HasPrefix(t, "default ") {
		sig = strings.TrimPrefix(t, "default ")
	}
	// A one-line body after the signature is no part of it.
	if i := strings.Index(sig, "{"); i >= 0 {
		sig = sig[:i+1]
	}
	m := reJavaMethod.FindStringSubmatch(sig)
	if m == nil {
		return "", false
	}
	switch m[1] {
	case "return", "new", "throw", "else", "case", "yield", "await", "assert", "goto", "do":
		return "", false
	}
	switch m[2] {
	case "if", "for", "while", "switch", "catch", "synchronized", "try":
		return "", false
	}
	return line, true
}

// configFile reports whether a file is a configuration of keys and values
// (properties, env, ini, toml, conf, tfvars, npmrc): its keys alone go into
// the skeleton, as a YAML's do, never a value a credential may be.
func configFile(base, ext string) bool {
	switch ext {
	case ".properties", ".env", ".ini", ".toml", ".conf", ".cfg", ".tfvars", ".npmrc":
		return true
	}
	return base == ".env" || strings.HasPrefix(base, ".env.") || base == ".npmrc"
}

// cutLine is line within skeletonLineMax bytes, cut where a rune starts.
func cutLine(line string) string {
	if len(line) <= skeletonLineMax {
		return line
	}
	n := skeletonLineMax
	for n > 0 && !utf8.RuneStart(line[n]) {
		n--
	}
	return line[:n] + "…"
}

// tsKeep keeps a TS/JS file's imports, exports, decorators and route
// paths; an exported constant without its value, which may be a key.
func tsKeep(line string) (string, bool) {
	if !reTSKeep.MatchString(line) {
		return "", false
	}
	t := strings.TrimSpace(line)
	// A route table on one line keeps its paths: a path is no credential.
	if strings.Contains(t, "path:") {
		return line, true
	}
	for _, decl := range []string{"export const ", "export let ", "export var "} {
		if strings.HasPrefix(t, decl) {
			if i := strings.Index(t, "="); i >= 0 {
				return strings.TrimSpace(t[:i]), true
			}
		}
	}
	return line, true
}

// containerKeep keeps a Containerfile's structure; an ENV or ARG by its
// name alone, never a value baked into the image.
func containerKeep(line string) (string, bool) {
	if !reContainer.MatchString(line) {
		return "", false
	}
	f := strings.Fields(line)
	switch strings.ToUpper(f[0]) {
	case "ENV", "ARG":
		names := []string{strings.ToUpper(f[0])}
		for i, w := range f[1:] {
			name, _, hasEq := strings.Cut(w, "=")
			// "ENV KEY value": the old form, one key and its value.
			if !hasEq && i > 0 {
				break
			}
			names = append(names, name)
		}
		return strings.Join(names, " "), true
	}
	return line, true
}

func matching(re *regexp.Regexp) func(string) (string, bool) {
	return func(line string) (string, bool) { return line, re.MatchString(line) }
}

// docAnd keeps what keep keeps and the first sentence of each doc comment
// that opens with text: what the type or method is for, in its author's words.
func docAnd(keep func(string) (string, bool)) func(string) (string, bool) {
	inDoc, taken := false, false
	return func(line string) (string, bool) {
		t := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(t, "/**"):
			inDoc, taken = true, false
			t = strings.TrimSpace(strings.TrimPrefix(t, "/**"))
		case inDoc && strings.HasPrefix(t, "*/"):
			inDoc = false
			return "", false
		case inDoc:
			t = strings.TrimSpace(strings.TrimPrefix(t, "*"))
		default:
			return keep(line)
		}
		if strings.HasSuffix(t, "*/") {
			inDoc, t = false, strings.TrimSpace(strings.TrimSuffix(t, "*/"))
		}
		if taken || t == "" || strings.HasPrefix(t, "@") {
			return "", false
		}
		taken = true
		if i := strings.Index(t, ". "); i >= 0 {
			t = t[:i+1]
		}
		return "/** " + t, true
	}
}

// goKeep keeps a Go file's package, imports (each of an import block),
// exported declarations, a grouped type, var or const block's exported
// names (sentinel errors among them), and the first line of each doc
// comment before one.
func goKeep() func(string) (string, bool) {
	inImports, inGroup, doc := false, false, ""
	return func(line string) (string, bool) {
		switch {
		case strings.HasPrefix(line, "import ("):
			inImports = true
			return "import (", true
		case inImports && strings.TrimSpace(line) == ")":
			inImports = false
			return ")", true
		case inImports:
			return "\t" + strings.TrimSpace(line), strings.TrimSpace(line) != ""
		case line == "type (" || line == "var (" || line == "const (":
			inGroup = true
			return line, true
		case inGroup && line == ")":
			inGroup = false
			return ")", true
		case strings.HasPrefix(line, "// "):
			if doc == "" {
				doc = line
			}
			return "", false
		case strings.HasPrefix(line, "\t") && !inGroup:
			doc = ""
			return "", false
		case reGoKeep.MatchString(line):
			d := doc
			doc = ""
			// A var or const by its name and type, never its value.
			if inGroup || strings.HasPrefix(line, "var ") || strings.HasPrefix(line, "const ") {
				if i := strings.Index(line, "="); i >= 0 {
					line = strings.TrimRight(line[:i], " \t")
				}
			}
			if d != "" && !strings.HasPrefix(line, "package") && !strings.HasPrefix(line, "import") {
				return d + "\n" + line, true
			}
			return line, true
		}
		doc = ""
		return "", false
	}
}

// yamlKey keeps a key of the first two levels, never its value: a value may
// be a credential redact has no shape for.
func yamlKey(line string) (string, bool) {
	m := reYAMLKey.FindStringSubmatch(line)
	if m == nil || strings.HasPrefix(strings.TrimSpace(line), "#") {
		return "", false
	}
	return m[1] + m[2] + m[3] + ":", true
}

// propKeys keeps a property's key, never its value, and a section header
// of an ini or toml file; a line a backslash continues is the value's, not
// a key of its own.
func propKeys() func(string) (string, bool) {
	continued := false
	return func(line string) (string, bool) {
		t := strings.TrimSpace(line)
		wasContinued := continued
		continued = strings.HasSuffix(t, `\`)
		switch {
		case wasContinued, t == "", strings.HasPrefix(t, "#"), strings.HasPrefix(t, "!"), strings.HasPrefix(t, ";"):
			return "", false
		case reSection.MatchString(t):
			return t, true
		}
		m := rePropKey.FindStringSubmatch(line)
		if m == nil {
			return "", false
		}
		return m[1], true
	}
}

// sqlKeep keeps the statements that make a schema and each column a CREATE
// TABLE names, by its name alone. A table ends at a line opening with ")"
// or ending with ";", its last column kept first: "amount NUMERIC);".
func sqlKeep() func(string) (string, bool) {
	inTable := false
	return func(line string) (string, bool) {
		t := strings.TrimSpace(line)
		if reSQLKeep.MatchString(line) {
			lower := strings.ToLower(t)
			// Columns only of a table defined by them: never one made AS a
			// SELECT, whose next lines are a query.
			inTable = strings.HasPrefix(lower, "create") && strings.Contains(lower, "table") && !strings.HasSuffix(t, ";") &&
				!strings.HasSuffix(lower, " as") && !strings.Contains(lower, " as select")
			return t, true
		}
		if !inTable {
			return "", false
		}
		if strings.HasPrefix(t, ")") {
			inTable = false
			return "", false
		}
		if strings.HasSuffix(t, ";") {
			inTable = false
		}
		if m := reSQLColumn.FindStringSubmatch(line); m != nil {
			switch strings.ToLower(m[1]) {
			case "constraint", "primary", "foreign", "unique", "check", "key", "index":
				return "", false
			}
			return "  " + m[1], true
		}
		return "", false
	}
}

// xmlKeep keeps a POM's coordinates and modules, a BPMN's process elements
// by id and name, anything else's first lines.
func xmlKeep(body []byte) func(string) (string, bool) {
	switch {
	case bytes.Contains(body, []byte("<project")) && bytes.Contains(body, []byte("<artifactId>")):
		return func(line string) (string, bool) { return strings.TrimSpace(line), rePomKeep.MatchString(line) }
	case bytes.Contains(body, []byte("bpmn")) || bytes.Contains(body, []byte("dmn")):
		return func(line string) (string, bool) {
			m := reBPMNKeep.FindString(line)
			if m == "" {
				return "", false
			}
			tag := m
			if i := strings.IndexAny(tag, " \t"); i >= 0 {
				tag = tag[:i]
			}
			attrs := reXMLAttr.FindAllString(m, -1)
			return strings.TrimSpace(tag + " " + strings.Join(attrs, " ")), true
		}
	}
	n := 0
	return func(line string) (string, bool) {
		if strings.TrimSpace(line) == "" || n >= 3 {
			return "", false
		}
		n++
		return strings.TrimSpace(line), true
	}
}

// avroKeep keeps an Avro schema's names, namespaces and types, which are
// what the schema defines.
func avroKeep(line string) (string, bool) {
	ms := reJSONSchema.FindAllString(line, -1)
	return strings.Join(ms, ", "), len(ms) > 0
}

// jsonKeys is a JSON file's top-level keys, in their order, whatever its
// indentation or none (a minified file); a body that is no JSON object (a
// list, JSON with comments) its first three lines.
func jsonKeys(body []byte) string {
	dec := json.NewDecoder(bytes.NewReader(body))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return headLines(body)
	}
	var keys []string
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return headLines(body)
		}
		key, _ := tok.(string)
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			return headLines(body)
		}
		keys = append(keys, strconv.Quote(key)+":")
	}
	return collect([]byte(strings.Join(keys, "\n")), func(l string) (string, bool) { return l, true })
}

// headLineCount is how many lines a file of a kind no rule knows keeps.
const headLineCount = 3

// headLines is a file's first headLineCount lines that are not blank.
func headLines(body []byte) string {
	kept := 0
	return collect(body, func(line string) (string, bool) {
		if strings.TrimSpace(line) == "" || kept >= headLineCount {
			return "", false
		}
		kept++
		return line, true
	})
}
