package scratchpad

import (
	"bytes"
	"context"
	"encoding/json"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var captureSchema, captureResolved = func() (*jsonschema.Schema, *jsonschema.Resolved) {
	schema, err := jsonschema.For[CaptureResult](nil)
	if err != nil {
		panic(err)
	}
	resolved, err := schema.Resolve(nil)
	if err != nil {
		panic(err)
	}
	return schema, resolved
}()

// Keep the typed schemas, but avoid the SDK's output-defaults round trip through
// float64. Source evidence is validated without reserializing that validation copy.
func addCaptureTool[Input any](server *mcp.Server, tool *mcp.Tool, handler mcp.ToolHandlerFor[Input, CaptureResult]) {
	tool.OutputSchema = captureSchema.CloneSchemas()
	mcp.AddTool(server, tool, func(ctx context.Context, request *mcp.CallToolRequest, input Input) (*mcp.CallToolResult, any, error) {
		result, output, err := handler(ctx, request, input)
		if err != nil {
			return result, nil, err
		}
		raw, err := json.Marshal(output)
		if err != nil {
			return nil, nil, err
		}
		var checked any
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		if err := decoder.Decode(&checked); err != nil {
			return nil, nil, err
		}
		if err := captureResolved.Validate(checked); err != nil {
			return nil, nil, err
		}
		if result == nil {
			result = &mcp.CallToolResult{}
		}
		result.StructuredContent = json.RawMessage(raw)
		if result.Content == nil {
			result.Content = []mcp.Content{&mcp.TextContent{Text: string(raw)}}
		}
		return result, nil, nil
	})
}
