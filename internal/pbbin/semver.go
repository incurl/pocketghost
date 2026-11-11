// Package pbbin manages the local PocketBase binary cache. It is the Go port of
// PocketBaseBinaryService: version discovery, download, checksum verification and
// extraction, with the exact same on-disk layout.
package pbbin

import (
	"fmt"
	"strconv"
	"strings"
)

// Version is a parsed semantic version.
type Version struct {
	Major, Minor, Patch int
	Pre                 string
	Raw                 string
}

// ParseVersion parses "1.2.3" or "1.2.3-rc1" (leading "v" tolerated).
func ParseVersion(s string) (Version, error) {
	raw := strings.TrimSpace(s)
	s = strings.TrimPrefix(raw, "v")
	pre := ""
	if i := strings.IndexByte(s, '-'); i >= 0 {
		pre = s[i+1:]
		s = s[:i]
	}
	parts := strings.Split(s, ".")
	if len(parts) < 2 || len(parts) > 3 {
		return Version{}, fmt.Errorf("pbbin: invalid version %q", raw)
	}
	nums := make([]int, 3)
	for i := 0; i < 3; i++ {
		if i >= len(parts) {
			nums[i] = 0
			continue
		}
		n, err := strconv.Atoi(parts[i])
		if err != nil {
			return Version{}, fmt.Errorf("pbbin: invalid version %q: %w", raw, err)
		}
		nums[i] = n
	}
	return Version{Major: nums[0], Minor: nums[1], Patch: nums[2], Pre: pre, Raw: raw}, nil
}

// String renders the version.
func (v Version) String() string {
	s := fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
	if v.Pre != "" {
		return s + "-" + v.Pre
	}
	return s
}

// Compare returns -1, 0 or 1.
func (v Version) Compare(o Version) int {
	if c := cmpInt(v.Major, o.Major); c != 0 {
		return c
	}
	if c := cmpInt(v.Minor, o.Minor); c != 0 {
		return c
	}
	if c := cmpInt(v.Patch, o.Patch); c != 0 {
		return c
	}
	// A prerelease sorts below the corresponding release.
	switch {
	case v.Pre == "" && o.Pre == "":
		return 0
	case v.Pre == "":
		return 1
	case o.Pre == "":
		return -1
	}
	return strings.Compare(v.Pre, o.Pre)
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

// Satisfies reports whether v matches a simplified npm-style range. Supported
// forms: "*", "", "1.2.3", "=1.2.3", ">=1.2.3", ">1.2.3", "<=1.2.3", "<1.2.3",
// "^1.2.3", "~1.2.3", "1.2.*", "1.*", plus space-separated conjunctions.
func (v Version) Satisfies(rng string) bool {
	rng = strings.TrimSpace(rng)
	if rng == "" || rng == "*" || rng == "latest" {
		return true
	}
	for _, clause := range strings.Fields(rng) {
		if !satisfiesClause(v, clause) {
			return false
		}
	}
	return true
}

func satisfiesClause(v Version, c string) bool {
	c = strings.TrimSpace(c)
	if c == "" || c == "*" {
		return true
	}
	// OR groups (||) are not used by PocketHost but are cheap to support.
	if strings.Contains(c, "||") {
		for _, alt := range strings.Split(c, "||") {
			if v.Satisfies(alt) {
				return true
			}
		}
		return false
	}

	for _, op := range []string{">=", "<=", "=", ">", "<", "^", "~"} {
		if strings.HasPrefix(c, op) {
			base := strings.TrimSpace(strings.TrimPrefix(c, op))
			if strings.HasSuffix(base, ".*") {
				base = strings.TrimSuffix(base, ".*")
			}
			bv, err := ParseVersion(base)
			if err != nil {
				return false
			}
			switch op {
			case ">=":
				return v.Compare(bv) >= 0
			case "<=":
				return v.Compare(bv) <= 0
			case ">":
				return v.Compare(bv) > 0
			case "<":
				return v.Compare(bv) < 0
			case "=":
				return wildcardEq(v, base)
			case "^":
				if v.Compare(bv) < 0 {
					return false
				}
				// ^0.x.y only allows the same minor.
				if bv.Major == 0 {
					return v.Major == 0 && v.Minor == bv.Minor
				}
				return v.Major == bv.Major
			case "~":
				if v.Compare(bv) < 0 {
					return false
				}
				return v.Major == bv.Major && v.Minor == bv.Minor
			}
		}
	}

	// Bare version, possibly with a wildcard segment ("0.41.*").
	if wildcardEq(v, c) {
		return true
	}
	bv, err := ParseVersion(c)
	if err != nil {
		return false
	}
	return v.Compare(bv) == 0
}

func wildcardEq(v Version, pattern string) bool {
	parts := strings.Split(pattern, ".")
	if len(parts) == 0 {
		return false
	}
	nums := []int{v.Major, v.Minor, v.Patch}
	for i, p := range parts {
		if i >= 3 {
			return false
		}
		if p == "*" || p == "x" || p == "X" {
			return true
		}
		n, err := strconv.Atoi(strings.TrimPrefix(p, "v"))
		if err != nil || n != nums[i] {
			return false
		}
	}
	// No wildcard: exact match on the specified precision.
	for i := len(parts); i < 3; i++ {
		if nums[i] != 0 {
			return false
		}
	}
	return true
}

// MaxSatisfying returns the highest version in list matching rng.
func MaxSatisfying(list []string, rng string) (string, bool) {
	var best Version
	found := false
	for _, raw := range list {
		v, err := ParseVersion(raw)
		if err != nil {
			continue
		}
		if !v.Satisfies(rng) {
			continue
		}
		if !found || v.Compare(best) > 0 {
			best = v
			found = true
		}
	}
	if !found {
		return "", false
	}
	return best.Raw, true
}
