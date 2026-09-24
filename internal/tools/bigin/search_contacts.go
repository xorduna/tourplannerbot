package bigin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strings"

	"tourplannerbot/internal/tools"
)

const (
	searchContactsToolName  = "search_bigin_contacts"
	maximumContactQuerySize = 200
)

var supportedContactSearchTypes = map[string]bool{
	"id":    true,
	"word":  true,
	"email": true,
	"phone": true,
}

// SearchContactsTool retrieves Bigin contacts by exact record ID or searches
// them by a general word, email address, or phone number.
type SearchContactsTool struct {
	client *Client
}

// NewSearchContacts creates the Bigin contact lookup tool.
func NewSearchContacts(client *Client) (*SearchContactsTool, error) {
	if client == nil {
		return nil, fmt.Errorf("Bigin client is required")
	}
	return &SearchContactsTool{client: client}, nil
}

// Definition describes the search_bigin_contacts function to the LLM.
func (searchContactsTool *SearchContactsTool) Definition() tools.Definition {
	return tools.Definition{
		Name:        searchContactsToolName,
		Description: "Retrieve Zoho Bigin contacts, including their email and phone fields. Search by exact contact ID whenever it is available; otherwise search by name or general text, email address, or phone number.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"search_by": map[string]any{
					"type":        "string",
					"enum":        []string{"id", "word", "email", "phone"},
					"description": "Use id for an exact Bigin contact record ID, word for a name or general text, email for an email address, or phone for a telephone number.",
				},
				"query": map[string]any{
					"type":        "string",
					"description": "The contact ID, name or general text, email address, or phone number to search for.",
				},
			},
			"required":             []string{"search_by", "query"},
			"additionalProperties": false,
		},
		Strict: true,
		Source: "bigin",
	}
}

// Execute validates the lookup mode, calls the appropriate Bigin endpoint, and
// returns the complete JSON envelope so standard and custom contact fields stay
// available to the model.
func (searchContactsTool *SearchContactsTool) Execute(ctx context.Context, rawArguments json.RawMessage) (string, error) {
	arguments := struct {
		SearchBy string `json:"search_by"`
		Query    string `json:"query"`
	}{}
	decoder := json.NewDecoder(bytes.NewReader(rawArguments))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&arguments); err != nil {
		return "", fmt.Errorf("decode %s arguments: %w", searchContactsToolName, err)
	}
	var trailingValue any
	if err := decoder.Decode(&trailingValue); err != io.EOF {
		if err == nil {
			return "", fmt.Errorf("decode %s arguments: multiple JSON values are not allowed", searchContactsToolName)
		}
		return "", fmt.Errorf("decode %s arguments: %w", searchContactsToolName, err)
	}

	searchBy := strings.ToLower(strings.TrimSpace(arguments.SearchBy))
	if !supportedContactSearchTypes[searchBy] {
		return "", fmt.Errorf("search_by must be one of id, word, email, or phone")
	}
	query := strings.TrimSpace(arguments.Query)
	if query == "" {
		return "", fmt.Errorf("query must not be empty")
	}
	if len([]rune(query)) > maximumContactQuerySize {
		return "", fmt.Errorf("query must not exceed %d characters", maximumContactQuerySize)
	}

	apiPath := ""
	if searchBy == "id" {
		if !biginRecordIDPattern.MatchString(query) {
			return "", fmt.Errorf("query must contain only digits when search_by is id")
		}
		apiPath = "/bigin/v2/Contacts/" + query
	} else {
		queryParameters := url.Values{}
		queryParameters.Set(searchBy, query)
		apiPath = "/bigin/v2/Contacts/search?" + queryParameters.Encode()
	}

	responseBody, err := searchContactsTool.client.get(ctx, apiPath)
	if err != nil {
		return "", fmt.Errorf("search Bigin contacts: %w", err)
	}
	return string(responseBody), nil
}
