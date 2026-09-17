package tls_client

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// PATCHES.md's FRONT MATTER IS PART OF THE PATCH, and these are its guards.
//
// It has been wrong three separate times, always in the same way: a commit changed the fork and left
// the document describing the fork as it was. It claimed "Three functional patches" while carrying
// six, and "No .go file changes behaviour" while five .go files changed behaviour. Nothing was red,
// because nothing read the document.
//
// These tests read it. The patch count is checked against the document's own sections, which needs
// no git; the file inventory is checked against the real diff, which does.

var patchHeadingRE = regexp.MustCompile(`(?m)^## Patch (\d+) — `)

// spelledOut is how the header states the patch count. A number that has to be spelled somewhere is
// a number that has to be maintained, so the guard has to be able to read it.
var spelledOut = map[string]int{
	"One": 1, "Two": 2, "Three": 3, "Four": 4, "Five": 5, "Six": 6,
	"Seven": 7, "Eight": 8, "Nine": 9, "Ten": 10, "Eleven": 11, "Twelve": 12,
}

func readPatchesMD(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("PATCHES.md")
	if err != nil {
		t.Fatalf("reading PATCHES.md: %v — this fork's provenance record is required to exist", err)
	}
	return string(b)
}

// TestPATCHESMDPatchCountMatchesItsOwnSections is the guard on the sentence that was wrong for two
// tags: the header's patch count, and the numbering of the sections it counts.
func TestPATCHESMDPatchCountMatchesItsOwnSections(t *testing.T) {
	doc := readPatchesMD(t)

	headerRE := regexp.MustCompile(`\*\*([A-Z][a-z]+) functional patches\*\*`)
	hm := headerRE.FindStringSubmatch(doc)
	if hm == nil {
		t.Fatalf("PATCHES.md's header no longer states a patch count in the form " +
			"**<Number> functional patches**. That sentence is the one this guard exists for: it said " +
			"\"Three\" while the document carried six sections, and nothing was red.")
	}
	claimed, ok := spelledOut[hm[1]]
	if !ok {
		t.Fatalf("PATCHES.md claims %q functional patches, which this guard cannot read as a number; "+
			"spell it as one of %v so the claim stays checkable", hm[1], sortedKeys(spelledOut))
	}

	nums := patchHeadingRE.FindAllStringSubmatch(doc, -1)
	if len(nums) == 0 {
		t.Fatal("PATCHES.md contains no `## Patch N — ` section at all, so either the headings were " +
			"reformatted or the patches were removed; either way the header's count is unchecked")
	}

	if len(nums) != claimed {
		t.Fatalf("PATCHES.md's header claims %s (%d) functional patches and the document contains %d "+
			"`## Patch N —` sections. The header is front matter that a patch commit has to maintain, "+
			"and it has been left behind before: it said \"Three\" while six sections existed",
			hm[1], claimed, len(nums))
	}

	// Mis-numbered markers: 1..N, in order, no repeats, no gaps.
	for i, m := range nums {
		want := fmt.Sprintf("%d", i+1)
		if m[1] != want {
			var got []string
			for _, n := range nums {
				got = append(got, n[1])
			}
			t.Fatalf("PATCHES.md's patch sections are numbered %v; they must run 1..%d in order. A "+
				"repeated or skipped marker makes every cross-reference in the document ambiguous",
				got, len(nums))
		}
	}
}

