package guard

import (
	"slices"
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
		{`cd web && sed -i 's/<b \/>/<i \/>/' src/x.vue && npm test`, []string{"src/x.vue"}},
	} {
		if got := Writes(tc.line); !slices.Equal(got, tc.want) {
			t.Errorf("Writes(%q) = %q, want %q", tc.line, got, tc.want)
		}
	}
}
