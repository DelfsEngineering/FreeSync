package odata

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseEntityPropertyNames_fixture(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "testdata", "odata_metadata_people.xml"))
	if err != nil {
		t.Fatal(err)
	}
	names, err := parseEntityPropertyNames(b, "People")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]struct{}{
		"id":                    {},
		"ModificationTimestamp": {},
		"notes":                 {},
		"totalCalc":             {},
		"fmCalc":                {},
	}
	if len(names) != len(want) {
		t.Fatalf("got %d props: %v", len(names), names)
	}
	for _, n := range names {
		if _, ok := want[n]; !ok {
			t.Errorf("unexpected property %q", n)
		}
	}
}

func TestEntityPropertyNames_http(t *testing.T) {
	xmlBody, err := os.ReadFile(filepath.Join("..", "..", "testdata", "odata_metadata_people.xml"))
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/BF_Test/$metadata" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write(xmlBody)
	}))
	defer srv.Close()

	cli := &Client{BaseURL: srv.URL + "/BF_Test", Username: "u", Password: "p"}
	names, err := EntityPropertyNames(context.Background(), cli, "People")
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 5 {
		t.Fatalf("got %v", names)
	}
}

func TestEntityPropertiesFromFileMakerFields_usesThinSchema(t *testing.T) {
	var metadataHits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/$metadata"):
			metadataHits++
			t.Fatalf("thin schema path should not fetch $metadata")
		case strings.HasSuffix(r.URL.Path, "/FileMaker_Fields"):
			if !strings.Contains(r.URL.RawQuery, "$filter=%22TableName%22%20eq%20%27Sites%27") {
				t.Fatalf("expected TableName filter for Sites, got %q", r.URL.RawQuery)
			}
			if !strings.Contains(r.URL.RawQuery, "$select=%22TableName%22,%22FieldName%22,%22FieldType%22,%22FieldClass%22,%22FieldReps%22,%22FieldId%22,%22ModCount%22") {
				t.Fatalf("expected thin selected fields, got %q", r.URL.RawQuery)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"value": []map[string]any{
					{"TableName": "Sites", "FieldName": "id", "FieldType": "varchar", "FieldClass": "Normal", "FieldReps": 1, "FieldId": 1, "ModCount": 1},
					{"TableName": "Sites", "FieldName": "asJSON", "FieldType": "varchar", "FieldClass": "Calculated", "FieldReps": 1, "FieldId": 2, "ModCount": 4},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	cli := &Client{BaseURL: srv.URL + "/BF_Test", Username: "u", Password: "p"}
	props, err := EntityPropertiesFromFileMakerFields(context.Background(), cli, "Sites")
	if err != nil {
		t.Fatal(err)
	}
	if metadataHits != 0 {
		t.Fatalf("unexpected metadata hits: %d", metadataHits)
	}
	if len(props) != 2 {
		t.Fatalf("got %d props: %+v", len(props), props)
	}
	if props[0].Name != "id" || props[0].Computed {
		t.Fatalf("expected normal id field, got %+v", props[0])
	}
	if props[1].Name != "asJSON" || !props[1].Computed {
		t.Fatalf("expected calculated field to be computed, got %+v", props[1])
	}
}

func TestEntityPropertiesPreferThinSchema_fallsBackToMetadata(t *testing.T) {
	xmlBody, err := os.ReadFile(filepath.Join("..", "..", "testdata", "odata_metadata_people.xml"))
	if err != nil {
		t.Fatal(err)
	}
	var thinHits, metadataHits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/FileMaker_Fields"):
			thinHits++
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"no schema table"}`))
		case strings.HasSuffix(r.URL.Path, "/$metadata"):
			metadataHits++
			w.Header().Set("Content-Type", "application/xml")
			_, _ = w.Write(xmlBody)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	cli := &Client{BaseURL: srv.URL + "/BF_Test", Username: "u", Password: "p"}
	props, source, err := EntityPropertiesPreferThinSchema(context.Background(), cli, "People")
	if err != nil {
		t.Fatal(err)
	}
	if source != "metadata" {
		t.Fatalf("expected metadata fallback source, got %q", source)
	}
	if thinHits != 1 || metadataHits != 1 {
		t.Fatalf("expected one thin hit and one metadata hit, got thin=%d metadata=%d", thinHits, metadataHits)
	}
	if len(props) != 5 {
		t.Fatalf("got %d props: %+v", len(props), props)
	}
}

func TestParseEntityProperties_computedAnnotation(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "testdata", "odata_metadata_people.xml"))
	if err != nil {
		t.Fatal(err)
	}
	props, err := parseEntityProperties(b, "People")
	if err != nil {
		t.Fatal(err)
	}
	var sawComputed bool
	for _, p := range props {
		if p.Name == "totalCalc" {
			sawComputed = true
			if !p.Computed {
				t.Fatal("expected totalCalc Computed=true")
			}
		}
		if p.Name == "id" && p.Computed {
			t.Fatal("id should not be computed")
		}
	}
	if !sawComputed {
		t.Fatal("totalCalc missing")
	}
	var sawFMClc bool
	for _, p := range props {
		if p.Name == "fmCalc" {
			sawFMClc = true
			if !p.Computed {
				t.Fatal("expected FileMaker-style Calculation annotation to mark field")
			}
		}
	}
	if !sawFMClc {
		t.Fatal("fmCalc missing")
	}
}

func TestParseEntityProperties_computedAnnotationWithoutBool(t *testing.T) {
	xml := `<?xml version="1.0" encoding="utf-8"?>
<edmx:Edmx Version="4.0" xmlns:edmx="http://docs.oasis-open.org/odata/ns/edmx">
  <edmx:DataServices>
    <Schema Namespace="com.filemaker.odata.BetterForms_Helper_prod" xmlns="http://docs.oasis-open.org/odata/ns/edm">
      <EntityContainer Name="Default">
        <EntitySet Name="Inbox" EntityType="com.filemaker.odata.BetterForms_Helper_prod.Inbox_" />
      </EntityContainer>
      <EntityType Name="Inbox_">
        <Property Name="id" Type="Edm.String" />
        <Property Name="sizePayload" Type="Edm.Int32">
          <Annotation Term="com.filemaker.odata.Calculation" />
        </Property>
      </EntityType>
    </Schema>
  </edmx:DataServices>
</edmx:Edmx>`
	props, err := parseEntityProperties([]byte(xml), "Inbox")
	if err != nil {
		t.Fatal(err)
	}
	var saw bool
	for _, p := range props {
		if p.Name == "sizePayload" {
			saw = true
			if !p.Computed {
				t.Fatal("expected sizePayload computed=true when Calculation term is present without Bool")
			}
		}
	}
	if !saw {
		t.Fatal("sizePayload missing")
	}
}
