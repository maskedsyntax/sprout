package buildfile

import (
	"os"
	"strings"
	"testing"
)

var exts = map[string]Kind{"xml": Maven, "gradle": GradleGroovy, "kts": GradleKotlin}

func ref(t *testing.T, name, ext string) *Doc {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name + "." + ext)
	if err != nil {
		t.Fatal(err)
	}
	d, err := Parse(exts[ext], string(b))
	if err != nil {
		t.Fatalf("%s.%s: %v", name, ext, err)
	}
	return d
}

// customize turns a generated file into a "user" file: 4-space indentation
// plus a pinned extra dependency with a comment.
func customize(t *testing.T, d *Doc) *Doc {
	t.Helper()
	src := strings.ReplaceAll(d.Src, "\t", "    ")
	if d.Kind == Maven {
		src = strings.Replace(src, "    </dependencies>", "        <!-- mine -->\n        <dependency>\n            <groupId>com.google.guava</groupId>\n            <artifactId>guava</artifactId>\n            <version>33.0</version>\n        </dependency>\n    </dependencies>", 1)
	} else {
		src = strings.Replace(src, "dependencies {\n", "dependencies {\n    // mine\n    implementation(\"com.google.guava:guava:33.0\")\n", 1)
	}
	u, err := Parse(d.Kind, src)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

// keys returns every key in the dependency-related containers.
func keys(d *Doc) map[string]bool {
	out := map[string]bool{}
	var walk func(n *Node, path string)
	walk = func(n *Node, path string) {
		for _, c := range n.Children {
			out[path+"/"+c.Key] = true
			if c.Container {
				walk(c, path+"/"+c.Key)
			}
		}
	}
	walk(d.Root, "")
	return out
}

func mustMerge(t *testing.T, user, a, b *Doc) (*Plan, *Doc) {
	t.Helper()
	p, err := Merge(user, a, b)
	if err != nil {
		t.Fatal(err)
	}
	res, err := Parse(user.Kind, p.Result)
	if err != nil {
		t.Fatalf("result does not parse: %v\n%s", err, p.Result)
	}
	return p, res
}

func TestMergeAddAllFormats(t *testing.T) {
	for ext := range exts {
		t.Run(ext, func(t *testing.T) {
			a, b := ref(t, "web", ext), ref(t, "web-lombok-cloud-native", ext)
			user := customize(t, a)
			p, res := mustMerge(t, user, a, b)
			if len(p.Notes) != 0 {
				t.Errorf("unexpected notes: %+v", p.Notes)
			}
			got := keys(res)
			for k := range keys(b) {
				if !got[k] && !strings.Contains(k, "stmt:") && !strings.Contains(k, "assign:") && !strings.HasPrefix(k, "/el:") {
					t.Errorf("missing %s after merge", k)
				}
			}
			for _, want := range []string{"guava", "mine", "lombok", "spring-cloud-dependencies", "native"} {
				if !strings.Contains(p.Result, want) {
					t.Errorf("result lacks %q", want)
				}
			}
			if strings.Contains(p.Result, "\t") {
				t.Errorf("inserted text kept tab indentation:\n%s", p.Result)
			}
			// Idempotent: applying the same change again does nothing.
			again, err := Merge(res, a, b)
			if err != nil || again.Changed() {
				t.Errorf("second merge changed the file: %+v %v", again.Changes, err)
			}
		})
	}
}

func TestMergeRemoveAllFormats(t *testing.T) {
	for ext := range exts {
		t.Run(ext, func(t *testing.T) {
			a, b := ref(t, "web-jpa-pg", ext), ref(t, "web", ext)
			user := customize(t, a)
			p, _ := mustMerge(t, user, a, b)
			for _, gone := range []string{"data-jpa", "postgresql"} {
				if strings.Contains(p.Result, gone) {
					t.Errorf("%s still present:\n%s", gone, p.Result)
				}
			}
			if !strings.Contains(p.Result, "guava") || !strings.Contains(p.Result, "webmvc") {
				t.Errorf("unrelated dependencies removed:\n%s", p.Result)
			}
			for _, l := range Diff(user.Src, p.Result, 0) {
				if l.Op == '+' {
					t.Errorf("removal added a line: %q", l.Text)
				}
			}
		})
	}
}

func TestMergeReplacesPlainStarter(t *testing.T) {
	for ext := range exts {
		t.Run(ext, func(t *testing.T) {
			a, b := ref(t, "none", ext), ref(t, "web", ext)
			p, res := mustMerge(t, customize(t, a), a, b)
			if strings.Contains(p.Result, "spring-boot-starter'") || strings.Contains(p.Result, "spring-boot-starter\"") ||
				strings.Contains(p.Result, "<artifactId>spring-boot-starter</artifactId>") {
				t.Errorf("plain starter not replaced:\n%s", p.Result)
			}
			if !strings.Contains(p.Result, "spring-boot-starter-webmvc-test") {
				t.Errorf("test starter not added:\n%s", p.Result)
			}
			// And back again: removing web restores the plain starter.
			back, _ := mustMerge(t, res, b, a)
			if !strings.Contains(back.Result, "spring-boot-starter-test") || strings.Contains(back.Result, "webmvc") {
				t.Errorf("round trip failed:\n%s", back.Result)
			}
		})
	}
}

func TestMergeLeavesCustomizedPluginAlone(t *testing.T) {
	a, b := ref(t, "web", "xml"), ref(t, "web-lombok-cloud-native", "xml")
	custom := strings.Replace(a.Src, "\t\t</plugins>", `			<plugin>
				<groupId>org.apache.maven.plugins</groupId>
				<artifactId>maven-compiler-plugin</artifactId>
				<configuration><release>21</release></configuration>
			</plugin>
		</plugins>`, 1)
	user, err := Parse(Maven, custom)
	if err != nil {
		t.Fatal(err)
	}
	p, _ := mustMerge(t, user, a, b)
	if strings.Count(p.Result, "maven-compiler-plugin") != 1 {
		t.Errorf("customized plugin duplicated or replaced:\n%s", p.Result)
	}
	if len(p.Notes) != 1 || !strings.Contains(p.Notes[0].Snippet, "annotationProcessorPaths") {
		t.Errorf("notes = %+v", p.Notes)
	}
}

func TestMergeKindMismatch(t *testing.T) {
	if _, err := Merge(ref(t, "web", "xml"), ref(t, "web", "gradle"), ref(t, "web", "gradle")); err == nil {
		t.Error("expected kind mismatch error")
	}
}

func TestMergeIntoEmptyAndInlineContainers(t *testing.T) {
	a, b := ref(t, "web", "gradle"), ref(t, "web-jpa-pg", "gradle")
	for name, deps := range map[string]string{
		"empty block":  "dependencies {\n}",
		"inline block": "dependencies {}",
	} {
		src := "plugins {\n  id 'java'\n}\n\n" + deps + "\n"
		user, err := Parse(GradleGroovy, src)
		if err != nil {
			t.Fatal(err)
		}
		p, _ := mustMerge(t, user, a, b)
		if !strings.Contains(p.Result, "  implementation 'org.springframework.boot:spring-boot-starter-data-jpa'") {
			t.Errorf("%s: not inserted with detected indent:\n%s", name, p.Result)
		}
	}
}

func TestInsertKeepsCommentsAttached(t *testing.T) {
	a, b := ref(t, "web", "kts"), ref(t, "web-jpa-pg", "kts")
	src := strings.Replace(a.Src, "dependencies {\n", "dependencies {\n\t// pinned on purpose\n\timplementation(\"com.google.guava:guava:33.0\")\n", 1)
	user, err := Parse(GradleKotlin, src)
	if err != nil {
		t.Fatal(err)
	}
	p, _ := mustMerge(t, user, a, b)
	if !strings.Contains(p.Result, "// pinned on purpose\n\timplementation(\"com.google.guava:guava:33.0\")") {
		t.Errorf("comment separated from its line:\n%s", p.Result)
	}
}
