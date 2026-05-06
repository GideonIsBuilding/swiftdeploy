package internal

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// OPADecision is the structured response from every OPA policy query.
// OPA never returns a bare boolean — every decision carries reasoning.
type OPADecision struct {
	Allow      bool     `json:"allow"`
	Violations []string `json:"violations"`
	Contact    string   `json:"contact"`
	CheckedAt  string   `json:"checked_at"`
}

// OPAResult wraps the raw OPA API response envelope.
type OPAResult struct {
	Result OPADecision `json:"result"`
}

// QueryOPA sends input to a named OPA policy rule and returns the decision.
// policy is the OPA path e.g. "swiftdeploy/infrastructure/decision"
func QueryOPA(opaPort int, policy string, input map[string]any) (*OPADecision, error) {
	// Attach timestamp so policies can log when they were checked
	input["checked_at"] = time.Now().UTC().Format(time.RFC3339)

	body, err := json.Marshal(map[string]any{"input": input})
	if err != nil {
		return nil, fmt.Errorf("marshaling OPA input: %w", err)
	}

	url := fmt.Sprintf("http://localhost:%d/v1/data/%s", opaPort, policy)

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		// Distinct failure mode: OPA is not reachable at all
		return nil, fmt.Errorf("OPA unreachable at %s — is the stack running? (%w)", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		// Distinct failure mode: OPA is up but policy path is wrong
		return nil, fmt.Errorf("OPA policy not found at path %q — check policies/ directory", policy)
	}

	if resp.StatusCode != http.StatusOK {
		// Distinct failure mode: OPA returned an unexpected error
		return nil, fmt.Errorf("OPA returned unexpected status %d for policy %q", resp.StatusCode, policy)
	}

	var result OPAResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding OPA response: %w", err)
	}

	return &result.Result, nil
}

// PrintDecision surfaces the OPA decision clearly to the operator.
func PrintDecision(policyName string, d *OPADecision) {
	if d.Allow {
		fmt.Printf("  ✅ ALLOW  [%s] — no violations\n", policyName)
		return
	}
	fmt.Printf("  ❌ DENY   [%s]\n", policyName)
	for _, v := range d.Violations {
		fmt.Printf("     → %s\n", v)
	}
	if d.Contact != "" {
		fmt.Printf("     contact: %s\n", d.Contact)
	}
}
