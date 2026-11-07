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
