package odata

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
