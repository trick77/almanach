package baseline

import (
	"fmt"
	"strings"
	"testing"

	"github.com/trick77/almanach/internal/gitrepo"
)

// Every file is named with its hints; skeletons fill what is left, the
// highest score first; when the names pass half the ceiling, the
// lowest-scored directories fold, an anchor's never before them.
func TestRouterListing(t *testing.T) {
	files := []File{{Path: "src/core/Rules.java", Size: 4000}, {Path: "Containerfile", Size: 300}}
	z := StageZero{
		Skeletons: map[string]string{"src/core/Rules.java": "package core;\npublic class Rules", "Containerfile": "FROM x"},
		FanIn:     map[string]int{"src/core/Rules.java": 2},
		Churn:     map[string]int{"src/core/Rules.java": 5},
	}
	got, folded := RouterListing(files, z, RouterMaxBytes)
	if len(folded) != 0 {
		t.Errorf("folded = %v in a listing that fits", folded)
	}
	for _, want := range []string{"Containerfile (300 B, anchor)\n    FROM x\n", "src/core/Rules.java (4 kB, in 2, churn 5)\n    package core;\n    public class Rules\n"} {
		if !strings.Contains(got, want) {
			t.Errorf("listing lacks %q:\n%s", want, got)
		}
	}
	if strings.Index(got, "Containerfile") > strings.Index(got, "src/core") {
		t.Errorf("listing not in path order:\n%s", got)
	}

	// A tree whose names alone pass half the ceiling: the plain directory
	// folds, the anchors' stays; skeletons go to the highest score.
	var many []File
	z = StageZero{Skeletons: map[string]string{}, FanIn: map[string]int{}, Churn: map[string]int{}}
	for i := range 200 {
		p := fmt.Sprintf("assets/img/picture-%03d.svg", i)
		many = append(many, File{Path: p, Size: 100})
		z.Skeletons[p] = "<svg>"
	}
	many = append(many, File{Path: "src/main/java/App.java", Size: 900}, File{Path: "src/main/java/Low.java", Size: 900})
	z.Skeletons["src/main/java/App.java"] = "public class App"
	z.Skeletons["src/main/java/Low.java"] = strings.Repeat("x", 5000)
	z.FanIn["src/main/java/App.java"] = 3
	got, folded = RouterListing(many, z, 4000)
	if !strings.Contains(got, "assets/img/* (folded: 200 files, 20 kB)\n") || strings.Contains(got, "picture-001") {
		t.Errorf("the plain directory did not fold:\n%s", got)
	}
	if len(folded) != 200 || !folded["assets/img/picture-001.svg"] || folded["src/main/java/App.java"] {
		t.Errorf("folded = %d files, want the 200 pictures alone", len(folded))
	}
	if !strings.Contains(got, "src/main/java/App.java (900 B, in 3)\n    public class App\n") || strings.Contains(got, "xxxx") {
		t.Errorf("skeletons not by score within the ceiling:\n%s", got)
	}
	if len(got) > 4000 {
		t.Errorf("listing of %d bytes over a ceiling of 4000", len(got))
	}

	// A deep tree, one or two files a directory: own-directory folds do not
	// get it under the ceiling, whole subtrees fold, deepest first; root
	// files fold as "*"; what the router is shown resolves to its files.
	var deep []File
	z = StageZero{Skeletons: map[string]string{}, FanIn: map[string]int{}, Churn: map[string]int{}}
	for i := range 300 {
		deep = append(deep, File{Path: fmt.Sprintf("src/main/java/shop/m%03d/Thing.java", i), Size: 50})
	}
	for i := range 20 {
		deep = append(deep, File{Path: fmt.Sprintf("tool-%02d.sh", i), Size: 10})
	}
	got, _ = RouterListing(deep, z, 3000)
	if len(got) > 3000 {
		t.Errorf("deep listing of %d bytes over 3000:\n%s", len(got), got)
	}
	ix := newIndex(deep)
	shown := 0
	for _, l := range strings.Split(strings.TrimSpace(got), "\n") {
		if strings.HasPrefix(l, "    ") {
			continue
		}
		pick, _, _ := strings.Cut(l, " (")
		if len(ix.resolve(pick)) == 0 {
			t.Errorf("listing line %q resolves to no file", l)
		}
		shown += len(ix.resolve(pick))
	}
	if shown != len(deep) {
		t.Errorf("the listing stands for %d files, the tree has %d:\n%s", shown, len(deep), got)
	}
	// As Files hands a big file on: capped at FileMaxBytes, marked Cut.
	big := FileOf(gitrepo.Entry{Path: "Big.java", Mode: "100644", Size: 3 * FileMaxBytes})
	if got, _ := RouterListing([]File{big}, StageZero{}, 1000); !big.Cut || !strings.Contains(got, "head only") {
		t.Error("a file read as its head is not marked so")
	}
}
