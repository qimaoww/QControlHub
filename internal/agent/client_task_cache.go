package agent

import (
	"encoding/json"
	"errors"
	"time"
)

// Called with credentialsMu held. Structured reports must survive restart
// without the ordinary 4 KiB output truncation, but the credentials file still
// has to fit its existing 512 KiB read limit. Always retain the current result.
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
