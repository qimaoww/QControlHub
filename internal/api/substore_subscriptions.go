package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func ownedSubStoreSubscription(subscriptions []map[string]any, integrationID string) map[string]any {
	for _, subscription := range subscriptions {
		owner, _ := subscription["qcontrolhub_integration_id"].(string)
		if owner == integrationID {
			return subscription
		}
	}
	return nil
}

// cloneSubStoreSubscription keeps fields that are owned by Sub-Store (for
// example custom remarks, filters, and processing options) when we update only
// the content and QControlHub ownership metadata. The API returns maps whose
// values are not mutated after this point, so a shallow copy is sufficient.
func cloneSubStoreSubscription(source map[string]any) map[string]any {
	clone := make(map[string]any, len(source))
	for key, value := range source {
		clone[key] = value
	}
	return clone
}

func subStoreSubscriptionUpdate(existing, desired map[string]any) map[string]any {
	updated := cloneSubStoreSubscription(existing)
	for key, value := range desired {
		updated[key] = value
	}
	// These options are intentionally user-owned in Sub-Store. Keep them when
	// the control plane refreshes node content, while still updating the name,
	// source, content, and QControlHub ownership fields above.
	for _, key := range []string{"displayName", "display-name", "noFlow", "remark", "process"} {
		if value, ok := existing[key]; ok {
			updated[key] = value
		}
	}
	return updated
}

func subStoreSubscriptionPayload(target core.SubStoreSyncTarget, content string) map[string]any {
	managedNames, _ := subStoreNodeNames(content)
	return map[string]any{
		"name":                       target.SubscriptionName,
		"displayName":                target.SubscriptionName,
		"display-name":               target.SubscriptionName,
		"source":                     "local",
		"content":                    content,
		"noFlow":                     true,
		"remark":                     "Managed by QControlHub",
		"process":                    []any{},
		"qcontrolhub_integration_id": target.IntegrationID,
		"qcontrolhub_managed_nodes":  managedNames,
	}
}

func (s *Server) renameSubStoreSubscription(ctx context.Context, settings core.SubStoreSyncSettings, target core.SubStoreSyncTarget, name string) (bool, error) {
	var subscriptions []map[string]any
	if _, err := s.subStoreRequest(ctx, settings.EndpointURL, http.MethodGet, "/api/subs", nil, &subscriptions); err != nil {
		return false, err
	}
	owned := ownedSubStoreSubscription(subscriptions, target.IntegrationID)
	if owned == nil {
		return false, nil
	}
	for _, subscription := range subscriptions {
		existingName, _ := subscription["name"].(string)
		owner, _ := subscription["qcontrolhub_integration_id"].(string)
		if existingName == name && owner != target.IntegrationID {
			return false, errors.New("Sub-Store 中已存在同名订阅，请更换同步组名称")
		}
	}
	existingName, _ := owned["name"].(string)
	if existingName == "" {
		return false, errors.New("Sub-Store 返回的订阅缺少名称")
	}
	payload := make(map[string]any, len(owned))
	for key, value := range owned {
		payload[key] = value
	}
	payload["name"] = name
	payload["displayName"] = name
	payload["display-name"] = name
	payload["qcontrolhub_integration_id"] = target.IntegrationID
	_, err := s.subStoreRequest(ctx, settings.EndpointURL, http.MethodPatch, "/api/sub/"+existingName, payload, nil)
	return err == nil, err
}

