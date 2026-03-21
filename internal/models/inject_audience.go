package models

import (
	"slices"
	"testing"
)

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

func TestInjectAudienceRule_AgentTags(t *testing.T) {
	t.Parallel()

	rule := InjectAudienceRule{AgentTags: []string{"backend", "go"}}

	t.Run("match when session agent has overlapping tag", func(t *testing.T) {
		t.Parallel()
		ctx := []string{"go", "senior"}
		if !rule.matches("agent-1", "", "", "", ctx, nil) {
			t.Error("should match when session agent has overlapping tag")
		}
	})

	t.Run("no match when no tag overlap", func(t *testing.T) {
		t.Parallel()
		ctx := []string{"frontend", "typescript"}
		if rule.matches("agent-1", "", "", "", ctx, nil) {
			t.Error("should not match when no tag overlap")
		}
	})

	t.Run("no match when session agent has no tags", func(t *testing.T) {
		t.Parallel()
		ctx := []string{}
		if rule.matches("agent-1", "", "", "", ctx, nil) {
			t.Error("should not match when session agent has no tags")
		}
	})

	t.Run("rule with no agent_tags constraint always matches", func(t *testing.T) {
		t.Parallel()
		emptyRule := InjectAudienceRule{}
		ctx := []string{}
		if !emptyRule.matches("agent-1", "", "", "", ctx, nil) {
			t.Error("rule with no agent_tags constraint should always match (unless other constraints fail)")
		}
	})

	t.Run("agent_tags combined with agent_type", func(t *testing.T) {
		t.Parallel()
		combinedRule := InjectAudienceRule{
			AgentTypes: []string{"CODER"},
			AgentTags:  []string{"backend"},
		}
		match := combinedRule.matches("agent-1", "CODER", "", "", []string{"backend", "go"}, nil)
		if !match {
			t.Error("should match when both agent_type and agent_tags align")
		}
		noMatch := combinedRule.matches("agent-1", "QA", "", "", []string{"backend"}, nil)
		if noMatch {
			t.Error("should not match when agent_type doesn't match even with correct tags")
		}
	})
}

func TestAnyOverlap(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		a, b     []string
		expected bool
	}{
		{"both empty", []string{}, []string{}, false},
		{"a empty", []string{}, []string{"x"}, false},
		{"b empty", []string{"x"}, []string{}, false},
		{"overlap", []string{"a", "b"}, []string{"b", "c"}, true},
		{"no overlap", []string{"a"}, []string{"b"}, false},
		{"single match", []string{"x"}, []string{"x"}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := AnyOverlap(tt.a, tt.b)
			if got != tt.expected {
				t.Errorf("AnyOverlap(%v, %v) = %v, want %v", tt.a, tt.b, got, tt.expected)
			}
		})
	}
}
