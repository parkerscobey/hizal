package api

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"

	"github.com/XferOps/winnow/internal/billing"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stripe/stripe-go/v78"
	checkoutsession "github.com/stripe/stripe-go/v78/checkout/session"
	portalsession "github.com/stripe/stripe-go/v78/billingportal/session"
	stripewebhook "github.com/stripe/stripe-go/v78/webhook"
)

type BillingHandlers struct {
	pool *pgxpool.Pool
}

func NewBillingHandlers(pool *pgxpool.Pool) *BillingHandlers {
	return &BillingHandlers{pool: pool}
}

// POST /v1/billing/checkout
// Creates a Stripe Checkout session for Pro upgrade. Returns {url}.
func (h *BillingHandlers) CreateCheckout(w http.ResponseWriter, r *http.Request) {
	user, ok := JWTUserFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "AUTH_REQUIRED", "not authenticated")
		return
	}

	// Resolve the user's personal org
	var orgID, tier string
	var stripeCustomerID *string
	err := h.pool.QueryRow(r.Context(), `
		SELECT o.id, o.tier, o.stripe_customer_id
		FROM orgs o
		JOIN org_memberships om ON om.org_id = o.id
		WHERE om.user_id = $1 AND o.is_personal = TRUE
		LIMIT 1
	`, user.ID).Scan(&orgID, &tier, &stripeCustomerID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "DB_ERROR", err.Error())
		return
	}

	if tier == "pro" || tier == "team" || tier == "enterprise" {
		writeError(w, http.StatusBadRequest, "ALREADY_SUBSCRIBED", "already on a paid tier")
		return
	}

	stripe.Key = os.Getenv("STRIPE_SECRET_KEY")
	priceID := os.Getenv("STRIPE_PRO_PRICE_ID")
	successURL := os.Getenv("STRIPE_SUCCESS_URL")
	cancelURL := os.Getenv("STRIPE_CANCEL_URL")

	params := &stripe.CheckoutSessionParams{
		Mode: stripe.String(string(stripe.CheckoutSessionModeSubscription)),
		LineItems: []*stripe.CheckoutSessionLineItemParams{
			{Price: stripe.String(priceID), Quantity: stripe.Int64(1)},
		},
		SuccessURL: stripe.String(successURL),
		CancelURL:  stripe.String(cancelURL),
		Metadata:   map[string]string{"org_id": orgID},
	}
	// Reuse existing Stripe customer to avoid duplicates
	if stripeCustomerID != nil && *stripeCustomerID != "" {
		params.Customer = stripeCustomerID
	}

	sess, err := checkoutsession.New(params)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "STRIPE_ERROR", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"url": sess.URL})
}

// POST /v1/billing/portal
// Creates a Stripe Customer Portal session. Returns {url}.
func (h *BillingHandlers) CreatePortal(w http.ResponseWriter, r *http.Request) {
	user, ok := JWTUserFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "AUTH_REQUIRED", "not authenticated")
		return
	}

	var stripeCustomerID *string
	err := h.pool.QueryRow(r.Context(), `
		SELECT o.stripe_customer_id
		FROM orgs o
		JOIN org_memberships om ON om.org_id = o.id
		WHERE om.user_id = $1 AND o.is_personal = TRUE
		LIMIT 1
	`, user.ID).Scan(&stripeCustomerID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "DB_ERROR", err.Error())
		return
	}
	if stripeCustomerID == nil || *stripeCustomerID == "" {
		writeError(w, http.StatusBadRequest, "NO_SUBSCRIPTION", "no billing account found")
		return
	}

	stripe.Key = os.Getenv("STRIPE_SECRET_KEY")
	returnURL := os.Getenv("STRIPE_CANCEL_URL") // reuse cancel URL as portal return

	params := &stripe.BillingPortalSessionParams{
		Customer:  stripeCustomerID,
		ReturnURL: stripe.String(returnURL),
	}
	sess, err := portalsession.New(params)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "STRIPE_ERROR", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"url": sess.URL})
}

