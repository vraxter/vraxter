// Copyright (c) 2026 Vraxter. All rights reserved.
//
// This file is part of Vraxter.
// Vraxter is free software licensed under the GNU Affero General Public License (AGPL) v3.0.
// See the LICENSE file in the project root for full license information.
//
// For commercial licensing inquiries, contact Vraxter.

package skills

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/vraxter/vraxter/pkg/types"
)

const maxSchemaDepth = 32

// ValidateSkillInputParams validates runtime tool invocation params against the
// skill's declared io.input.schema (or JSON-object ParamsSchema) before WASM execution.
// Plain non-JSON descriptive strings (legacy human hints) are ignored gracefully;
// formal JSON Schema Draft-07 objects are enforced fail-closed.
func ValidateSkillInputParams(manifest types.SkillManifest, params map[string]interface{}) error {
	schemaObj, ok, err := extractInputJSONSchema(manifest)
	if err != nil {
		return fmt.Errorf("invalid io.input.schema definition: %w", err)
	}
	if !ok {
		return nil
	}

	if params == nil {
		params = map[string]interface{}{}
	}

	// Normalize params via JSON round-trip so numeric/slice types match the exact wire JSON sent to WASM.
	rawParams, err := json.Marshal(params)
	if err != nil {
		return fmt.Errorf("failed marshaling input parameters for schema validation: %w", err)
	}
	var normalized interface{}
	if err := json.Unmarshal(rawParams, &normalized); err != nil {
		return fmt.Errorf("failed decoding normalized input parameters: %w", err)
	}

	return validateJSONSchemaValue("$", normalized, schemaObj, 0)
}

func extractInputJSONSchema(manifest types.SkillManifest) (map[string]interface{}, bool, error) {
	var raw string
	if manifest.ManifestV1 != nil && manifest.ManifestV1.IO != nil && len(manifest.ManifestV1.IO.Input.Schema) > 0 {
		trimmed := strings.TrimSpace(string(manifest.ManifestV1.IO.Input.Schema))
		if trimmed != "" && trimmed != "null" {
			if strings.HasPrefix(trimmed, "\"") {
				var unquoted string
				if err := json.Unmarshal(manifest.ManifestV1.IO.Input.Schema, &unquoted); err == nil {
					raw = strings.TrimSpace(unquoted)
				}
			} else {
				raw = trimmed
			}
		}
	}
	if raw == "" {
		raw = strings.TrimSpace(manifest.ParamsSchema)
	}
	if raw == "" || !strings.HasPrefix(raw, "{") {
		return nil, false, nil
	}

	var obj map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &obj); err != nil {
		return nil, false, err
	}
	if !looksLikeJSONSchemaObject(obj) {
		return nil, false, nil
	}
	return obj, true, nil
}

func looksLikeJSONSchemaObject(obj map[string]interface{}) bool {
	if len(obj) == 0 {
		return false
	}
	schemaKeywords := []string{
		"$schema", "type", "properties", "required", "additionalProperties",
		"enum", "const", "allOf", "anyOf", "oneOf", "not",
		"minProperties", "maxProperties",
	}
	for _, kw := range schemaKeywords {
		if _, ok := obj[kw]; ok {
			return true
		}
	}
	return false
}

func validateJSONSchemaValue(path string, val interface{}, schema map[string]interface{}, depth int) error {
	if depth > maxSchemaDepth {
		return fmt.Errorf("%s: schema nesting depth exceeds maximum (%d)", path, maxSchemaDepth)
	}

	// 1. type
	if rawType, hasType := schema["type"]; hasType {
		if err := validateSchemaType(path, val, rawType); err != nil {
			return err
		}
	}

	// 2. const
	if constVal, hasConst := schema["const"]; hasConst {
		if !jsonDeepEqual(val, constVal) {
			return fmt.Errorf("%s: value %v does not match required const %v", path, val, constVal)
		}
	}

	// 3. enum
	if rawEnum, hasEnum := schema["enum"]; hasEnum {
		enumSlice, ok := rawEnum.([]interface{})
		if !ok {
			return fmt.Errorf("%s: schema 'enum' must be an array", path)
		}
		matched := false
		for _, cand := range enumSlice {
			if jsonDeepEqual(val, cand) {
				matched = true
				break
			}
		}
		if !matched {
			return fmt.Errorf("%s: value %v is not one of allowed enum values %v", path, val, enumSlice)
		}
	}

	// 4. Combinators: allOf, anyOf, oneOf, not
	if rawAllOf, ok := schema["allOf"]; ok {
		subs, err := asSchemaSlice(path, "allOf", rawAllOf)
		if err != nil {
			return err
		}
		for _, sub := range subs {
			if err := validateJSONSchemaValue(path, val, sub, depth+1); err != nil {
				return err
			}
		}
	}
	if rawAnyOf, ok := schema["anyOf"]; ok {
		subs, err := asSchemaSlice(path, "anyOf", rawAnyOf)
		if err != nil {
			return err
		}
		matched := false
		for _, sub := range subs {
			if validateJSONSchemaValue(path, val, sub, depth+1) == nil {
				matched = true
				break
			}
		}
		if !matched {
			return fmt.Errorf("%s: value did not match any schema in anyOf", path)
		}
	}
	if rawOneOf, ok := schema["oneOf"]; ok {
		subs, err := asSchemaSlice(path, "oneOf", rawOneOf)
		if err != nil {
			return err
		}
		matches := 0
		for _, sub := range subs {
			if validateJSONSchemaValue(path, val, sub, depth+1) == nil {
				matches++
			}
		}
		if matches != 1 {
			return fmt.Errorf("%s: value matched %d schemas in oneOf (expected exactly 1)", path, matches)
		}
	}
	if rawNot, ok := schema["not"]; ok {
		notMap, okMap := rawNot.(map[string]interface{})
		if !okMap {
			return fmt.Errorf("%s: schema 'not' must be an object", path)
		}
		if validateJSONSchemaValue(path, val, notMap, depth+1) == nil {
			return fmt.Errorf("%s: value must not match negated 'not' schema", path)
		}
	}

	// 5. Type-specific constraints
	switch typed := val.(type) {
	case map[string]interface{}:
		return validateObjectConstraints(path, typed, schema, depth)
	case []interface{}:
		return validateArrayConstraints(path, typed, schema, depth)
	case string:
		return validateStringConstraints(path, typed, schema)
	case float64:
		return validateNumberConstraints(path, typed, schema)
	}

	return nil
}

