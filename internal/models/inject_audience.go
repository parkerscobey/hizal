package models

import "slices"

type InjectAudienceRule struct {
	All            bool     `json:"all,omitempty"`
	AgentTypes     []string `json:"agent_types,omitempty"`
	AgentIDs       []string `json:"agent_ids,omitempty"`
	LifecycleTypes []string `json:"lifecycle_types,omitempty"`
	AgentTags      []string `json:"agent_tags,omitempty"`
	FocusTags      []string `json:"focus_tags,omitempty"`
	OrgIDs         []string `json:"org_ids,omitempty"`
}

type InjectAudience struct {
	Rules []InjectAudienceRule `json:"rules"`
}

func (ia *InjectAudience) MatchesSession(agentID, agentType, lifecycleType, orgID string, agentTags, focusTags []string) bool {
	if ia == nil || len(ia.Rules) == 0 {
		return false
	}
	for _, rule := range ia.Rules {
		if rule.matches(agentID, agentType, lifecycleType, orgID, agentTags, focusTags) {
			return true
		}
	}
	return false
}

func (r InjectAudienceRule) matches(agentID, agentType, lifecycleType, orgID string, agentTags, focusTags []string) bool {
	if r.All {
		return true
	}
	if len(r.AgentIDs) > 0 && !slices.Contains(r.AgentIDs, agentID) {
		return false
	}
	if len(r.AgentTypes) > 0 && !slices.Contains(r.AgentTypes, agentType) {
		return false
	}
	if len(r.LifecycleTypes) > 0 && !slices.Contains(r.LifecycleTypes, lifecycleType) {
		return false
	}
	if len(r.OrgIDs) > 0 && !slices.Contains(r.OrgIDs, orgID) {
		return false
	}
	if len(r.AgentTags) > 0 && !AnyOverlap(r.AgentTags, agentTags) {
		return false
	}
	if len(r.FocusTags) > 0 && !AnyOverlap(r.FocusTags, focusTags) {
		return false
	}
	return true
}

func AnyOverlap(a, b []string) bool {
	for _, x := range a {
		if slices.Contains(b, x) {
			return true
		}
	}
	return false
}

func DefaultInjectAudienceAll() *InjectAudience {
	return &InjectAudience{
		Rules: []InjectAudienceRule{{All: true}},
	}
}

func (ia *InjectAudience) IsInjectable() bool {
	return ia != nil && len(ia.Rules) > 0
}
