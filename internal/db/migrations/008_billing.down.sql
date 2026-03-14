ALTER TABLE projects DROP COLUMN IF EXISTS locked_at;
ALTER TABLE orgs DROP COLUMN IF EXISTS is_personal;
ALTER TABLE orgs DROP COLUMN IF EXISTS stripe_subscription_status;
ALTER TABLE orgs DROP COLUMN IF EXISTS stripe_subscription_id;
ALTER TABLE orgs DROP COLUMN IF EXISTS stripe_customer_id;
