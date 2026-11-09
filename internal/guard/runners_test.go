package guard

import "testing"

// TestRuns: the code runners whose writes no word names (field report 4, review: python fix.py wrote outside the
// lane and only warned).
func TestRuns(t *testing.T) {
	for line, want := range map[string]bool{
		`python fix.py`: true,
		`python -c "open('docs/x.md','w').write('x')"`: true,
		`py -3 tools\\gen.py`:                          true,
		`node gen.js`:                                  true,
		`go run ./tools/x`:                             true,
		`go generate ./...`:                            true,
		`go build ./...`:                               false,
		`npm run format`:                               true,
		`pnpm exec prettier --write .`:                 true,
		`make`:                                         true,
		`cargo run --bin x`:                            true,
		`bash scripts/gen.sh`:                          true,
		`bash -c "python x.py"`:                        true,
		`bash -c "ls -la"`:                             false,
		`./gen.sh`:                                     true,
		`.\\fix.ps1`:                                   true,
		`& python x.py`:                                true,
		`cd web && npm test`:                           true,
		`powershell -Command "Get-ChildItem"`:          false,
		`$x = python a.py`:                             true,
		`$x = Get-Content a.txt`:                       false,
		`ls -la; cat a.txt | grep x`:                   false,
		`rg -n foo .`:                                  false,
		`git status`:                                   false,
		`echo hi > notes.txt`:                          false,
	} {
		if got := Runs(line); got != want {
			t.Errorf("Runs(%q) = %v, want %v", line, got, want)
		}
	}
}
