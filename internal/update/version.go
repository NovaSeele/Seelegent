package update

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Version is a semantic version (vMAJOR.MINOR.PATCH[-PRERELEASE][+BUILD]); the build part is ignored.
type Version struct {
	Major, Minor, Patch int
	// Pre holds the dot-separated pre-release identifiers ("rc", "1" for -rc.1); empty for a release.
	Pre []string
}

var versionRE = regexp.MustCompile(`^v?(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)` +
	`(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$`)

// ParseVersion parses s, with or without a leading "v".
func ParseVersion(s string) (Version, error) {
	m := versionRE.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return Version{}, fmt.Errorf("%q is not a version like v1.2.3", s)
	}
	var v Version
	for i, p := range []*int{&v.Major, &v.Minor, &v.Patch} {
		n, err := strconv.Atoi(m[i+1])
		if err != nil {
			return Version{}, fmt.Errorf("%q is not a version like v1.2.3: %w", s, err)
		}
		*p = n
	}
	if m[4] != "" {
		v.Pre = strings.Split(m[4], ".")
	}
	return v, nil
}

// String is the version with a leading "v" ("v1.2.3-rc.1").
func (v Version) String() string {
	s := fmt.Sprintf("v%d.%d.%d", v.Major, v.Minor, v.Patch)
	if len(v.Pre) > 0 {
		s += "-" + strings.Join(v.Pre, ".")
	}
	return s
}

// Compare returns -1, 0 or 1 as v is lower than, equal to or higher than o, by semver precedence: a pre-release
// is lower than its release, numeric identifiers compare as numbers and are lower than alphanumeric ones.
func (v Version) Compare(o Version) int {
	for _, d := range [][2]int{{v.Major, o.Major}, {v.Minor, o.Minor}, {v.Patch, o.Patch}} {
		if c := cmpInt(d[0], d[1]); c != 0 {
			return c
		}
	}
	switch {
	case len(v.Pre) == 0 && len(o.Pre) == 0:
		return 0
	case len(v.Pre) == 0:
		return 1
	case len(o.Pre) == 0:
		return -1
	}
	for i := 0; i < len(v.Pre) && i < len(o.Pre); i++ {
		if c := cmpIdent(v.Pre[i], o.Pre[i]); c != 0 {
			return c
		}
	}
	return cmpInt(len(v.Pre), len(o.Pre))
}

func cmpInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

func cmpIdent(a, b string) int {
	na, errA := strconv.Atoi(a)
	nb, errB := strconv.Atoi(b)
	switch {
	case errA == nil && errB == nil:
		return cmpInt(na, nb)
	case errA == nil:
		return -1
	case errB == nil:
		return 1
	}
	return strings.Compare(a, b)
}

// describeRE matches the end that `git describe` adds after a tag: -<commits>-g<hash>.
var describeRE = regexp.MustCompile(`(?:^|[.-])\d+-g[0-9a-f]{4,}$`)

// releaseVersion returns the version of a release build. A development build has none: "dev", anything that is not
// a version, a `git describe` version between tags (v1.2.3-4-gabcdef), a dirty tree or a goreleaser snapshot.
func releaseVersion(s string) (Version, bool) {
	v, err := ParseVersion(s)
	if err != nil {
		return Version{}, false
	}
	pre := strings.ToLower(strings.Join(v.Pre, "."))
	if strings.Contains(pre, "dirty") || strings.Contains(pre, "snapshot") || describeRE.MatchString(pre) {
		return Version{}, false
	}
	return v, true
}
