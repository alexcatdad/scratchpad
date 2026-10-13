package backend_test

import (
	"encoding/json"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func retainedLegacyImporter(t *testing.T) string {
	t.Helper()
	path, _ := portableInstance(t, "sqlite")
	root, _ := filepath.Abs("..")
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	listener.Close()
	origin := "http://" + address
	apiPath, _ := json.Marshal(filepath.Join(root, "apps/web/src/server/api.ts"))
	fixture := filepath.Join(t.TempDir(), "legacy-retained.mjs")
	source := `import {createServer} from 'node:http';
import {createApi} from ` + string(apiPath) + `;
const origin=process.env.SCRATCHPAD_PUBLIC_URL;
const api=createApi({origin,databasePath:process.env.SCRATCHPAD_DATABASE_PATH});
const server=createServer(async(req,res)=>{try{
 const chunks=[];for await(const chunk of req)chunks.push(chunk);
 const headers=new Headers();for(const[name,value]of Object.entries(req.headers))if(value!==undefined)headers.set(name,String(value));
 const result=await api.handleRequest(new Request(new URL(req.url,origin),{method:req.method,headers,...(req.method==='GET'||req.method==='HEAD'?{}:{body:Buffer.concat(chunks)})}),req.socket.remoteAddress);
 res.writeHead(result.status,Object.fromEntries(result.headers));res.end(Buffer.from(await result.arrayBuffer()));
}catch{res.writeHead(500).end();}});
server.listen(Number(new URL(origin).port),'127.0.0.1');
process.once('SIGTERM',()=>{server.close(async()=>{await api.close();process.exit(0);});server.closeAllConnections();});`
	if err = os.WriteFile(fixture, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("node", "--import", filepath.Join(root, "node_modules/tsx/dist/loader.mjs"), fixture)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "SCRATCHPAD_DATABASE_PATH="+path, "SCRATCHPAD_DATABASE_URL=", "SCRATCHPAD_PUBLIC_URL="+origin, "SCRATCHPAD_GITHUB_CLIENT_ID=", "SCRATCHPAD_GITHUB_CLIENT_SECRET=")
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	t.Cleanup(func() {
		cmd.Process.Signal(syscall.SIGTERM)
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("retained importer exit: %v", err)
			}
		case <-time.After(10 * time.Second):
			cmd.Process.Kill()
			<-done
			t.Error("retained importer did not stop")
		}
	})
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		response, err := http.Get(origin + "/ready")
		if err == nil {
			response.Body.Close()
			if response.StatusCode == 200 {
				return origin
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("retained importer not ready")
	return ""
}

func TestLegacyNumericIdentityMatchesRetainedImporterWithoutRoundingEvidence(t *testing.T) {
	retained := retainedLegacyImporter(t)
	status, resolved := projectRequest(t, retained, "POST", "/resolve-explicit", map[string]any{"name": "Numeric identity parity"}, restoredToken)
	if status != 200 {
		t.Fatal("retained project fixture failed")
	}
	project := resolved["project"].(map[string]any)["id"].(string)
	lines := []string{`{"finding":"Exponent evidence","n":1e2}`, `{"finding":"Decimal evidence","n":1.0}`, `{"finding":"Negative zero evidence","n":-0}`, `{"finding":"Small exponent evidence","n":1e-7}`, `{"finding":"Large exponent evidence","n":1e21}`, `{"id":"legacy_precise_retained","finding":"Exact raw evidence","payload":{"counter":9007199254740993}}`, `{"finding":"Anonymous precise identity","n":9007199254740993}`, `{"finding":"Overflow identity","n":1e400}`}
	input := map[string]any{"format": "jsonl", "projectId": project, "sourceName": "numeric-parity.jsonl", "jsonl": strings.Join(lines, "\n")}
	status, golden := curationRequest(t, retained, "POST", "/import", input)
	if status != 200 || golden["imported"] != float64(len(lines)) {
		t.Fatal("actual retained importer fixture failed")
	}
	_, archive := curationRequest(t, retained, "POST", "/export", map[string]any{})
	fingerprints := map[string]any{}
	for _, value := range archive["data"].(map[string]any)["record"].([]any) {
		entry := value.(map[string]any)
		fingerprints[entry["id"].(string)] = entry["provenance"].(map[string]any)["import"].(map[string]any)["sha256"]
	}
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			path, databaseURL := portableInstance(t, engine)
			origin := startServer(t, path, databaseURL)
			projectEntry := archive["data"].(map[string]any)["project"]
			status, _ := curationRequest(t, origin, "POST", "/import", map[string]any{"format": "scratchpad", "version": 1, "data": map[string]any{"project": projectEntry}})
			if status != 200 {
				t.Fatal("Go project fixture failed")
			}
			status, result := curationRequest(t, origin, "POST", "/import", input)
			if status != 200 {
				t.Fatal("Go numerical import failed")
			}
			for i, value := range result["records"].([]any) {
				if value.(map[string]any)["recordId"] != golden["records"].([]any)[i].(map[string]any)["recordId"] {
					t.Fatalf("numeric identity differs from retained importer on line%d", i+1)
				}
				id := value.(map[string]any)["recordId"].(string)
				status, detail := numericArchiveRequest(t, origin, "GET", "/records/"+id, "")
				if status != 200 || detail["record"].(map[string]any)["provenance"].(map[string]any)["import"].(map[string]any)["sha256"] != fingerprints[id] {
					t.Fatal("source fingerprint differs from actual retained importer")
				}
			}
			id := result["records"].([]any)[0].(map[string]any)["recordId"].(string)
			status, detail := numericArchiveRequest(t, origin, "GET", "/records/"+id, "")
			if status != 200 || detail["record"].(map[string]any)["provenance"].(map[string]any)["import"].(map[string]any)["rawLine"] != lines[0] {
				t.Fatal("identity canonicalization rounded stored evidence")
			}
			status, precise := numericArchiveRequest(t, origin, "GET", "/records/legacy_precise_retained", "")
			if status != 200 || precise["record"].(map[string]any)["payload"].(map[string]any)["counter"] != json.Number("9007199254740993") {
				t.Fatal("identity canonicalization rounded precise payload evidence")
			}
			input["jsonl"] = `{"finding":"Exponent evidence","n":100}`
			status, result = curationRequest(t, origin, "POST", "/import", input)
			if status != 200 || result["imported"] != float64(0) || result["records"].([]any)[0].(map[string]any)["status"] != "skipped" {
				t.Fatal("equivalent numeric spelling did not replay")
			}
			input["jsonl"] = strings.Join(lines, "\n")
			for _, withRawLine := range []bool{true, false} {
				freshPath, freshURL := portableInstance(t, engine)
				fresh := startServer(t, freshPath, freshURL)
				encoded, _ := json.Marshal(archive)
				var historical map[string]any
				json.Unmarshal(encoded, &historical)
				if !withRawLine {
					for _, value := range historical["data"].(map[string]any)["record"].([]any) {
						entry := value.(map[string]any)
						if entry["id"] == id {
							delete(entry["provenance"].(map[string]any)["import"].(map[string]any), "rawLine")
						}
					}
				}
				status, _ := curationRequest(t, fresh, "POST", "/import", historical)
				if status != 200 {
					t.Fatal("actual retained archive cannot restore into Go")
				}
				replayLines := lines
				if !withRawLine {
					replayLines = lines[:1]
				}
				status, replay := curationRequest(t, fresh, "POST", "/import", map[string]any{"format": "jsonl", "projectId": project, "sourceName": "numeric-parity.jsonl", "jsonl": strings.Join(replayLines, "\n")})
				if status != 200 || replay["imported"] != float64(0) || replay["skipped"] != float64(len(replayLines)) {
					t.Fatal("retained numerical archive replay failed")
				}
				for _, value := range replay["records"].([]any) {
					if value.(map[string]any)["status"] != "skipped" {
						t.Fatal("actual retained source numbers conflicted during replay")
					}
				}
			}
		})
	}
}
