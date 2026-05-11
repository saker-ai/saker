package aigo

import (
	"github.com/godeps/aigo/tooldef"

	"github.com/cinience/saker/pkg/tool"
)

// convertSchema converts a tooldef.Schema to a tool.JSONSchema.
func convertSchema(s tooldef.Schema) *tool.JSONSchema {
	js := &tool.JSONSchema{
		Type:     s.Type,
		Required: s.Required,
	}

	if len(s.Enum) > 0 {
		enums := make([]interface{}, len(s.Enum))
		for i, v := range s.Enum {
			enums[i] = v
		}
		js.Enum = enums
	}

	if s.Items != nil {
		js.Items = convertSchema(*s.Items)
	}

	if len(s.Properties) > 0 {
		props := make(map[string]interface{}, len(s.Properties))
		for k, v := range s.Properties {
			props[k] = schemaToMap(v)
		}
		js.Properties = props
	}

	return js
}

// schemaToMap converts a tooldef.Schema to a map for embedding in JSONSchema.Properties.
func schemaToMap(s tooldef.Schema) map[string]interface{} {
	m := map[string]interface{}{
		"type": s.Type,
	}
	if s.Description != "" {
		m["description"] = s.Description
	}
	if len(s.Enum) > 0 {
		enums := make([]interface{}, len(s.Enum))
		for i, v := range s.Enum {
			enums[i] = v
		}
		m["enum"] = enums
	}
	if s.Default != "" {
		m["default"] = s.Default
	}
	if len(s.Properties) > 0 {
		props := make(map[string]interface{}, len(s.Properties))
		for k, v := range s.Properties {
			props[k] = schemaToMap(v)
		}
		m["properties"] = props
	}
	if len(s.Required) > 0 {
		m["required"] = s.Required
	}
	if s.Items != nil {
		m["items"] = schemaToMap(*s.Items)
	}
	return m
}
