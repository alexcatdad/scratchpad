package scratchpad

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestStdioRelayPreservesExactAPINumericalEvidence(t *testing.T) {
	const record = `{"id":"rec_number","payload":{"finding":"Exact synthetic evidence.","counter":9007199254740993,"fraction":1.00000000000000000001}}`
	ctx, session := presenceStdioSession(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v1/records/rec_number":
			fmt.Fprint(w, `{"record":`+record+`}`)
		case r.URL.Path == "/api/v1/records" && r.Method == "POST":
			var request map[string]any
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Error(err)
			}
			var central map[string]any
			decoder := json.NewDecoder(strings.NewReader(record))
			decoder.UseNumber()
			if err := decoder.Decode(&central); err != nil {
				t.Error(err)
			}
			central["clientSavedAt"] = request["record"].(map[string]any)["clientSavedAt"]
			json.NewEncoder(w).Encode(map[string]any{"record": central, "mirror": map[string]any{"eligible": false}})
		case r.URL.Path == "/api/v1/records":
			fmt.Fprint(w, `{"records":[`+record+`],"nextCursor":null}`)
		case r.URL.Path == "/api/v1/projects/proj_number/context":
			fmt.Fprint(w, `{"openFindings":[`+record+`]}`)
		default:
			http.Error(w, "unexpected route", http.StatusNotFound)
		}
	})
	for _, tool := range []struct {
		name      string
		arguments map[string]any
	}{
		{"get_record", map[string]any{"id": "rec_number"}},
		{"get_project_context", map[string]any{"projectId": "proj_number"}},
		{"search_memory", map[string]any{"projectId": "proj_number", "query": "synthetic"}},
		{"record_finding", map[string]any{"projectId": "proj_number", "requestId": "numeric-finding", "title": "Exact numbers", "authority": "observed", "confidence": "high", "finding": "Exact synthetic evidence."}},
	} {
		t.Run(tool.name, func(t *testing.T) {
			result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: tool.name, Arguments: tool.arguments})
			if err != nil || result.IsError {
				t.Fatalf("tool unavailable: %+v, %v", result, err)
			}
			text := ""
			for _, content := range result.Content {
				if content, ok := content.(*mcp.TextContent); ok {
					text += content.Text
				}
			}
			if !strings.Contains(text, "9007199254740993") || !strings.Contains(text, "1.00000000000000000001") {
				t.Fatalf("stdio relay changed immutable numerical evidence: %s", text)
			}
		})
	}
}

func TestStdioCompactContextChecksExactNumericalVersion(t *testing.T) {
	versions := map[string]string{
		"integer": "2", "decimal": "2.0", "exponent": "20e-1",
		"string": `"2"`, "rounded": "2.0000000000000000000000001", "old": "1", "missing": "null",
	}
	ctx, session := presenceStdioSession(t, func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(r.URL.Path, "/")
		version := versions[parts[len(parts)-2]]
		fmt.Fprint(w, `{"version":`+version+`,"records":[]}`)
	})
	for _, scenario := range []struct {
		project   string
		wantError bool
	}{{"integer", false}, {"decimal", false}, {"exponent", false}, {"string", true}, {"rounded", true}, {"old", true}, {"missing", true}} {
		t.Run(scenario.project, func(t *testing.T) {
			result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "get_project_context", Arguments: map[string]any{"projectId": scenario.project, "version": 2}})
			failed := err != nil || (result != nil && result.IsError)
			if failed != scenario.wantError {
				t.Fatalf("unsupported or equivalent response version misidentified: %+v, %v", result, err)
			}
		})
	}
}

func TestStdioCaptureRetainsOutputSchemaAndRejectsMalformedEnvelope(t *testing.T) {
	ctx, session := presenceStdioSession(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"record":null,"mirror":{"eligible":false}}`)
	})
	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range tools.Tools {
		if !strings.HasPrefix(tool.Name, "record_") {
			continue
		}
		raw, _ := json.Marshal(tool.OutputSchema)
		var schema struct {
			Type                 string                    `json:"type"`
			Required             []string                  `json:"required"`
			AdditionalProperties *bool                     `json:"additionalProperties"`
			Properties           map[string]map[string]any `json:"properties"`
		}
		if json.Unmarshal(raw, &schema) != nil || schema.Type != "object" || schema.AdditionalProperties == nil || *schema.AdditionalProperties || len(schema.Required) != 2 || len(schema.Properties) != 7 {
			t.Fatalf("capture output schema changed: %s", raw)
		}
		for _, name := range []string{"outcome", "receipt"} {
			found := false
			for _, required := range schema.Required {
				found = found || required == name
			}
			if !found {
				t.Fatalf("capture output is missing required field: %s", name)
			}
		}
		for _, name := range []string{"project", "record", "mirror", "receipt", "centralRecordId", "centralProjectId"} {
			if name == "centralRecordId" || name == "centralProjectId" {
				if schema.Properties[name]["type"] != "string" {
					t.Fatalf("capture output property lost its identity contract: %s", name)
				}
				continue
			}
			if schema.Properties[name]["type"] != "object" {
				t.Fatalf("capture output property lost its object contract: %s", name)
			}
		}
	}
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "record_finding", Arguments: map[string]any{"projectId": "proj_number", "requestId": "malformed-envelope", "title": "Witness", "authority": "observed", "confidence": "high", "finding": "Synthetic evidence."}})
	if err == nil && (result == nil || !result.IsError) {
		t.Fatal("malformed API envelope bypassed the capture output schema")
	}
}
