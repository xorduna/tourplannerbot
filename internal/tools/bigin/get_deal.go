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

// NewGetDeal creates the read-only Bigin deal lookup tool.
func NewGetDeal(client *Client) (*GetDealTool, error) {
	if client == nil {
		return nil, fmt.Errorf("Bigin client is required")
	}
	return &GetDealTool{client: client}, nil
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

	dealID := strings.TrimSpace(arguments.DealID)
	if !biginRecordIDPattern.MatchString(dealID) {
		return "", fmt.Errorf("deal_id must contain only digits")
	}
	responseBody, err := getDealTool.client.get(ctx, "/bigin/v2/Pipelines/"+dealID)
	if err != nil {
		return "", fmt.Errorf("retrieve Bigin deal: %w", err)
	}
	return string(responseBody), nil
}
