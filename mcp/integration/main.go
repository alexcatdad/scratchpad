// Command integration exercises an independently running Scratchpad HTTP server
// through the real MCP executable's stdio transport. It never bypasses auth.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/alexcatdad/scratchpad/mcp/internal/localcapture"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type capture struct {
	Tool           string         `json:"tool"`
	Arguments      map[string]any `json:"arguments"`
	RecordID       string         `json:"recordId"`
	ProjectID      string         `json:"projectId"`
	ReceiptID      string         `json:"receiptId,omitempty"`
	NeedsSelection bool           `json:"needsSelection,omitempty"`
	GitContext     map[string]any `json:"gitContext,omitempty"`
}
type state struct {
	Captures         []capture `json:"captures"`
	ProjectScenarios []capture `json:"projectScenarios,omitempty"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	serverURL := flag.String("url", "http://localhost:3000", "Running Scratchpad HTTP server")
	publicKey := flag.String("public-key", "", "Enrolled SSH public key path")
	signingKey := flag.String("signing-key", "", "Signing private key path (or use SSH agent)")
	workspace := flag.String("workspace", "", "Existing disposable Git repository with one remote")
	binary := flag.String("binary", "", "Built scratchpad-mcp executable")
	statePath := flag.String("state", "", "Capture-state file retained across server restart")
	serviceSocket := flag.String("service-socket", "", "Private local capture service socket (blank uses the platform default)")
	secondWorkspace := flag.String("second-workspace", "", "Second disposable Git repository for per-call scope phase")
	fingerprint := flag.String("fingerprint", "", "SHA256 fingerprint of the enrolled SSH public key")
	phase := flag.String("phase", "capture", "capture, records-only, projects, read-only context, detail, questions, or verify after server restart")
	recordID := flag.String("record-id", "", "Existing disposable record ID for detail phase")
	questionProjectID := flag.String("project-id", "", "Explicit disposable project for question phase")
	restrictedProject := flag.String("restricted-project", "", "Disposable external project ID for projects phase")
	mirror := flag.Bool("mirror", false, "Require successful central and local mirroring for decisions (enable server project setting first)")
	flag.Parse()
	if *publicKey == "" || *workspace == "" || *binary == "" || (*statePath == "" && *phase != "detail" && *phase != "questions" && *phase != "configure-service" && *phase != "scope") {
		return errors.New("public-key, workspace, binary, and state flags are required")
	}
	if *phase != "capture" && *phase != "records" && *phase != "projects" && *phase != "verify" && *phase != "verify-projects" && *phase != "read" && *phase != "detail" && *phase != "questions" && *phase != "outage-capture" && *phase != "receipt" && *phase != "service-unavailable" && *phase != "configure-service" && *phase != "scope" && *phase != "lost-response-capture" && *phase != "lost-response-retry" && *phase != "central-response-drop" && *phase != "central-response-retry" {
		return errors.New("phase must be capture, records, projects, verify, verify-projects, read, detail, questions, outage-capture, receipt, service-unavailable, configure-service, scope, lost-response-capture, lost-response-retry, central-response-drop, or central-response-retry")
	}
	absWorkspace, err := filepath.Abs(*workspace)
	if err != nil {
		return err
	}
	absBinary, err := filepath.Abs(*binary)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if *phase == "configure-service" {
		if *serviceSocket == "" || *fingerprint == "" || *signingKey == "" {
			return errors.New("service-socket, fingerprint, and signing-key are required")
		}
		service := localcapture.NewClient(*serviceSocket)
		if err := service.SetConfiguration(ctx, localcapture.ServiceConfiguration{
			ServerURL: *serverURL, CredentialFingerprint: *fingerprint,
			PublicKeyPath: *publicKey, SigningKeyPath: *signingKey,
		}); err != nil {
			return fmt.Errorf("configure durable capture service: %w", err)
		}
		verified, err := service.VerifyConnection(ctx)
		if err != nil || verified.CredentialID == "" || !contains(verified.CaptureCapabilities, localcapture.CapabilityClientSavedAtV1) {
			return fmt.Errorf("real Scratchpad authentication/capability check failed: %+v %v", verified, err)
		}
		fmt.Println("PASS configure-service: actual Scratchpad SSH identity and capture timestamp capability")
		return nil
	}
	command := exec.CommandContext(ctx, absBinary)
	command.Dir = absWorkspace
	command.Stderr = os.Stderr
	command.Env = append(os.Environ(), "SCRATCHPAD_URL="+*serverURL, "SCRATCHPAD_PUBLIC_KEY="+*publicKey, "SCRATCHPAD_SIGNING_KEY="+*signingKey, "SCRATCHPAD_SERVICE_SOCKET="+*serviceSocket, fmt.Sprintf("SCRATCHPAD_MIRROR=%t", *mirror || *phase == "projects"))
	session, err := mcp.NewClient(&mcp.Implementation{Name: "scratchpad-integration", Version: "1"}, nil).Connect(ctx, &mcp.CommandTransport{Command: command}, nil)
	if err != nil {
		return fmt.Errorf("initialize real stdio MCP: %w", err)
	}
	defer session.Close()
	listed, err := session.ListTools(ctx, nil)
	if err != nil {
		return err
	}
	if len(listed.Tools) != 21 {
		return fmt.Errorf("expected 21 tools, got %d", len(listed.Tools))
	}
	if *phase == "questions" {
		if *questionProjectID == "" {
			return errors.New("project-id is required for question phase")
		}
		input := map[string]any{"projectId": *questionProjectID, "title": "Unanswered integration question", "authority": "explicit", "confidence": "high", "question": "Which database supports optional RAG?", "requestId": "question-only-integration"}
		out, err := call(ctx, session, "record_qa", input)
		if err != nil {
			return err
		}
		record, _ := out["record"].(map[string]any)
		id, _ := record["id"].(string)
		if id == "" {
			return errors.New("question capture omitted its identity")
		}
		retried, err := call(ctx, session, "record_qa", input)
		if err != nil {
			return err
		}
		if retried["record"].(map[string]any)["id"] != id {
			return errors.New("question retry duplicated capture")
		}
		detail, err := call(ctx, session, "get_record", map[string]any{"id": id})
		if err != nil {
			return err
		}
		payload := detail["record"].(map[string]any)["payload"].(map[string]any)
		if _, exists := payload["answer"]; exists {
			return errors.New("question capture invented an answer")
		}
		memory, err := call(ctx, session, "get_project_context", map[string]any{"projectId": *questionProjectID})
		if err != nil {
			return err
		}
		if questions, _ := memory["openQuestions"].([]any); len(questions) != 1 || questions[0].(map[string]any)["id"] != id {
			return errors.New("unresolved question missing from context")
		}
		compact, err := call(ctx, session, "get_project_context", map[string]any{"projectId": *questionProjectID, "version": 2, "limit": 3, "maxChars": 4096})
		if err != nil {
			return err
		}
		if compact["version"] != float64(2) || compact["counts"].(map[string]any)["openQuestions"] != float64(1) || compact["records"].([]any)[0].(map[string]any)["id"] != id {
			return errors.New("compact question context lost version, source or unresolved status")
		}
		fmt.Println("PASS questions: question-only real stdio capture, retry and scoped context")
		return nil
	}
	if *phase == "detail" {
		if *recordID == "" {
			return errors.New("record-id is required for detail phase")
		}
		result, err := call(ctx, session, "get_record", map[string]any{"id": *recordID})
		if err != nil {
			return err
		}
		record, ok := result["record"].(map[string]any)
		if !ok || record["id"] != *recordID {
			return errors.New("record identity was not preserved")
		}
		fmt.Println("PASS detail: real stdio → local SSH possession proof → authenticated record details")
		return nil
	}
	var saved state
	if *phase == "service-unavailable" {
		data, err := os.ReadFile(*statePath)
		if err != nil {
			return fmt.Errorf("read prior capture state for central read assertion: %w", err)
		}
		if err = json.Unmarshal(data, &saved); err != nil {
			return fmt.Errorf("load all eight prior captures for central read assertion: %w", err)
		}
		if len(saved.Captures) != 8 {
			return errors.New("central read assertion requires all eight prior captures")
		}
		resumeContext, err := call(ctx, session, "get_project_context", map[string]any{"workingDirectory": absWorkspace})
		if err != nil {
			return fmt.Errorf("central read failed while local collector startup was unavailable: %w", err)
		}
		if err = verifyResumeContext(resumeContext, saved.Captures); err != nil {
			return fmt.Errorf("central read omitted known captures while local collector startup was unavailable: %w", err)
		}
		_, err = call(ctx, session, "record_finding", map[string]any{
			"projectId": "proj_unavailable_service", "requestId": "service-unavailable-no-fallback",
			"title": "Synthetic unavailable-service proof", "authority": "observed", "confidence": "high",
			"finding": "The MCP client must require durable local intake.",
		})
		if err == nil || !strings.Contains(strings.ToLower(err.Error()), "service") {
			return fmt.Errorf("capture did not fail clearly when local service was unavailable: %v", err)
		}
		fmt.Println("PASS service-unavailable: central reads remained available and capture failed at local intake")
		return nil
	}
	if *phase == "scope" {
		if *secondWorkspace == "" {
			return errors.New("second-workspace is required for the scope phase")
		}
		projectIDs := []string{}
		for index, directory := range []string{absWorkspace, *secondWorkspace} {
			absDirectory, absErr := filepath.Abs(directory)
			if absErr != nil {
				return absErr
			}
			args := map[string]any{
				"workingDirectory": absDirectory, "requestId": fmt.Sprintf("per-call-scope-%d", index),
				"title": fmt.Sprintf("Synthetic per-call scope %d", index), "authority": "observed", "confidence": "high",
				"finding": fmt.Sprintf("Capture scoped to disposable checkout %d.", index),
			}
			out, captureErr := call(ctx, session, "record_finding", args)
			if captureErr != nil {
				return captureErr
			}
			record, _ := out["record"].(map[string]any)
			project, _ := out["project"].(map[string]any)
			receipt, _ := out["receipt"].(map[string]any)
			projectID, _ := project["id"].(string)
			recordID, _ := record["id"].(string)
			receiptID, _ := receipt["receiptId"].(string)
			if out["outcome"] != "delivered" || receipt["state"] != "delivered" || projectID == "" || recordID == "" || receiptID == "" || receipt["centralRecordId"] != recordID {
				return fmt.Errorf("per-call capture %d was not delivered with a receipt: %v", index, out)
			}
			fetched, fetchErr := call(ctx, session, "get_record", map[string]any{"id": recordID})
			if fetchErr != nil {
				return fetchErr
			}
			central, _ := fetched["record"].(map[string]any)
			if central["id"] != recordID || central["projectId"] != projectID || central["clientSavedAt"] == nil {
				return fmt.Errorf("per-call scope %d did not persist centrally: %v", index, central)
			}
			projectIDs = append(projectIDs, projectID)
		}
		if projectIDs[0] == projectIDs[1] {
			return fmt.Errorf("different per-call Git checkouts resolved to the same project: %v", projectIDs)
		}
		fmt.Println("PASS scope: one MCP process preserved independent per-call Git project scopes and central receipts")
		return nil
	}
	if *phase == "lost-response-capture" {
		input := map[string]any{
			"workingDirectory": absWorkspace, "requestId": "lost-response-stable-request-id",
			"title": "Synthetic lost acknowledgement retry", "authority": "observed", "confidence": "high",
			"finding": "A later MCP process will retry this exact durable intake identity.",
		}
		result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "record_finding", Arguments: input})
		if err != nil || result.IsError {
			return fmt.Errorf("initial durable intake failed: %v", err)
		}
		// Deliberately discard the caller-visible result. The next process has
		// only the same request ID and input, modelling a lost MCP acknowledgement.
		fmt.Println("PASS lost-response-capture: intake accepted; caller acknowledgement intentionally discarded")
		return nil
	}
	if *phase == "lost-response-retry" {
		input := map[string]any{
			"workingDirectory": absWorkspace, "requestId": "lost-response-stable-request-id",
			"title": "Synthetic lost acknowledgement retry", "authority": "observed", "confidence": "high",
			"finding": "A later MCP process will retry this exact durable intake identity.",
		}
		out, err := call(ctx, session, "record_finding", input)
		if err != nil {
			return err
		}
		record, _ := out["record"].(map[string]any)
		receipt, _ := out["receipt"].(map[string]any)
		recordID, _ := record["id"].(string)
		if out["outcome"] != "delivered" || recordID == "" || receipt["state"] != "delivered" || receipt["centralRecordId"] != recordID {
			return fmt.Errorf("retry after discarded acknowledgement changed or lost its receipt: %v", out)
		}
		fetched, err := call(ctx, session, "get_record", map[string]any{"id": recordID})
		if err != nil {
			return err
		}
		central, _ := fetched["record"].(map[string]any)
		if central["id"] != recordID || central["clientSavedAt"] == nil {
			return fmt.Errorf("lost-ack retry record was not centrally preserved: %v", central)
		}
		fmt.Printf("PASS lost-response-retry: fresh MCP process recovered the same central identity %s\n", recordID)
		return nil
	}
	if *phase == "central-response-drop" {
		input := centralResponseDropInput(absWorkspace)
		out, err := call(ctx, session, "record_finding", input)
		if err != nil {
			return err
		}
		receipt, _ := out["receipt"].(map[string]any)
		receiptID, _ := receipt["receiptId"].(string)
		if receiptID == "" || receipt["state"] == nil {
			return fmt.Errorf("committed-response-drop capture did not retain a durable receipt: %v", out)
		}
		saved.Captures = []capture{{Tool: "record_finding", Arguments: input, ReceiptID: receiptID}}
		data, err := json.MarshalIndent(saved, "", "  ")
		if err != nil {
			return err
		}
		if err := os.WriteFile(*statePath, append(data, '\n'), 0600); err != nil {
			return err
		}
		fmt.Println("PASS central-response-drop: actual backend committed; response path left receipt recoverable")
		return nil
	}
	if *phase == "central-response-retry" {
		data, err := os.ReadFile(*statePath)
		if err != nil {
			return err
		}
		if err := json.Unmarshal(data, &saved); err != nil || len(saved.Captures) != 1 || saved.Captures[0].ReceiptID == "" {
			return errors.New("state must contain the committed-response-drop receipt")
		}
		item := saved.Captures[0]
		if !reflect.DeepEqual(item.Arguments, centralResponseDropInput(absWorkspace)) {
			return errors.New("central retry changed the original capture request")
		}
		if _, err := call(ctx, session, "record_finding", item.Arguments); err != nil {
			return err
		}
		deadline := time.Now().Add(20 * time.Second)
		for {
			receipt, callErr := call(ctx, session, "get_capture_receipt", map[string]any{"receiptId": item.ReceiptID})
			if callErr != nil {
				return callErr
			}
			if receipt["state"] == "delivered" {
				if receipt["centralRecordId"] == nil || receipt["receiptId"] != item.ReceiptID {
					return fmt.Errorf("delivered central retry omitted identity: %v", receipt)
				}
				recordID, _ := receipt["centralRecordId"].(string)
				detail, detailErr := call(ctx, session, "get_record", map[string]any{"id": recordID})
				if detailErr != nil {
					return detailErr
				}
				central, _ := detail["record"].(map[string]any)
				if central["id"] != recordID || central["clientSavedAt"] == nil {
					return fmt.Errorf("central retry did not recover the committed record and timestamp: %v", central)
				}
				fmt.Printf("PASS central-response-retry: stable retry recovered committed record %s and original timestamp\n", recordID)
				return nil
			}
			if time.Now().After(deadline) {
				return fmt.Errorf("central retry did not reach a truthful delivered receipt: %v", receipt)
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	if *phase == "outage-capture" {
		args := map[string]any{
			"workingDirectory": absWorkspace, "requestId": "restart-outage-capture",
			"title": "Synthetic capture retained during server outage", "authority": "observed", "confidence": "high",
			"finding": "The durable service retained this while Scratchpad HTTP was unavailable.",
		}
		out, err := call(ctx, session, "record_finding", args)
		if err != nil {
			return err
		}
		receipt, _ := out["receipt"].(map[string]any)
		if out["outcome"] != "saved_pending" || receipt["receiptId"] == nil || receipt["centralRecordId"] != nil || out["record"] != nil || out["project"] != nil {
			return fmt.Errorf("outage capture fabricated central confirmation: %v", out)
		}
		saved.Captures = []capture{{Tool: "record_finding", Arguments: args, ReceiptID: fmt.Sprint(receipt["receiptId"])}}
		data, err := json.MarshalIndent(saved, "", "  ")
		if err != nil {
			return err
		}
		if err = os.WriteFile(*statePath, append(data, '\n'), 0600); err != nil {
			return err
		}
		fmt.Println("PASS outage-capture: pending local receipt without central IDs")
		return nil
	}
	if *phase == "receipt" {
		data, err := os.ReadFile(*statePath)
		if err != nil {
			return err
		}
		if err = json.Unmarshal(data, &saved); err != nil || len(saved.Captures) != 1 || saved.Captures[0].ReceiptID == "" {
			return errors.New("state must contain one pending receipt")
		}
		item := saved.Captures[0]
		deadline := time.Now().Add(20 * time.Second)
		for {
			receipt, callErr := call(ctx, session, "get_capture_receipt", map[string]any{"receiptId": item.ReceiptID})
			if callErr != nil {
				return callErr
			}
			if receipt["state"] == "delivered" {
				item.RecordID, _ = receipt["centralRecordId"].(string)
				item.ProjectID, _ = receipt["centralProjectId"].(string)
				if item.RecordID == "" || item.ProjectID == "" {
					return errors.New("delivered receipt omitted central identities")
				}
				out, getErr := call(ctx, session, "get_record", map[string]any{"id": item.RecordID})
				if getErr != nil {
					return getErr
				}
				record, _ := out["record"].(map[string]any)
				if record["id"] != item.RecordID || record["projectId"] != item.ProjectID || record["clientSavedAt"] == nil {
					return fmt.Errorf("central record did not preserve the pending capture: %v", record)
				}
				retried, retryErr := call(ctx, session, item.Tool, item.Arguments)
				if retryErr != nil || retried["record"].(map[string]any)["id"] != item.RecordID {
					return fmt.Errorf("stable receipt retry changed central identity: %v %v", retried, retryErr)
				}
				fmt.Println("PASS receipt: pending capture survived service restart, delivered once and retries by original identity")
				return nil
			}
			if time.Now().After(deadline) {
				return fmt.Errorf("receipt did not deliver after restart: %v", receipt)
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	if *phase == "capture" || *phase == "records" {
		nonce := make([]byte, 12)
		if _, err = rand.Read(nonce); err != nil {
			return err
		}
		prefix := "integration-" + hex.EncodeToString(nonce)
		kinds := []struct {
			kind    string
			payload map[string]any
		}{
			{"decision", map[string]any{"decision": "Keep private project memory", "rationale": "Reproducible integration"}},
			{"adr", map[string]any{"decision": "Use SQLite", "context": "Disposable integration instance"}},
			{"business_decision", map[string]any{"decision": "Self hosted", "requestedBy": "integration"}},
			{"finding", map[string]any{"finding": "Cross-process capture works", "limitations": []string{"Hardware validation remains unresolved"}}},
			{"qa", map[string]any{"question": "Does real stdio reach persistence?", "answer": "Verified by rereading the record"}},
			{"failure", map[string]any{"observed": "Example captured failure", "lesson": "Preserve failure context"}},
			{"constraint", map[string]any{"constraint": "English only"}},
			{"project_state", map[string]any{"state": "paused", "reason": "Awaiting a disposable test device", "previousState": "active", "followUp": "Validate the remaining hardware behavior before resuming"}},
		}
		for _, kind := range kinds {
			args := kind.payload
			args["title"] = "Integration " + kind.kind
			args["requestId"] = prefix + "-" + kind.kind
			args["authority"] = "observed"
			args["confidence"] = "high"
			out, err := call(ctx, session, "record_"+kind.kind, args)
			if err != nil {
				return err
			}
			receipt, _ := out["receipt"].(map[string]any)
			if out["outcome"] != "delivered" || receipt["state"] != "delivered" {
				return fmt.Errorf("capture did not return a confirmed local receipt: %v", out)
			}
			record, ok := out["record"].(map[string]any)
			if !ok {
				return fmt.Errorf("missing record in %v", out)
			}
			project, ok := out["project"].(map[string]any)
			if !ok {
				return fmt.Errorf("missing resolved project in %v", out)
			}
			id, _ := record["id"].(string)
			pid, _ := project["id"].(string)
			receiptID, _ := receipt["receiptId"].(string)
			if id == "" || pid == "" || receiptID == "" || receipt["centralRecordId"] != id || receipt["centralProjectId"] != pid {
				return errors.New("missing record/project ID")
			}
			if *mirror && (kind.kind == "decision" || kind.kind == "adr" || kind.kind == "business_decision") {
				status, _ := out["mirror"].(map[string]any)
				if status["succeeded"] != true {
					return fmt.Errorf("mirror did not succeed: %v", status)
				}
			}
			gitContext, _ := record["gitContext"].(map[string]any)
			saved.Captures = append(saved.Captures, capture{Tool: "record_" + kind.kind, Arguments: args, RecordID: id, ProjectID: pid, ReceiptID: receiptID, GitContext: gitContext})
		}
		data, err := json.MarshalIndent(saved, "", "  ")
		if err != nil {
			return err
		}
		if err = os.WriteFile(*statePath, append(data, '\n'), 0600); err != nil {
			return err
		}
	} else {
		data, err := os.ReadFile(*statePath)
		if err != nil {
			return err
		}
		if err = json.Unmarshal(data, &saved); err != nil {
			return err
		}
		if len(saved.Captures) != 8 {
			return errors.New("state must contain all eight captures")
		}
	}
	if *phase == "read" {
		resumeContext, err := call(ctx, session, "get_project_context", map[string]any{"workingDirectory": absWorkspace})
		if err != nil {
			return err
		}
		if err = verifyResumeContext(resumeContext, saved.Captures); err != nil {
			return err
		}
		fmt.Println("PASS read: real stdio → SSH-agent possession → authenticated context in confirmed project")
		return nil
	}
	if *phase == "projects" {
		if *restrictedProject == "" {
			return errors.New("restricted-project is required for projects phase")
		}
		saved.ProjectScenarios, err = projectScenarios(ctx, session, absWorkspace, *restrictedProject)
		if err != nil {
			return err
		}
		data, err := json.MarshalIndent(saved, "", "  ")
		if err != nil {
			return err
		}
		if err = os.WriteFile(*statePath, append(data, '\n'), 0600); err != nil {
			return err
		}
		fmt.Println("PASS projects: actual API and real stdio; restricted checkout unchanged with local mirroring enabled; linked worktrees share identity and preserve provenance; held non-Git capture, explicit resolution and stable replay")
		return nil
	}
	items := append(append([]capture(nil), saved.Captures...), saved.ProjectScenarios...)
	if *phase == "verify" {
		items = saved.Captures
	} else if *phase == "verify-projects" {
		items = saved.ProjectScenarios
	}
	for _, item := range items {
		if item.NeedsSelection {
			receipt, callErr := call(ctx, session, "get_capture_receipt", map[string]any{"receiptId": item.ReceiptID})
			content, _ := receipt["content"].(map[string]any)
			scope, _ := content["scope"].(map[string]any)
			expectedDirectory, _ := item.Arguments["workingDirectory"].(string)
			if callErr != nil || receipt["state"] != "needs_selection" || receipt["centralRecordId"] != nil || receipt["centralProjectId"] != nil || scope["workingDirectory"] != expectedDirectory {
				return fmt.Errorf("ambiguous capture was not preserved for owner selection after restart: %v %v", receipt, callErr)
			}
			if *phase == "verify" || *phase == "verify-projects" || *phase == "records" {
				replayed, replayErr := call(ctx, session, item.Tool, item.Arguments)
				replayReceipt, _ := replayed["receipt"].(map[string]any)
				if replayErr != nil || replayed["outcome"] != "saved_pending" || replayReceipt["receiptId"] != item.ReceiptID || replayReceipt["state"] != "needs_selection" {
					return fmt.Errorf("held capture replay changed its receipt after restart: %v %v", replayed, replayErr)
				}
			}
			continue
		}
		if item.ReceiptID != "" {
			receipt, err := call(ctx, session, "get_capture_receipt", map[string]any{"receiptId": item.ReceiptID})
			if err != nil || receipt["state"] != "delivered" || receipt["centralRecordId"] != item.RecordID || receipt["centralProjectId"] != item.ProjectID {
				return fmt.Errorf("receipt lookup disagrees with central capture %s: %v %v", item.RecordID, receipt, err)
			}
		}
		out, err := call(ctx, session, "get_record", map[string]any{"id": item.RecordID})
		if err != nil {
			return err
		}
		record, ok := out["record"].(map[string]any)
		if !ok {
			return fmt.Errorf("missing fetched record: %v", out)
		}
		if record["id"] != item.RecordID || record["projectId"] != item.ProjectID || record["type"] != strings.TrimPrefix(item.Tool, "record_") {
			return fmt.Errorf("record mismatch: %v", record)
		}
		if item.GitContext != nil {
			gitContext, _ := record["gitContext"].(map[string]any)
			if !reflect.DeepEqual(gitContext, item.GitContext) {
				return fmt.Errorf("record %s lost its original Git provenance snapshot", item.RecordID)
			}
		}
		payload, ok := record["payload"].(map[string]any)
		if !ok || len(payload) == 0 {
			return errors.New("typed payload was not persisted")
		}
		if content, ok := record["content"].(string); !ok || content == "" {
			return errors.New("deterministic content missing")
		}
		if *phase == "verify" || *phase == "verify-projects" || *phase == "records" {
			retried, err := call(ctx, session, item.Tool, item.Arguments)
			if err != nil {
				return fmt.Errorf("replay %s (%v): %w", item.Tool, item.Arguments["requestId"], err)
			}
			centralRecordID, _ := retried["centralRecordId"].(string)
			receipt, _ := retried["receipt"].(map[string]any)
			if centralRecordID == "" {
				centralRecordID, _ = receipt["centralRecordId"].(string)
			}
			if centralRecordID != item.RecordID || retried["outcome"] != "delivered" {
				return errors.New("retry created a duplicate")
			}
		}
	}
	if *phase == "records" {
		search, err := call(ctx, session, "search_memory", map[string]any{"projectId": saved.Captures[0].ProjectID, "query": "Integration", "limit": 100})
		if err != nil {
			return err
		}
		encoded, err := json.Marshal(search)
		if err != nil {
			return err
		}
		for _, item := range saved.Captures {
			if !strings.Contains(string(encoded), item.RecordID) {
				return errors.New("lexical search omitted a captured record")
			}
		}
		fmt.Println("PASS records: all eight captures, immutable details, stable retries and lexical search through real stdio and authenticated HTTP")
		return nil
	}
	projectID := saved.Captures[0].ProjectID
	resumeContext, err := call(ctx, session, "get_project_context", map[string]any{"workingDirectory": absWorkspace})
	if err != nil {
		return err
	}
	if err = verifyResumeContext(resumeContext, saved.Captures); err != nil {
		return err
	}
	search, err := call(ctx, session, "search_memory", map[string]any{"projectId": projectID, "query": "Integration", "limit": 100})
	if err != nil {
		return err
	}
	encoded, _ := json.Marshal(search)
	for _, item := range saved.Captures {
		if !strings.Contains(string(encoded), item.RecordID) {
			return fmt.Errorf("search omitted captured record %s: %s", item.RecordID, encoded)
		}
	}
	if _, err = call(ctx, session, "get_decision_history", map[string]any{"projectId": projectID}); err != nil {
		return err
	}
	if _, err = call(ctx, session, "find_related", map[string]any{"id": saved.Captures[0].RecordID}); err != nil {
		return err
	}
	if *phase == "verify" {
		changed := map[string]any{}
		for key, value := range saved.Captures[0].Arguments {
			changed[key] = value
		}
		changed["decision"] = "Changed content must conflict"
		result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: saved.Captures[0].Tool, Arguments: changed})
		if err != nil {
			return err
		}
		data, _ := json.Marshal(result)
		if !result.IsError || !strings.Contains(string(data), "CONFLICT") {
			return fmt.Errorf("changed replay did not report CONFLICT: %s", data)
		}
	}
	fmt.Printf("PASS %s: real stdio → SSH challenge → HTTP → persistent records; 8 typed captures, search, context, history, relationships", *phase)
	if *phase == "verify" || *phase == "verify-projects" {
		fmt.Print("; restart persistence, exact replay, changed-payload conflict")
		if len(saved.ProjectScenarios) > 0 {
			fmt.Printf("; %d project-boundary captures survive restore", len(saved.ProjectScenarios))
		}
	}
	fmt.Println()
	return nil
}

// Verify the information a new agent needs to resume the disposable project,
// rather than treating a successful context request as semantic acceptance.
func verifyResumeContext(context map[string]any, captures []capture) error {
	fields := map[string]string{
		"record_decision": "recentDecisions", "record_adr": "recentDecisions", "record_business_decision": "recentDecisions",
		"record_finding": "openFindings", "record_failure": "failures", "record_constraint": "constraints", "record_project_state": "stateHistory",
	}
	for _, item := range captures {
		field, relevant := fields[item.Tool]
		if !relevant {
			continue
		}
		list, _ := context[field].([]any)
		var linked map[string]any
		for _, value := range list {
			record, _ := value.(map[string]any)
			if record["id"] == item.RecordID {
				linked = record
				break
			}
		}
		if linked == nil || linked["projectId"] != item.ProjectID {
			return fmt.Errorf("resume context omitted linked %s capture %s", field, item.RecordID)
		}
		if item.Tool == "record_project_state" {
			current, _ := context["currentState"].(map[string]any)
			payload, _ := current["payload"].(map[string]any)
			if current["id"] != item.RecordID {
				return errors.New("resume context did not identify the current state source")
			}
			for _, name := range []string{"state", "reason", "previousState", "followUp"} {
				if payload[name] != item.Arguments[name] {
					return fmt.Errorf("resume context lost project-state %s", name)
				}
			}
		}
	}
	return nil
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func centralResponseDropInput(workingDirectory string) map[string]any {
	return map[string]any{
		"workingDirectory": workingDirectory, "requestId": "central-committed-response-drop",
		"title": "Synthetic central response lost after commit", "authority": "observed", "confidence": "high",
		"finding": "The backend commits this record before a test proxy discards its first response.",
	}
}

func call(ctx context.Context, session *mcp.ClientSession, name string, args map[string]any) (map[string]any, error) {
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	if result.IsError {
		data, _ := json.Marshal(result)
		return nil, fmt.Errorf("%s: %s", name, data)
	}
	data, err := json.Marshal(result.StructuredContent)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if string(data) != "null" {
		if err = json.Unmarshal(data, &out); err != nil {
			return nil, err
		}
		return out, nil
	}
	for _, content := range result.Content {
		if text, ok := content.(*mcp.TextContent); ok {
			if err = json.Unmarshal([]byte(text.Text), &out); err == nil {
				return out, nil
			}
		}
	}
	return nil, fmt.Errorf("%s returned no JSON object", name)
}