func validateSchemaType(path string, val interface{}, rawType interface{}) error {
	var allowed []string
	switch t := rawType.(type) {
	case string:
		allowed = []string{t}
	case []interface{}:
		for _, item := range t {
			s, ok := item.(string)
			if !ok {
				return fmt.Errorf("%s: schema 'type' array must contain strings", path)
			}
			allowed = append(allowed, s)
		}
	default:
		return fmt.Errorf("%s: schema 'type' must be string or string array", path)
	}

	for _, want := range allowed {
		if matchesJSONType(val, want) {
			return nil
		}
	}
	return fmt.Errorf("%s: got type %s, expected %s", path, actualJSONType(val), strings.Join(allowed, "|"))
}

func matchesJSONType(val interface{}, want string) bool {
	switch want {
	case "null":
		return val == nil
	case "boolean":
		_, ok := val.(bool)
		return ok
	case "string":
		_, ok := val.(string)
		return ok
	case "number":
		_, ok := val.(float64)
		return ok
	case "integer":
		f, ok := val.(float64)
		return ok && !math.IsNaN(f) && !math.IsInf(f, 0) && math.Trunc(f) == f
	case "array":
		_, ok := val.([]interface{})
		return ok
	case "object":
		_, ok := val.(map[string]interface{})
		return ok
	default:
		return false
	}
}

func actualJSONType(val interface{}) string {
	switch v := val.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case string:
		return "string"
	case float64:
		if math.Trunc(v) == v {
			return "integer"
		}
		return "number"
	case []interface{}:
		return "array"
	case map[string]interface{}:
		return "object"
	default:
		return fmt.Sprintf("%T", val)
	}
}

func validateObjectConstraints(path string, obj map[string]interface{}, schema map[string]interface{}, depth int) error {
	if minP, ok := asIntConstraint(schema["minProperties"]); ok && len(obj) < minP {
		return fmt.Errorf("%s: object has %d properties, minimum is %d", path, len(obj), minP)
	}
	if maxP, ok := asIntConstraint(schema["maxProperties"]); ok && len(obj) > maxP {
		return fmt.Errorf("%s: object has %d properties, maximum is %d", path, len(obj), maxP)
	}

	if rawReq, ok := schema["required"]; ok {
		reqSlice, isSlice := rawReq.([]interface{})
		if !isSlice {
			return fmt.Errorf("%s: schema 'required' must be an array", path)
		}
		for _, r := range reqSlice {
			reqKey, okStr := r.(string)
			if !okStr {
				return fmt.Errorf("%s: schema 'required' entries must be strings", path)
			}
			if _, exists := obj[reqKey]; !exists {
				return fmt.Errorf("%s: missing required property %q", path, reqKey)
			}
		}
	}

	var props map[string]interface{}
	if rawProps, ok := schema["properties"]; ok {
		pm, isMap := rawProps.(map[string]interface{})
		if !isMap {
			return fmt.Errorf("%s: schema 'properties' must be an object", path)
		}
		props = pm
	}

	for k, v := range obj {
		childPath := path + "." + k
		if props != nil {
			if propSchemaRaw, declared := props[k]; declared {
				propSchema, isMap := propSchemaRaw.(map[string]interface{})
				if !isMap {
					return fmt.Errorf("%s: property schema must be an object", childPath)
				}
				if err := validateJSONSchemaValue(childPath, v, propSchema, depth+1); err != nil {
					return err
				}
				continue
			}
		}
		if rawAdd, hasAdd := schema["additionalProperties"]; hasAdd {
			switch add := rawAdd.(type) {
			case bool:
				if !add {
					return fmt.Errorf("%s: additional property %q is not allowed", path, k)
				}
			case map[string]interface{}:
				if err := validateJSONSchemaValue(childPath, v, add, depth+1); err != nil {
					return err
				}
			}
		}
	}

	return nil
}

