package httpapi

import (
	"context"
	"database/sql"
	"net/http"

	"github.com/alexcatdad/scratchpad/backend/internal/domain"
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
	current := domain.RelationshipFrom(link)
	if !current.HasVersion || current.Version != expected {
		return nil, &versionConflict{link["version"]}
	}
	previous, err := copyEntity(link)
	if err != nil {
		return nil, err
	}
	status := "rejected"
	action := "reject"
	if operation == "relationship-accept" {
		if err = relationshipSafe(ctx, conn, current.FromRecordID, current.ToRecordID, current.Type, current.ID); err != nil {
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
	firstType, secondType := "replaces", "partially_replaces"
	if kind == "depends_on" {
		firstType, secondType = kind, kind
	}
	// Both supported engines implement ->> for JSON fields. UNION deduplicates
	// visited vertices, including preexisting cyclic historical input. The graph
	// intentionally spans projects: project scope cannot make a real cycle safe.
	var cycle bool
	err := conn.QueryRowContext(ctx, `WITH RECURSIVE reachable(record_id) AS (
	 SELECT CAST($1 AS TEXT)
	 UNION
	 SELECT e.data->>'toRecordId' FROM entities e JOIN reachable r ON e.data->>'fromRecordId'=r.record_id
	 WHERE e.kind='relationship' AND e.data->>'status'='accepted'
	 AND e.data->>'type' IN ($3,$4)
	 AND CAST(e.data->'fromRecordId' AS TEXT) LIKE '"%'
	 AND CAST(e.data->'toRecordId' AS TEXT) LIKE '"%'
	 AND (e.data->>'id' IS NULL OR CAST(e.data->'id' AS TEXT) NOT LIKE '"%' OR e.data->>'id'<>$5)
	) SELECT EXISTS(SELECT 1 FROM reachable WHERE record_id=$2)`, to, from, firstType, secondType, excluded).Scan(&cycle)
	if err != nil {
		return err
	}
	if cycle {
		return &protocolError{"RELATIONSHIP_CYCLE", "This relationship creates a cycle.", 409}
	}
	return nil
}
