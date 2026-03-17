package billing

// TierLimits defines the resource limits for a given subscription tier.
// A limit of -1 means unlimited.
type TierLimits struct {
	ProjectLimit        int
	ChunkLimit          int
	ChunkWarn           int  // threshold at which to show the 80% warning; -1 = no warning
	AllowAgents         bool // agents are allowed on all tiers
	AllowAgentMemory    bool // agent memory (memory_enabled) - Pro+ only
	AllowOrgMemory      bool // org-scoped context - Pro+ only
	OrgMemoryGovernance bool // org memory governance features - Team only
	IsTeam              bool
}

var limits = map[string]TierLimits{
	"free":       {ProjectLimit: 1, ChunkLimit: 1000, ChunkWarn: 800, AllowAgents: true, AllowAgentMemory: false, AllowOrgMemory: false, OrgMemoryGovernance: false},
	"pro":        {ProjectLimit: 5, ChunkLimit: 10000, ChunkWarn: 8000, AllowAgents: true, AllowAgentMemory: true, AllowOrgMemory: true, OrgMemoryGovernance: false},
	"team":       {ProjectLimit: -1, ChunkLimit: -1, ChunkWarn: -1, AllowAgents: true, AllowAgentMemory: true, AllowOrgMemory: true, OrgMemoryGovernance: true, IsTeam: true},
	"enterprise": {ProjectLimit: -1, ChunkLimit: -1, ChunkWarn: -1, AllowAgents: true, AllowAgentMemory: true, AllowOrgMemory: true, OrgMemoryGovernance: true, IsTeam: true},
}

// For returns the TierLimits for the given tier string.
// Unknown tiers fall back to free limits.
func For(tier string) TierLimits {
	if l, ok := limits[tier]; ok {
		return l
	}
	return limits["free"]
}
