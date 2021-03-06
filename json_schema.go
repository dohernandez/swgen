package swgen

import (
	"strings"

	"github.com/pkg/errors"
)

type schemaBuilder struct {
	refs map[string]int64 // References counter
	g    *Generator
}

// JSONSchemaConfig is optional JSON schema rendering configuration
type JSONSchemaConfig struct {
	CollectDefinitions map[string]map[string]interface{}
	StripDefinitions   bool
	DefinitionsPrefix  string
}

// JSONSchema builds JSON Schema for Swagger Schema object
func (g *Generator) JSONSchema(s SchemaObj, option ...JSONSchemaConfig) (map[string]interface{}, error) {
	var cfg *JSONSchemaConfig
	if len(option) != 0 {
		cfg = &option[0]
	}

	refs := make(map[string]int64)
	sb := &schemaBuilder{
		refs: make(map[string]int64),
		g:    g,
	}
	res, err := sb.jsonSchemaPlain(s, cfg)
	if err != nil {
		return nil, err
	}
	allDef := g.definitions.GenDefinitions()

	definitions := make(map[string]map[string]interface{})

	for len(sb.refs) > 0 {
		for ref, cnt := range sb.refs {
			refs[ref] = refs[ref] + cnt
		}
		sb.refs = make(map[string]int64)

		for ref := range refs {
			ref := strings.TrimPrefix(ref, "#/definitions/")
			if _, ok := definitions[ref]; !ok {
				jsonSchema, err := sb.jsonSchemaPlain(allDef[ref], cfg)
				if err != nil {
					return nil, err
				}
				definitions[ref] = jsonSchema
				if cfg != nil && cfg.CollectDefinitions != nil {
					cfg.CollectDefinitions[ref] = jsonSchema
				}
			}

		}
	}

	// Expanding top-level reference
	if ref, isString := res["$ref"].(string); isString && strings.HasPrefix(ref, "#/definitions/") {
		defName := ref[len("#/definitions/"):]
		def := definitions[defName]
		if def != nil {
			for k, v := range def {
				res[k] = v
			}
			delete(res, "$ref")
			// Delete root definition if only one (root) usage
			if refs[ref] == 1 {
				delete(definitions, defName)
			}
		}
	}

	// Inject definitions into result
	if len(definitions) > 0 {
		if cfg == nil || !cfg.StripDefinitions {
			res["definitions"] = definitions
		}
	}

	return res, nil
}

func (sb *schemaBuilder) jsonSchemaPlain(s SchemaObj, cfg *JSONSchemaConfig) (map[string]interface{}, error) {
	if s.Ref != "" {
		sb.refs[s.Ref] = sb.refs[s.Ref] + 1
		ref := s.Ref
		if cfg != nil && cfg.DefinitionsPrefix != "" {
			ref = cfg.DefinitionsPrefix + strings.TrimPrefix(ref, "#/definitions/")
		}
		return map[string]interface{}{"$ref": ref}, nil
	}
	res, err := jsonRecode(s)
	if err != nil {
		return nil, err
	}

	if s.Nullable && s.Type != "" {
		res["type"] = []interface{}{s.Type, "null"}
	}

	if s.Properties != nil && len(s.Properties) > 0 {
		properties := make(map[string]interface{}, len(s.Properties))
		for name, schema := range s.Properties {
			properties[name], err = sb.jsonSchemaPlain(schema, cfg)
			if err != nil {
				return nil, err
			}
		}
		res["properties"] = properties
	}

	if s.AdditionalProperties != nil {
		jsonSchema, err := sb.jsonSchemaPlain(*s.AdditionalProperties, cfg)
		if err != nil {
			return nil, err
		}
		res["additionalProperties"] = jsonSchema
	}

	if s.Items != nil {
		jsonSchema, err := sb.jsonSchemaPlain(*s.Items, cfg)
		if err != nil {
			return nil, err
		}
		res["items"] = jsonSchema
	}

	return res, nil
}

// ParamJSONSchema builds JSON Schema for Swagger Parameter object
func (g *Generator) ParamJSONSchema(p ParamObj, cfg ...JSONSchemaConfig) (map[string]interface{}, error) {
	if p.Schema != nil {
		return g.JSONSchema(*p.Schema, cfg...)
	}

	p.Name = ""
	p.In = ""
	p.Required = false
	p.CollectionFormat = ""
	if p.Type == "file" {
		p.Type = ""
	}

	res, err := jsonRecode(p)
	return res, err
}

