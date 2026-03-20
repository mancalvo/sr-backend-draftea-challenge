package domain

import "time"

// AccessStatus represents the status of an access record.
type AccessStatus string

const (
	AccessStatusActive  AccessStatus = "active"
	AccessStatusRevoked AccessStatus = "revoked"
)

// User represents a user in the catalog-access schema.
type User struct {
	ID        string    `json:"id"`
	Email     string    `json:"email"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Offering represents a purchasable offering.
type Offering struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	Price       int64     `json:"price"`
	Currency    string    `json:"currency"`
	Active      bool      `json:"active"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// AccessRecord represents a user's access to an offering.
type AccessRecord struct {
	ID            string       `json:"id"`
	UserID        string       `json:"user_id"`
	OfferingID    string       `json:"offering_id"`
	TransactionID string       `json:"transaction_id"`
	Status        AccessStatus `json:"status"`
	GrantedAt     time.Time    `json:"granted_at"`
	RevokedAt     *time.Time   `json:"revoked_at,omitempty"`
	CreatedAt     time.Time    `json:"created_at"`
	UpdatedAt     time.Time    `json:"updated_at"`
}

// Entitlement is the public-facing representation of an active access record.
type Entitlement struct {
	OfferingID    string    `json:"offering_id"`
	OfferingName  string    `json:"offering_name"`
	TransactionID string    `json:"transaction_id"`
	GrantedAt     time.Time `json:"granted_at"`
}
