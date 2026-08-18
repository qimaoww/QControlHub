package webui

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/store"
)

// agentsPageBranch carries the per-engine view data shared by the agents and
// the client-access pages: latest deployments, drift state, client profiles
// and line-level config diffs.
type agentsPageBranch struct {
	EnrollmentTokens   []core.EnrollmentToken
	Deployments        map[string]core.Deployment
	DeploymentDetails  map[string]deploymentDetail
	DeploymentStatuses map[string]deploymentStatus
	ClientAccess       map[string]clientAccessData
	ConfigDiffs        map[string]string
}

// loadAgentsBranch fetches the deployment and enrollment data for the agents
// and client-access pages. The independent store queries run concurrently.
func (s *Server) loadAgentsBranch(ctx context.Context, includeEnrollment bool, agents []core.Agent, archiveConfigs []core.Config) (agentsPageBranch, error) {
	branch := agentsPageBranch{
		Deployments:        make(map[string]core.Deployment),
		DeploymentDetails:  make(map[string]deploymentDetail),
		DeploymentStatuses: make(map[string]deploymentStatus),
		ClientAccess:       make(map[string]clientAccessData),
		ConfigDiffs:        make(map[string]string),
	}
	var (
		enrollmentTokensErr error
		latest              []core.Deployment
		latestErr           error
		agentConfigs        []core.Config
		agentConfigErr      error
	)
	var branchWg sync.WaitGroup
	if includeEnrollment {
		branchWg.Add(3)
		go func() {
			defer branchWg.Done()
			branch.EnrollmentTokens, enrollmentTokensErr = s.store.ListEnrollmentTokens(ctx)
		}()
	} else {
		branchWg.Add(2)
	}
	go func() {
		defer branchWg.Done()
		latest, latestErr = s.store.LatestDeployments(ctx)
	}()
	go func() {
		defer branchWg.Done()
		agentConfigs, agentConfigErr = s.store.ListAgentConfigs(ctx)
	}()
	branchWg.Wait()
	for _, err := range []error{enrollmentTokensErr, latestErr, agentConfigErr} {
		if err != nil {
			return branch, err
		}
	}

	configsByID := make(map[string]core.Config, len(archiveConfigs)+len(agentConfigs))
	agentsByID := make(map[string]core.Agent, len(agents))
	for _, agent := range agents {
		agentsByID[agent.ID] = agent
	}
	for _, config := range archiveConfigs {
		configsByID[config.ID] = config
	}
	for _, config := range agentConfigs {
		configsByID[config.ID] = config
	}
	deployedContents := make(map[string]string)
	for _, deployment := range latest {
		key := deploymentKey(deployment.AgentID, deployment.Engine)
		branch.Deployments[key] = deployment
		deployedConfig, exists := configsByID[deployment.ConfigID]
		if !exists {
			continue
		}
		if deployedConfig.Version != deployment.ConfigVersion {
			revision, revisionErr := s.store.ConfigRevision(ctx, deployment.ConfigID, deployment.ConfigVersion)
			if errors.Is(revisionErr, store.ErrNotFound) {
				continue
			}
			if revisionErr != nil {
				return branch, revisionErr
			}
			deployedConfig = revision
		}
		deployedContents[key] = deployedConfig.Content
		if detail, parsed := deploymentDetailFor(deployment.Engine, deployedConfig.Content); parsed {
			branch.DeploymentDetails[key] = detail
		}
		if agent, exists := agentsByID[deployment.AgentID]; exists {
			if access := s.clientAccessFor(agent, deployment.Engine, deployedConfig.Content); len(access.Profiles) > 0 {
				branch.ClientAccess[key] = access
			}
		}
	}
	for _, config := range agentConfigs {
		key := deploymentKey(config.AgentID, config.Engine)
		branch.DeploymentStatuses[key] = deploymentStatusFor(config, branch.Deployments[key])
		if !branch.DeploymentStatuses[key].Drift {
			continue
		}
		deployedContent, deployedOK := deployedContents[key]
		if !deployedOK || deployedContent == config.Content {
			continue
		}
		if diff := renderConfigDiff(config.Content, deployedContent); diff != "" {
			branch.ConfigDiffs[key] = diff
		}
	}
	return branch, nil
}

// buildClientAccessPage derives the /client-access page data from the shared
// agents branch, honoring the agent/engine/query filters in the request.
func buildClientAccessPage(request *http.Request, agents []core.Agent, branch agentsPageBranch) *clientAccessPageData {
	var engine core.Engine
	if requestedEngine := strings.TrimSpace(request.URL.Query().Get("engine")); requestedEngine != "" {
		parsedEngine, parseErr := core.ParseEngine(requestedEngine)
		if parseErr == nil {
			engine = parsedEngine
		}
	}
	clientPage := clientAccessPageFor(
		agents,
		branch.ClientAccess,
		request.URL.Query().Get("agent_id"),
		engine,
		request.URL.Query().Get("q"),
	)
	return &clientPage
}

// loadSelectedMetricHistory loads the last 24 hours of metric samples for the
// selected node so the inspector can render its traffic chart.
func (s *Server) loadSelectedMetricHistory(ctx context.Context, agentID string) ([]core.MetricSample, error) {
	if agentID == "" {
		return nil, nil
	}
	return s.store.MetricSamples(ctx, agentID, time.Now().Add(-24*time.Hour), 288)
}