func (s *Server) upsertSubStoreSubscription(ctx context.Context, settings core.SubStoreSyncSettings, target core.SubStoreSyncTarget, content string) (bool, error) {
	mode, valid := core.NormalizeSubStoreSyncMode(strings.TrimSpace(target.SyncMode))
	if !valid {
		return false, errors.New("Sub-Store 同步组模式无效，请重新保存组设置")
	}
	currentNames, err := subStoreNodeNames(content)
	if err != nil {
		return false, err
	}
	payload := subStoreSubscriptionPayload(target, content)
	var subscriptions []map[string]any
	if _, err := s.subStoreRequest(ctx, settings.EndpointURL, http.MethodGet, "/api/subs", nil, &subscriptions); err != nil {
		return false, err
	}
	var owned map[string]any
	var colliding map[string]any
	for _, subscription := range subscriptions {
		name, _ := subscription["name"].(string)
		owner, _ := subscription["qcontrolhub_integration_id"].(string)
		if owner == target.IntegrationID {
			owned = subscription
		}
		if name == target.SubscriptionName {
			colliding = subscription
		}
	}
	if owned == nil && colliding == nil {
		_, err := s.subStoreRequest(ctx, settings.EndpointURL, http.MethodPost, "/api/subs", payload, nil)
		return true, err
	}
	if owned == nil {
		collisionOwner, _ := colliding["qcontrolhub_integration_id"].(string)
		if strings.TrimSpace(collisionOwner) != "" {
			return false, errors.New("Sub-Store 中已存在同名订阅，但它属于其他 QControlHub；请更换订阅名称")
		}
		existingName, _ := colliding["name"].(string)
		if existingName == "" {
			return false, errors.New("Sub-Store 返回的同名订阅缺少名称")
		}
		if mode == core.SubStoreSyncModeIncremental {
			existingContent, _ := colliding["content"].(string)
			merged, mergeErr := mergeSubStoreContentByName(existingContent, content, nil)
			if mergeErr != nil {
				return false, mergeErr
			}
			payload["content"] = merged
		}
		payload["qcontrolhub_managed_nodes"] = currentNames
		// Preserve fields configured directly in Sub-Store while replacing only
		// values owned by QControlHub.
		payload = subStoreSubscriptionUpdate(colliding, payload)
		_, err := s.subStoreRequest(ctx, settings.EndpointURL, http.MethodPatch, "/api/sub/"+existingName, payload, nil)
		return false, err
	}
	existingName, _ := owned["name"].(string)
	if existingName == "" {
		return false, errors.New("Sub-Store 返回的订阅缺少名称")
	}
	if colliding != nil {
		collisionOwner, _ := colliding["qcontrolhub_integration_id"].(string)
		if collisionOwner != target.IntegrationID {
			return false, errors.New("Sub-Store 中已存在同名订阅，但它属于其他同步组；请更换订阅名称")
		}
	}
	if mode == core.SubStoreSyncModeIncremental {
		existingContent, _ := owned["content"].(string)
		// Once ownership metadata exists, remove nodes that this target managed in
		// the previous sync but are no longer selected. A legacy group without the
		// metadata is different: its existing nodes have unknown provenance, so the
		// first incremental sync must preserve all of them and only start tracking
		// the nodes written by this run.
		var previouslyManaged []string
		if _, metadataPresent := owned["qcontrolhub_managed_nodes"]; metadataPresent {
			previouslyManaged = subStoreManagedNames(owned["qcontrolhub_managed_nodes"])
		}
		merged, mergeErr := mergeSubStoreContentByName(existingContent, content, previouslyManaged)
		if mergeErr != nil {
			return false, mergeErr
		}
		payload["content"] = merged
	}
	payload["qcontrolhub_managed_nodes"] = currentNames
	// Preserve fields configured directly in Sub-Store while replacing only
	// values owned by QControlHub.
	payload = subStoreSubscriptionUpdate(owned, payload)
	_, err = s.subStoreRequest(ctx, settings.EndpointURL, http.MethodPatch, "/api/sub/"+existingName, payload, nil)
	return false, err
}

func subStoreNodeNames(content string) ([]string, error) {
	result := make([]string, 0)
	seen := make(map[string]struct{})
	for _, line := range strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		name := subStoreNodeName(line)
		if name == "" {
			return nil, errors.New("Sub-Store 同步要求每个节点配置都包含名称")
		}
		if _, exists := seen[name]; exists {
			return nil, fmt.Errorf("Sub-Store 同步清单存在重名节点 %q，请先修改同步名称", name)
		}
		seen[name] = struct{}{}
		result = append(result, name)
	}
	return result, nil
}

func subStoreNodeName(rawValue string) string {
	rawValue = strings.TrimSpace(rawValue)
	if _, ok := subStoreSurgeConfig(rawValue); ok {
		name, _, _ := strings.Cut(rawValue, "=")
		return strings.TrimSpace(name)
	}
	if _, nameNode, ok := subStoreMihomoNode(rawValue); ok {
		return strings.TrimSpace(nameNode.Value)
	}
	fragment := strings.LastIndexByte(rawValue, '#')
	if fragment < 0 || fragment == len(rawValue)-1 {
		return ""
	}
	name := strings.TrimSpace(rawValue[fragment+1:])
	if decoded, err := url.PathUnescape(name); err == nil {
		name = strings.TrimSpace(decoded)
	}
	return name
}

func subStoreManagedNames(value any) []string {
	result := make([]string, 0)
	switch names := value.(type) {
	case []any:
		for _, item := range names {
			if name, ok := item.(string); ok && strings.TrimSpace(name) != "" {
				result = append(result, strings.TrimSpace(name))
			}
		}
	case []string:
		for _, name := range names {
			if strings.TrimSpace(name) != "" {
				result = append(result, strings.TrimSpace(name))
			}
		}
	}
	return result
}

func mergeSubStoreContentByName(existingContent, desiredContent string, previouslyManaged []string) (string, error) {
	desiredNames, err := subStoreNodeNames(desiredContent)
	if err != nil {
		return "", err
	}
	desiredLines := make(map[string]string, len(desiredNames))
	for _, line := range strings.Split(strings.ReplaceAll(desiredContent, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			desiredLines[subStoreNodeName(line)] = line
		}
	}
	managed := make(map[string]struct{}, len(previouslyManaged))
	for _, name := range previouslyManaged {
		managed[name] = struct{}{}
	}
	used := make(map[string]struct{}, len(desiredNames))
	merged := make([]string, 0)
	for _, line := range strings.Split(strings.ReplaceAll(existingContent, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		name := subStoreNodeName(line)
		if replacement, exists := desiredLines[name]; exists {
			if _, alreadyUsed := used[name]; !alreadyUsed {
				merged = append(merged, replacement)
				used[name] = struct{}{}
			}
			continue
		}
		if _, remove := managed[name]; remove {
			continue
		}
		merged = append(merged, line)
	}
	for _, name := range desiredNames {
		if _, exists := used[name]; !exists {
			merged = append(merged, desiredLines[name])
		}
	}
	return strings.Join(merged, "\n"), nil
}
