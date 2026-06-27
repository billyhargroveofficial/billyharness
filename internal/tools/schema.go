package tools

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"strings"
)

func validateArgs(schema json.RawMessage, args json.RawMessage) error {
	if len(schema) == 0 {
		return nil
	}
	if len(args) == 0 || string(args) == "null" {
		args = json.RawMessage(`{}`)
	}
	var root schemaNode
	if err := json.Unmarshal(schema, &root); err != nil {
		return fmt.Errorf("invalid tool schema: %w", err)
	}
	var value any
	dec := json.NewDecoder(strings.NewReader(string(args)))
	dec.UseNumber()
	if err := dec.Decode(&value); err != nil {
		return fmt.Errorf("invalid JSON args: %w", err)
	}
	var trailing any
	if err := dec.Decode(&trailing); err != io.EOF {
		return fmt.Errorf("invalid JSON args: trailing data")
	}
	if err := validateValue("$", root, value); err != nil {
		return err
	}
	return nil
}

type schemaNode struct {
	Type                 any                   `json:"type"`
	Properties           map[string]schemaNode `json:"properties"`
	Required             []string              `json:"required"`
	AdditionalProperties any                   `json:"additionalProperties"`
	Items                *schemaNode           `json:"items"`
	Enum                 []any                 `json:"enum"`
	MinItems             *int                  `json:"minItems"`
	MaxItems             *int                  `json:"maxItems"`
}

func validateValue(path string, schema schemaNode, value any) error {
	if len(schema.Enum) > 0 && !enumContains(schema.Enum, value) {
		return fmt.Errorf("%s must be one of %s", path, enumValues(schema.Enum))
	}
	types := schemaTypes(schema.Type)
	if len(types) > 0 && !matchesAnyType(types, value) {
		return fmt.Errorf("%s must be %s", path, strings.Join(types, " or "))
	}
	if shouldValidateObject(schema, value) {
		obj, ok := value.(map[string]any)
		if !ok {
			return fmt.Errorf("%s must be object", path)
		}
		for _, required := range schema.Required {
			if _, ok := obj[required]; !ok {
				return fmt.Errorf("%s missing required property %q", path, required)
			}
		}
		if additionalPropertiesFalse(schema.AdditionalProperties) {
			for key := range obj {
				if _, ok := schema.Properties[key]; !ok {
					return fmt.Errorf("%s has unknown property %q", path, key)
				}
			}
		}
		for key, child := range schema.Properties {
			if childValue, ok := obj[key]; ok {
				if err := validateValue(path+"."+key, child, childValue); err != nil {
					return err
				}
			}
		}
	}
	if shouldValidateArray(schema, value) {
		items, ok := value.([]any)
		if !ok {
			return fmt.Errorf("%s must be array", path)
		}
		if schema.MinItems != nil && len(items) < *schema.MinItems {
			return fmt.Errorf("%s must contain at least %d items", path, *schema.MinItems)
		}
		if schema.MaxItems != nil && len(items) > *schema.MaxItems {
			return fmt.Errorf("%s must contain at most %d items", path, *schema.MaxItems)
		}
		if schema.Items != nil {
			for i, item := range items {
				if err := validateValue(fmt.Sprintf("%s[%d]", path, i), *schema.Items, item); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func schemaTypes(raw any) []string {
	switch value := raw.(type) {
	case string:
		if value == "" {
			return nil
		}
		return []string{value}
	case []any:
		var out []string
		for _, item := range value {
			if text, ok := item.(string); ok && text != "" {
				out = append(out, text)
			}
		}
		return out
	default:
		return nil
	}
}

func shouldValidateObject(schema schemaNode, value any) bool {
	if hasSchemaType(schema, "object") || len(schema.Properties) > 0 || len(schema.Required) > 0 {
		return true
	}
	_, ok := value.(map[string]any)
	return ok && additionalPropertiesFalse(schema.AdditionalProperties)
}

func additionalPropertiesFalse(value any) bool {
	boolValue, ok := value.(bool)
	return ok && !boolValue
}

func shouldValidateArray(schema schemaNode, value any) bool {
	return hasSchemaType(schema, "array") || schema.Items != nil || schema.MinItems != nil || schema.MaxItems != nil
}

func hasSchemaType(schema schemaNode, typ string) bool {
	for _, candidate := range schemaTypes(schema.Type) {
		if candidate == typ {
			return true
		}
	}
	return false
}

func matchesAnyType(types []string, value any) bool {
	for _, typ := range types {
		if matchesType(typ, value) {
			return true
		}
	}
	return false
}

func matchesType(typ string, value any) bool {
	switch typ {
	case "object":
		_, ok := value.(map[string]any)
		return ok
	case "array":
		_, ok := value.([]any)
		return ok
	case "string":
		_, ok := value.(string)
		return ok
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "integer":
		return isInteger(value)
	case "number":
		return isNumber(value)
	case "null":
		return value == nil
	default:
		return true
	}
}

func isInteger(value any) bool {
	switch n := value.(type) {
	case json.Number:
		if _, err := n.Int64(); err == nil {
			return true
		}
		f, err := n.Float64()
		return err == nil && math.Trunc(f) == f
	case float64:
		return math.Trunc(n) == n
	default:
		return false
	}
}

func isNumber(value any) bool {
	switch value.(type) {
	case json.Number, float64:
		return true
	default:
		return false
	}
}

func enumContains(enum []any, value any) bool {
	for _, candidate := range enum {
		if fmt.Sprint(candidate) == fmt.Sprint(value) {
			return true
		}
	}
	return false
}

func enumValues(enum []any) string {
	values := make([]string, 0, len(enum))
	for _, value := range enum {
		values = append(values, fmt.Sprint(value))
	}
	return strings.Join(values, ", ")
}
