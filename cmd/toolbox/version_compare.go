package main

import (
	"regexp"
	"sort"
	"strconv"
	"strings"

	tooldef "github.com/solidarity-ai/toolbox/tool"
)

var semverStrictPattern = regexp.MustCompile(`^v([0-9]+)\.([0-9]+)\.([0-9]+)(?:-([0-9A-Za-z.-]+))?(?:\+.*)?$`)

type parsedSemver struct {
	major int
	minor int
	patch int
	pre   string
}

func sortVersionsDesc(versions []tooldef.Version) {
	sort.Slice(versions, func(i, j int) bool {
		return compareVersions(versions[i], versions[j]) > 0
	})
}

func compareVersions(a, b tooldef.Version) int {
	if a == b {
		return 0
	}
	if a.IsPseudo() && b.IsPseudo() {
		if cmp := strings.Compare(a.PseudoTimestamp(), b.PseudoTimestamp()); cmp != 0 {
			return cmp
		}
		return strings.Compare(a.PseudoCommit(), b.PseudoCommit())
	}
	if a.IsPseudo() {
		return -1
	}
	if b.IsPseudo() {
		return 1
	}

	pa, oka := parseSemver(a)
	pb, okb := parseSemver(b)
	if oka && okb {
		if pa.major != pb.major {
			return intCompare(pa.major, pb.major)
		}
		if pa.minor != pb.minor {
			return intCompare(pa.minor, pb.minor)
		}
		if pa.patch != pb.patch {
			return intCompare(pa.patch, pb.patch)
		}
		return comparePrerelease(pa.pre, pb.pre)
	}

	return strings.Compare(a.String(), b.String())
}

func parseSemver(v tooldef.Version) (parsedSemver, bool) {
	m := semverStrictPattern.FindStringSubmatch(v.String())
	if len(m) != 5 {
		return parsedSemver{}, false
	}
	major, err := strconv.Atoi(m[1])
	if err != nil {
		return parsedSemver{}, false
	}
	minor, err := strconv.Atoi(m[2])
	if err != nil {
		return parsedSemver{}, false
	}
	patch, err := strconv.Atoi(m[3])
	if err != nil {
		return parsedSemver{}, false
	}
	return parsedSemver{major: major, minor: minor, patch: patch, pre: m[4]}, true
}

func comparePrerelease(a, b string) int {
	if a == b {
		return 0
	}
	if a == "" {
		return 1
	}
	if b == "" {
		return -1
	}

	aParts := strings.Split(a, ".")
	bParts := strings.Split(b, ".")
	for i := 0; i < len(aParts) && i < len(bParts); i++ {
		if aParts[i] == bParts[i] {
			continue
		}
		aNum, aNumOK := parseNumericIdentifier(aParts[i])
		bNum, bNumOK := parseNumericIdentifier(bParts[i])
		switch {
		case aNumOK && bNumOK:
			return intCompare(aNum, bNum)
		case aNumOK:
			return -1
		case bNumOK:
			return 1
		default:
			return strings.Compare(aParts[i], bParts[i])
		}
	}
	return intCompare(len(aParts), len(bParts))
}

func parseNumericIdentifier(s string) (int, bool) {
	if s == "" {
		return 0, false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, false
		}
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, false
	}
	return n, true
}

func intCompare(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}
