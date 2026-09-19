package llm

import (
	"encoding/json"
	"fmt"
)

// normalizeFunctionParameters clones a provider-independent tool schema and
// converts its root to the subset accepted by OpenAI function tools.
func normalizeFunctionParameters(parameters map[string]any) (map[string]any, error) {
	serializedParameters, err := json.Marshal(parameters)
	if err != nil {
		return nil, fmt.Errorf("serialize function parameters: %w", err)
	}
	var normalizedParameters map[string]any
	if err := json.Unmarshal(serializedParameters, &normalizedParameters); err != nil {
		return nil, fmt.Errorf("decode function parameters: %w", err)
	}
	if normalizedParameters == nil {
		normalizedParameters = make(map[string]any)
	}
	if err := makeOpenAICompatibleObjectSchema(normalizedParameters); err != nil {
		return nil, err
	}
	return normalizedParameters, nil
}

// makeOpenAICompatibleObjectSchema flattens MCP-style root composition while
// preserving the properties and compatible requirements of every branch.
func makeOpenAICompatibleObjectSchema(schema map[string]any) error {
	if schemaType, hasSchemaType := schema["type"]; hasSchemaType && schemaType != "object" {
		return fmt.Errorf("schema type must be object, got %v", schemaType)
	}
	schema["type"] = "object"

	properties, hasProperties := schema["properties"]
	if !hasProperties {
		properties = map[string]any{}
		schema["properties"] = properties
	}
	objectProperties, propertiesAreObject := properties.(map[string]any)
	if !propertiesAreObject {
		return fmt.Errorf("schema properties must be a JSON object")
	}

	requiredProperties, err := schemaRequiredProperties(schema)
	if err != nil {
		return err
	}
	for _, compositionKeyword := range []string{"allOf", "anyOf", "oneOf"} {
		compositionValue, hasComposition := schema[compositionKeyword]
		if !hasComposition {
			continue
		}
		compositionBranches, branchesAreArray := compositionValue.([]any)
		if !branchesAreArray {
			return fmt.Errorf("schema %s must be a JSON array", compositionKeyword)
		}
		branchRequiredProperties := make([][]string, 0, len(compositionBranches))
		for branchIndex, compositionBranch := range compositionBranches {
			branchSchema, branchIsObject := compositionBranch.(map[string]any)
			if !branchIsObject {
				return fmt.Errorf("schema %s branch %d must be a JSON object", compositionKeyword, branchIndex)
			}
			if branchProperties, branchHasProperties := branchSchema["properties"]; branchHasProperties {
				branchObjectProperties, branchPropertiesAreObject := branchProperties.(map[string]any)
				if !branchPropertiesAreObject {
					return fmt.Errorf("schema %s branch %d properties must be a JSON object", compositionKeyword, branchIndex)
				}
				for propertyName, propertySchema := range branchObjectProperties {
					if _, propertyAlreadyPresent := objectProperties[propertyName]; !propertyAlreadyPresent {
						objectProperties[propertyName] = propertySchema
					}
				}
			}
			branchRequired, requiredError := schemaRequiredProperties(branchSchema)
			if requiredError != nil {
				return fmt.Errorf("schema %s branch %d: %w", compositionKeyword, branchIndex, requiredError)
			}
			branchRequiredProperties = append(branchRequiredProperties, branchRequired)
		}

		if compositionKeyword == "allOf" {
			for _, branchRequired := range branchRequiredProperties {
				requiredProperties = appendUniqueStrings(requiredProperties, branchRequired...)
			}
		} else {
			requiredProperties = appendUniqueStrings(requiredProperties, intersectStringLists(branchRequiredProperties)...)
		}
		delete(schema, compositionKeyword)
	}

	for _, unsupportedKeyword := range []string{"enum", "const", "not"} {
		delete(schema, unsupportedKeyword)
	}
	if len(requiredProperties) == 0 {
		delete(schema, "required")
	} else {
		schema["required"] = requiredProperties
	}
	return nil
}

// schemaRequiredProperties decodes one schema's required list while rejecting
// malformed entries before they can reach the OpenAI API.
func schemaRequiredProperties(schema map[string]any) ([]string, error) {
	requiredValue, hasRequired := schema["required"]
	if !hasRequired {
		return nil, nil
	}
	requiredValues, requiredIsArray := requiredValue.([]any)
	if !requiredIsArray {
		return nil, fmt.Errorf("schema required must be a JSON array")
	}
	requiredProperties := make([]string, 0, len(requiredValues))
	for requiredIndex, requiredEntry := range requiredValues {
		requiredProperty, requiredIsString := requiredEntry.(string)
		if !requiredIsString {
			return nil, fmt.Errorf("schema required entry %d must be a string", requiredIndex)
		}
		requiredProperties = appendUniqueStrings(requiredProperties, requiredProperty)
	}
	return requiredProperties, nil
}

// appendUniqueStrings appends values in input order while suppressing duplicates.
func appendUniqueStrings(existingValues []string, newValues ...string) []string {
	seenValues := make(map[string]struct{}, len(existingValues)+len(newValues))
	for _, existingValue := range existingValues {
		seenValues[existingValue] = struct{}{}
	}
	for _, newValue := range newValues {
		if _, alreadyPresent := seenValues[newValue]; alreadyPresent {
			continue
		}
		existingValues = append(existingValues, newValue)
		seenValues[newValue] = struct{}{}
	}
	return existingValues
}

// intersectStringLists returns values required by every alternative branch.
// Requiring the union would incorrectly make mutually exclusive inputs mandatory.
func intersectStringLists(valueLists [][]string) []string {
	if len(valueLists) == 0 {
		return nil
	}
	intersection := append([]string(nil), valueLists[0]...)
	for _, valueList := range valueLists[1:] {
		valuesInList := make(map[string]struct{}, len(valueList))
		for _, value := range valueList {
			valuesInList[value] = struct{}{}
		}
		filteredIntersection := intersection[:0]
		for _, intersectionValue := range intersection {
			if _, presentInList := valuesInList[intersectionValue]; presentInList {
				filteredIntersection = append(filteredIntersection, intersectionValue)
			}
		}
		intersection = filteredIntersection
	}
	return intersection
}
