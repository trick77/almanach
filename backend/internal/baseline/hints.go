package baseline

import (
	"bufio"
	"bytes"
	"path"
	"regexp"
	"slices"
	"strings"
)

// Hints are what a router reads beside a file's skeleton, never a cut: each
// cheap signal misjudges exactly the code that matters (fan-in rewards
// StringUtils, churn rewards pom.xml; the domain core is imported by two
// files), so code shows them and a model judges.

var (
	reJavaPackage = regexp.MustCompile(`^\s*package\s+([A-Za-z_][\w.]*)`)
	reJavaImport  = regexp.MustCompile(`^\s*import\s+(?:static\s+)?([A-Za-z_]\w*(?:\.\w+)*(?:\.\*)?)`)
	// reJVMType is a Kotlin declaration, at its line's start: never a
	// "class Money" a comment or a string mentions.
	reJVMType  = regexp.MustCompile(`^\s*(?:(?:public|private|internal|protected|data|sealed|enum|abstract|open|inner|value|annotation|fun)\s+)*(?:class|interface|object)\s+([A-Z]\w*)`)
	reTSImport = regexp.MustCompile(`(?:from\s+|require\(\s*|import\(\s*|^\s*import\s+)['"](\.{1,2}/[^'"]+)['"]`)
	reGoModule = regexp.MustCompile(`(?m)^module\s+(\S+)`)
	// reGoImportSpec is one import spec: in a block or after "import".
	reGoImportSpec = regexp.MustCompile(`^\s*(?:[A-Za-z_.]+\s+)?"([^"]+)"`)
)

// tsExts are what a relative TS/JS import may leave off, in the order a
// bundler tries them.
var tsExts = []string{"", ".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs", ".vue", "/index.ts", "/index.tsx", "/index.js", "/index.mjs"}

// FanIn is how many files of bodies use each file of it: a Java or Kotlin
// type by an import of it or of its package (`.*`), or by its name in a
// file of its own package; a TS/JS module by a relative import (`./x.js`
// naming x.ts as ESM does); a Go file by an import of its package, of any
// module of the tree (every file of the package counts one per importer).
// A file no one uses is absent; a test is no user (a package with many
// tests is not the more used for them). Python and the rest import by
// names code cannot map to a file without running it: none.
// Deterministic: the files are taken in path order, so of two declaring
// one type the first by path is its file on every run.
func FanIn(bodies map[string][]byte) map[string]int {
	paths := make([]string, 0, len(bodies))
	for p := range bodies {
		paths = append(paths, p)
	}
	slices.Sort(paths)
	byFQN := map[string]string{}
	byPkg := map[string][]string{}
	// pkgTypes is each package's types by simple name: a word of a file
	// of the package is looked up here, one pass over its body.
	pkgTypes := map[string]map[string]string{}
	jvmPkg := map[string]string{}
	goDirs := map[string][]string{}
	var modules [][2]string // module path, its directory
	for _, p := range paths {
		body := bodies[p]
		switch strings.ToLower(path.Ext(p)) {
		case ".java", ".kt":
			m := reJavaPackage.FindSubmatch(firstMatchLine(body, reJavaPackage))
			if m == nil {
				continue
			}
			pkg := string(m[1])
			var types []string
			if strings.EqualFold(path.Ext(p), ".java") {
				types = []string{strings.TrimSuffix(path.Base(p), path.Ext(p))}
			} else {
				eachLine(body, func(line string) {
					if t := reJVMType.FindStringSubmatch(line); t != nil {
						types = append(types, t[1])
					}
				})
			}
			jvmPkg[p] = pkg
			if pkgTypes[pkg] == nil {
				pkgTypes[pkg] = map[string]string{}
			}
			for _, t := range types {
				if _, taken := byFQN[pkg+"."+t]; !taken {
					byFQN[pkg+"."+t] = p
					pkgTypes[pkg][t] = p
				}
			}
			byPkg[pkg] = append(byPkg[pkg], p)
		case ".go":
			if !strings.HasSuffix(p, "_test.go") {
				goDirs[path.Dir(p)] = append(goDirs[path.Dir(p)], p)
			}
		}
		if path.Base(p) == "go.mod" {
			if m := reGoModule.FindSubmatch(body); m != nil {
				modules = append(modules, [2]string{string(m[1]), path.Dir(p)})
			}
		}
	}
	in := map[string]int{}
	for _, p := range paths {
		if TestFile(p) {
			continue
		}
		body := bodies[p]
		seen := map[string]bool{}
		count := func(target string) {
			if target != "" && target != p && !seen[target] {
				seen[target] = true
				in[target]++
			}
		}
		ext := strings.ToLower(path.Ext(p))
		inImports := false
		eachLine(body, func(line string) {
			switch ext {
			case ".java", ".kt", ".kts":
				m := reJavaImport.FindStringSubmatch(line)
				if m == nil {
					return
				}
				name := m[1]
				if pkg, ok := strings.CutSuffix(name, ".*"); ok {
					// A package's every type, or a static import's own.
					if files, ok := byPkg[pkg]; ok {
						for _, f := range files {
							count(f)
						}
					} else {
						count(byFQN[pkg])
					}
					return
				}
				// A static import names a member: its type is one up.
				if _, ok := byFQN[name]; !ok {
					name = name[:max(strings.LastIndex(name, "."), 0)]
				}
				count(byFQN[name])
			case ".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs", ".vue":
				for _, m := range reTSImport.FindAllStringSubmatch(line, -1) {
					count(resolveTS(bodies, path.Join(path.Dir(p), m[1])))
				}
			case ".go":
				// Only an import declaration's specs: a string elsewhere
				// that looks like an import path is none.
				spec := ""
				switch {
				case strings.HasPrefix(line, "import ("):
					inImports = true
				case inImports && strings.TrimSpace(line) == ")":
					inImports = false
				case inImports:
					spec = line
				case strings.HasPrefix(line, "import "):
					spec = strings.TrimPrefix(line, "import ")
				}
				if m := reGoImportSpec.FindStringSubmatch(spec); m != nil {
					for _, f := range goDirs[goImportDir(modules, m[1])] {
						count(f)
					}
				}
			}
		})
		// A type of its own package is used by name, never imported: each
		// word of the body looked up once.
		if pkg, ok := jvmPkg[p]; ok && len(pkgTypes[pkg]) > 1 {
			eachWord(body, func(w string) {
				if f, ok := pkgTypes[pkg][w]; ok {
					count(f)
				}
			})
		}
	}
	return in
}

