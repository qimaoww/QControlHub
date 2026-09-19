package agent

import (
	"encoding/json"
	"errors"
	"time"
)

// Called with credentialsMu held. Bound the in-memory retransmission cache
// as well as the ordinary credential state. Always retain the current result.
func (c *Client) boundCompletedTaskCache(currentID string) error {
	for {
		content, err := json.Marshal(c.creds)
		if err != nil {
			return err
		}
		if len(content) < (512<<10)-4096 {
			return nil
		}
		oldestID := ""
		var oldest time.Time
		for id, item := range c.creds.CompletedTasks {
			if id != currentID && (oldestID == "" || item.CompletedAt.Before(oldest)) {
				oldestID, oldest = id, item.CompletedAt
			}
		}
		if oldestID == "" {
			return errors.New("task result exceeds the credential cache size limit")
		}
		delete(c.creds.CompletedTasks, oldestID)
	}
}

// Reports are persisted only in the panel database. Keep the live cache for
// reconnect retransmission, but never serialize its report entries to disk.
func credentialsWithoutIPQualityReports(value credentials) credentials {
	copy := make(map[string]completedTask, len(value.CompletedTasks))
	for id, item := range value.CompletedTasks {
		if item.IPQuality == nil {
			copy[id] = item
		}
	}
	value.CompletedTasks = copy
	return value
}
