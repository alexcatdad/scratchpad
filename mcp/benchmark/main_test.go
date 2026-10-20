package main

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestConfirmCaptureWaitsForMatchingDeliveredReceiptAndReadsCentralRecord(t *testing.T) {
	args := captureArgs("proj_expected", "synthetic-agent-0-round-0")
	calls := 0
	callTool := func(_ context.Context, tool string, input map[string]any) (map[string]any, int, error) {
		calls++
		switch tool {
		case "record_finding":
			return map[string]any{"outcome": "saved_pending", "receipt": map[string]any{"receiptId": "receipt-1", "state": "pending"}}, 11, nil
		case "get_capture_receipt":
			if input["receiptId"] != "receipt-1" {
				t.Fatalf("looked up unexpected receipt: %v", input)
			}
			return map[string]any{"receiptId": "receipt-1", "state": "delivered", "centralRecordId": "rec-1", "centralProjectId": "proj_expected"}, 13, nil
		case "get_record":
			if input["id"] != "rec-1" {
				t.Fatalf("looked up unexpected record: %v", input)
			}
			return map[string]any{"record": map[string]any{
				"id": "rec-1", "projectId": "proj_expected", "title": args["title"],
				"payload": map[string]any{"finding": args["finding"]},
			}}, 17, nil
		default:
			t.Fatalf("unexpected tool call: %s", tool)
			return nil, 0, nil
		}
	}

	record, size, err := confirmCapture(context.Background(), callTool, "proj_expected", args, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if record["id"] != "rec-1" || size != 41 || calls != 3 {
		t.Fatalf("capture was not confirmed and fully measured: record=%v bytes=%d calls=%d", record, size, calls)
	}
}

func TestConfirmCaptureAcceptsDeliveredReceiptWithoutEmbeddedRecord(t *testing.T) {
	args := captureArgs("proj_expected", "synthetic-agent-0-round-0")
	callTool := func(_ context.Context, tool string, _ map[string]any) (map[string]any, int, error) {
		switch tool {
		case "record_finding":
			return map[string]any{"outcome": "delivered", "receipt": map[string]any{
				"receiptId": "receipt-1", "state": "delivered", "centralRecordId": "rec-1", "centralProjectId": "proj_expected",
			}}, 5, nil
		case "get_record":
			return map[string]any{"record": map[string]any{
				"id": "rec-1", "projectId": "proj_expected", "title": args["title"],
				"payload": map[string]any{"finding": args["finding"]},
			}}, 7, nil
		default:
			t.Fatalf("unexpected tool call: %s", tool)
			return nil, 0, nil
		}
	}
	if _, size, err := confirmCapture(context.Background(), callTool, "proj_expected", args, time.Second); err != nil || size != 12 {
		t.Fatalf("receipt-only delivered replay was not confirmed: bytes=%d err=%v", size, err)
	}
}

func TestConfirmCaptureRejectsInvalidReceiptsAndCentralRecords(t *testing.T) {
	args := captureArgs("proj_expected", "synthetic-agent-0-round-0")
	tests := []struct {
		name    string
		initial map[string]any
		receipt map[string]any
		record  map[string]any
	}{
		{name: "missing receipt", initial: map[string]any{"outcome": "saved_pending"}},
		{name: "wrong receipt id", initial: map[string]any{"outcome": "saved_pending", "receipt": map[string]any{"receiptId": "receipt-1", "state": "pending"}}, receipt: map[string]any{"receiptId": "receipt-other", "state": "delivered", "centralRecordId": "rec-1", "centralProjectId": "proj_expected"}},
		{name: "missing central id", initial: map[string]any{"outcome": "delivered", "receipt": map[string]any{"receiptId": "receipt-1", "state": "delivered", "centralProjectId": "proj_expected"}}},
		{name: "wrong project", initial: map[string]any{"outcome": "delivered", "receipt": map[string]any{"receiptId": "receipt-1", "state": "delivered", "centralRecordId": "rec-1", "centralProjectId": "proj_other"}}},
		{name: "central record missing", initial: map[string]any{"outcome": "delivered", "receipt": map[string]any{"receiptId": "receipt-1", "state": "delivered", "centralRecordId": "rec-1", "centralProjectId": "proj_expected"}}},
		{name: "central payload changed", initial: map[string]any{"outcome": "delivered", "receipt": map[string]any{"receiptId": "receipt-1", "state": "delivered", "centralRecordId": "rec-1", "centralProjectId": "proj_expected"}}, record: map[string]any{"record": map[string]any{"id": "rec-1", "projectId": "proj_expected", "title": args["title"], "payload": map[string]any{"finding": "different"}}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			callTool := func(_ context.Context, tool string, _ map[string]any) (map[string]any, int, error) {
				switch tool {
				case "record_finding":
					return tt.initial, 1, nil
				case "get_capture_receipt":
					return tt.receipt, 1, nil
				case "get_record":
					if tt.record == nil {
						return nil, 1, errors.New("not found")
					}
					return tt.record, 1, nil
				default:
					return nil, 0, errors.New("unexpected tool")
				}
			}
			if _, _, err := confirmCapture(context.Background(), callTool, "proj_expected", args, time.Second); err == nil {
				t.Fatal("invalid capture outcome was counted as success")
			}
		})
	}
}

func TestConfirmCaptureStopsWhenDeliveryDeadlineExpires(t *testing.T) {
	args := captureArgs("proj_expected", "synthetic-agent-0-round-0")
	callTool := func(_ context.Context, tool string, _ map[string]any) (map[string]any, int, error) {
		if tool == "record_finding" {
			return map[string]any{"outcome": "saved_pending", "receipt": map[string]any{"receiptId": "receipt-1", "state": "pending"}}, 1, nil
		}
		return nil, 0, errors.New("unexpected lookup after short deadline")
	}
	if _, _, err := confirmCapture(context.Background(), callTool, "proj_expected", args, time.Millisecond); err == nil {
		t.Fatal("pending capture exceeded its delivery deadline without failure")
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := confirmCapture(canceled, callTool, "proj_expected", args, time.Second); err == nil {
		t.Fatal("canceled workload context was counted as a capture")
	}
}
