package baseline

import (
	"bufio"
	"bytes"
	"path"
	"regexp"
	"strings"
)

// Hints are what a router reads beside a file's skeleton, never a cut: each
// cheap signal misjudges exactly the code that matters (fan-in rewards
// StringUtils, churn rewards pom.xml; the domain core is imported by two
// files), so code shows them and a model judges.

var (
	reJavaPackage = regexp.MustCompile(`^\s*package\s+([A-Za-z_][\w.]*)`)
	reJavaImport  = regexp.MustCompile(`^\s*import\s+(?:static\s+)?([A-Za-z_]\w*(?:\.\w+)*(?:\.\*)?)`)
	reJVMType     = regexp.MustCompile(`\b(?:class|interface|enum|record|object)\s+([A-Z]\w*)`)
	reTSImport    = regexp.MustCompile(`(?:from\s+|require\(\s*|import\(\s*|^\s*import\s+)['"](\.{1,2}/[^'"]+)['"]`)
	reGoModule    = regexp.MustCompile(`(?m)^module\s+(\S+)`)
	reGoImport    = regexp.MustCompile(`^\s*(?:import\s+)?(?:[A-Za-z_.]+\s+)?"([^"]+)"`)
)

// tsExts are what a relative TS/JS import may leave off, in the order a
// bundler tries them.
var tsExts = []string{"", ".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs", ".vue", "/index.ts", "/index.tsx", "/index.js", "/index.mjs"}

// jvmFile is a Java or Kotlin file's package and the types it declares:
// a Java file its own name, a Kotlin file whatever it declares.
type jvmFile struct {
	pkg   string
	types []string
}

// FanIn is how many files of bodies use each file of it: a Java or Kotlin
// type by an import of it or of its package (`.*`), or by its name in a
// file of its own package; a TS/JS module by a relative import (`./x.js`
// naming x.ts as ESM does); a Go file by an import of its package, of any
// module of the tree (every file of the package counts one per importer).
// A file no one uses is absent. Python and the rest import by names code
// cannot map to a file without running it: none.
func FanIn(bodies map[string][]byte) map[string]int {
	byFQN := map[string]string{}
	byPkg := map[string][]string{}
	jvm := map[string]jvmFile{}
	goDirs := map[string][]string{}
	var modules [][2]string // module path, its directory
	for p, body := range bodies {
		switch strings.ToLower(path.Ext(p)) {
		case ".java", ".kt":
			m := reJavaPackage.FindSubmatch(firstMatchLine(body, reJavaPackage))
			if m == nil {
				continue
			}
			f := jvmFile{pkg: string(m[1])}
			if strings.EqualFold(path.Ext(p), ".java") {
				f.types = []string{strings.TrimSuffix(path.Base(p), path.Ext(p))}
			} else {
				for _, t := range reJVMType.FindAllSubmatch(body, -1) {
					f.types = append(f.types, string(t[1]))
				}
			}
			jvm[p] = f
			for _, t := range f.types {
				byFQN[f.pkg+"."+t] = p
			}
			byPkg[f.pkg] = append(byPkg[f.pkg], p)
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
	for p, body := range bodies {
		seen := map[string]bool{}
		count := func(target string) {
			if target != "" && target != p && !seen[target] {
				seen[target] = true
				in[target]++
			}
		}
		ext := strings.ToLower(path.Ext(p))
		eachLine(body, func(line string) {
			switch ext {
			case ".java", ".kt", ".kts":
				m := reJavaImport.FindStringSubmatch(line)
				if m == nil {
					return
				}
				name := m[1]
				if pkg, ok := strings.CutSuffix(name, ".*"); ok {
					for _, f := range byPkg[pkg] {
						count(f)
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
				if m := reGoImport.FindStringSubmatch(line); m != nil {
					for _, f := range goDirs[goImportDir(modules, m[1])] {
						count(f)
					}
				}
			}
		})
		// A type of its own package is used by name, never imported.
		if f, ok := jvm[p]; ok {
			for _, other := range byPkg[f.pkg] {
				if other == p || seen[other] {
					continue
				}
				for _, t := range jvm[other].types {
					if containsWord(body, t) {
						count(other)
						break
					}
				}
			}
		}
	}
	return in
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

// containsWord reports whether body names w as a word of its own.
func containsWord(body []byte, w string) bool {
	word := func(b byte) bool {
		return b == '_' || b >= '0' && b <= '9' || b >= 'A' && b <= 'Z' || b >= 'a' && b <= 'z'
	}
	for i := 0; ; {
		j := bytes.Index(body[i:], []byte(w))
		if j < 0 {
			return false
		}
		j += i
		end := j + len(w)
		if (j == 0 || !word(body[j-1])) && (end == len(body) || !word(body[end])) {
			return true
		}
		i = j + 1
	}
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

func firstMatchLine(body []byte, re *regexp.Regexp) []byte {
	var hit []byte
	eachLine(body, func(line string) {
		if hit == nil && re.MatchString(line) {
			hit = []byte(line)
		}
	})
	return hit
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
