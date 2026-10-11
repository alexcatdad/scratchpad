package httpapi

import (
	"context"
	"database/sql"
	"net/http"
)

var relationshipTypes = []string{"related_to", "supports", "contradicts", "refines", "replaces", "partially_replaces", "depends_on", "implements", "caused_by", "answers"}

func relationshipMutation(ctx context.Context, conn *sql.Conn, r *http.Request, body, actor map[string]any, operation string) (map[string]any, error) {
	if operation == "relationship-create" {
		from, err := captureText(body["fromRecordId"], 100000)
		if err != nil {
			return nil, err
		}
		to, err := captureText(body["toRecordId"], 100000)
		if err != nil {
			return nil, err
		}
		kind, err := captureEnum(body["type"], relationshipTypes)
		if err != nil {
			return nil, err
		}
		authority := any("explicit")
		if value, exists := body["authority"]; exists {
			authority = value
		}
		level, err := captureEnum(authority, []string{"explicit", "inferred", "suggested"})
		if err != nil {
			return nil, err
		}
		if _, err = curationEntity(ctx, conn, "record", from); err != nil {
			return nil, err
		}
		if _, err = curationEntity(ctx, conn, "record", to); err != nil {
			return nil, err
		}
		if from == to {
			return nil, &protocolError{"INVALID_RELATIONSHIP", "A record cannot relate to itself.", 400}
		}
		if level != "suggested" {
			if err = relationshipSafe(ctx, conn, from, to, kind, ""); err != nil {
				return nil, err
			}
		}
		status := "accepted"
		if level == "suggested" {
			status = "suggested"
		}
		link := map[string]any{"fromRecordId": from, "toRecordId": to, "type": kind, "authority": level, "status": status}
		if value, exists := body["note"]; exists {
			link["note"], err = curationText(value, 10000)
			if err != nil {
				return nil, err
			}
		}
		id, err := entityID("rel")
		if err != nil {
			return nil, err
		}
		if err = insert(ctx, conn, "relationship", id, link); err != nil {
			return nil, err
		}
		if err = apiAudit(ctx, conn, "relationship.created", "relationship", id, actor, nil, link); err != nil {
			return nil, err
		}
		return map[string]any{"relationship": link}, nil
	}
	link, err := curationEntity(ctx, conn, "relationship", r.PathValue("id"))
	if err != nil {
		return nil, err
	}
	expected, err := expectedVersion(body, r)
	if err != nil {
		return nil, err
	}
	if link["version"] != expected {
		return nil, &versionConflict{link["version"]}
	}
	previous, err := copyEntity(link)
	if err != nil {
		return nil, err
	}
	status := "rejected"
	action := "reject"
	if operation == "relationship-accept" {
		if err = relationshipSafe(ctx, conn, stringValue(link, "fromRecordId"), stringValue(link, "toRecordId"), stringValue(link, "type"), stringValue(link, "id")); err != nil {
			return nil, err
		}
		status = "accepted"
		action = "accept"
		link["authority"] = "explicit"
	}
	link["status"] = status
	if err = update(ctx, conn, "relationship", link); err != nil {
		return nil, err
	}
	if err = apiAudit(ctx, conn, "relationship."+action, "relationship", stringValue(link, "id"), actor, previous, link); err != nil {
		return nil, err
	}
	return map[string]any{"relationship": link}, nil
}

func relationshipSafe(ctx context.Context, conn *sql.Conn, from, to, kind, excluded string) error {
	if from == to {
		return &protocolError{"INVALID_RELATIONSHIP", "A record cannot relate to itself.", 400}
	}
	if kind != "replaces" && kind != "partially_replaces" && kind != "depends_on" {
		return nil
	}
	links, err := entityList(ctx, conn, "relationship")
	if err != nil {
		return err
	}
	edges := map[string][]string{}
	for _, link := range links {
		if link["status"] != "accepted" || link["id"] == excluded {
			continue
		}
		same := link["type"] == "replaces" || link["type"] == "partially_replaces"
		if kind == "depends_on" {
			same = link["type"] == "depends_on"
		}
		if same {
			source := stringValue(link, "fromRecordId")
			edges[source] = append(edges[source], stringValue(link, "toRecordId"))
		}
	}
	pending := []string{to}
	seen := map[string]bool{}
	for len(pending) > 0 {
		next := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if next == from {
			return &protocolError{"RELATIONSHIP_CYCLE", "This relationship creates a cycle.", 409}
		}
		if seen[next] {
			continue
		}
		seen[next] = true
		pending = append(pending, edges[next]...)
	}
	return nil
}
