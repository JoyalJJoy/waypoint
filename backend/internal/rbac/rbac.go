package rbac

type Role string

const (
	RoleOwner             Role = "owner"
	RoleAdmin             Role = "admin"
	RoleDispatcher        Role = "dispatcher"
	RoleWarehouseManager  Role = "warehouse_manager"
	RoleWarehouseOperator Role = "warehouse_operator"
	RoleDeliveryAgent     Role = "delivery_agent"
	RoleSupport           Role = "support"
)

// Permission is an application capability. Keep permissions fine-grained
// enough that a warehouse operator cannot cancel an order or manage members.
type Permission string

const (
	PermTenantManage    Permission = "tenant:manage"
	PermMembersRead     Permission = "members:read"
	PermMembersManage   Permission = "members:manage"
	PermCatalogRead     Permission = "catalog:read"
	PermCatalogWrite    Permission = "catalog:write"
	PermInventoryRead   Permission = "inventory:read"
	PermInventoryWrite  Permission = "inventory:write"
	PermOrdersRead      Permission = "orders:read"
	PermOrdersCreate    Permission = "orders:create"
	PermOrdersUpdate    Permission = "orders:update"
	PermOrdersCancel    Permission = "orders:cancel"
	PermShipmentsRead   Permission = "shipments:read"
	PermShipmentsWrite  Permission = "shipments:write"
	PermShipmentsAssign Permission = "shipments:assign"
	PermTrackingRead    Permission = "tracking:read"
	PermReportsRead     Permission = "reports:read"
)

var rolePermissions = map[Role]map[Permission]struct{}{
	RoleOwner: permissionSet(
		PermTenantManage, PermMembersRead, PermMembersManage,
		PermCatalogRead, PermCatalogWrite,
		PermInventoryRead, PermInventoryWrite,
		PermOrdersRead, PermOrdersCreate, PermOrdersUpdate, PermOrdersCancel,
		PermShipmentsRead, PermShipmentsWrite, PermShipmentsAssign,
		PermTrackingRead, PermReportsRead,
	),
	RoleAdmin: permissionSet(
		PermTenantManage, PermMembersRead, PermMembersManage,
		PermCatalogRead, PermCatalogWrite,
		PermInventoryRead, PermInventoryWrite,
		PermOrdersRead, PermOrdersCreate, PermOrdersUpdate, PermOrdersCancel,
		PermShipmentsRead, PermShipmentsWrite, PermShipmentsAssign,
		PermTrackingRead, PermReportsRead,
	),
	RoleDispatcher: permissionSet(
		PermMembersRead,
		PermCatalogRead, PermInventoryRead,
		PermOrdersRead, PermOrdersCreate, PermOrdersUpdate, PermOrdersCancel,
		PermShipmentsRead, PermShipmentsWrite, PermShipmentsAssign,
		PermTrackingRead, PermReportsRead,
	),
	RoleWarehouseManager: permissionSet(
		PermMembersRead,
		PermCatalogRead, PermCatalogWrite,
		PermInventoryRead, PermInventoryWrite,
		PermOrdersRead, PermOrdersUpdate,
		PermShipmentsRead, PermShipmentsWrite,
		PermTrackingRead, PermReportsRead,
	),
	RoleWarehouseOperator: permissionSet(
		PermCatalogRead, PermInventoryRead, PermInventoryWrite,
		PermOrdersRead, PermOrdersUpdate,
		PermShipmentsRead, PermShipmentsWrite,
		PermTrackingRead,
	),
	RoleDeliveryAgent: permissionSet(
		PermShipmentsRead, PermShipmentsWrite,
		PermTrackingRead,
	),
	RoleSupport: permissionSet(
		PermMembersRead, PermCatalogRead, PermInventoryRead,
		PermOrdersRead, PermShipmentsRead, PermTrackingRead,
	),
}

func permissionSet(perms ...Permission) map[Permission]struct{} {
	set := make(map[Permission]struct{}, len(perms))
	for _, p := range perms {
		set[p] = struct{}{}
	}
	return set
}

func (r Role) Can(p Permission) bool {
	perms, ok := rolePermissions[r]
	if !ok {
		return false
	}
	_, ok = perms[p]
	return ok
}

func (r Role) Valid() bool {
	_, ok := rolePermissions[r]
	return ok
}

func (r Role) String() string { return string(r) }