// POST /v1/webhooks/stripe
// Receives and processes Stripe webhook events. Verified by signature.
func (h *BillingHandlers) HandleWebhook(w http.ResponseWriter, r *http.Request) {
	const maxBodyBytes = int64(65536)
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "READ_ERROR", err.Error())
		return
	}

	webhookSecret := os.Getenv("STRIPE_WEBHOOK_SECRET")
	event, err := stripewebhook.ConstructEvent(body, r.Header.Get("Stripe-Signature"), webhookSecret)
	if err != nil {
		log.Printf("billing: webhook signature verification failed: %v", err)
		writeError(w, http.StatusBadRequest, "INVALID_SIGNATURE", "webhook signature verification failed")
		return
	}

	switch event.Type {
	case "checkout.session.completed":
		h.handleCheckoutCompleted(r, event)
	case "customer.subscription.updated":
		h.handleSubscriptionUpdated(r, event)
	case "customer.subscription.deleted":
		h.handleSubscriptionDeleted(r, event)
	case "invoice.payment_failed":
		h.handlePaymentFailed(r, event)
	default:
		// Unknown events are silently acknowledged
	}

	w.WriteHeader(http.StatusOK)
}

func (h *BillingHandlers) handleCheckoutCompleted(r *http.Request, event stripe.Event) {
	var sess stripe.CheckoutSession
	if err := json.Unmarshal(event.Data.Raw, &sess); err != nil {
		log.Printf("billing: failed to parse checkout.session.completed: %v", err)
		return
	}
	orgID, ok := sess.Metadata["org_id"]
	if !ok || orgID == "" {
		log.Printf("billing: checkout.session.completed missing org_id in metadata")
		return
	}
	subID := ""
	if sess.Subscription != nil {
		subID = sess.Subscription.ID
	}
	customerID := ""
	if sess.Customer != nil {
		customerID = sess.Customer.ID
	}
	_, err := h.pool.Exec(r.Context(), `
		UPDATE orgs SET
			tier = 'pro',
			stripe_customer_id = $1,
			stripe_subscription_id = $2,
			stripe_subscription_status = 'active',
			updated_at = NOW()
		WHERE id = $3
	`, customerID, subID, orgID)
	if err != nil {
		log.Printf("billing: failed to upgrade org %s: %v", orgID, err)
	}
}

func (h *BillingHandlers) handleSubscriptionUpdated(r *http.Request, event stripe.Event) {
	var sub stripe.Subscription
	if err := json.Unmarshal(event.Data.Raw, &sub); err != nil {
		log.Printf("billing: failed to parse customer.subscription.updated: %v", err)
		return
	}
	_, err := h.pool.Exec(r.Context(), `
		UPDATE orgs SET stripe_subscription_status = $1, updated_at = NOW()
		WHERE stripe_subscription_id = $2
	`, string(sub.Status), sub.ID)
	if err != nil {
		log.Printf("billing: failed to sync subscription status for sub %s: %v", sub.ID, err)
	}
}

func (h *BillingHandlers) handleSubscriptionDeleted(r *http.Request, event stripe.Event) {
	var sub stripe.Subscription
	if err := json.Unmarshal(event.Data.Raw, &sub); err != nil {
		log.Printf("billing: failed to parse customer.subscription.deleted: %v", err)
		return
	}

	tx, err := h.pool.Begin(r.Context())
	if err != nil {
		log.Printf("billing: failed to begin transaction for subscription deletion: %v", err)
		return
	}
	defer tx.Rollback(r.Context())

	// Downgrade org to free
	var orgID string
	err = tx.QueryRow(r.Context(), `
		UPDATE orgs SET
			tier = 'free',
			stripe_subscription_status = 'cancelled',
			updated_at = NOW()
		WHERE stripe_subscription_id = $1
		RETURNING id
	`, sub.ID).Scan(&orgID)
	if err != nil {
		log.Printf("billing: failed to downgrade org for sub %s: %v", sub.ID, err)
		return
	}

	// Lock all projects — idempotent (only locks currently unlocked ones)
	_, err = tx.Exec(r.Context(), `
		UPDATE projects SET locked_at = NOW()
		WHERE org_id = $1 AND locked_at IS NULL
	`, orgID)
	if err != nil {
		log.Printf("billing: failed to lock projects for org %s: %v", orgID, err)
		return
	}

	if err := tx.Commit(r.Context()); err != nil {
		log.Printf("billing: failed to commit downgrade transaction for org %s: %v", orgID, err)
	}
}

