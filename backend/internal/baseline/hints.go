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
	reJavaImport  = regexp.MustCompile(`^\s*import\s+(?:static\s+)?([A-Za-z_][\w.]*)`)
	reTSImport    = regexp.MustCompile(`(?:from\s+|require\(\s*|import\(\s*|^\s*import\s+)['"](\.{1,2}/[^'"]+)['"]`)
	reGoModule    = regexp.MustCompile(`(?m)^module\s+(\S+)`)
	reGoImport    = regexp.MustCompile(`^\s*(?:import\s+)?(?:[A-Za-z_.]+\s+)?"([^"]+)"`)
)

// tsExts are what a relative TS/JS import may leave off, in the order a
// bundler tries them.
var tsExts = []string{"", ".ts", ".tsx", ".js", ".jsx", ".mjs", ".vue", "/index.ts", "/index.tsx", "/index.js"}

// FanIn is how many files of bodies import each file of it: a Java or
// Kotlin class by its package and name, a TS/JS module by a relative
// import, a Go file by its package (every file of an imported package
// counts one per importer). A file no one imports is absent. Python and
// the rest import by names code cannot map to a file without running it:
// none.
func FanIn(bodies map[string][]byte) map[string]int {
	byFQN := map[string]string{}
	goDirs := map[string][]string{}
	module := ""
	for p, body := range bodies {
		switch strings.ToLower(path.Ext(p)) {
		case ".java", ".kt":
			if m := reJavaPackage.FindSubmatch(firstMatchLine(body, reJavaPackage)); m != nil {
				byFQN[string(m[1])+"."+strings.TrimSuffix(path.Base(p), path.Ext(p))] = p
			}
		case ".go":
			if !strings.HasSuffix(p, "_test.go") {
				goDirs[path.Dir(p)] = append(goDirs[path.Dir(p)], p)
			}
		}
		if path.Base(p) == "go.mod" {
			if m := reGoModule.FindSubmatch(body); m != nil && (module == "" || path.Dir(p) == ".") {
				module = string(m[1])
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
				if m := reJavaImport.FindStringSubmatch(line); m != nil {
					name := m[1]
					// A static import names a member: its class is one up.
					if _, ok := byFQN[name]; !ok {
						name = name[:max(strings.LastIndex(name, "."), 0)]
					}
					count(byFQN[name])
				}
			case ".ts", ".tsx", ".js", ".jsx", ".mjs", ".vue":
				for _, m := range reTSImport.FindAllStringSubmatch(line, -1) {
					count(resolveTS(bodies, path.Join(path.Dir(p), m[1])))
				}
			case ".go":
				if module == "" {
					return
				}
				if m := reGoImport.FindStringSubmatch(line); m != nil && strings.HasPrefix(m[1], module+"/") {
					for _, f := range goDirs[strings.TrimPrefix(m[1], module+"/")] {
						count(f)
					}
				}
			}
		})
	}
	return in
}

// resolveTS is the file a relative import names, "" when bodies holds none.
func resolveTS(bodies map[string][]byte, p string) string {
	for _, ext := range tsExts {
		if _, ok := bodies[p+ext]; ok {
			return p + ext
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
