package tools

// exportInputSchema makes optional object arguments explicit. Some model
// gateways turn an omitted required field into JSON null when converting tool
// schemas, even though omission and [] have the same JSON Schema meaning.
// Only schema locations are visited: null defaults, enums and examples are data
// and must not be rewritten. Source schemas (including shared MCP/plugin maps)
// are not mutated.
func exportInputSchema(schema map[string]any) map[string]any {
	if schema == nil {
		return nil
	}
	result := make(map[string]any, len(schema)+1)
	for key, value := range schema {
		result[key] = value
	}
	_, hasProperties := schema["properties"]
	if objectSchemaType(schema["type"]) || hasProperties {
		if required, exists := schema["required"]; !exists || emptySchemaArray(required) {
			result["required"] = []string{}
		}
	}
	for _, key := range []string{"properties", "patternProperties", "$defs", "definitions", "dependentSchemas"} {
		if schemas, ok := schema[key].(map[string]any); ok {
			next := make(map[string]any, len(schemas))
			for name, child := range schemas {
				next[name] = exportSubschema(child)
			}
			result[key] = next
		}
	}
	for _, key := range []string{"items", "additionalItems", "additionalProperties", "unevaluatedProperties", "unevaluatedItems", "contains", "propertyNames", "not", "if", "then", "else", "allOf", "anyOf", "oneOf", "prefixItems"} {
		if child, exists := schema[key]; exists {
			result[key] = exportSubschema(child)
		}
	}
	// Draft-07 dependencies can be either a subschema or a property-name array.
	if dependencies, ok := schema["dependencies"].(map[string]any); ok {
		next := make(map[string]any, len(dependencies))
		for name, value := range dependencies {
			next[name] = exportSubschema(value)
		}
		result["dependencies"] = next
	}
	return result
}

func exportSubschema(value any) any {
	switch value := value.(type) {
	case map[string]any:
		return exportInputSchema(value)
	case []any:
		if value == nil {
			return value
		}
		result := make([]any, len(value))
		for index, child := range value {
			result[index] = exportSubschema(child)
		}
		return result
	case []map[string]any:
		if value == nil {
			return value
		}
		result := make([]map[string]any, len(value))
		for index, child := range value {
			result[index] = exportInputSchema(child)
		}
		return result
	default:
		return value
	}
}

func emptySchemaArray(value any) bool {
	switch value := value.(type) {
	case nil:
		return true
	case []string:
		return len(value) == 0
	case []any:
		return len(value) == 0
	default:
		return false
	}
}

func objectSchemaType(value any) bool {
	switch value := value.(type) {
	case string:
		return value == "object"
	case []string:
		for _, kind := range value {
			if kind == "object" {
				return true
			}
		}
	case []any:
		for _, kind := range value {
			if kind == "object" {
				return true
			}
		}
	}
	return false
}
