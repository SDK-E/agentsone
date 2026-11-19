// SPDX-License-Identifier: MIT

// Package contracttest checks HTTP responses against the OpenAPI contract.
// Adapter tests call CheckResponse after serving a request so a handler can
// never drift from contract/dist/openapi.json.
package contracttest

import (
	"bytes"
	"errors"
	"fmt"
	"mime"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// specURL is where the OpenAPI document is registered inside the compiler.
// Response schemas are compiled from JSON pointers into it, so their local
// $refs (#/components/schemas/...) resolve against the whole document.
const specURL = "mem://contract/openapi.json"

var httpMethods = []string{"get", "put", "post", "delete", "options", "head", "patch", "trace"}

// Validator validates response bodies against one OpenAPI 3.1 document.
// Schemas compile on first use and are cached. It is safe for concurrent use.
type Validator struct {
	doc      map[string]any
	ops      map[string]operation
	routes   []operation
	mu       sync.Mutex
	compiler *jsonschema.Compiler
	schemas  map[string]*jsonschema.Schema
}

type operation struct {
	id     string
	method string // lowercase, as in the document
	path   string
}

// NewValidator parses spec, the bundled OpenAPI document.
func NewValidator(spec []byte) (*Validator, error) {
	raw, err := jsonschema.UnmarshalJSON(bytes.NewReader(spec))
	if err != nil {
		return nil, fmt.Errorf("parse OpenAPI document: %w", err)
	}
	doc, ok := raw.(map[string]any)
	if !ok {
		return nil, errors.New("OpenAPI document is not an object")
	}
	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	if err := c.AddResource(specURL, doc); err != nil {
		return nil, fmt.Errorf("register OpenAPI document: %w", err)
	}
	v := &Validator{doc: doc, compiler: c, schemas: map[string]*jsonschema.Schema{}}
	v.indexOperations()
	return v, nil
}

func (v *Validator) indexOperations() {
	v.ops = map[string]operation{}
	paths, _ := v.doc["paths"].(map[string]any)
	for path, item := range paths {
		methods, _ := item.(map[string]any)
		for _, m := range httpMethods {
			op, ok := methods[m].(map[string]any)
			if !ok {
				continue
			}
			id, _ := op["operationId"].(string)
			o := operation{id: id, method: m, path: path}
			v.ops[id] = o
			v.routes = append(v.routes, o)
		}
	}
}

// ValidateResponse checks one response of an operation. A body is checked
// only for JSON media types; other declared types (for example
// text/event-stream) pass once the type itself is declared.
func (v *Validator) ValidateResponse(operationID string, status int, contentType string, body []byte) error {
	op, ok := v.ops[operationID]
	if !ok {
		return fmt.Errorf("operation %q is not in the contract", operationID)
	}
	respPtr, resp, err := v.response(op, status)
	if err != nil {
		return err
	}
	content, _ := resp["content"].(map[string]any)
	if len(content) == 0 {
		return requireEmpty(operationID, status, body)
	}
	mediaType, err := declaredMediaType(content, contentType)
	if err != nil {
		return fmt.Errorf("%s %d: %w", operationID, status, err)
	}
	if !isJSON(mediaType) {
		return nil
	}
	if _, hasSchema := content[mediaType].(map[string]any)["schema"]; !hasSchema {
		return nil
	}
	schema, err := v.schema(respPtr + "/content/" + escape(mediaType) + "/schema")
	if err != nil {
		return fmt.Errorf("%s %d: %w", operationID, status, err)
	}
	return validateBody(schema, operationID, status, body)
}

// ValidateJSONResponse checks a body whose content type is not known, such
// as a fixture, using the first JSON media type the response declares
// (application/json before any +json type). A response that declares no
// content must have an empty body.
func (v *Validator) ValidateJSONResponse(operationID string, status int, body []byte) error {
	op, ok := v.ops[operationID]
	if !ok {
		return fmt.Errorf("operation %q is not in the contract", operationID)
	}
	_, resp, err := v.response(op, status)
	if err != nil {
		return err
	}
	content, _ := resp["content"].(map[string]any)
	mediaType, ok := firstJSONMediaType(content)
	if !ok {
		return v.ValidateResponse(operationID, status, "", body)
	}
	return v.ValidateResponse(operationID, status, mediaType, body)
}

func firstJSONMediaType(content map[string]any) (string, bool) {
	if _, ok := content["application/json"]; ok {
		return "application/json", true
	}
	types := make([]string, 0, len(content))
	for mt := range content {
		if isJSON(mt) {
			types = append(types, mt)
		}
	}
	if len(types) == 0 {
		return "", false
	}
	sort.Strings(types)
	return types[0], true
}

// response finds the response object for status, following a
// #/components/responses/... reference. It returns the object and its
// JSON pointer inside the document.
func (v *Validator) response(op operation, status int) (string, map[string]any, error) {
	opPtr := "/paths/" + escape(op.path) + "/" + op.method
	responses, _ := v.lookup(opPtr + "/responses").(map[string]any)
	for _, key := range statusKeys(status) {
		resp, ok := responses[key].(map[string]any)
		if !ok {
			continue
		}
		ptr := opPtr + "/responses/" + escape(key)
		if ref, isRef := resp["$ref"].(string); isRef {
			return v.followRef(ref)
		}
		return ptr, resp, nil
	}
	return "", nil, fmt.Errorf("%s does not declare status %d and has no default response", op.id, status)
}

func (v *Validator) followRef(ref string) (string, map[string]any, error) {
	ptr, ok := strings.CutPrefix(ref, "#")
	if !ok {
		return "", nil, fmt.Errorf("response reference %q is not local; bundle the spec first", ref)
	}
	resp, ok := v.lookup(ptr).(map[string]any)
	if !ok {
		return "", nil, fmt.Errorf("response reference %q does not resolve", ref)
	}
	return ptr, resp, nil
}

// statusKeys lists the response keys that may describe status, most specific first.
func statusKeys(status int) []string {
	return []string{strconv.Itoa(status), strconv.Itoa(status/100) + "XX", "default"}
}

func (v *Validator) lookup(ptr string) any {
	var node any = v.doc
	for _, token := range strings.Split(strings.TrimPrefix(ptr, "/"), "/") {
		obj, ok := node.(map[string]any)
		if !ok {
			return nil
		}
		node = obj[unescape(token)]
	}
	return node
}

func (v *Validator) schema(ptr string) (*jsonschema.Schema, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if s, ok := v.schemas[ptr]; ok {
		return s, nil
	}
	s, err := v.compiler.Compile(specURL + "#" + ptr)
	if err != nil {
		return nil, fmt.Errorf("compile schema at %s: %w", ptr, err)
	}
	v.schemas[ptr] = s
	return s, nil
}

func requireEmpty(operationID string, status int, body []byte) error {
	if len(bytes.TrimSpace(body)) > 0 {
		return fmt.Errorf("%s %d declares no content but the response has a body", operationID, status)
	}
	return nil
}

func declaredMediaType(content map[string]any, contentType string) (string, error) {
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return "", fmt.Errorf("content type %q: %w", contentType, err)
	}
	if _, ok := content[mediaType]; !ok {
		return "", fmt.Errorf("content type %q is not declared", mediaType)
	}
	return mediaType, nil
}

func isJSON(mediaType string) bool {
	return mediaType == "application/json" || strings.HasSuffix(mediaType, "+json")
}

func validateBody(schema *jsonschema.Schema, operationID string, status int, body []byte) error {
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("%s %d: body is not JSON: %w", operationID, status, err)
	}
	if err := schema.Validate(doc); err != nil {
		return fmt.Errorf("%s %d: body breaks the contract: %w", operationID, status, err)
	}
	return nil
}

// escape encodes one JSON pointer token (RFC 6901).
func escape(token string) string {
	return strings.ReplaceAll(strings.ReplaceAll(token, "~", "~0"), "/", "~1")
}

func unescape(token string) string {
	return strings.ReplaceAll(strings.ReplaceAll(token, "~1", "/"), "~0", "~")
}
