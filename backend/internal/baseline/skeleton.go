package baseline

import (
	"bufio"
	"bytes"
	"path"
	"regexp"
	"strings"
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
	reJavaKeep   = regexp.MustCompile(`^\s*(package|import)\s|^\s*@[A-Za-z]|\b(class|interface|enum|record|object)\s+[A-Z]\w*|^\s*(public|protected|private|internal|override|static|abstract|default|suspend|fun|open)\b[^=;]*\(`)
	reTSKeep     = regexp.MustCompile(`^\s*(import|export)\s|^\s*@[A-Za-z]\w*\(|\bpath:\s*['"]`)
	reGoKeep     = regexp.MustCompile(`^package\s|^import\s|^func\s+[A-Z]|^func\s+\([^)]*\)\s+[A-Z]|^type\s+[A-Z]`)
	rePyKeep     = regexp.MustCompile(`^\s*(import|from)\s|^\s*(class|def|async\s+def)\s|^\s*@[A-Za-z]`)
	reYAMLKey    = regexp.MustCompile(`^( {0,2})(- )?["']?([A-Za-z0-9_.$/{}\-]+)["']?\s*:`)
	rePropKey    = regexp.MustCompile(`^\s*([A-Za-z0-9_.\-\[\]]+)\s*[=:]`)
	reSQLKeep    = regexp.MustCompile(`(?i)^\s*(create|alter|drop)\s+(table|view|index|unique\s+index|sequence|schema|type)\b`)
	reSQLColumn  = regexp.MustCompile(`^\s*"?([A-Za-z_][A-Za-z0-9_]*)"?\s+[A-Za-z]`)
	rePomKeep    = regexp.MustCompile(`<(groupId|artifactId|version|packaging|module|parent)>`)
	reBPMNKeep   = regexp.MustCompile(`<(\w+:)?(process|\w*[Tt]ask|startEvent|endEvent|\w*Gateway|subProcess|callActivity|boundaryEvent|intermediate\w*Event|decision)\b[^>]*`)
	reXMLAttr    = regexp.MustCompile(`\b(id|name)="[^"]*"`)
	reProtoKeep  = regexp.MustCompile(`^\s*(package|import|message|service|rpc|enum)\s`)
	reJSONTop    = regexp.MustCompile(`^\s{0,2}"[^"]+"\s*:`)
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
		keep = docAnd(matching(reJavaKeep))
	case ext == ".ts" || ext == ".tsx" || ext == ".js" || ext == ".jsx" || ext == ".mjs" || ext == ".cjs" || ext == ".vue":
		keep = matching(reTSKeep)
	case ext == ".go":
		keep = goKeep()
	case ext == ".py":
		keep = matching(rePyKeep)
	case ext == ".yaml" || ext == ".yml":
		keep = yamlKey
	case ext == ".properties" || ext == ".env" || base == ".env":
		keep = propKey
	case ext == ".sql":
		keep = sqlKeep()
	case ext == ".xml" || ext == ".bpmn" || ext == ".dmn":
		keep = xmlKeep(body)
	case ext == ".proto":
		keep = matching(reProtoKeep)
	case ext == ".avsc" || ext == ".json":
		keep = jsonKeep(ext == ".avsc")
	case base == "containerfile" || base == "dockerfile" || strings.HasPrefix(base, "dockerfile.") || strings.HasSuffix(base, ".containerfile"):
		keep = matching(reContainer)
	case strings.HasPrefix(base, "jenkinsfile"):
		keep = matching(rePipeline)
	case ext == ".md" || ext == ".adoc":
		keep = matching(reMDHeading)
	default:
		return headLines(body, 3)
	}
	return collect(body, keep)
}

// collect is the lines keep keeps, as keep rewrites them, within the caps.
func collect(body []byte, keep func(string) (string, bool)) string {
	var b strings.Builder
	sc := bufio.NewScanner(bytes.NewReader(body))
	sc.Buffer(make([]byte, 0, 64*1024), len(body)+1)
	seen := map[string]bool{}
	for sc.Scan() {
		line, ok := keep(sc.Text())
		if !ok {
			continue
		}
		line = strings.TrimRight(line, " \t\r{")
		if line = cutLine(line); line == "" || seen[line] {
			continue
		}
		seen[line] = true
		if b.Len()+len(line)+1 > SkeletonMaxBytes {
			break
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String()
}

func cutLine(line string) string {
	if len(line) <= skeletonLineMax {
		return line
	}
	cut := line[:skeletonLineMax]
	for len(cut) > 0 && cut[len(cut)-1]&0xC0 == 0x80 {
		cut = cut[:len(cut)-1]
	}
	if len(cut) > 0 && cut[len(cut)-1] >= 0xC0 {
		cut = cut[:len(cut)-1]
	}
	return cut + "…"
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
// exported declarations and the first line of each doc comment before one.
func goKeep() func(string) (string, bool) {
	inImports, doc := false, ""
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
		case strings.HasPrefix(line, "// "):
			if doc == "" {
				doc = line
			}
			return "", false
		case reGoKeep.MatchString(line):
			d := doc
			doc = ""
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

// propKey keeps a property's key, never its value.
func propKey(line string) (string, bool) {
	t := strings.TrimSpace(line)
	if strings.HasPrefix(t, "#") || strings.HasPrefix(t, "!") {
		return "", false
	}
	m := rePropKey.FindStringSubmatch(line)
	if m == nil {
		return "", false
	}
	return m[1], true
}

// sqlKeep keeps the statements that make a schema and each column a CREATE
// TABLE names, by its name alone.
func sqlKeep() func(string) (string, bool) {
	inTable := false
	return func(line string) (string, bool) {
		t := strings.TrimSpace(line)
		if reSQLKeep.MatchString(line) {
			inTable = strings.Contains(strings.ToLower(t), "table") && !strings.HasSuffix(t, ";")
			return t, true
		}
		if !inTable {
			return "", false
		}
		if strings.HasPrefix(t, ")") {
			inTable = false
			return "", false
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

// jsonKeep keeps a JSON file's top-level keys; an Avro schema's names,
// namespaces and types, which are what the schema defines.
func jsonKeep(avro bool) func(string) (string, bool) {
	return func(line string) (string, bool) {
		if avro {
			ms := reJSONSchema.FindAllString(line, -1)
			return strings.Join(ms, ", "), len(ms) > 0
		}
		if !reJSONTop.MatchString(line) {
			return "", false
		}
		key := strings.TrimSpace(line)
		if i := strings.Index(key, ":"); i >= 0 {
			key = key[:i+1]
		}
		return key, true
	}
}

// headLines is a file's first n lines that are not blank.
func headLines(body []byte, n int) string {
	kept := 0
	return collect(body, func(line string) (string, bool) {
		if strings.TrimSpace(line) == "" || kept >= n {
			return "", false
		}
		kept++
		return line, true
	})
}
