package api

import "context"

type contextKey string

const (
	ctxOrgID     contextKey = "org_id"
	ctxProjectID contextKey = "project_id"
	ctxKeyID     contextKey = "key_id"
)

// AuthClaims holds authenticated identity info stored in context.
type AuthClaims struct {
	OrgID            string
	ProjectID        string // resolved project scope (may be empty if scope_all_projects)
	KeyID            string
	ScopeAllProjects bool
	AllowedProjects  []string
}

type claimsKey struct{}

func withClaims(ctx context.Context, c AuthClaims) context.Context {
	return context.WithValue(ctx, claimsKey{}, c)
}

func ClaimsFrom(ctx context.Context) (AuthClaims, bool) {
	c, ok := ctx.Value(claimsKey{}).(AuthClaims)
	return c, ok
}
