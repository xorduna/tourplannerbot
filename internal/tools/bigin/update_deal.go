package bigin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"regexp"
	"sort"
	"strings"

	"tourplannerbot/internal/tools"
)

const updateDealToolName = "update_bigin_deal"

var biginFieldPathSegmentPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*$`)

// UpdateDealConfig identifies the Bigin custom field that contains the JSON
// metadata document managed through metadata.* update paths.
type UpdateDealConfig struct {
	MetadataFieldAPIName string
}

// UpdateDealTool applies selected Bigin field updates while preserving all
// unmentioned fields and deep-merging metadata.* paths into formatted JSON.
type UpdateDealTool struct {
	client               *Client
	metadataFieldAPIName string
}

// NewUpdateDeal creates the standard Bigin deal update tool.
func NewUpdateDeal(client *Client, configuration UpdateDealConfig) (*UpdateDealTool, error) {
	if client == nil {
		return nil, fmt.Errorf("Bigin client is required")
	}
	metadataFieldAPIName := strings.TrimSpace(configuration.MetadataFieldAPIName)
	if !biginFieldPathSegmentPattern.MatchString(metadataFieldAPIName) {
		return nil, fmt.Errorf("Bigin metadata field API name is invalid")
	}
	return &UpdateDealTool{client: client, metadataFieldAPIName: metadataFieldAPIName}, nil
}

// Definition describes update_bigin_deal to the LLM.
func (updateDealTool *UpdateDealTool) Definition() tools.Definition {
	return tools.Definition{
		Name:        updateDealToolName,
		Description: "Update only selected fields on a Zoho Bigin deal. Each update uses a Bigin field API name such as Amount or Payment_Link. To change the JSON metadata field, use a nested path such as metadata.monei_payment_id; existing metadata keys are preserved and stored as formatted JSON. Use this write tool only when Diana explicitly asks to update the deal.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"deal_id": map[string]any{
					"type":        "string",
					"description": "The numeric Zoho Bigin pipeline record ID.",
				},
				"updates": map[string]any{
					"type": "array",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"path": map[string]any{
								"type":        "string",
								"description": "A top-level Bigin field API name, or metadata.<key>[.<nested_key>...].",
							},
							"value": map[string]any{
								"type":        []string{"string", "number", "boolean", "null"},
								"description": "The new scalar value for this field or metadata path.",
							},
						},
						"required":             []string{"path", "value"},
						"additionalProperties": false,
					},
					"minItems":    1,
					"description": "Every listed path is updated; all other deal fields remain unchanged.",
				},
			},
			"required":             []string{"deal_id", "updates"},
			"additionalProperties": false,
		},
		Strict: true,
		Source: "bigin",
	}
}

// Execute fetches the deal, applies selected normal and metadata updates, and
// sends a Bigin update containing only the changed top-level fields.
func (updateDealTool *UpdateDealTool) Execute(ctx context.Context, rawArguments json.RawMessage) (string, error) {
	arguments := struct {
		DealID  string `json:"deal_id"`
		Updates []struct {
			Path  string          `json:"path"`
			Value json.RawMessage `json:"value"`
		} `json:"updates"`
	}{}
	decoder := json.NewDecoder(bytes.NewReader(rawArguments))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&arguments); err != nil {
		return "", fmt.Errorf("decode %s arguments: %w", updateDealToolName, err)
	}
	var trailingValue any
	if err := decoder.Decode(&trailingValue); err != io.EOF {
		if err == nil {
			return "", fmt.Errorf("decode %s arguments: multiple JSON values are not allowed", updateDealToolName)
		}
		return "", fmt.Errorf("decode %s arguments: %w", updateDealToolName, err)
	}

	dealID := strings.TrimSpace(arguments.DealID)
	if !biginRecordIDPattern.MatchString(dealID) {
		return "", fmt.Errorf("deal_id must contain only digits")
	}
	if len(arguments.Updates) == 0 {
		return "", fmt.Errorf("updates must contain at least one field")
	}

	updateRecord := make(map[string]any, len(arguments.Updates))
	metadataUpdates := make([]metadataUpdate, 0, len(arguments.Updates))
	expectedUpdates := make(map[string]any, len(arguments.Updates))
	seenPaths := make(map[string]bool, len(arguments.Updates))
	for _, update := range arguments.Updates {
		path, value, err := validateDealUpdate(update.Path, update.Value, updateDealTool.metadataFieldAPIName)
		if err != nil {
			return "", err
		}
		if seenPaths[path] {
			return "", fmt.Errorf("updates must not contain the same path more than once: %s", path)
		}
		seenPaths[path] = true
		expectedUpdates[path] = value
		if strings.HasPrefix(path, "metadata.") {
			metadataUpdates = append(metadataUpdates, metadataUpdate{path: strings.Split(path, ".")[1:], value: value})
			continue
		}
		updateRecord[path] = value
	}
	if len(metadataUpdates) > 0 {
		dealResponse, err := updateDealTool.client.GetDeal(ctx, dealID)
		if err != nil {
			return "", fmt.Errorf("retrieve Bigin deal before metadata update: %w", err)
		}
		metadata, err := extractMetadata(dealResponse, updateDealTool.metadataFieldAPIName)
		if err != nil {
			return "", err
		}
		for _, update := range metadataUpdates {
			if err := mergeMetadataValue(metadata, update.path, update.value); err != nil {
				return "", err
			}
		}
		formattedMetadata, err := json.MarshalIndent(metadata, "", "  ")
		if err != nil {
			return "", fmt.Errorf("format Bigin metadata: %w", err)
		}
		updateRecord[updateDealTool.metadataFieldAPIName] = string(formattedMetadata)
	}

	requestBody := struct {
		Data []map[string]any `json:"data"`
	}{Data: []map[string]any{updateRecord}}
	responseBody, err := updateDealTool.client.putJSON(ctx, "/bigin/v2/Pipelines/"+dealID, requestBody)
	if err != nil {
		return "", fmt.Errorf("update Bigin deal: %w", err)
	}
	verifiedDealResponse, err := updateDealTool.client.GetDeal(ctx, dealID)
	if err != nil {
		return "", fmt.Errorf("retrieve Bigin deal after update: %w", err)
	}
	if err := verifyDealUpdates(verifiedDealResponse, expectedUpdates, updateDealTool.metadataFieldAPIName); err != nil {
		return "", err
	}
	encodedResult, err := json.Marshal(struct {
		BiginResponse   json.RawMessage `json:"bigin_response"`
		VerifiedUpdates []string        `json:"verified_updates"`
	}{
		BiginResponse:   responseBody,
		VerifiedUpdates: sortedUpdatePaths(expectedUpdates),
	})
	if err != nil {
		return "", fmt.Errorf("encode verified Bigin update result: %w", err)
	}
	return string(encodedResult), nil
}

// metadataUpdate contains a relative metadata object path and its scalar value.
type metadataUpdate struct {
	path  []string
	value any
}

// validateDealUpdate validates a Bigin top-level field or metadata.* path and a scalar JSON value.
func validateDealUpdate(rawPath string, rawValue json.RawMessage, metadataFieldAPIName string) (string, any, error) {
	path := strings.TrimSpace(rawPath)
	if len(rawValue) == 0 || !json.Valid(rawValue) {
		return "", nil, fmt.Errorf("update value for %q must be valid JSON", path)
	}
	var value any
	if err := json.Unmarshal(rawValue, &value); err != nil {
		return "", nil, fmt.Errorf("decode update value for %q: %w", path, err)
	}
	switch value.(type) {
	case string, float64, bool, nil:
	default:
		return "", nil, fmt.Errorf("update value for %q must be a string, number, boolean, or null", path)
	}

	pathSegments := strings.Split(path, ".")
	if pathSegments[0] == "metadata" {
		if len(pathSegments) < 2 {
			return "", nil, fmt.Errorf("metadata updates must name a key, for example metadata.monei_payment_id")
		}
		for _, pathSegment := range pathSegments[1:] {
			if !biginFieldPathSegmentPattern.MatchString(pathSegment) {
				return "", nil, fmt.Errorf("metadata path %q is invalid", path)
			}
		}
		return path, value, nil
	}
	if len(pathSegments) != 1 || !biginFieldPathSegmentPattern.MatchString(path) {
		return "", nil, fmt.Errorf("update path %q must be a Bigin field API name or metadata.<key>", path)
	}
	if path == metadataFieldAPIName {
		return "", nil, fmt.Errorf("update metadata through metadata.<key>, not %s", metadataFieldAPIName)
	}
	return path, value, nil
}

// mergeMetadataValue deep-merges a scalar value at a validated metadata path.
func mergeMetadataValue(metadata map[string]any, path []string, value any) error {
	currentObject := metadata
	for _, pathSegment := range path[:len(path)-1] {
		nestedValue, found := currentObject[pathSegment]
		if !found || nestedValue == nil {
			nestedObject := make(map[string]any)
			currentObject[pathSegment] = nestedObject
			currentObject = nestedObject
			continue
		}
		nestedObject, isObject := nestedValue.(map[string]any)
		if !isObject {
			return fmt.Errorf("metadata path %s cannot be merged because %s is not a JSON object", strings.Join(path, "."), pathSegment)
		}
		currentObject = nestedObject
	}
	currentObject[path[len(path)-1]] = value
	return nil
}

// extractMetadata parses the configured Bigin metadata field as a JSON object.
func extractMetadata(dealResponse json.RawMessage, metadataFieldAPIName string) (map[string]any, error) {
	var responseEnvelope struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(dealResponse, &responseEnvelope); err != nil {
		return nil, fmt.Errorf("decode Bigin deal metadata: %w", err)
	}
	if len(responseEnvelope.Data) == 0 {
		return nil, fmt.Errorf("Bigin deal was not found")
	}
	rawMetadata, found := responseEnvelope.Data[0][metadataFieldAPIName]
	if !found || rawMetadata == nil {
		return map[string]any{}, nil
	}
	metadataText, isString := rawMetadata.(string)
	if !isString {
		return nil, fmt.Errorf("Bigin metadata field %q must be a JSON string", metadataFieldAPIName)
	}
	if strings.TrimSpace(metadataText) == "" {
		return map[string]any{}, nil
	}
	metadata := make(map[string]any)
	if err := json.Unmarshal([]byte(metadataText), &metadata); err != nil {
		return nil, fmt.Errorf("Bigin metadata field %q must contain a JSON object: %w", metadataFieldAPIName, err)
	}
	return metadata, nil
}

// verifyDealUpdates confirms a subsequent Bigin read contains every requested
// top-level field and metadata path, preventing an ignored update from looking
// like a success solely because the PUT endpoint returned HTTP 2xx.
func verifyDealUpdates(dealResponse json.RawMessage, expectedUpdates map[string]any, metadataFieldAPIName string) error {
	var responseEnvelope struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(dealResponse, &responseEnvelope); err != nil {
		return fmt.Errorf("decode Bigin deal after update: %w", err)
	}
	if len(responseEnvelope.Data) == 0 {
		return fmt.Errorf("Bigin deal was not found after update")
	}
	updatedDeal := responseEnvelope.Data[0]
	updatedMetadata := make(map[string]any)
	for path := range expectedUpdates {
		if strings.HasPrefix(path, "metadata.") {
			var err error
			updatedMetadata, err = extractMetadata(dealResponse, metadataFieldAPIName)
			if err != nil {
				return err
			}
			break
		}
	}
	failedPaths := make([]string, 0)
	for path, expectedValue := range expectedUpdates {
		var actualValue any
		var found bool
		if strings.HasPrefix(path, "metadata.") {
			actualValue, found = metadataValue(updatedMetadata, strings.Split(path, ".")[1:])
		} else {
			actualValue, found = updatedDeal[path]
		}
		if !found || !reflect.DeepEqual(actualValue, expectedValue) {
			failedPaths = append(failedPaths, path)
		}
	}
	if len(failedPaths) > 0 {
		sort.Strings(failedPaths)
		return fmt.Errorf("Bigin did not persist the requested update paths: %s; verify the field API names and edit permissions", strings.Join(failedPaths, ", "))
	}
	return nil
}

// metadataValue returns a scalar value at one validated metadata path.
func metadataValue(metadata map[string]any, path []string) (any, bool) {
	currentValue := any(metadata)
	for _, pathSegment := range path {
		currentObject, isObject := currentValue.(map[string]any)
		if !isObject {
			return nil, false
		}
		var found bool
		currentValue, found = currentObject[pathSegment]
		if !found {
			return nil, false
		}
	}
	return currentValue, true
}

// sortedUpdatePaths returns deterministic verified-path output for the model.
func sortedUpdatePaths(updates map[string]any) []string {
	paths := make([]string, 0, len(updates))
	for path := range updates {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths
}
