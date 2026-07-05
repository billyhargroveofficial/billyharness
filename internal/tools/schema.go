package tools

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"sort"
	"strings"
)

func validateArgs(schema json.RawMessage, args json.RawMessage) error {
	_, err := validateArgsWithMode(schema, args, schemaValidationNativeStrict)
	return err
}

func validateExternalMCPArgs(schema json.RawMessage, args json.RawMessage) (schemaValidationReport, error) {
	return validateArgsWithMode(schema, args, schemaValidationExternalMCP)
}

func externalMCPSchemaReport(schema json.RawMessage) schemaValidationReport {
	report := schemaValidationReport{Mode: schemaValidationExternalMCP.String()}
	if len(schema) == 0 {
		return report
	}
	var root schemaNode
	if err := json.Unmarshal(schema, &root); err != nil {
		report.ParseError = truncate(err.Error(), 240)
		return report
	}
	report.UnsupportedKeywords = unsupportedSchemaKeywords(root)
	return report
}

type schemaValidationMode int

const (
	schemaValidationNativeStrict schemaValidationMode = iota
	schemaValidationExternalMCP
)

func (m schemaValidationMode) String() string {
	switch m {
	case schemaValidationExternalMCP:
		return "external_mcp_json_schema_subset"
	default:
		return "native_strict_subset"
	}
}

func (m schemaValidationMode) failOnUnsupportedKeywords() bool {
	return m == schemaValidationNativeStrict
}

type schemaValidationReport struct {
	Mode                string
	UnsupportedKeywords []string
	ParseError          string
}

func validateArgsWithMode(schema json.RawMessage, args json.RawMessage, mode schemaValidationMode) (schemaValidationReport, error) {
	report := schemaValidationReport{Mode: mode.String()}
	if len(schema) == 0 {
		return report, nil
	}
	if len(args) == 0 || string(args) == "null" {
		args = json.RawMessage(`{}`)
	}
	var root schemaNode
	if err := json.Unmarshal(schema, &root); err != nil {
		report.ParseError = truncate(err.Error(), 240)
		return report, fmt.Errorf("invalid tool schema: %w", err)
	}
	report.UnsupportedKeywords = unsupportedSchemaKeywords(root)
	var value any
	dec := json.NewDecoder(strings.NewReader(string(args)))
	dec.UseNumber()
	if err := dec.Decode(&value); err != nil {
		return report, fmt.Errorf("invalid JSON args: %w", err)
	}
	var trailing any
	if err := dec.Decode(&trailing); err != io.EOF {
		return report, fmt.Errorf("invalid JSON args: trailing data")
	}
	if err := validateValue("$", root, value, mode); err != nil {
		return report, err
	}
	return report, nil
}

type schemaNode struct {
	Type                 any
	Properties           map[string]schemaNode
	Required             []string
	AdditionalProperties any
	Items                *schemaNode
	Enum                 []any
	MinItems             *int
	MaxItems             *int
	AnyOf                []schemaNode
	UnsupportedKeywords  []string
}

func (s *schemaNode) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	var parsed struct {
		Type                 any                   `json:"type"`
		Properties           map[string]schemaNode `json:"properties"`
		Required             []string              `json:"required"`
		AdditionalProperties any                   `json:"additionalProperties"`
		Items                *schemaNode           `json:"items"`
		Enum                 []any                 `json:"enum"`
		MinItems             *int                  `json:"minItems"`
		MaxItems             *int                  `json:"maxItems"`
		AnyOf                []schemaNode          `json:"anyOf"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return err
	}
	*s = schemaNode{
		Type:                 parsed.Type,
		Properties:           parsed.Properties,
		Required:             parsed.Required,
		AdditionalProperties: parsed.AdditionalProperties,
		Items:                parsed.Items,
		Enum:                 parsed.Enum,
		MinItems:             parsed.MinItems,
		MaxItems:             parsed.MaxItems,
		AnyOf:                parsed.AnyOf,
	}
	for key := range raw {
		if !supportedSchemaKeyword(key) && !schemaAnnotationKeyword(key) {
			s.UnsupportedKeywords = append(s.UnsupportedKeywords, key)
		}
	}
	sort.Strings(s.UnsupportedKeywords)
	return nil
}

func validateValue(path string, schema schemaNode, value any, mode schemaValidationMode) error {
	if mode.failOnUnsupportedKeywords() && len(schema.UnsupportedKeywords) > 0 {
		return fmt.Errorf("%s uses unsupported JSON Schema keyword %q", path, schema.UnsupportedKeywords[0])
	}
	if len(schema.AnyOf) > 0 {
		var failures []string
		for _, option := range schema.AnyOf {
			if err := validateValue(path, option, value, mode); err == nil {
				failures = nil
				break
			} else {
				failures = append(failures, err.Error())
			}
		}
		if len(failures) > 0 {
			return fmt.Errorf("%s must match at least one anyOf schema: %s", path, strings.Join(failures, "; "))
		}
	}
	if len(schema.Enum) > 0 && !enumContains(schema.Enum, value) {
		return fmt.Errorf("%s must be one of %s", path, enumValues(schema.Enum))
	}
	types := schemaTypes(schema.Type)
	for _, typ := range types {
		if !supportedSchemaType(typ) {
			return fmt.Errorf("%s uses unsupported JSON Schema type %q", path, typ)
		}
	}
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
				if err := validateValue(path+"."+key, child, childValue, mode); err != nil {
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
				if err := validateValue(fmt.Sprintf("%s[%d]", path, i), *schema.Items, item, mode); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func unsupportedSchemaKeywords(schema schemaNode) []string {
	seen := map[string]struct{}{}
	var walk func(schemaNode)
	walk = func(node schemaNode) {
		for _, keyword := range node.UnsupportedKeywords {
			if keyword != "" {
				seen[keyword] = struct{}{}
			}
		}
		for _, child := range node.Properties {
			walk(child)
		}
		if node.Items != nil {
			walk(*node.Items)
		}
		for _, child := range node.AnyOf {
			walk(child)
		}
	}
	walk(schema)
	out := make([]string, 0, len(seen))
	for keyword := range seen {
		out = append(out, keyword)
	}
	sort.Strings(out)
	return out
}

func supportedSchemaKeyword(key string) bool {
	switch key {
	case "type", "properties", "required", "additionalProperties", "items", "enum", "minItems", "maxItems", "anyOf":
		return true
	default:
		return false
	}
}

func schemaAnnotationKeyword(key string) bool {
	switch key {
	case "$id", "$schema", "default", "deprecated", "description", "examples", "title":
		return true
	default:
		return false
	}
}

func supportedSchemaType(typ string) bool {
	switch typ {
	case "object", "array", "string", "boolean", "integer", "number", "null":
		return true
	default:
		return false
	}
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
	if hasSchemaType(schema, "object") {
		_, ok := value.(map[string]any)
		return ok
	}
	if len(schema.Properties) > 0 || len(schema.Required) > 0 {
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
	if hasSchemaType(schema, "array") {
		_, ok := value.([]any)
		return ok
	}
	return schema.Items != nil || schema.MinItems != nil || schema.MaxItems != nil
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
