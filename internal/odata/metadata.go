package odata

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// PropertySpec describes one OData scalar Property from $metadata (see EntityProperties).
type PropertySpec struct {
	Name     string
	Computed bool // Org.OData.Core.V1.Computed or equivalent annotation
}

// FileMakerFieldSpec is a thin schema row from FileMaker_Fields.
type FileMakerFieldSpec struct {
	TableName  string `json:"TableName"`
	FieldName  string `json:"FieldName"`
	FieldType  string `json:"FieldType"`
	FieldClass string `json:"FieldClass"`
	FieldReps  int    `json:"FieldReps"`
	FieldID    int    `json:"FieldId"`
	ModCount   int    `json:"ModCount"`
}

// GetBytes performs GET and returns the raw body and HTTP status (any Accept).
func (c *Client) GetBytes(ctx context.Context, url string) ([]byte, int, error) {
	return c.doRequest(ctx, "GET", url, nil, "application/xml, */*", "")
}

// EntityPropertyNames returns OData scalar Property names for an entity set (e.g. "People")
// by parsing the service $metadata document (CSDL).
func EntityPropertyNames(ctx context.Context, cli *Client, entitySet string) ([]string, error) {
	props, err := EntityProperties(ctx, cli, entitySet)
	if err != nil {
		return nil, err
	}
	out := make([]string, len(props))
	for i, p := range props {
		out[i] = p.Name
	}
	return out, nil
}

// EntityProperties returns scalar Property metadata for an entity set, including Computed when annotated.
func EntityProperties(ctx context.Context, cli *Client, entitySet string) ([]PropertySpec, error) {
	url := JoinPath(cli.BaseURL, "$metadata")
	b, code, err := cli.GetBytes(ctx, url)
	if err != nil {
		return nil, err
	}
	if code >= 300 {
		return nil, fmt.Errorf("metadata GET %d: %s", code, truncate(string(b), 200))
	}
	props, err := parseEntityProperties(b, entitySet)
	if err != nil {
		return nil, fmt.Errorf("parse metadata: %w", err)
	}
	return props, nil
}

// EntityPropertiesPreferThinSchema returns scalar property metadata using FileMaker_Fields first.
// It falls back to full $metadata when FileMaker_Fields is unavailable or incomplete.
func EntityPropertiesPreferThinSchema(ctx context.Context, cli *Client, entitySet string) ([]PropertySpec, string, error) {
	props, err := EntityPropertiesFromFileMakerFields(ctx, cli, entitySet)
	if err == nil {
		return props, "filemaker_fields", nil
	}
	props, metaErr := EntityProperties(ctx, cli, entitySet)
	if metaErr != nil {
		return nil, "", fmt.Errorf("thin schema: %v; metadata fallback: %w", err, metaErr)
	}
	return props, "metadata", nil
}

// EntityPropertiesFromFileMakerFields reads the small FileMaker_Fields system table slice for one table.
func EntityPropertiesFromFileMakerFields(ctx context.Context, cli *Client, entitySet string) ([]PropertySpec, error) {
	rows, err := FileMakerFields(ctx, cli, entitySet)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("FileMaker_Fields: no rows for table %q", entitySet)
	}
	props := make([]PropertySpec, 0, len(rows))
	for _, row := range rows {
		if row.FieldName == "" {
			continue
		}
		props = append(props, PropertySpec{
			Name:     row.FieldName,
			Computed: !isNormalFileMakerFieldClass(row.FieldClass),
		})
	}
	if len(props) == 0 {
		return nil, fmt.Errorf("FileMaker_Fields: no usable rows for table %q", entitySet)
	}
	return props, nil
}

// FileMakerFields fetches selected schema rows from FileMaker_Fields for one table.
func FileMakerFields(ctx context.Context, cli *Client, tableName string) ([]FileMakerFieldSpec, error) {
	filter := fmt.Sprintf("%s eq '%s'", quoteFilterField("TableName"), escapeODataString(tableName))
	selects := []string{"TableName", "FieldName", "FieldType", "FieldClass", "FieldReps", "FieldId", "ModCount"}
	q := "$filter=" + encodeSpaces(filter) +
		"&$select=" + strings.Join(quoteSelectFields(selects), ",") +
		"&$orderby=" + quoteSelectField("FieldName") +
		"&$top=1000"
	path := JoinPath(cli.BaseURL, "FileMaker_Fields") + "?" + q
	b, code, err := cli.GetJSON(ctx, path)
	if err != nil {
		return nil, err
	}
	if code == http.StatusNotFound {
		return nil, ErrNotFound
	}
	if code >= 300 {
		return nil, fmt.Errorf("FileMaker_Fields GET %d: %s", code, truncate(string(b), 300))
	}
	var envelope struct {
		Value []FileMakerFieldSpec `json:"value"`
	}
	if err := json.Unmarshal(b, &envelope); err != nil {
		return nil, err
	}
	return envelope.Value, nil
}

func quoteSelectFields(fields []string) []string {
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		out = append(out, quoteSelectField(f))
	}
	return out
}

