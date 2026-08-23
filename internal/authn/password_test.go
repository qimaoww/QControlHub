package authn

import (
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func TestHashAndCheckPasswordRoundTrip(t *testing.T) {
	hash, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	if !CheckPassword(hash, "correct horse battery staple") {
		t.Fatal("expected the original password to verify")
	}
	if CheckPassword(hash, "wrong password entirely") {
		t.Fatal("expected a wrong password to be rejected")
	}
	if CheckPassword("", "something") || CheckPassword(hash, "") {
		t.Fatal("empty hash or password must be rejected")
	}
}

// The dummy hash must be produced once at package init with the production
// cost. Lazily generating it in a once block on the first unknown-username
// request would add a second bcrypt hash generation to that request and make it
// distinguishable from a wrong-password login after a process restart.
func TestCheckPasswordDummyHashIsPrecomputedCost12(t *testing.T) {
	if dummyHash == "" {
		t.Fatal("dummy hash must be generated at package init")
	}
	cost, err := bcrypt.Cost([]byte(dummyHash))
	if err != nil {
		t.Fatalf("parse dummy hash cost: %v", err)
	}
	if cost != passwordCost {
		t.Fatalf("dummy hash cost = %d, want %d", cost, passwordCost)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(dummyHash), []byte(dummySeed)); err != nil {
		t.Fatalf("dummy hash must verify its seed password: %v", err)
	}
}

func withCountingBcrypt(t *testing.T, fn func(count *int)) {
	t.Helper()
	original := compareBcrypt
	count := 0
	compareBcrypt = func(hash, password []byte) error {
		count++
		return original(hash, password)
	}
	defer func() { compareBcrypt = original }()
	fn(&count)
}

// The first and every subsequent call must each pay exactly one bcrypt
// comparison. Counting the real primitive avoids a flaky wall-clock threshold
// that would still pass if a single call ran two bcrypt operations.
func TestCheckPasswordDummyPerformsSingleComparisonPerCall(t *testing.T) {
	withCountingBcrypt(t, func(count *int) {
		CheckPasswordDummy("first unknown login")
		CheckPasswordDummy("second unknown login")
		if *count != 2 {
			t.Fatalf("two dummy calls performed %d bcrypt comparisons, want exactly 2", *count)
		}
	})
}

// A maximum-length input must reach the comparison too and never be
// short-circuited before paying the production bcrypt cost.
func TestCheckPasswordDummyLongInputSingleComparison(t *testing.T) {
	withCountingBcrypt(t, func(count *int) {
		CheckPasswordDummy(strings.Repeat("x", maxPasswordBytes))
		if *count != 1 {
			t.Fatalf("long-input dummy call performed %d bcrypt comparisons, want exactly 1", *count)
		}
	})
}

func TestCheckPasswordDummyEmptyInputSkipsBcrypt(t *testing.T) {
	withCountingBcrypt(t, func(count *int) {
		CheckPasswordDummy("")
		if *count != 0 {
			t.Fatalf("empty input must skip bcrypt, got %d comparisons", *count)
		}
	})
}
