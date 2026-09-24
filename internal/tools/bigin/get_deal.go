package bigin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"

	"tourplannerbot/internal/tools"
)

const getDealToolName = "get_bigin_deal"

var biginRecordIDPattern = regexp.MustCompile(`^[0-9]+$`)

// GetDealTool fetches one Bigin pipeline record by its numeric record ID.
type GetDealTool struct {
	client *Client
}

type dealEnvelope struct {
	Data []struct {
		Name string `json:"Deal_Name"`
	} `json:"data"`
}

// NewGetDeal creates the read-only Bigin deal lookup tool.
func NewGetDeal(client *Client) (*GetDealTool, error) {
	if client == nil {
		return nil, fmt.Errorf("Bigin client is required")
	}
	return &GetDealTool{client: client}, nil
}

// GetDealName retrieves the current deal from Bigin and returns its name.
// Callers should not cache any other deal data locally because Bigin remains
// the source of truth.
func (client *Client) GetDealName(applicationContext context.Context, dealID string) (string, error) {
	responseBody, err := client.GetDeal(applicationContext, dealID)
	if err != nil {
		return "", err
	}

	var responseEnvelope dealEnvelope
	if err := json.Unmarshal(responseBody, &responseEnvelope); err != nil {
		return "", fmt.Errorf("decode Bigin deal: %w", err)
	}
	if len(responseEnvelope.Data) == 0 {
		return "", fmt.Errorf("Bigin deal %s was not found", dealID)
	}
	dealName := strings.TrimSpace(responseEnvelope.Data[0].Name)
	if dealName == "" {
		return "", fmt.Errorf("Bigin deal %s has no name", dealID)
	}
	return dealName, nil
}

// GetDeal retrieves the complete current JSON envelope for one Bigin deal.
// Consumers may use it as request context but must not persist it as deal data.
func (client *Client) GetDeal(applicationContext context.Context, dealID string) (json.RawMessage, error) {
	responseBody, err := client.getDealRecord(applicationContext, dealID)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(responseBody), nil
}

// Definition describes the get_bigin_deal function to the LLM.
func (getDealTool *GetDealTool) Definition() tools.Definition {
	return tools.Definition{
		Name:        getDealToolName,
		Description: "Retrieve the complete current details of one Zoho Bigin deal (pipeline record) from its record ID. Use this whenever the user supplies a Bigin deal ID or asks to inspect or discuss that deal.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"deal_id": map[string]any{
					"type":        "string",
					"description": "The numeric Zoho Bigin pipeline record ID supplied by the user.",
				},
			},
			"required":             []string{"deal_id"},
			"additionalProperties": false,
		},
		Strict: true,
		Source: "bigin",
	}
}

// Execute validates the model arguments and returns the untouched JSON record
// envelope from Bigin so all current and custom fields remain available.
func (getDealTool *GetDealTool) Execute(ctx context.Context, rawArguments json.RawMessage) (string, error) {
	arguments := struct {
		DealID string `json:"deal_id"`
	}{}
	decoder := json.NewDecoder(bytes.NewReader(rawArguments))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&arguments); err != nil {
		return "", fmt.Errorf("decode %s arguments: %w", getDealToolName, err)
	}
	var trailingValue any
	if err := decoder.Decode(&trailingValue); err != io.EOF {
		if err == nil {
			return "", fmt.Errorf("decode %s arguments: multiple JSON values are not allowed", getDealToolName)
		}
		return "", fmt.Errorf("decode %s arguments: %w", getDealToolName, err)
	}

	responseBody, err := getDealTool.client.GetDeal(ctx, arguments.DealID)
	if err != nil {
		return "", err
	}
	return string(responseBody), nil
}

// getDealRecord validates a Bigin record ID and retrieves its untouched JSON
// envelope for both the native tool and topic association endpoint.
func (client *Client) getDealRecord(applicationContext context.Context, rawDealID string) ([]byte, error) {
	dealID := strings.TrimSpace(rawDealID)
	if !biginRecordIDPattern.MatchString(dealID) {
		return nil, fmt.Errorf("deal_id must contain only digits")
	}
	responseBody, err := client.get(applicationContext, "/bigin/v2/Pipelines/"+dealID)
	if err != nil {
		return nil, fmt.Errorf("retrieve Bigin deal: %w", err)
	}
	return responseBody, nil
}