func validateArrayConstraints(path string, arr []interface{}, schema map[string]interface{}, depth int) error {
	if minI, ok := asIntConstraint(schema["minItems"]); ok && len(arr) < minI {
		return fmt.Errorf("%s: array has %d items, minimum is %d", path, len(arr), minI)
	}
	if maxI, ok := asIntConstraint(schema["maxItems"]); ok && len(arr) > maxI {
		return fmt.Errorf("%s: array has %d items, maximum is %d", path, len(arr), maxI)
	}
	if uniq, ok := schema["uniqueItems"].(bool); ok && uniq {
		for i := 0; i < len(arr); i++ {
			for j := i + 1; j < len(arr); j++ {
				if jsonDeepEqual(arr[i], arr[j]) {
					return fmt.Errorf("%s: array items at [%d] and [%d] are duplicates (uniqueItems=true)", path, i, j)
				}
			}
		}
	}
	if rawItems, ok := schema["items"]; ok {
		switch items := rawItems.(type) {
		case map[string]interface{}:
			for i, elem := range arr {
				if err := validateJSONSchemaValue(fmt.Sprintf("%s[%d]", path, i), elem, items, depth+1); err != nil {
					return err
				}
			}
		case []interface{}:
			for i, rawSub := range items {
				if i >= len(arr) {
					break
				}
				subMap, isMap := rawSub.(map[string]interface{})
				if !isMap {
					return fmt.Errorf("%s[%d]: tuple item schema must be an object", path, i)
				}
				if err := validateJSONSchemaValue(fmt.Sprintf("%s[%d]", path, i), arr[i], subMap, depth+1); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func validateStringConstraints(path, s string, schema map[string]interface{}) error {
	runeLen := utf8.RuneCountInString(s)
	if minL, ok := asIntConstraint(schema["minLength"]); ok && runeLen < minL {
		return fmt.Errorf("%s: string length %d is shorter than minLength %d", path, runeLen, minL)
	}
	if maxL, ok := asIntConstraint(schema["maxLength"]); ok && runeLen > maxL {
		return fmt.Errorf("%s: string length %d exceeds maxLength %d", path, runeLen, maxL)
	}
	if pat, ok := schema["pattern"].(string); ok && pat != "" {
		re, err := regexp.Compile(pat)
		if err != nil {
			return fmt.Errorf("%s: invalid schema pattern %q: %w", path, pat, err)
		}
		if !re.MatchString(s) {
			return fmt.Errorf("%s: string %q does not match pattern %q", path, s, pat)
		}
	}
	return nil
}

func validateNumberConstraints(path string, n float64, schema map[string]interface{}) error {
	if minV, ok := schema["minimum"].(float64); ok && n < minV {
		return fmt.Errorf("%s: value %v is less than minimum %v", path, n, minV)
	}
	if maxV, ok := schema["maximum"].(float64); ok && n > maxV {
		return fmt.Errorf("%s: value %v exceeds maximum %v", path, n, maxV)
	}
	if exMin, ok := schema["exclusiveMinimum"].(float64); ok && n <= exMin {
		return fmt.Errorf("%s: value %v must be > exclusiveMinimum %v", path, n, exMin)
	}
	if exMax, ok := schema["exclusiveMaximum"].(float64); ok && n >= exMax {
		return fmt.Errorf("%s: value %v must be < exclusiveMaximum %v", path, n, exMax)
	}
	if mult, ok := schema["multipleOf"].(float64); ok && mult > 0 {
		q := n / mult
		if math.Abs(q-math.Round(q)) > 1e-9 {
			return fmt.Errorf("%s: value %v is not a multipleOf %v", path, n, mult)
		}
	}
	return nil
}

func asIntConstraint(v interface{}) (int, bool) {
	f, ok := v.(float64)
	if !ok {
		return 0, false
	}
	return int(f), true
}

func asSchemaSlice(path, keyword string, raw interface{}) ([]map[string]interface{}, error) {
	slice, ok := raw.([]interface{})
	if !ok {
		return nil, fmt.Errorf("%s: schema %q must be an array", path, keyword)
	}
	res := make([]map[string]interface{}, 0, len(slice))
	for _, item := range slice {
		m, isMap := item.(map[string]interface{})
		if !isMap {
			return nil, fmt.Errorf("%s: schema %q entries must be objects", path, keyword)
		}
		res = append(res, m)
	}
	return res, nil
}

func jsonDeepEqual(a, b interface{}) bool {
	return reflect.DeepEqual(a, b)
}
