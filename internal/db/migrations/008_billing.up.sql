-- 008_billing: Stripe billing state, personal workspace flag, project locking

-- Stripe billing columns on orgs
ALTER TABLE orgs ADD COLUMN IF NOT EXISTS stripe_customer_id         VARCHAR(255);
ALTER TABLE orgs ADD COLUMN IF NOT EXISTS stripe_subscription_id     VARCHAR(255);
ALTER TABLE orgs ADD COLUMN IF NOT EXISTS stripe_subscription_status VARCHAR(50) NOT NULL DEFAULT 'none';

-- Distinguish auto-created personal workspaces from team orgs
ALTER TABLE orgs ADD COLUMN IF NOT EXISTS is_personal BOOLEAN NOT NULL DEFAULT FALSE;

-- Project lock state for downgrade handling (locked_at IS NOT NULL = read-only)
ALTER TABLE projects ADD COLUMN IF NOT EXISTS locked_at TIMESTAMPTZ;
