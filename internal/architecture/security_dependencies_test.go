package architecture_test

import (
	"bufio"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// Each floor is the first version that carries every fix for the advisories in
// scope. Raising the requirement without lowering these floors keeps a reviewed
// dependency from silently sliding back to a version that Go's vulnerability
// database reports as affected. The Go toolchain floor mirrors the crypto/x509
// denylist added in GO-2026-6809.
var vulnerabilityFloors = []struct {
	Module     string
	Minimum    string
	Advisories []string
}{
	{
		Module:  "golang.org/x/net",
		Minimum: "v0.60.0",
		Advisories: []string{
			"GO-2026-6603",
			"GO-2026-6610",
			"GO-2026-6611",
			"GO-2026-6612",
			"GO-2026-6617",
		},
	},
	{
		Module:     "golang.org/x/text",
		Minimum:    "v0.41.0",
		Advisories: []string{"GO-2026-6629"},
	},
	{
		Module:     "golang.org/x/crypto",
		Minimum:    "v0.56.0",
		Advisories: []string{"GO-2026-6354", "GO-2026-6355", "GO-2026-6303"},
	},
	{
		Module:     "google.golang.org/grpc",
		Minimum:    "v1.83.2",
		Advisories: []string{"GO-2026-4762", "GO-2026-6061", "GO-2026-6348", "GO-2026-6441", "GO-2026-6443"},
	},
}

// GoDirectiveFloor is the released 1.26 toolchain that contains the
// crypto/x509 fix for GO-2026-6809 (CVE-2026-111943). The 1.27 line has its own
// fix point, so a newer minor release is not automatically fixed.
const GoDirectiveFloor = "1.26.10"

// Go127Fix is the first 1.27 release that carries the same crypto/x509 fix.
const Go127Fix = "1.27.3"

func toolchainCarriesX509Fix(version string) bool {
	if compareVersion(version, "1.27.0") >= 0 {
		return compareVersion(version, Go127Fix) >= 0
	}
	return compareVersion(version, GoDirectiveFloor) >= 0
}

func TestModulesStayAboveVulnerabilityFloors(t *testing.T) {
	goVersion, requirements := readModuleRequirements(t)
	if !toolchainCarriesX509Fix(goVersion) {
		t.Fatalf("go directive %q does not carry the crypto/x509 fix for GO-2026-6809", goVersion)
	}
	for _, floor := range vulnerabilityFloors {
		version, ok := requirements[floor.Module]
		if !ok {
			t.Errorf("go.mod no longer requires %s; re-review %s before dropping the requirement", floor.Module, strings.Join(floor.Advisories, ", "))
			continue
		}
		if compareVersion(version, floor.Minimum) < 0 {
			t.Errorf("%s %s is below the fixed version %s for %s", floor.Module, version, floor.Minimum, strings.Join(floor.Advisories, ", "))
		}
	}
}

// TestVulnerabilityFloorComparisonRejectsOlderVersions keeps the guard honest:
// a comparator that accepted equal-or-older versions would pass the policy
// check while leaving the module affected.
func TestVulnerabilityFloorComparisonRejectsOlderVersions(t *testing.T) {
	for _, testCase := range []struct {
		version string
		minimum string
		want    int
	}{
		{version: "v0.56.0", minimum: "v0.60.0", want: -1},
		{version: "v1.75.1", minimum: "v1.83.2", want: -1},
		{version: "v0.39.0", minimum: "v0.41.0", want: -1},
		{version: "v0.56.0", minimum: "v0.56.0", want: 0},
		{version: "v0.60.0", minimum: "v0.56.0", want: 1},
		{version: "v1.83.2", minimum: "v1.82.1", want: 1},
		{version: "1.26.10", minimum: "1.26.10", want: 0},
		{version: "1.26.9", minimum: "1.26.10", want: -1},
		{version: "1.27.0", minimum: "1.26.10", want: 1},
	} {
		if got := compareVersion(testCase.version, testCase.minimum); got != testCase.want {
			t.Errorf("compareVersion(%q, %q) = %d, want %d", testCase.version, testCase.minimum, got, testCase.want)
		}
	}
}

// TestToolchainFloorRejectsEveryAffectedMinorRelease pins both fix points so a
// jump to a newer minor cannot silently reintroduce GO-2026-6809.
func TestToolchainFloorRejectsEveryAffectedMinorRelease(t *testing.T) {
	for _, testCase := range []struct {
		version string
		want    bool
	}{
		{version: "1.26.9", want: false},
		{version: "1.26.10", want: true},
		{version: "1.26.11", want: true},
		{version: "1.27.0", want: false},
		{version: "1.27.2", want: false},
		{version: "1.27.3", want: true},
		{version: "1.28.0", want: true},
	} {
		if got := toolchainCarriesX509Fix(testCase.version); got != testCase.want {
			t.Errorf("toolchainCarriesX509Fix(%q) = %t, want %t", testCase.version, got, testCase.want)
		}
	}
}

func readModuleRequirements(t *testing.T) (string, map[string]string) {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate architecture test")
	}
	root, err := filepath.Abs(filepath.Join(filepath.Dir(file), "..", ".."))
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
	file, err = filepath.Abs(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatalf("resolve go.mod: %v", err)
	}
	handle, err := os.Open(file)
	if err != nil {
		t.Fatalf("open go.mod: %v", err)
	}
	defer handle.Close()
	requirements := make(map[string]string)
	goVersion := ""
	inRequireBlock := false
	scanner := bufio.NewScanner(handle)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if index := strings.Index(line, "//"); index >= 0 {
			line = strings.TrimSpace(line[:index])
		}
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "require (") || line == "require (" {
			inRequireBlock = true
			continue
		}
		if inRequireBlock && line == ")" {
			inRequireBlock = false
			continue
		}
		fields := strings.Fields(line)
		switch {
		case inRequireBlock && len(fields) >= 2:
			requirements[fields[0]] = fields[1]
		case len(fields) >= 2 && fields[0] == "require":
			requirements[fields[1]] = fields[2]
		case len(fields) >= 2 && fields[0] == "go":
			goVersion = fields[1]
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("read go.mod: %v", err)
	}
	if goVersion == "" {
		t.Fatal("go.mod has no go directive")
	}
	return goVersion, requirements
}

// compareVersion orders dotted numeric versions and treats a prerelease as
// lower than the matching release, which is all the policy needs.
func compareVersion(left, right string) int {
	leftNumbers, leftPre := splitVersion(left)
	rightNumbers, rightPre := splitVersion(right)
	for index := 0; index < len(leftNumbers) || index < len(rightNumbers); index++ {
		leftNumber := 0
		if index < len(leftNumbers) {
			leftNumber = leftNumbers[index]
		}
		rightNumber := 0
		if index < len(rightNumbers) {
			rightNumber = rightNumbers[index]
		}
		if leftNumber != rightNumber {
			if leftNumber < rightNumber {
				return -1
			}
			return 1
		}
	}
	switch {
	case leftPre && !rightPre:
		return -1
	case !leftPre && rightPre:
		return 1
	}
	return 0
}

func splitVersion(value string) ([]int, bool) {
	value = strings.TrimPrefix(strings.TrimSpace(value), "v")
	prerelease := false
	if index := strings.IndexAny(value, "-+"); index >= 0 {
		if value[index] == '-' {
			prerelease = true
		}
		value = value[:index]
	}
	parts := strings.Split(value, ".")
	numbers := make([]int, 0, len(parts))
	for _, part := range parts {
		number, err := strconv.Atoi(part)
		if err != nil {
			break
		}
		numbers = append(numbers, number)
	}
	return numbers, prerelease
}
