package model

import "time"

// DislikedSource tracks sources that users have disliked to prevent immediate reuse
type DislikedSource struct {
	SourceID   string    `json:"source_id"`
	Duration   int64     `json:"duration_s"` // Ban duration in seconds (e.g., 86400 for 24 hours)
	DislikedAt time.Time `json:"disliked_at"`
}

// DislikedSourceIndex stores blacklisted sources
type DislikedSourceIndex struct {
	UpdatedAt time.Time        `json:"updated_at"`
	Items     []DislikedSource `json:"items"`
}

// IsBlacklisted checks if a source is currently blacklisted
func (idx *DislikedSourceIndex) IsBlacklisted(sourceID string) bool {
	if idx == nil {
		return false
	}
	now := time.Now()
	for _, item := range idx.Items {
		if item.SourceID == sourceID {
			// Check if ban period has expired
			expiresAt := item.DislikedAt.Add(time.Duration(item.Duration) * time.Second)
			if now.Before(expiresAt) {
				return true // Still blacklisted
			}
		}
	}
	return false
}

// CleanupExpired removes sources whose ban period has expired
func (idx *DislikedSourceIndex) CleanupExpired() {
	if idx == nil {
		return
	}
	now := time.Now()
	filtered := make([]DislikedSource, 0, len(idx.Items))
	for _, item := range idx.Items {
		expiresAt := item.DislikedAt.Add(time.Duration(item.Duration) * time.Second)
		if now.Before(expiresAt) {
			filtered = append(filtered, item)
		}
	}
	idx.Items = filtered
	idx.UpdatedAt = time.Now()
}
