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