// ObjectJSONSchema is a simplified JSON Schema for object
type ObjectJSONSchema struct {
	ID         string                            `json:"id,omitempty"`
	Schema     string                            `json:"$schema,omitempty"`
	Type       string                            `json:"type"`
	Required   []string                          `json:"required,omitempty"`
	Properties map[string]map[string]interface{} `json:"properties"`
}

// ToMap converts ObjectJSONSchema to a map
func (o ObjectJSONSchema) ToMap() (map[string]interface{}, error) {
	return jsonRecode(o)
}

// GetJSONSchemaRequestBody returns returns request body schema if any
func (g *Generator) GetJSONSchemaRequestBody(op *OperationObj, cfg ...JSONSchemaConfig) (map[string]interface{}, error) {
	for _, param := range op.Parameters {
		if param.In == "body" {
			schema, err := g.ParamJSONSchema(param, cfg...)
			if err != nil {
				return nil, err
			}
			return schema, nil
		}
	}
	return nil, nil
}

// GetJSONSchemaRequestGroups returns a map of object schemas converted from parameters (excluding in body), grouped by in
func (g *Generator) GetJSONSchemaRequestGroups(op *OperationObj, cfg ...JSONSchemaConfig) (map[string]ObjectJSONSchema, error) {
	var err error
	requestSchemas := map[string]ObjectJSONSchema{}

	for _, param := range op.Parameters {
		if param.In == "body" {
			continue
		}
		if _, ok := requestSchemas[param.In]; !ok {
			requestSchemas[param.In] = ObjectJSONSchema{
				Schema:     "http://json-schema.org/draft-04/schema#",
				Type:       "object",
				Required:   []string{},
				Properties: map[string]map[string]interface{}{},
			}
		}

		if param.Required {
			rs := requestSchemas[param.In]
			rs.Required = append(rs.Required, param.Name)
			requestSchemas[param.In] = rs
		}
		requestSchemas[param.In].Properties[param.Name], err = g.ParamJSONSchema(param, cfg...)
		if err != nil {
			return nil, err
		}
	}

	return requestSchemas, nil
}

// WalkJSONSchemaRequestGroups iterates over all request parameters grouped by path, method and in into an instance of JSON Schema
func (g *Generator) WalkJSONSchemaRequestGroups(function func(path, method, in string, schema ObjectJSONSchema)) error {
	for path, pi := range g.paths {
		for method, op := range pi.Map() {
			requestSchemas, err := g.GetJSONSchemaRequestGroups(op)
			if err != nil {
				return errors.Wrapf(err, "failed to get schema request groups schemas for %s %s", method, path)
			}

			for in, schema := range requestSchemas {
				function(path, method, in, schema)
			}
		}
	}
	return nil
}

// WalkJSONSchemaRequestGroups iterates over all request bodies
func (g *Generator) WalkJSONSchemaRequestBodies(function func(path, method string, schema map[string]interface{})) error {
	for path, pi := range g.paths {
		for method, op := range pi.Map() {
			for _, param := range op.Parameters {
				if param.In == "body" {
					schema, err := g.ParamJSONSchema(param)
					if err != nil {
						return err
					}
					function(path, method, schema)
				}
			}
		}
	}
	return nil
}

// WalkJSONSchemaResponses iterates over all responses grouped by path, method and status code into an instance of JSON Schema
func (g *Generator) WalkJSONSchemaResponses(function func(path, method string, statusCode int, schema map[string]interface{})) error {
	for path, pi := range g.paths {
		for method, op := range pi.Map() {
			for statusCode, resp := range op.Responses {
				if resp.Schema == nil {
					continue
				}
				schema, err := g.JSONSchema(*resp.Schema)
				if err != nil {
					return errors.Wrapf(err, "failed to get response schema for %s %s %d", method, path, statusCode)
				}
				schema["$schema"] = "http://json-schema.org/draft-04/schema#"
				function(path, method, statusCode, schema)
			}
		}
	}
	return nil
}
