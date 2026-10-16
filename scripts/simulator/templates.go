package main

import (
	"fmt"
	"strings"
)

// TemplatePreset represents a pre-configured scenario for quick testing.
type TemplatePreset struct {
	ID           string            `json:"id"`
	Name         string            `json:"name"`
	Description  string            `json:"description"`
	URL          string            `json:"url"`
	Method       string            `json:"method"`
	SignProvider string            `json:"sign_provider,omitempty"`
	SignSecret   string            `json:"sign_secret,omitempty"`
	Headers      map[string]string `json:"headers"`
	Body         string            `json:"body"`
}

// GetDefaultTemplates returns the pre-packaged webhook and job simulation templates.
func GetDefaultTemplates(relayBaseURL string) []TemplatePreset {
	baseURL := strings.TrimRight(relayBaseURL, "/")

	return []TemplatePreset{
		{
			ID:           "stripe_webhook",
			Name:         "Stripe Webhook (HMAC-SHA256)",
			Description:  "Simulates Stripe payment_intent.succeeded with dynamic Stripe-Signature header",
			URL:          fmt.Sprintf("%s/api/v1/webhook/stripe-test", baseURL),
			Method:       "POST",
			SignProvider: "stripe",
			SignSecret:   "whsec_test_secret123",
			Headers: map[string]string{
				"Content-Type": "application/json",
			},
			Body: `{
  "id": "evt_test_1234567890",
  "object": "event",
  "api_version": "2023-10-16",
  "created": 1729089000,
  "type": "payment_intent.succeeded",
  "data": {
    "object": {
      "id": "pi_3MtwBwLkdIwHu7ix28a3tqPa",
      "amount": 2000,
      "currency": "usd",
      "status": "succeeded"
    }
  }
}`,
		},
		{
			ID:           "github_webhook",
			Name:         "GitHub Webhook (HMAC-SHA256)",
			Description:  "Simulates GitHub push event with dynamic X-Hub-Signature-256 header",
			URL:          fmt.Sprintf("%s/api/v1/webhook/github-test", baseURL),
			Method:       "POST",
			SignProvider: "github",
			SignSecret:   "github_test_secret123",
			Headers: map[string]string{
				"Content-Type":   "application/json",
				"X-GitHub-Event": "push",
			},
			Body: `{
  "ref": "refs/heads/main",
  "before": "0000000000000000000000000000000000000000",
  "after": "6dcb09b5b57875f334f61aebed695e2e4193db5e",
  "repository": {
    "id": 1296269,
    "name": "simple-relay",
    "full_name": "simple-relay/simple-relay"
  },
  "pusher": {
    "name": "pmlonng",
    "email": "phanminlongg@gmail.com"
  }
}`,
		},
		{
			ID:          "query_param_auth",
			Name:        "Query-Param Authentication",
			Description: "Simulates inbound webhook with token authentication in URL query parameters",
			URL:         fmt.Sprintf("%s/api/v1/webhook/query-auth-test?token=webhook_secret_key_abc123&region=us-east", baseURL),
			Method:      "POST",
			Headers: map[string]string{
				"Content-Type": "application/json",
			},
			Body: `{
  "event": "customer.created",
  "account_id": "acc_998877",
  "status": "active"
}`,
		},
		{
			ID:          "header_auth",
			Name:        "Custom Header Authentication",
			Description: "Simulates inbound webhook authenticated via X-Api-Key header",
			URL:         fmt.Sprintf("%s/api/v1/webhook/header-auth-test", baseURL),
			Method:      "POST",
			Headers: map[string]string{
				"Content-Type": "application/json",
				"X-Api-Key":    "relay_secret_key_xyz987",
			},
			Body: `{
  "event": "order.fulfilled",
  "order_id": "ord_100293",
  "amount_cents": 4500
}`,
		},
		{
			ID:          "async_job",
			Name:        "Async Job (Outbound Fire-and-Forget)",
			Description: "Simulates asynchronous outbound job ingestion with Idempotency-Key",
			URL:         fmt.Sprintf("%s/api/v1/jobs/external-api", baseURL),
			Method:      "POST",
			Headers: map[string]string{
				"Content-Type":    "application/json",
				"Idempotency-Key": "idemp_job_test_001",
			},
			Body: `{
  "action": "send_notification",
  "recipient": "user@example.com",
  "template": "welcome_email"
}`,
		},
	}
}