func (h *BillingHandlers) handlePaymentFailed(r *http.Request, event stripe.Event) {
	var inv stripe.Invoice
	if err := json.Unmarshal(event.Data.Raw, &inv); err != nil {
		log.Printf("billing: failed to parse invoice.payment_failed: %v", err)
		return
	}
	subID := ""
	if inv.Subscription != nil {
		subID = inv.Subscription.ID
	}
	if subID == "" {
		return
	}
	_, err := h.pool.Exec(r.Context(), `
		UPDATE orgs SET stripe_subscription_status = 'past_due', updated_at = NOW()
		WHERE stripe_subscription_id = $1
	`, subID)
	if err != nil {
		log.Printf("billing: failed to mark past_due for sub %s: %v", subID, err)
	}
}

// POST /v1/billing/downgrade-choice
// Called when a downgraded user chooses what to do with their locked projects.
func (h *BillingHandlers) DowngradeChoice(w http.ResponseWriter, r *http.Request) {
	user, ok := JWTUserFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "AUTH_REQUIRED", "not authenticated")
		return
	}

	var body struct {
		Action    string  `json:"action"`     // "keep_one" | "start_fresh"
		ProjectID *string `json:"project_id"` // required when action="keep_one"
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_BODY", err.Error())
		return
	}

	// Resolve personal org
	var orgID string
	err := h.pool.QueryRow(r.Context(), `
		SELECT o.id FROM orgs o
		JOIN org_memberships om ON om.org_id = o.id
		WHERE om.user_id = $1 AND o.is_personal = TRUE
		LIMIT 1
	`, user.ID).Scan(&orgID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "DB_ERROR", err.Error())
		return
	}

	switch body.Action {
	case "keep_one":
		if body.ProjectID == nil || *body.ProjectID == "" {
			writeError(w, http.StatusBadRequest, "MISSING_PROJECT_ID", "project_id required for keep_one")
			return
		}
		// Unlock chosen project; all others remain locked
		_, err = h.pool.Exec(r.Context(), `
			UPDATE projects SET locked_at = NULL
			WHERE id = $1 AND org_id = $2
		`, *body.ProjectID, orgID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "DB_ERROR", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"project_id": *body.ProjectID})

	case "start_fresh":
		// Create a new empty project; locked projects remain locked
		var newProjectID string
		err = h.pool.QueryRow(r.Context(), `
			INSERT INTO projects (org_id, name, slug)
			VALUES ($1, 'My Project', 'my-project')
			ON CONFLICT (org_id, slug) DO UPDATE SET name = EXCLUDED.name
			RETURNING id
		`, orgID).Scan(&newProjectID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "DB_ERROR", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"project_id": newProjectID})

	default:
		writeError(w, http.StatusBadRequest, "INVALID_ACTION", "action must be keep_one or start_fresh")
	}
}

// GET /v1/orgs/:id/usage
// Returns current tier, resource counts, limits, and warn flag.
func UsageHandler(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		orgID := chi.URLParam(r, "id")
		if _, err := requireOrgRole(r, pool, orgID, "owner", "admin", "member", "viewer"); err != nil {
			writeError(w, http.StatusForbidden, "FORBIDDEN", err.Error())
			return
		}

		var tier, subStatus string
		err := pool.QueryRow(r.Context(), `
			SELECT tier, stripe_subscription_status FROM orgs WHERE id = $1
		`, orgID).Scan(&tier, &subStatus)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "DB_ERROR", err.Error())
			return
		}

		var projectCount int
		pool.QueryRow(r.Context(), `SELECT COUNT(*) FROM projects WHERE org_id = $1`, orgID).Scan(&projectCount)

		var chunkCount int
		pool.QueryRow(r.Context(), `
			SELECT COUNT(*) FROM context_chunks cc
			JOIN projects p ON p.id = cc.project_id
			WHERE p.org_id = $1
		`, orgID).Scan(&chunkCount)

		limits := billing.For(tier)

		chunkWarn := false
		if limits.ChunkWarn >= 0 && chunkCount >= limits.ChunkWarn {
			chunkWarn = true
		}

		writeJSON(w, http.StatusOK, map[string]interface{}{
			"tier":                       tier,
			"stripe_subscription_status": subStatus,
			"chunk_count":                chunkCount,
			"chunk_limit":                limits.ChunkLimit,
			"chunk_warn":                 chunkWarn,
			"project_count":              projectCount,
			"project_limit":              limits.ProjectLimit,
		})
	}
}