// TestPATCHESMDFileInventoryMatchesTheDiff checks the inventory against the diff it describes.
//
// It needs the git history, so it skips — INDIVIDUALLY, with the reason printed — when run from an
// extracted module zip, which has no .git. It does not skip in this repository.
func TestPATCHESMDFileInventoryMatchesTheDiff(t *testing.T) {
	doc := readPatchesMD(t)

	if _, err := os.Stat(filepath.Join("..", ".git")); err != nil {
		if _, err := os.Stat(".git"); err != nil {
			t.Skipf("no .git here (%v): this tree is an extracted module zip, which carries no history, "+
				"so the diff this inventory describes cannot be recomputed. Run it in a clone.", err)
		}
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git is not on PATH (%v), so the diff cannot be recomputed", err)
	}

	baseRE := regexp.MustCompile(`commit ` + "`" + `([0-9a-f]{40})` + "`")
	bm := baseRE.FindStringSubmatch(doc)
	if bm == nil {
		t.Fatal("PATCHES.md no longer names its upstream base as `commit <40-hex>`. The base commit is " +
			"what every figure in the inventory is measured against; without it the inventory is unfalsifiable.")
	}
	base := bm[1]

	if out, err := exec.Command("git", "cat-file", "-e", base+"^{commit}").CombinedOutput(); err != nil {
		t.Skipf("the base commit %s named by PATCHES.md is not in this clone (%v: %s), so the diff "+
			"cannot be recomputed here", base, err, strings.TrimSpace(string(out)))
	}

	out, err := exec.Command("git", "diff", "--name-status", base, "HEAD").Output()
	if err != nil {
		t.Fatalf("git diff --name-status %s HEAD: %v", base, err)
	}

	var added, modified []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		switch f[0] {
		case "A":
			added = append(added, f[1])
		case "M":
			modified = append(modified, f[1])
		}
	}
	sort.Strings(added)
	sort.Strings(modified)

	total := len(added) + len(modified)
	want := fmt.Sprintf("**%d files in total: %d added, %d modified.**", total, len(added), len(modified))
	if !strings.Contains(doc, want) {
		t.Fatalf("PATCHES.md does not state the inventory the diff actually produces.\n"+
			"  the diff against %s is: %d added, %d modified, %d in total\n"+
			"  PATCHES.md must therefore contain, verbatim: %s\n"+
			"This is the sentence the document has never had and the reason it could claim \"No .go "+
			"file changes behaviour\" for two tags without anything turning red.",
			base[:12], len(added), len(modified), total, want)
	}

	// Every added file has to be named, so a new file cannot be shipped undocumented.
	var unnamed []string
	for _, f := range added {
		if !strings.Contains(doc, "`"+f+"`") {
			unnamed = append(unnamed, f)
		}
	}
	if len(unnamed) > 0 {
		t.Fatalf("PATCHES.md's inventory does not name %d file(s) this fork ADDS: %v. Every added file "+
			"has to appear in the inventory as `name`, or the fork ships code its provenance record "+
			"does not mention", len(unnamed), unnamed)
	}

	// The behaviour/rename split has to be the real one. A modified file whose every changed line
	// mentions one of the two module paths is an import-path move and nothing else.
	var behaviour []string
	for _, f := range modified {
		if !strings.HasSuffix(f, ".go") {
			continue
		}
		d, derr := exec.Command("git", "diff", "-U0", base, "HEAD", "--", f).Output()
		if derr != nil {
			t.Fatalf("git diff -U0 -- %s: %v", f, derr)
		}
		for _, line := range strings.Split(string(d), "\n") {
			if len(line) < 2 || (line[0] != '+' && line[0] != '-') || line[1] == '+' || line[1] == '-' {
				continue
			}
			if strings.Contains(line, "bogdanfinn") || strings.Contains(line, "Berserk-Automation-Hub") {
				continue
			}
			behaviour = append(behaviour, f)
			break
		}
	}
	sort.Strings(behaviour)

	if len(behaviour) > 0 && strings.Contains(doc, "No `.go` file changes behaviour") {
		t.Fatalf("PATCHES.md still claims \"No `.go` file changes behaviour\", and %d .go files do: %v",
			len(behaviour), behaviour)
	}
	for _, f := range behaviour {
		if !strings.Contains(doc, "`"+f+"`") {
			t.Fatalf("`%s` changes behaviour — it has at least one changed line that mentions neither "+
				"module path — and PATCHES.md never names it. A behaviour change absent from the "+
				"provenance record is exactly what this document exists to prevent", f)
		}
	}
	t.Logf("inventory checked against %s: %d added, %d modified, %d .go files changing behaviour (%v)",
		base[:12], len(added), len(modified), len(behaviour), behaviour)
}

func sortedKeys(m map[string]int) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Slice(ks, func(i, j int) bool { return m[ks[i]] < m[ks[j]] })
	return ks
}
