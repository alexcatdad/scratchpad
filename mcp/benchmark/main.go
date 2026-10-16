// Command benchmark measures the same synthetic agent workload against either
// backend through real stdio MCP processes and authenticated HTTP requests.
package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type observation struct {
	operation string
	ms        float64
	bytes     int
	failed    bool
}

type distribution struct {
	Min  float64 `json:"min"`
	P50  float64 `json:"p50"`
	P95  float64 `json:"p95"`
	P99  float64 `json:"p99"`
	Max  float64 `json:"max"`
	Mean float64 `json:"mean"`
}

type measurement struct {
	Count         int          `json:"count"`
	Errors        int          `json:"errors"`
	Latency       distribution `json:"latencyMs"`
	ResponseBytes struct {
		Total int `json:"total"`
		Max   int `json:"max"`
	} `json:"responseBytes"`
}

type report struct {
	SchemaVersion  int    `json:"schemaVersion"`
	BackendLabel   string `json:"backendLabel"`
	SourceRevision string `json:"sourceRevision"`
	Database       string `json:"database"`
	ClientRuntime  string `json:"clientRuntime"`
	Platform       string `json:"platform"`
	StartedAt      string `json:"startedAt"`
	Workload       struct {
		Agents             int      `json:"agents"`
		Rounds             int      `json:"rounds"`
		SeedRecords        int      `json:"seedRecords"`
		UniqueCaptures     int      `json:"uniqueCaptures"`
		OperationsPerRound []string `json:"operationsPerRound"`
	} `json:"workload"`
	SetupMs    float64                `json:"setupMs"`
	ElapsedMs  float64                `json:"elapsedMs"`
	Errors     int                    `json:"errors"`
	Operations map[string]measurement `json:"operations"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	serverURL := flag.String("url", "http://localhost:3000", "Disposable Scratchpad instance")
	publicKey := flag.String("public-key", "", "Enrolled disposable SSH public key")
	flag.String("signing-key", "", "Disposable local signing key (or use SSH agent)")
	workspace := flag.String("workspace", "", "Disposable Git checkout with one remote")
	binary := flag.String("binary", "", "Built scratchpad-mcp executable")
	serviceSocket := flag.String("service-socket", "", "Private durable capture service socket")
	agents := flag.Int("agents", 4, "Independent concurrent MCP processes")
	rounds := flag.Int("rounds", 20, "Sequential workload rounds per agent")
	seedRecords := flag.Int("seed-records", 100, "Synthetic history before measured rounds")
	backendLabel := flag.String("backend-label", "typescript", "Operator supplied backend label")
	sourceRevision := flag.String("source-revision", "", "Operator supplied backend source revision")
	timeout := flag.Duration("timeout", 5*time.Minute, "Overall workload deadline")
	flag.Parse()
	if *publicKey == "" || *workspace == "" || *binary == "" {
		return errors.New("public-key, workspace and binary are required")
	}
	if *agents < 1 || *agents > 64 || *rounds < 1 || *rounds > 10000 || *seedRecords < 0 || *seedRecords > 100000 || *timeout <= 0 {
		return errors.New("workload size or deadline is outside supported bounds")
	}
	absWorkspace, err := filepath.Abs(*workspace)
	if err != nil {
		return errors.New("cannot resolve disposable workspace")
	}
	absBinary, err := filepath.Abs(*binary)
	if err != nil {
		return errors.New("cannot resolve MCP executable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, *serverURL+"/ready", nil)
	if err != nil {
		return errors.New("invalid instance URL")
	}
	client := &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	ready, err := client.Do(request)
	if err != nil {
		return errors.New("disposable instance is not reachable")
	}
	var status struct {
		Database string `json:"database"`
	}
	err = json.NewDecoder(io.LimitReader(ready.Body, 65536)).Decode(&status)
	ready.Body.Close()
	if err != nil || ready.StatusCode != http.StatusOK {
		return errors.New("disposable instance is not ready")
	}
	result := report{
		SchemaVersion: 1, BackendLabel: *backendLabel, SourceRevision: *sourceRevision,
		Database: status.Database, ClientRuntime: runtime.Version(), Platform: runtime.GOOS + "/" + runtime.GOARCH,
		StartedAt: time.Now().UTC().Format(time.RFC3339), Operations: make(map[string]measurement),
	}
	result.Workload.Agents, result.Workload.Rounds, result.Workload.SeedRecords = *agents, *rounds, *seedRecords
	result.Workload.UniqueCaptures = *agents * *rounds
	result.Workload.OperationsPerRound = []string{"capture", "retry", "detail", "search", "context"}
	setupStarted := time.Now()
	sessions := make([]*mcp.ClientSession, 0, *agents)
	defer func() {
		for _, session := range sessions {
			session.Close()
		}
	}()
	var projectID string
	for range *agents {
		command := exec.CommandContext(ctx, absBinary)
		command.Dir = absWorkspace
		command.Env = append(os.Environ(), "SCRATCHPAD_SERVICE_SOCKET="+*serviceSocket, "SCRATCHPAD_MIRROR=false")
		session, err := mcp.NewClient(&mcp.Implementation{Name: "scratchpad-benchmark", Version: "1"}, nil).Connect(ctx, &mcp.CommandTransport{Command: command}, nil)
		if err != nil {
			return errors.New("cannot initialize real stdio MCP")
		}
		sessions = append(sessions, session)
		// Authenticate sequentially outside the timed workload so proof admission
		// limits do not masquerade as steady-state memory latency.
		out, _, err := call(ctx, session, "get_project_context", map[string]any{"workingDirectory": absWorkspace})
		if err != nil {
			return errors.New("cannot authenticate or resolve disposable project")
		}
		project, _ := out["project"].(map[string]any)
		id, _ := project["id"].(string)
		if id == "" || (projectID != "" && id != projectID) {
			return errors.New("agents did not resolve the same project")
		}
		projectID = id
	}
	prefix := "synthetic-baseline-" + rand.Text()
	for index := range *seedRecords {
		args := captureArgs(projectID, fmt.Sprintf("%s-seed-%d", prefix, index))
		if _, _, err := call(ctx, sessions[0], "record_finding", args); err != nil {
			return errors.New("synthetic history capture failed")
		}
	}
	result.SetupMs = float64(time.Since(setupStarted)) / float64(time.Millisecond)
	observations := make(chan observation, *agents**rounds*5)
	var workers sync.WaitGroup
	start := make(chan struct{})
	for index, session := range sessions {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			for round := range *rounds {
				args := captureArgs(projectID, fmt.Sprintf("%s-agent-%d-round-%d", prefix, index, round))
				measure := func(operation, tool string, arguments map[string]any, validate func(map[string]any) bool) (map[string]any, bool) {
					started := time.Now()
					out, size, err := call(ctx, session, tool, arguments)
					elapsed := float64(time.Since(started)) / float64(time.Millisecond)
					failed := err != nil || !validate(out)
					observations <- observation{operation: operation, ms: elapsed, bytes: size, failed: failed}
					return out, !failed
				}
				captured, ok := measure("capture", "record_finding", args, func(out map[string]any) bool {
					record, _ := out["record"].(map[string]any)
					id, _ := record["id"].(string)
					payload, _ := record["payload"].(map[string]any)
					return id != "" && record["projectId"] == projectID && record["title"] == args["title"] && payload["finding"] == args["finding"]
				})
				if !ok {
					return
				}
				record := captured["record"].(map[string]any)
				sameRecord := func(out map[string]any) bool { return reflect.DeepEqual(out["record"], record) }
				if _, ok = measure("retry", "record_finding", args, sameRecord); !ok {
					return
				}
				if _, ok = measure("detail", "get_record", map[string]any{"id": record["id"]}, sameRecord); !ok {
					return
				}
				if _, ok = measure("search", "search_memory", map[string]any{"projectId": projectID, "query": args["title"], "limit": 10}, func(out map[string]any) bool {
					results, _ := out["results"].(map[string]any)
					records, _ := results["records"].([]any)
					for _, value := range records {
						found, _ := value.(map[string]any)
						if found["id"] == record["id"] && found["projectId"] == projectID {
							return true
						}
					}
					return false
				}); !ok {
					return
				}
				if _, ok = measure("context", "get_project_context", map[string]any{"projectId": projectID}, func(out map[string]any) bool {
					project, _ := out["project"].(map[string]any)
					return project["id"] == projectID
				}); !ok {
					return
				}
			}
		}()
	}
	started := time.Now()
	close(start)
	workers.Wait()
	result.ElapsedMs = float64(time.Since(started)) / float64(time.Millisecond)
	close(observations)
	durations := make(map[string][]float64)
	for item := range observations {
		measurement := result.Operations[item.operation]
		measurement.Count++
		if item.failed {
			measurement.Errors++
			result.Errors++
		}
		measurement.ResponseBytes.Total += item.bytes
		measurement.ResponseBytes.Max = max(measurement.ResponseBytes.Max, item.bytes)
		result.Operations[item.operation] = measurement
		durations[item.operation] = append(durations[item.operation], item.ms)
	}
	for operation, values := range durations {
		slices.Sort(values)
		percentile := func(p float64) float64 { return values[int(math.Ceil(p*float64(len(values))))-1] }
		var total float64
		for _, value := range values {
			total += value
		}
		measurement := result.Operations[operation]
		measurement.Latency = distribution{Min: values[0], P50: percentile(.5), P95: percentile(.95), P99: percentile(.99), Max: values[len(values)-1], Mean: total / float64(len(values))}
		result.Operations[operation] = measurement
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(result); err != nil {
		return errors.New("cannot write workload report")
	}
	if result.Errors > 0 {
		return errors.New("workload failed; inspect operation error counts")
	}
	return nil
}

func captureArgs(projectID, marker string) map[string]any {
	return map[string]any{
		"projectId": projectID, "requestId": marker, "title": strings.ReplaceAll(marker, "-", " "),
		"authority": "observed", "confidence": "high",
		"finding": "Synthetic workload fixture: preserve immutable evidence and exact capture retry identity.",
	}
}

func call(ctx context.Context, session *mcp.ClientSession, name string, args map[string]any) (map[string]any, int, error) {
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		return nil, 0, errors.New("MCP transport failed")
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return nil, 0, errors.New("MCP response cannot be encoded")
	}
	if result.IsError {
		return nil, len(encoded), errors.New("MCP operation failed")
	}
	data, err := json.Marshal(result.StructuredContent)
	if err != nil {
		return nil, len(encoded), errors.New("MCP structured content cannot be encoded")
	}
	var out map[string]any
	if string(data) != "null" && json.Unmarshal(data, &out) == nil {
		return out, len(encoded), nil
	}
	for _, content := range result.Content {
		if text, ok := content.(*mcp.TextContent); ok && json.Unmarshal([]byte(text.Text), &out) == nil {
			return out, len(encoded), nil
		}
	}
	return nil, len(encoded), errors.New("MCP returned no JSON object")
}
