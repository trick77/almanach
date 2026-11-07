// Package evalrun runs today's baseline pipeline over a folder of
// repositories and scores the pages it writes against hand-made labels:
// which files a reader must see, which are noise, and the questions a page
// must answer. It never runs in the gate: every run pays for model calls.
package evalrun

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"
)

// The folder's layout: projects/<project>/<repo>/, each repo a git
// repository whose HEAD is read, and labels/<project>/<repo>.yaml beside
// them. A repository without labels is run and measured, never scored.
const (
	projectsDir = "projects"
	labelsDir   = "labels"
)

// Project is one project of the folder: its repositories run in one
// database, as one wiki project.
type Project struct {
	Key   string
	Repos []Repo
}

// Repo is one repository of a project.
type Repo struct {
	Slug string
	// Dir is the git repository the clone is made from.
	Dir    string
	Labels *Labels
}

// Labels is what a page about a repository is held to.
type Labels struct {
	Repo      string     `yaml:"repo"`
	Kind      string     `yaml:"kind"`
	SummaryDE string     `yaml:"summary_de"`
	MustRead  []MustRead `yaml:"must_read"`
	NeverRead []Glob     `yaml:"never_read"`
	Generated []Glob     `yaml:"generated"`
	Questions []Question `yaml:"questions"`
}

// MustRead is a file a reader of the repository has to see.
type MustRead struct {
	Path string `yaml:"path"`
	Why  string `yaml:"why"`
}

// Glob names noise: files a reader skips, generated ones among them.
type Glob struct {
	Glob string `yaml:"glob"`
	Why  string `yaml:"why"`
	How  string `yaml:"how"`
}

// Question is one a reader opening the page asks, with the answer the code
// gives.
type Question struct {
	ID       string   `yaml:"id"`
	Category string   `yaml:"category"`
	Q        string   `yaml:"q"`
	Answer   string   `yaml:"answer"`
	Evidence []string `yaml:"evidence"`
}

// Load reads the folder's projects and their labels.
func Load(dir string) ([]Project, error) {
	root := filepath.Join(dir, projectsDir)
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", root, err)
	}
	var out []Project
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		p := Project{Key: e.Name()}
		repos, err := os.ReadDir(filepath.Join(root, e.Name()))
		if err != nil {
			return nil, err
		}
		for _, r := range repos {
			rdir := filepath.Join(root, e.Name(), r.Name())
			if !r.IsDir() || !isGit(rdir) {
				continue
			}
			repo := Repo{Slug: r.Name(), Dir: rdir}
			l, err := loadLabels(filepath.Join(dir, labelsDir, e.Name(), r.Name()+".yaml"))
			if err != nil {
				return nil, err
			}
			repo.Labels = l
			p.Repos = append(p.Repos, repo)
		}
		if len(p.Repos) > 0 {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no project with a git repository under %s", root)
	}
	return out, nil
}

func isGit(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil
}

// loadLabels reads one labels file; nil without error when there is none.
func loadLabels(path string) (*Labels, error) {
	b, err := os.ReadFile(path) //nolint:gosec // the operator's folder, by design
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var l Labels
	if err := yaml.Unmarshal(b, &l); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &l, nil
}

// globSet matches repository paths against labels' globs: `**` any depth,
// `*` and `?` within a segment, `{a,b}` alternatives.
type globSet []*regexp.Regexp

func newGlobSet(globs []Glob) globSet {
	var out globSet
	for _, g := range globs {
		for _, alt := range expandBraces(g.Glob) {
			out = append(out, globRegexp(alt))
		}
	}
	return out
}

func (s globSet) match(p string) bool {
	for _, re := range s {
		if re.MatchString(p) {
			return true
		}
	}
	return false
}

// expandBraces turns `a{b,c}d` into `abd`, `acd`, nested braces included.
func expandBraces(g string) []string {
	open := strings.IndexByte(g, '{')
	if open < 0 {
		return []string{g}
	}
	depth := 0
	for i := open; i < len(g); i++ {
		switch g[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				var out []string
				for _, alt := range splitTop(g[open+1 : i]) {
					out = append(out, expandBraces(g[:open]+alt+g[i+1:])...)
				}
				return out
			}
		}
	}
	return []string{g}
}

// splitTop splits on the commas outside any nested brace.
func splitTop(s string) []string {
	var out []string
	depth, start := 0, 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '{':
			depth++
		case '}':
			depth--
		case ',':
			if depth == 0 {
				out = append(out, s[start:i])
				start = i + 1
			}
		}
	}
	return append(out, s[start:])
}

func globRegexp(g string) *regexp.Regexp {
	var b strings.Builder
	b.WriteString("^")
	for i := 0; i < len(g); i++ {
		c := g[i]
		switch {
		case c == '*' && i+1 < len(g) && g[i+1] == '*':
			i++
			if i+1 < len(g) && g[i+1] == '/' {
				i++
				b.WriteString("(?:.*/)?")
			} else {
				b.WriteString(".*")
			}
		case c == '*':
			b.WriteString("[^/]*")
		case c == '?':
			b.WriteString("[^/]")
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	b.WriteString("$")
	return regexp.MustCompile(b.String())
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
