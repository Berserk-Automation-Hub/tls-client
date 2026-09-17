package tls_client

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/Berserk-Automation-Hub/tls-client/profiles"
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

	// NOT a skip. This used to skip when the named base was absent, which made the document's base
	// commit unguarded in the one direction the fork has already been wrong in: replacing the SHA
	// with a 40-hex string that names nothing left the package green. An absent base means either
	// the document names a commit that does not exist or this clone was truncated below it; both
	// are conditions under which the inventory below CANNOT be trusted, and neither is a pass.
	if out, err := exec.Command("git", "cat-file", "-e", base+"^{commit}").CombinedOutput(); err != nil {
		t.Fatalf("PATCHES.md names upstream base %s and this clone has no such commit (%v: %s). "+
			"Every figure in the inventory is measured against that commit, so either the SHA is "+
			"wrong or this clone is truncated below it — run `git fetch --unshallow` and re-run "+
			"before believing the inventory", base, err, strings.TrimSpace(string(out)))
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

// TestPATCHESMDUpstreamVersionIsTheTagAtItsBase is the guard on the OTHER half of "## Upstream base":
// the version NAME. TestPATCHESMDFileInventoryMatchesTheDiff checks the 40-hex commit; nothing
// checked the `v1.16.0` beside it, and a fork claiming the wrong upstream version is exactly the
// kind of stale front matter this file exists to catch — the same document has already shipped a
// wrong patch count and a wrong behaviour claim.
//
// The claim is only checkable against UPSTREAM, so this asks upstream. It prefers a tag already in
// this clone (offline, no network at all) and falls back to `git ls-remote` on the upstream URL the
// document itself names. When neither is available it skips INDIVIDUALLY, printing the error that
// prevented it, rather than passing.
func TestPATCHESMDUpstreamVersionIsTheTagAtItsBase(t *testing.T) {
	doc := readPatchesMD(t)

	baseRE := regexp.MustCompile(`commit ` + "`" + `([0-9a-f]{40})` + "`")
	bm := baseRE.FindStringSubmatch(doc)
	if bm == nil {
		t.Fatal("PATCHES.md no longer names its upstream base as `commit <40-hex>`")
	}
	base := bm[1]

	verRE := regexp.MustCompile(`\*\*` + "`" + `(v\d+\.\d+\.\d+)` + "`" + `\*\*`)
	vm := verRE.FindStringSubmatch(doc)
	if vm == nil {
		t.Fatal("PATCHES.md's \"## Upstream base\" section no longer states the upstream version as " +
			"**`vX.Y.Z`**. The version name is what a reader uses to find the upstream tree; the SHA " +
			"beside it is checked elsewhere, and the two have to agree.")
	}
	claimed := vm[1]

	urlRE := regexp.MustCompile(`https://github\.com/bogdanfinn/[a-z0-9-]+\.git`)
	um := urlRE.FindString(doc)
	if um == "" {
		t.Fatal("PATCHES.md no longer names the upstream repository URL, so this guard has nothing " +
			"to ask about the version it claims")
	}

	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git is not on PATH (%v), so the upstream tag cannot be resolved", err)
	}

	// 1. Offline: a tag already in this clone that points at the base commit.
	if out, err := exec.Command("git", "tag", "--points-at", base).Output(); err == nil {
		for _, tag := range strings.Fields(string(out)) {
			if tag == claimed {
				t.Logf("upstream version %s confirmed offline: a local tag points at %s", claimed, base[:12])
				return
			}
		}
	}

	// 2. Otherwise ask upstream. tagsAtUpstream returns the name(s) pointing at base and the newest
	// release tag, so both sentences in "## Upstream base" are checked in one round trip.
	at, newest, err := tagsAtUpstream(um, base)
	if err != nil {
		t.Skipf("cannot reach %s to resolve the upstream tag at %s (%v), and no local tag points at "+
			"it either, so PATCHES.md's claim of %s is unverifiable here — it is NOT assumed true",
			um, base[:12], err, claimed)
	}

	if len(at) == 0 {
		t.Fatalf("PATCHES.md says its upstream base is %s, and %s publishes NO tag at commit %s. "+
			"The version name and the SHA in \"## Upstream base\" do not describe the same upstream "+
			"tree", claimed, um, base[:12])
	}
	found := false
	for _, tag := range at {
		if tag == claimed {
			found = true
		}
	}
	if !found {
		t.Fatalf("PATCHES.md says this fork is based on upstream %s, and the tag upstream actually "+
			"publishes at commit %s is %v. A fork that names the wrong upstream version sends every "+
			"reader of this document to the wrong tree", claimed, base[:12], at)
	}

	// The document also asserts the base is upstream's NEWEST release, i.e. the fork is not behind.
	if newest != "" && newest != claimed {
		t.Fatalf("PATCHES.md says %s \"is also the newest tag upstream publishes\", and upstream's "+
			"newest release tag is %s. This fork is behind upstream and the document says it is not",
			claimed, newest)
	}
	t.Logf("upstream version checked against %s: %s at %s, newest release tag %s", um, claimed, base[:12], newest)
}