// eachWord calls f with each identifier-like word of body.
func eachWord(body []byte, f func(string)) {
	isWord := func(b byte) bool {
		return b == '_' || b >= '0' && b <= '9' || b >= 'A' && b <= 'Z' || b >= 'a' && b <= 'z'
	}
	for i := 0; i < len(body); {
		if !isWord(body[i]) {
			i++
			continue
		}
		j := i
		for j < len(body) && isWord(body[j]) {
			j++
		}
		f(string(body[i:j]))
		i = j
	}
}

// goImportDir is the tree directory a Go import names, "" when no module
// of the tree holds it: the longest module path it falls under, so a
// nested module is its own, whatever order the files came in.
func goImportDir(modules [][2]string, imp string) string {
	best, dir := "", ""
	for _, m := range modules {
		if (imp == m[0] || strings.HasPrefix(imp, m[0]+"/")) && len(m[0]) > len(best) {
			best, dir = m[0], path.Join(m[1], strings.TrimPrefix(imp, m[0]))
		}
	}
	return dir
}

// resolveTS is the file a relative import names, "" when bodies holds none.
// ESM writes the built name: "./api.js" for api.ts.
func resolveTS(bodies map[string][]byte, p string) string {
	bases := []string{p}
	for _, js := range []string{".js", ".mjs", ".cjs", ".jsx"} {
		if stem, ok := strings.CutSuffix(p, js); ok {
			bases = append(bases, stem)
		}
	}
	for _, b := range bases {
		for _, ext := range tsExts {
			if _, ok := bodies[b+ext]; ok && b+ext != "" {
				return b + ext
			}
		}
	}
	return ""
}

// firstMatchLine is the first line of body re matches, nil for none; the
// rest of the body is not read.
func firstMatchLine(body []byte, re *regexp.Regexp) []byte {
	for len(body) > 0 {
		line, rest, _ := bytes.Cut(body, []byte("\n"))
		if re.Match(line) {
			return line
		}
		body = rest
	}
	return nil
}

func eachLine(body []byte, f func(string)) {
	sc := bufio.NewScanner(bytes.NewReader(body))
	sc.Buffer(make([]byte, 0, 64*1024), len(body)+1)
	for sc.Scan() {
		f(sc.Text())
	}
}

// StageZero is what stage 0 makes of a tree, before any model: every file's
// skeleton and its hints. Built by code; the router is its reader.
type StageZero struct {
	Skeletons map[string]string
	FanIn     map[string]int
	Churn     map[string]int
	// Bytes is the readable bytes the skeletons stand for, SkeletonBytes
	// their own: the share a router reads of the tree.
	Bytes, SkeletonBytes int64
}

// BuildStageZero is stage 0 over the bodies of a tree's readable files and
// the commits that touched them (churn, nil when unknown).
func BuildStageZero(bodies map[string][]byte, churn map[string]int) StageZero {
	z := StageZero{Skeletons: make(map[string]string, len(bodies)), FanIn: FanIn(bodies), Churn: churn}
	for p, body := range bodies {
		s := Skeleton(p, body)
		z.Skeletons[p] = s
		z.Bytes += int64(len(body))
		z.SkeletonBytes += int64(len(s))
	}
	return z
}
