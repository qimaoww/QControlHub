package core

import (
	"slices"
	"testing"
)

func TestGrantablePermissionsExcludeAdministratorCapabilities(t *testing.T) {
	t.Parallel()
	grantable := GrantablePermissions()
	if HasPermission(grantable, PermissionUsersManage) {
		t.Fatal("users.manage is administrator-only and must not be grantable")
	}
	if len(grantable) != len(AllPermissions())-1 {
		t.Fatalf("grantable permissions = %d, all = %d; expected exactly one administrator-only capability",
			len(grantable), len(AllPermissions()))
	}
	if got := NormalizeGrantablePermissions(AllPermissions()); !slices.Equal(got, grantable) {
		t.Fatalf("normalization and the grantable list disagree: %v", got)
	}
}

func TestPermissionNormalization(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name      string
		input     []Permission
		all       []Permission
		grantable []Permission
	}{
		{name: "empty"},
		{name: "unknown", input: []Permission{"unknown.permission"}},
		{
			name: "order and duplicates",
			input: []Permission{PermissionAgentsRead, PermissionOverviewRead, PermissionAgentsRead,
				PermissionUsersManage, "unknown.permission", PermissionOverviewRead},
			all:       []Permission{PermissionAgentsRead, PermissionOverviewRead, PermissionUsersManage},
			grantable: []Permission{PermissionAgentsRead, PermissionOverviewRead},
		},
		{
			name: "administrator only", input: []Permission{PermissionUsersManage},
			all: []Permission{PermissionUsersManage},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			original := slices.Clone(test.input)
			for _, normalization := range []struct {
				name string
				run  func([]Permission) []Permission
				want []Permission
			}{
				{"all", NormalizePermissions, test.all},
				{"grantable", NormalizeGrantablePermissions, test.grantable},
			} {
				got := normalization.run(test.input)
				if got == nil || !slices.Equal(got, normalization.want) {
					t.Fatalf("%s normalization = %v, want %v (non-nil)", normalization.name, got, normalization.want)
				}
				if len(got) != 0 {
					got[0] = "modified.result"
				}
				if !slices.Equal(test.input, original) {
					t.Fatalf("%s normalization modified the input: %v", normalization.name, test.input)
				}
			}
		})
	}
}

func TestPermissionListsAreIndependentSnapshots(t *testing.T) {
	t.Parallel()
	all, grantable := AllPermissions(), GrantablePermissions()
	wantAll, wantGrantable := slices.Clone(all), slices.Clone(grantable)
	all[0], grantable[0] = "modified.all", "modified.grantable"
	if !slices.Equal(AllPermissions(), wantAll) || !slices.Equal(GrantablePermissions(), wantGrantable) {
		t.Fatal("mutating a returned list changed the shared permission policy")
	}
	if !slices.Equal(NormalizePermissions(wantAll), wantAll) ||
		!slices.Equal(NormalizeGrantablePermissions(wantAll), wantGrantable) {
		t.Fatal("mutating a returned list changed the normalization policy")
	}
	for _, permission := range wantAll {
		if !RoleAdmin.Allows(permission) {
			t.Fatalf("administrator lost %q", permission)
		}
		if RoleUser.Allows(permission) {
			t.Fatalf("user role implicitly gained %q", permission)
		}
	}
	if RoleAdmin.Allows("unknown.permission") || Role("unknown").Allows(PermissionUsersManage) {
		t.Fatal("unknown roles or capabilities must remain denied")
	}
}

var benchmarkNormalizedPermissions []Permission

func BenchmarkNormalizeGrantablePermissions(b *testing.B) {
	values := append(AllPermissions(), PermissionOverviewRead, Permission("unknown.permission"))
	b.ReportAllocs()
	for b.Loop() {
		benchmarkNormalizedPermissions = NormalizeGrantablePermissions(values)
	}
}
