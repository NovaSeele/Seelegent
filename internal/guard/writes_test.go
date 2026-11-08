package guard

import (
	"slices"
	"strings"
	"testing"
)

func TestWrites(t *testing.T) {
	for _, tc := range []struct {
		line string
		want []string
		not  []string
	}{
		{`echo hi > /etc/motd`, []string{"/etc/motd"}, nil},
		{`cat a >> "/tmp/x y.txt" 2>&1`, []string{"/tmp/x y.txt"}, []string{"1"}},
		{`rm -rf /home/u/proj ./build`, []string{"/home/u/proj", "./build"}, []string{"-rf"}},
		{`cp src/a.txt /opt/dest/`, []string{"/opt/dest/"}, []string{"src/a.txt"}},
		{`Copy-Item -Path a.txt -Destination 'C:\Other\b.txt'`, []string{`C:\Other\b.txt`}, []string{"a.txt"}},
		{`Remove-Item -Recurse -Force 'D:\Data\x'`, []string{`D:\Data\x`}, nil},
		{`sudo -n tee /etc/hosts < h`, []string{"/etc/hosts"}, nil},
		{`bash -c "mv /a/b /c/d"`, []string{"/a/b", "/c/d"}, nil},
		{`cmd /c del /q C:\x\y.txt`, []string{`C:\x\y.txt`}, []string{"/q"}},
		{`ls -la /etc && git status`, nil, []string{"/etc"}},
		// A lone apostrophe is no POSIX quoting: cmd still reads the line.
		{`echo don't > C:\notes.txt`, []string{`C:\notes.txt`}, nil},
		// sed and perl write only when they edit in place, and never their script.
		{`sed -n 5p /etc/hosts`, nil, []string{"/etc/hosts", "5p"}},
		{`sed 's/a/b/' /etc/hosts > /tmp/out`, []string{"/tmp/out"}, []string{"/etc/hosts"}},
		{`sed -i 's/a/b/' /etc/hosts`, []string{"/etc/hosts"}, []string{"s/a/b/"}},
		{`sed -i.bak -e 's/a/b/' /x/y`, []string{"/x/y"}, []string{"s/a/b/", "-i.bak"}},
		{`sed --in-place=.orig 's/a/b/' /x/y`, []string{"/x/y"}, []string{"s/a/b/"}},
		{`sed -E --in-place -e 's/a/b/' -e 's/c/d/' /p/a /p/b`, []string{"/p/a", "/p/b"}, []string{"s/c/d/"}},
		{`sed -ni 's/a/b/p' /f`, []string{"/f"}, []string{"s/a/b/p"}},
		{`sudo sed -i 's/a/b/' /etc/hosts`, []string{"/etc/hosts"}, nil},
		{`perl -ne 'print' /etc/hosts`, nil, []string{"/etc/hosts"}},
		{`perl script.pl /etc/hosts`, nil, []string{"/etc/hosts", "script.pl"}},
		{`perl -Mstrict -e 'print 1' /etc/hosts`, nil, []string{"/etc/hosts"}},
		{`perl -pi -e 's/a/b/' /etc/x`, []string{"/etc/x"}, []string{"s/a/b/"}},
		{`perl -i.bak -pe 's/a/b/' /etc/x /etc/y`, []string{"/etc/x", "/etc/y"}, []string{"s/a/b/"}},
		{`perl -i -p fix.pl /etc/x`, []string{"/etc/x"}, []string{"fix.pl"}},
		// PowerShell's file cmdlets, named or positional, and redirections (field report 4, F6).
		{`Remove-Item -Path src/components/lighting -Recurse -Force`, []string{"src/components/lighting"}, []string{"-Recurse"}},
		{`Remove-Item -LiteralPath 'src\a b.vue'`, []string{`src\a b.vue`}, nil},
		{`Remove-Item src/old.ts`, []string{"src/old.ts"}, nil},
		{`Set-Content -Path src/a.ts -Value 'x'`, []string{"src/a.ts"}, nil},
		{`Set-Content src/b.ts 'x'`, []string{"src/b.ts"}, nil},
		{`'x' | Out-File -FilePath logs/out.txt -Encoding utf8`, []string{"logs/out.txt"}, nil},
		{`New-Item -ItemType Directory -Path src/new`, []string{"src/new"}, nil},
		{`Move-Item -Path src/a.ts -Destination lib/a.ts`, []string{"src/a.ts", "lib/a.ts"}, nil},
		{`Copy-Item src/a.ts lib/b.ts`, []string{"lib/b.ts"}, []string{"src/a.ts"}},
		{`Get-Content a.txt > b.txt`, []string{"b.txt"}, []string{"a.txt"}},
		{`powershell -Command "Remove-Item -Path src/x -Recurse"`, []string{"src/x"}, nil},
	} {
		got := Writes(tc.line)
		for _, w := range tc.want {
			if !slices.Contains(got, w) {
				t.Errorf("Writes(%q) = %q, missing %q", tc.line, got, w)
			}
		}
		for _, w := range tc.not {
			if slices.Contains(got, w) {
				t.Errorf("Writes(%q) = %q, has %q", tc.line, got, w)
			}
		}
	}
}

