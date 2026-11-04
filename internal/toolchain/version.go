// Package toolchain detects, installs and pins git, Node.js and npm.
package toolchain

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Version is a parsed semantic-ish version.
type Version struct {
	Major, Minor, Patch int
	Raw                 string
}

func (v Version) String() string {
	return fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
}

// AtLeast reports whether v >= other.
func (v Version) AtLeast(other Version) bool {
	if v.Major != other.Major {
		return v.Major > other.Major
	}
	if v.Minor != other.Minor {
		return v.Minor > other.Minor
	}
	return v.Patch >= other.Patch
}

var versionRE = regexp.MustCompile(`(\d+)\.(\d+)(?:\.(\d+))?`)

// ParseVersion extracts the first dotted version from arbitrary tool output.
func ParseVersion(out string) (Version, error) {
	m := versionRE.FindStringSubmatch(out)
	if m == nil {
		return Version{}, fmt.Errorf("no version found in %q", strings.TrimSpace(out))
	}
	major, _ := strconv.Atoi(m[1])
	minor, _ := strconv.Atoi(m[2])
	patch := 0
	if m[3] != "" {
		patch, _ = strconv.Atoi(m[3])
	}
	return Version{Major: major, Minor: minor, Patch: patch, Raw: m[0]}, nil
}

// Minimum versions the installer will reuse. Anything older falls back to a
// portable toolchain instead of overwriting the system copy.
var (
	MinGit  = Version{Major: 2, Minor: 20}
	MinNode = Version{Major: 18}
)

// NodeSupported reports whether a system Node is within the supported range.
func NodeSupported(v Version) bool { return v.Major >= MinNode.Major }

// GitSupported reports whether a system git is new enough.
func GitSupported(v Version) bool { return v.AtLeast(MinGit) }

// MatchWant reports whether have satisfies a want string of the form "22",
// "22.14" or "22.14.0". A leading "v" is ignored.
func MatchWant(want string, have Version) bool {
	want = strings.TrimPrefix(strings.TrimSpace(want), "v")
	if want == "" {
		return true
	}
	parts := strings.Split(want, ".")
	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return false
	}
	if have.Major != major {
		return false
	}
	if len(parts) > 1 {
		minor, err := strconv.Atoi(parts[1])
		if err != nil || have.Minor != minor {
			return false
		}
	}
	if len(parts) > 2 {
		patch, err := strconv.Atoi(parts[2])
		if err != nil || have.Patch != patch {
			return false
		}
	}
	return true
}
