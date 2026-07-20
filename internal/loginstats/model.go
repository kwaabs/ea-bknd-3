package loginstats

import "time"

type DailyCount struct {
	Date  string `json:"date" bun:"date"`
	Count int    `json:"count" bun:"count"`
}

type ProviderCount struct {
	Provider string `json:"provider" bun:"provider"`
	Count    int    `json:"count" bun:"count"`
}

type UserFrequency struct {
	Email       string    `json:"email" bun:"email"`
	Name        string    `json:"name" bun:"name"`
	Provider    string    `json:"provider" bun:"provider"`
	LoginCount  int       `json:"login_count" bun:"login_count"`
	LastLoginAt time.Time `json:"last_login_at" bun:"last_login_at"`
}

type RecentEvent struct {
	Email      string    `json:"email" bun:"email"`
	Name       string    `json:"name" bun:"name"`
	Provider   string    `json:"provider" bun:"provider"`
	DeviceInfo *string   `json:"device_info,omitempty" bun:"device_info"`
	CreatedAt  time.Time `json:"created_at" bun:"created_at"`
}

type StatsResponse struct {
	Success      bool            `json:"success"`
	From         string          `json:"from"`
	To           string          `json:"to"`
	TotalLogins  int             `json:"total_logins"`
	UniqueUsers  int             `json:"unique_users"`
	ByDay        []DailyCount    `json:"by_day"`
	ByProvider   []ProviderCount `json:"by_provider"`
	ByUser       []UserFrequency `json:"by_user"`
	RecentEvents []RecentEvent   `json:"recent_events"`
}
