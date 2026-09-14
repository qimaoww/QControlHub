package core

import (
	"time"
)

// Role identifies the account class. Fine-grained access is carried by the
// explicit Permissions field on a user; only admin/user are persisted.
type Role string

const (
	RoleAdmin Role = "admin"
	RoleUser  Role = "user"
	// Deprecated aliases keep older integrations compiling. They serialize as
	// "user" and are not accepted as separate persisted identities.
	RoleOperator Role = RoleUser
	RoleAuditor  Role = RoleUser
	RoleReadonly Role = RoleUser
)

// AtLeast reports whether the role grants at least the given privilege level.
func (role Role) AtLeast(minimum Role) bool {
	rank := func(value Role) int {
		switch value {
		case RoleAdmin:
			return 3
		case RoleUser:
			return 1
		default:
			return 1
		}
	}
	return rank(role) >= rank(minimum)
}

func (role Role) Valid() bool {
	switch role {
	case RoleAdmin, RoleUser:
		return true
	default:
		return false
	}
}

// User is a durable panel login identity. Password hashes are intentionally
// never included in this public model.
type User struct {
	ID           string       `json:"id"`
	Username     string       `json:"username"`
	DisplayName  string       `json:"display_name,omitempty"`
	Role         Role         `json:"role"`
	Permissions  []Permission `json:"permissions,omitempty"`
	Disabled     bool         `json:"disabled"`
	CreatedAt    time.Time    `json:"created_at"`
	UpdatedAt    time.Time    `json:"updated_at"`
	LastLoginAt  *time.Time   `json:"last_login_at,omitempty"`
	AuthRevision int64        `json:"-"`
}

type UserRequest struct {
	Username       string       `json:"username"`
	DisplayName    string       `json:"display_name"`
	Role           Role         `json:"role"`
	Password       string       `json:"password"`
	Permissions    []Permission `json:"permissions,omitempty"`
	AgentIsolation bool         `json:"agent_isolation"`
}

type UserUpdate struct {
	DisplayName *string       `json:"display_name"`
	Role        *Role         `json:"role"`
	Password    *string       `json:"password"`
	Disabled    *bool         `json:"disabled"`
	Permissions *[]Permission `json:"permissions"`
}
