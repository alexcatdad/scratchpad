package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"golang.org/x/net/idna"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/alexcatdad/scratchpad/backend/internal/domain"
)

var projectSlugPattern = regexp.MustCompile(`[^a-z0-9]+`)
var recordTypes = domain.RecordTypes()

func projectDefaults(kind string) map[string]any {
	return domain.DefaultProjectSettings(kind).Fields()
}
func apiActor(ctx context.Context, q entityReader, r *http.Request, origin string) (map[string]any, error) {
	if err := authorize(ctx, q, r); err != nil {
		if errors.Is(err, errAuthRequired) {
			return nil, invalidProof("AUTH_REQUIRED", "Sign in to Scratchpad.")
		}
		if errors.Is(err, errUnauthorized) {
			return nil, invalidProof("AUTH_INVALID", "Invalid session.")
		}
		return nil, err
	}
	matches := bearerPattern.FindStringSubmatch(r.Header.Get("Authorization"))
	token, kind := "", "user"
	if len(matches) == 2 {
		token, kind = matches[1], "agent"
	} else {
		cookie, _ := r.Cookie("scratchpad_session")
		token = cookie.Value
		if r.Method != "GET" && r.Method != "HEAD" && r.Header.Get("Origin") != origin {
			return nil, &protocolError{"AUTH_INVALID", "Invalid request origin.", 403}
		}
	}
	session, err := entityMap(ctx, q, "session", tokenID(token))
	if err != nil {
		return nil, err
	}
	credential, err := entityMap(ctx, q, "credential", stringValue(session, "credentialId"))
	if err != nil {
		return nil, err
	}
	fingerprint := stringValue(credential, "fingerprint")
	if fingerprint == "" {
		fingerprint = stringValue(credential, "id")
	}
	return map[string]any{"kind": kind, "credentialFingerprint": fingerprint}, nil
}
func projectEntity(ctx context.Context, q entityReader, id string) (map[string]any, error) {
	value, err := entityMap(ctx, q, "project", id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, &protocolError{"PROJECT_NOT_FOUND", "Project not found.", 404}
	}
	if err != nil {
		return nil, err
	}
	project, err := domain.ProjectFrom(value)
	if err != nil {
		return nil, validationProject()
	}
	value["settings"] = project.Settings.Fields()
	return value, err
}
func createProject(ctx context.Context, conn *sql.Conn, name, kind string) (map[string]any, error) {
	settings := projectDefaults(kind)
	if kind != "external" {
		raw, err := optional(ctx, conn, "settings", "global")
		if err != nil {
			return nil, err
		}
		if raw != nil {
			var global map[string]any
			if err = json.Unmarshal(raw, &global); err != nil {
				return nil, err
			}
			if _, ok := global["defaultProjectSettings"].(map[string]any); ok {
				defaults := domain.GlobalSettingsFrom(global).DefaultProjectSettings
				if defaults == nil {
					return nil, validationProject()
				}
				settings = defaults.Fields()
			}
		}
	}
	id, err := entityID("proj")
	if err != nil {
		return nil, err
	}
	settings, err = normalizeSettings(settings)
	if err != nil {
		return nil, err
	}
	project := map[string]any{"name": name, "slug": projectSlugPattern.ReplaceAllString(strings.ToLower(name), "-"), "kind": kind, "settings": settings, "updatedAt": timestamp(time.Now())}
	return project, insert(ctx, conn, "project", id, project)
}
func validationProject() error {
	return &protocolError{"VALIDATION_FAILED", "Invalid project request.", 400}
}
func projectName(value any) (string, error) {
	name, ok := value.(string)
	name = strings.TrimSpace(name)
	if !ok || name == "" || len([]rune(name)) > 100000 {
		return "", validationProject()
	}
	return name, nil
}
func (s *SSH) Projects(operation string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		if operation == "list" || operation == "detail" || operation == "settings" {
			tx, err := s.Store.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
			if err != nil {
				s.fail(w, err)
				return
			}
			defer tx.Rollback()
			if _, err = apiActor(ctx, tx, r, s.Origin); err != nil {
				s.fail(w, err)
				return
			}
			if operation == "detail" || operation == "settings" {
				project, err := projectEntity(ctx, tx, r.PathValue("id"))
				if err != nil {
					s.fail(w, err)
					return
				}
				if operation == "settings" {
					JSON(w, 200, map[string]any{"settings": project["settings"], "version": project["version"]})
					return
				}
				JSON(w, 200, map[string]any{"project": project})
				return
			}
			projects, err := entityList(ctx, tx, "project")
			if err != nil {
				s.fail(w, err)
				return
			}
			sources, err := entityList(ctx, tx, "source")
			if err != nil {
				s.fail(w, err)
				return
			}
			for _, project := range projects {
				project["settings"], err = normalizeSettings(project["settings"])
				if err != nil {
					s.fail(w, err)
					return
				}
				selected := []map[string]any{}
				for _, source := range sources {
					if source["projectId"] == project["id"] {
						selected = append(selected, source)
					}
				}
				project["sources"] = selected
			}
			JSON(w, 200, map[string]any{"projects": projects})
			return
		}
		var body map[string]any
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024*1024))
		if decoder.Decode(&body) != nil || body == nil || decoder.Decode(new(any)) != io.EOF {
			s.fail(w, validationProject())
			return
		}
		var result map[string]any
		err := s.Store.Write(ctx, func(conn *sql.Conn) error {
			actor, err := apiActor(ctx, conn, r, s.Origin)
			if err != nil {
				return err
			}
			if operation == "update" || operation == "settings-update" {
				result, err = updateProject(ctx, conn, r, body, actor)
				return err
			}
			var project map[string]any
			if operation == "remote" {
				project, err = resolveRemoteProject(ctx, conn, body, actor)
				if err != nil {
					return err
				}
				result = map[string]any{"project": project, "resolution": map[string]any{"source": "git_remote", "confidence": "high"}}
				return nil
			}
			if body["projectId"] != nil {
				id, err := projectName(body["projectId"])
				if err != nil {
					return err
				}
				project, err = projectEntity(ctx, conn, id)
				if err != nil {
					return err
				}
			} else {
				name, err := projectName(body["name"])
				if err != nil {
					return err
				}
				kind := "normal"
				if raw, exists := body["kind"]; exists {
					kind, _ = raw.(string)
					if kind != "normal" && kind != "external" {
						return validationProject()
					}
				}
				projects, err := entityList(ctx, conn, "project")
				if err != nil {
					return err
				}
				for _, candidate := range projects {
					if strings.ToLower(stringValue(candidate, "name")) == strings.ToLower(name) {
						if project != nil {
							return &protocolError{"PROJECT_IDENTITY_AMBIGUOUS", "Select an existing project by projectId.", 409}
						}
						project = candidate
					}
				}
				if project == nil {
					project, err = createProject(ctx, conn, name, kind)
					if err != nil {
						return err
					}
					if err = apiAudit(ctx, conn, "project.created", "project", stringValue(project, "id"), actor, nil, project); err != nil {
						return err
					}
				}
			}
			result = map[string]any{"project": project, "resolution": map[string]any{"source": "manual", "confidence": "high"}}
			return nil
		})
		if err != nil {
			s.fail(w, err)
			return
		}
		JSON(w, 200, result)
	}
}

