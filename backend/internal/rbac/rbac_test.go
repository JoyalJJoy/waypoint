package rbac

import "testing"

func TestRolePermissionMatrix(t *testing.T) {
	t.Parallel()

	// The assertions below are intentionally written out rather than derived
	// from rolePermissions, so a mistaken edit to the policy fails the test.
	cases := []struct {
		role    Role
		perm    Permission
		allowed bool
	}{
		{RoleOwner, PermTenantManage, true},
		{RoleOwner, PermOrdersCreate, true},
		{RoleAdmin, PermMembersManage, true},

		{RoleDispatcher, PermOrdersCreate, true},
		{RoleDispatcher, PermOrdersCancel, true},
		{RoleDispatcher, PermInventoryWrite, false},
		{RoleDispatcher, PermCatalogWrite, false},

		{RoleWarehouseManager, PermInventoryWrite, true},
		{RoleWarehouseManager, PermOrdersCreate, false},
		{RoleWarehouseManager, PermOrdersCancel, false},

		{RoleWarehouseOperator, PermInventoryWrite, true},
		{RoleWarehouseOperator, PermMembersManage, false},
		{RoleWarehouseOperator, PermOrdersCancel, false},

		{RoleDeliveryAgent, PermShipmentsWrite, true},
		{RoleDeliveryAgent, PermOrdersRead, false},
		{RoleDeliveryAgent, PermInventoryRead, false},

		{RoleSupport, PermOrdersRead, true},
		{RoleSupport, PermOrdersCreate, false},
		{RoleSupport, PermInventoryWrite, false},
		{RoleSupport, PermMembersManage, false},
	}

	for _, tc := range cases {
		if got := tc.role.Can(tc.perm); got != tc.allowed {
			t.Errorf("%s.Can(%s) = %v, want %v", tc.role, tc.perm, got, tc.allowed)
		}
	}
}

func TestUnknownRolesHaveNoPermissions(t *testing.T) {
	t.Parallel()

	unknown := Role("intern")
	if unknown.Valid() {
		t.Fatal("unknown role reported as valid")
	}
	for _, role := range []Role{
		RoleOwner, RoleAdmin, RoleDispatcher, RoleWarehouseManager,
		RoleWarehouseOperator, RoleDeliveryAgent, RoleSupport,
	} {
		if !role.Valid() {
			t.Errorf("%s should be valid", role)
		}
	}
	if unknown.Can(PermOrdersRead) {
		t.Fatal("unknown role was granted a permission")
	}
}