// tagsAtUpstream lists the release tags upstream publishes, returning those that point at base
// (dereferencing annotated tags) and the highest one by numeric version.
func tagsAtUpstream(url, base string) (at []string, newest string, err error) {
	out, err := exec.Command("git", "ls-remote", "--tags", url).Output()
	if err != nil {
		return nil, "", err
	}
	release := regexp.MustCompile(`^v(\d+)\.(\d+)\.(\d+)$`)
	sha := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		f := strings.Fields(line)
		if len(f) != 2 {
			continue
		}
		name := strings.TrimPrefix(f[1], "refs/tags/")
		deref := strings.HasSuffix(name, "^{}")
		name = strings.TrimSuffix(name, "^{}")
		if !release.MatchString(name) {
			continue
		}
		// An annotated tag's ^{} line is the commit it points at and wins over the tag object's own.
		if _, seen := sha[name]; !seen || deref {
			sha[name] = f[0]
		}
	}
	var best [3]int
	for name, s := range sha {
		if s == base {
			at = append(at, name)
		}
		m := release.FindStringSubmatch(name)
		var v [3]int
		for i := 0; i < 3; i++ {
			v[i], _ = strconv.Atoi(m[i+1])
		}
		if v[0] > best[0] || (v[0] == best[0] && (v[1] > best[1] || (v[1] == best[1] && v[2] > best[2]))) {
			best, newest = v, name
		}
	}
	sort.Strings(at)
	return at, newest, nil
}

// TestPATCHESMDHTTP3SettingsOrderFiguresMatchTheProfiles guards the two figures patch 8's section
// states about the profile set, both of which were WRONG when first written: the document named
// "Chrome_144 and Chrome_133_PSK" as the profiles that declare an http3SettingsOrder, and
// Chrome_133_PSK declares none.
//
// The set is ENUMERATED from profiles.MappedTLSClients here, so the document cannot drift from it
// again and a new upstream profile that declares an order turns this red until it is recorded.
func TestPATCHESMDHTTP3SettingsOrderFiguresMatchTheProfiles(t *testing.T) {
	doc := readPatchesMD(t)

	var withOrder []string
	for name, p := range profiles.MappedTLSClients {
		if len(p.GetHttp3SettingsOrder()) > 0 {
			withOrder = append(withOrder, name)
		}
	}
	sort.Strings(withOrder)
	total := len(profiles.MappedTLSClients)
	without := total - len(withOrder)

	want := fmt.Sprintf("%d of the %d", without, total)
	if !strings.Contains(doc, want) {
		t.Fatalf("PATCHES.md must state the size of the affected profile set as %q: %d of the %d "+
			"profiles in profiles.MappedTLSClients declare no http3SettingsOrder and had a randomly "+
			"ordered HTTP/3 SETTINGS frame before patch 8", want, without, total)
	}

	var missing []string
	for _, name := range withOrder {
		if !strings.Contains(doc, "`"+name+"`") {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		t.Fatalf("the profiles that DO declare an http3SettingsOrder are %v, and PATCHES.md does not "+
			"name %v as `name`. The document named the wrong pair once already — it said "+
			"\"Chrome_144 and Chrome_133_PSK\" while Chrome_133_PSK declares no order at all — and "+
			"the list has to come from the map, not from memory", withOrder, missing)
	}
	t.Logf("profiles declaring an http3SettingsOrder: %v (%d of %d declare none)", withOrder, without, total)
}
