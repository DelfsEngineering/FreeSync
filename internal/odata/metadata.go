package odata

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"strings"
)

// PropertySpec describes one OData scalar Property from $metadata (see EntityProperties).
type PropertySpec struct {
	Name     string
	Computed bool // Org.OData.Core.V1.Computed or equivalent annotation
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