func isNormalFileMakerFieldClass(class string) bool {
	class = strings.TrimSpace(strings.ToLower(class))
	return class == "" || class == "normal"
}

// parseEntityPropertyNames extracts Property Name values for the given entity set name.
func parseEntityPropertyNames(xmlData []byte, entitySet string) ([]string, error) {
	props, err := parseEntityProperties(xmlData, entitySet)
	if err != nil {
		return nil, err
	}
	out := make([]string, len(props))
	for i, p := range props {
		out[i] = p.Name
	}
	return out, nil
}

func parseEntityProperties(xmlData []byte, entitySet string) ([]PropertySpec, error) {
	dec := xml.NewDecoder(bytes.NewReader(xmlData))

	var schemaNS string
	typeStack := []string{}
	inEntityType := 0
	var props []PropertySpec
	var curProp *PropertySpec

	setOfType := make(map[string]string)           // entity set name -> qualified entity type name
	propsOfType := make(map[string][]PropertySpec) // qualified entity type name -> properties

	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "Schema":
				for _, a := range t.Attr {
					if a.Name.Local == "Namespace" {
						schemaNS = a.Value
					}
				}
			case "EntityType":
				inEntityType++
				props = nil
				curProp = nil
				tLocal := ""
				for _, a := range t.Attr {
					if a.Name.Local == "Name" {
						tLocal = a.Value
						break
					}
				}
				typeStack = append(typeStack, tLocal)
			case "EntitySet":
				var setName, typeRef string
				for _, a := range t.Attr {
					switch a.Name.Local {
					case "Name":
						setName = a.Value
					case "EntityType":
						typeRef = a.Value
					}
				}
				if setName != "" && typeRef != "" {
					setOfType[setName] = typeRef
				}
			case "Property":
				if inEntityType > 0 && len(typeStack) > 0 {
					name := xmlAttr(t, "Name")
					if name != "" {
						curProp = &PropertySpec{Name: name}
					}
				}
			case "Annotation":
				if curProp != nil {
					term := xmlAttr(t, "Term")
					if isComputedAnnotationTerm(term) {
						boolAttr := strings.TrimSpace(xmlAttr(t, "Bool"))
						// For FileMaker terms like *.Calculation and *.Summary, presence of the
						// annotation itself implies computed/non-writable semantics even when Bool
						// is omitted in metadata.
						if boolAttr == "" || boolAttr == "true" {
							curProp.Computed = true
						}
					}
				}
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "EntityType":
				if inEntityType > 0 {
					tLocal := ""
					if len(typeStack) > 0 {
						tLocal = typeStack[len(typeStack)-1]
						typeStack = typeStack[:len(typeStack)-1]
					}
					if schemaNS != "" && tLocal != "" {
						qn := schemaNS + "." + tLocal
						propsOfType[qn] = append([]PropertySpec{}, props...)
					}
					inEntityType--
				}
				curProp = nil
			case "Property":
				if curProp != nil {
					props = append(props, *curProp)
					curProp = nil
				}
			}
		}
	}

	typeRef, ok := setOfType[entitySet]
	if !ok {
		return nil, fmt.Errorf("entity set %q not found in metadata", entitySet)
	}

	qn := resolveEntityTypeQName(typeRef, propsOfType)
	out, ok := propsOfType[qn]
	if !ok || len(out) == 0 {
		for full, p := range propsOfType {
			if strings.HasSuffix(full, "."+entitySet) || strings.HasSuffix(full, "."+lastSegment(typeRef)) {
				out = p
				ok = true
				break
			}
		}
	}
	if !ok || len(out) == 0 {
		return nil, fmt.Errorf("entity type %q: no properties found (resolved %q)", typeRef, qn)
	}
	return out, nil
}

func xmlAttr(se xml.StartElement, local string) string {
	for _, a := range se.Attr {
		if a.Name.Local == local {
			return a.Value
		}
	}
	return ""
}

func isComputedAnnotationTerm(term string) bool {
	if term == "" {
		return false
	}
	// OData 4: https://docs.oasis-open.org/odata/odata/v4.01/os/part3-csdl-xml/v4.01-os-part3-csdl-xml.html
	if strings.HasSuffix(term, ".Computed") || strings.Contains(term, "Core.V1.Computed") {
		return true
	}
	// FileMaker Server: Claris OData guide — Get metadata — Boolean annotation "Calculation"
	if strings.HasSuffix(term, ".Calculation") || strings.Contains(term, ".Calculation") {
		return true
	}
	// Same guide — "Summary" (aggregate) fields are not writable via merge-patch like normal data.
	if strings.HasSuffix(term, ".Summary") || strings.Contains(term, ".Summary") {
		return true
	}
	return false
}

func lastSegment(q string) string {
	if i := strings.LastIndex(q, "."); i >= 0 {
		return q[i+1:]
	}
	return q
}

func resolveEntityTypeQName(typeRef string, propsOfType map[string][]PropertySpec) string {
	if _, ok := propsOfType[typeRef]; ok {
		return typeRef
	}
	suffix := lastSegment(typeRef)
	for full := range propsOfType {
		if lastSegment(full) == suffix {
			return full
		}
	}
	return typeRef
}