// TestWritesSed: sed writes only the file operands after its script, and only with -i; nothing inside the script is
// a path (field report 3, B2: `\/` in a script was reported as a write outside the repository).
func TestWritesSed(t *testing.T) {
	for _, tc := range []struct {
		line string
		want []string
	}{
		// The two commands of the field report.
		{`sed -i '/<Button v-if="false" \/>/d; /<Toggle :x="1" \/>/d' src/components/advanced/PairEditor.vue`,
			[]string{"src/components/advanced/PairEditor.vue"}},
		{`sed -i 's/^const STEM_TRAVEL = 64;/const STEM_TRAVEL = 48; \/\/ cap at 48/' src/components/live/SwitchSim.vue`,
			[]string{"src/components/live/SwitchSim.vue"}},
		// Without -i sed only prints.
		{`sed 's/a/b/' src/a.vue`, nil},
		{`sed -n '/<a \/>/p' src/a.vue`, nil},
		{`sed -e 's/a/b/' -e 's/c/d/' src/a.vue`, nil},
		// In-place forms.
		{`sed -i.bak 's/a/b/' src/a.vue`, []string{"src/a.vue"}},
		{`sed -i '' 's/a/b/' src/a.vue`, []string{"src/a.vue"}},
		{`sed -i -e 's/a/b/' -e 's/c\/d/e/' src/a.vue src/b.vue`, []string{"src/a.vue", "src/b.vue"}},
		{`sed -i -f /home/u/fix.sed src/a.vue`, []string{"src/a.vue"}},
		{`sed -E -s -n -r -i 's/(a)/\1b/p' src/a.vue`, []string{"src/a.vue"}},
		{`sed --in-place=.orig --expression='s/a/b/' src/a.vue`, []string{"src/a.vue"}},
		{`sed --in-place --line-length 80 's/a/b/' src/a.vue`, []string{"src/a.vue"}},
		// A script holding real-looking absolute paths.
		{`sed -i 's|/etc/hosts|/tmp/hosts|; s/\/usr\/local\/bin/\/opt\/bin/' conf/app.conf`, []string{"conf/app.conf"}},
		{`sed -i "s|C:\Tools|D:\Tools|" conf/app.conf`, []string{"conf/app.conf"}},
		{`cd web && sed -i 's/<b \/>/<i \/>/' src/x.vue && npm test`, []string{"web/src/x.vue"}},
	} {
		if got := Writes(tc.line); !slices.Equal(got, tc.want) {
			t.Errorf("Writes(%q) = %q, want %q", tc.line, got, tc.want)
		}
	}
}

// TestWriteTargets: a write is joined to the folder the line moved to before it (cd, Set-Location, sl, chdir,
// pushd/popd, over &&, ; and |), and a write whose place an expansion names is unknown, or its written-out folder
// (field report 4, F6 review).
func TestWriteTargets(t *testing.T) {
	type tg = Target
	unknown := tg{Unknown: true}
	for _, tc := range []struct {
		line string
		want []Target
	}{
		{`cd app && rm -rf dist`, []Target{{Path: "app/dist"}}},
		{`cd app; touch a.txt`, []Target{{Path: "app/a.txt"}}},
		{`cd app | rm x`, []Target{{Path: "app/x"}}},
		{`cd app && cd src && rm x`, []Target{{Path: "app/src/x"}}},
		{`cd app && echo hi > out.txt`, []Target{{Path: "app/out.txt"}}},
		{`cd /tmp && rm x`, []Target{{Path: "/tmp/x"}}},
		{`cd D:\Other && del x.txt`, []Target{{Path: `D:\Other/x.txt`}}},
		{`cd /d D:\Other && del x.txt`, []Target{{Path: `D:\Other/x.txt`}}},
		{`Set-Location src; Remove-Item x -Recurse`, []Target{{Path: "src/x"}}},
		{`Set-Location -Path src; Remove-Item x`, []Target{{Path: "src/x"}}},
		{`sl lib; ni b.ts`, []Target{{Path: "lib/b.ts"}}},
		{`chdir lib && del b.ts`, []Target{{Path: "lib/b.ts"}}},
		{`pushd lib && rm a && popd && rm b`, []Target{{Path: "lib/a"}, {Path: "b"}}},
		{`Push-Location lib; Remove-Item a; Pop-Location; Remove-Item b`, []Target{{Path: "lib/a"}, {Path: "b"}}},
		{`bash -c "cd x && rm y" && rm z`, []Target{{Path: "x/y"}, {Path: "z"}}},
		{`cd app && bash -c "rm y"`, []Target{{Path: "app/y"}}},
		// Expansions: unknown, or the folder written before them.
		{`rm -rf $DIR`, []Target{unknown}},
		{`Remove-Item -Path $env:X\dist -Recurse`, []Target{unknown}},
		{`del %OUT%\x.txt`, []Target{unknown}},
		{`cd $env:X; Remove-Item dist`, []Target{unknown}},
		{`cd $(mktemp -d) && rm x`, []Target{unknown}},
		{`echo hi > "$LOG"`, []Target{unknown}},
		{`rm src/$name.ts`, []Target{{Path: "src"}, {Path: "src/$name.ts"}}}, // cmd: a literal name
		{`cd $X && rm /abs/y`, []Target{{Path: "/abs/y"}}},
		// The temporary folder and the null devices are no write in the repository.
		{`echo hi > $env:TEMP\x.txt`, nil},
		{`rm -f $TMPDIR/x`, nil},
		{`npm test > $null 2>&1`, nil},
		{`npm test > /dev/null`, nil},
		{`npm test > nul`, nil},
	} {
		// Each dialect that reads the line adds its reading: cmd takes $X literally, POSIX reads D:\\x as D:x. The
		// callers drop a path that starts with $ or %, as the start of a variable; the other readings must match.
		var got []Target
		for _, x := range WriteTargets(tc.line) {
			if x.Unknown || !strings.HasPrefix(x.Path, "$") && !strings.HasPrefix(x.Path, "%") &&
				!strings.HasPrefix(x.Path, "D:O") {
				got = append(got, x)
			}
		}
		if !slices.Equal(got, tc.want) {
			t.Errorf("WriteTargets(%q) = %+v, want %+v", tc.line, got, tc.want)
		}
	}
}