var scpRemotePattern = regexp.MustCompile(`^(?:[^@\s]+@)?([^:/\s]+):(.+)$`)

func normalizeRemote(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if !strings.Contains(raw, "://") {
		if match := scpRemotePattern.FindStringSubmatch(raw); match != nil {
			raw = "ssh://" + match[1] + "/" + match[2]
		}
	}
	target, err := url.Parse(raw)
	if err != nil || target.Hostname() == "" {
		return "", &protocolError{"PROJECT_IDENTITY_REQUIRED", "Use an absolute Git remote URL or SCP-style remote.", 400}
	}
	target.Scheme = strings.ToLower(target.Scheme)
	switch target.Scheme {
	case "http", "https", "ssh", "git":
	default:
		return "", &protocolError{"PROJECT_IDENTITY_REQUIRED", "Unsupported Git remote.", 400}
	}
	host := target.Hostname()
	if parsed := net.ParseIP(host); parsed != nil {
		host = parsed.String()
	} else if target.Scheme == "http" || target.Scheme == "https" {
		host, err = idna.New(idna.MapForLookup(), idna.StrictDomainName(false), idna.CheckHyphens(false), idna.BidiRule()).ToASCII(host)
		if err == nil {
			host, err = remoteIPv4(host)
		}
		if err != nil {
			return "", &protocolError{"PROJECT_IDENTITY_REQUIRED", "Invalid Git host.", 400}
		}
	}
	if net.ParseIP(host) == nil && target.Scheme != "http" && target.Scheme != "https" {
		var encoded strings.Builder
		for _, value := range []byte(host) {
			if value >= 128 {
				fmt.Fprintf(&encoded, "%%%02x", value)
			} else {
				encoded.WriteByte(value)
			}
		}
		host = encoded.String()
	}
	host = strings.ToLower(host)
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	port := target.Port()
	if (target.Scheme == "https" && port == "443") || (target.Scheme == "http" && port == "80") {
		port = ""
	}
	if port != "" {
		host += ":" + port
	}
	segments := []string{}
	for _, segment := range strings.Split(target.EscapedPath(), "/") {
		decoded, err := url.PathUnescape(segment)
		if err != nil {
			return "", validationProject()
		}
		switch decoded {
		case ".":
		case "..":
			if len(segments) > 0 {
				segments = segments[:len(segments)-1]
			}
		default:
			segments = append(segments, segment)
		}
	}
	path := strings.TrimSuffix(strings.Trim(strings.Join(segments, "/"), "/"), ".git")
	if path == "" || strings.HasSuffix(path, "/") {
		return "", &protocolError{"PROJECT_IDENTITY_REQUIRED", "Use a Git remote with a repository path.", 400}
	}
	return host + "/" + path, nil
}
func apiAudit(ctx context.Context, conn *sql.Conn, action, kind, id string, actor, previous, next map[string]any) error {
	auditID, err := entityID("audit")
	if err != nil {
		return err
	}
	value := map[string]any{"action": action, "entityType": kind, "entityId": id, "actor": actor}
	if previous != nil {
		value["previous"] = previous
	}
	if next != nil {
		value["next"] = next
	}
	return insert(ctx, conn, "audit", auditID, value)
}
func resolveRemoteProject(ctx context.Context, conn *sql.Conn, body, actor map[string]any) (map[string]any, error) {
	contextValue, _ := body["context"].(map[string]any)
	git, _ := contextValue["git"].(map[string]any)
	raw, ok := git["remote"].(string)
	if !ok {
		return nil, &protocolError{"PROJECT_IDENTITY_REQUIRED", "A Git remote or explicit project selection is required.", 400}
	}
	identity, err := normalizeRemote(raw)
	if err != nil {
		return nil, err
	}
	var projectID string
	err = conn.QueryRowContext(ctx, `SELECT project_id FROM identities WHERE identity=$1`, identity).Scan(&projectID)
	if err == nil {
		return projectEntity(ctx, conn, projectID)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	parts := strings.Split(identity, "/")
	name := parts[len(parts)-1]
	project, err := createProject(ctx, conn, name, "normal")
	if err != nil {
		return nil, err
	}
	if _, err = conn.ExecContext(ctx, `INSERT INTO identities(identity,project_id) VALUES($1,$2)`, identity, project["id"]); err != nil {
		return nil, err
	}
	sourceID, err := entityID("source")
	if err != nil {
		return nil, err
	}
	if err = insert(ctx, conn, "source", sourceID, map[string]any{"projectId": project["id"], "kind": "git_remote", "identity": identity}); err != nil {
		return nil, err
	}
	if err = apiAudit(ctx, conn, "project.created", "project", stringValue(project, "id"), actor, nil, project); err != nil {
		return nil, err
	}
	return project, nil
}

type versionConflict struct{ current any }

func (e *versionConflict) Error() string { return "CONFLICT" }
func expectedVersion(body map[string]any, r *http.Request) (float64, error) {
	return expectedVersionMinimum(body, r, 1)
}
func expectedVersionMinimum(body map[string]any, r *http.Request, minimum float64) (float64, error) {
	raw := body["expectedVersion"]
	if raw == nil {
		if header := r.Header.Get("If-Match"); header != "" {
			raw = strings.ReplaceAll(header, `"`, "")
		}
	}
	if raw == nil {
		return 0, &protocolError{"PRECONDITION_REQUIRED", "Supply expectedVersion or If-Match.", 428}
	}
	value, err := strconv.ParseFloat(fmt.Sprint(raw), 64)
	if err != nil || value < minimum || math.IsNaN(value) || math.IsInf(value, 0) || value != math.Trunc(value) {
		return 0, validationProject()
	}
	return value, nil
}
func normalizeSettings(raw any) (map[string]any, error) {
	settings, err := domain.ParseProjectSettings(raw)
	if err != nil {
		return nil, validationProject()
	}
	return settings.Fields(), nil
}
func updateProject(ctx context.Context, conn *sql.Conn, r *http.Request, body, actor map[string]any) (map[string]any, error) {
	project, err := projectEntity(ctx, conn, r.PathValue("id"))
	if err != nil {
		return nil, err
	}
	expected, err := expectedVersion(body, r)
	if err != nil {
		return nil, err
	}
	state, err := domain.ProjectFrom(project)
	if err != nil {
		return nil, validationProject()
	}
	if !state.HasVersion || state.Version != expected {
		return nil, &versionConflict{project["version"]}
	}
	encoded, err := json.Marshal(project)
	if err != nil {
		return nil, err
	}
	var previous map[string]any
	if err = json.Unmarshal(encoded, &previous); err != nil {
		return nil, err
	}
	if strings.HasSuffix(r.URL.Path, "/settings") {
		raw := body["settings"]
		if raw == nil {
			raw = body
		}
		settings, err := normalizeSettings(raw)
		if err != nil {
			return nil, err
		}
		project["settings"] = settings
		if err = update(ctx, conn, "project", project); err != nil {
			return nil, err
		}
		if err = apiAudit(ctx, conn, "project.settings_updated", "project", stringValue(project, "id"), actor, previous, project); err != nil {
			return nil, err
		}
		return map[string]any{"settings": settings, "version": project["version"]}, nil
	}
	for key := range body {
		switch key {
		case "name", "kind", "expectedVersion":
		default:
			return nil, validationProject()
		}
	}
	if name, exists := body["name"]; exists {
		project["name"], err = projectName(name)
		if err != nil {
			return nil, err
		}
	}
	if raw, exists := body["kind"]; exists {
		kind, ok := raw.(string)
		if !ok || (kind != "normal" && kind != "external") {
			return nil, validationProject()
		}
		if kind == "external" && state.Kind != "external" {
			state.MakeExternal()
			project["settings"] = state.Settings.Fields()
		}
		project["kind"] = kind
	}
	if err = update(ctx, conn, "project", project); err != nil {
		return nil, err
	}
	if err = apiAudit(ctx, conn, "project.updated", "project", stringValue(project, "id"), actor, previous, project); err != nil {
		return nil, err
	}
	return map[string]any{"project": project}, nil
}

// Match the legacy URL parser's IPv4-number forms without resolving DNS.
func remoteIPv4(host string) (string, error) {
	parts := strings.Split(strings.TrimSuffix(host, "."), ".")
	number := func(token string) (uint64, error) {
		if token == "" {
			return 0, errors.New("empty IPv4 number")
		}
		base := 10
		if len(token) >= 2 && strings.EqualFold(token[:2], "0x") {
			base = 16
			token = token[2:]
		} else if len(token) >= 2 && token[0] == '0' {
			base = 8
			token = token[1:]
		}
		if token == "" {
			return 0, nil
		}
		return strconv.ParseUint(token, base, 32)
	}
	last := parts[len(parts)-1]
	_, err := number(last)
	digits := last != ""
	for _, value := range last {
		if value < '0' || value > '9' {
			digits = false
		}
	}
	if err != nil && !digits {
		return host, nil
	}
	if len(parts) > 4 {
		return "", errors.New("invalid IPv4 host")
	}
	values := make([]uint64, len(parts))
	for i, part := range parts {
		value, err := number(part)
		if err != nil {
			return "", err
		}
		if i < len(parts)-1 && value > 255 {
			return "", errors.New("invalid IPv4 host")
		}
		values[i] = value
	}
	if values[len(values)-1] >= uint64(1)<<uint(8*(5-len(values))) {
		return "", errors.New("invalid IPv4 host")
	}
	address := values[len(values)-1]
	for i := 0; i < len(values)-1; i++ {
		address += values[i] << uint(8*(3-i))
	}
	return fmt.Sprintf("%d.%d.%d.%d", address>>24, (address>>16)&255, (address>>8)&255, address&255), nil
}
