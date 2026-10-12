package httpapi

import (
	"context"
	"database/sql"
	"strings"
)

var artifactKinds = []string{"summary", "classification", "duplicate_candidate", "relationship_candidate", "contradiction", "cluster", "pattern", "recommendation", "export"}

type portableSources struct {
	projects map[string]bool
	records  map[string]map[string]any
}

func loadPortableSources(ctx context.Context, conn *sql.Conn) (*portableSources, error) {
	projects, err := entityList(ctx, conn, "project")
	if err != nil {
		return nil, err
	}
	records, err := entityList(ctx, conn, "record")
	if err != nil {
		return nil, err
	}
	snapshot := &portableSources{projects: map[string]bool{}, records: map[string]map[string]any{}}
	for _, project := range projects {
		snapshot.projects[stringValue(project, "id")] = true
	}
	for _, record := range records {
		snapshot.records[stringValue(record, "id")] = record
	}
	return snapshot, nil
}

func artifactInvalid() error {
	return &protocolError{"AI_ARTIFACT_INVALID", "Derived artifact content or references are invalid.", 400}
}
func privacyInvalid() error {
	return &protocolError{"AI_PRIVACY_INVALID", "AI privacy dependencies are invalid.", 400}
}
func portableIDs(value any) ([]string, bool) {
	array, ok := value.([]any)
	if !ok || len(array) == 0 {
		return nil, false
	}
	ids := []string{}
	for _, item := range array {
		if !portableID(item) {
			return nil, false
		}
		ids = append(ids, item.(string))
	}
	return ids, true
}
func uniqueStrings(values []string) bool {
	seen := map[string]bool{}
	for _, value := range values {
		if seen[value] {
			return false
		}
		seen[value] = true
	}
	return true
}

func validatePortableArtifact(ctx context.Context, conn *sql.Conn, section string, value map[string]any, snapshot *portableSources) error {
	if value["authority"] != "derived" || !contains(artifactKinds, stringValue(value, "kind")) {
		return artifactInvalid()
	}
	if _, ok := value["generator"].(map[string]any); !ok {
		return artifactInvalid()
	}
	sources, ok := portableIDs(value["sourceRecordIds"])
	if !ok || !uniqueStrings(sources) {
		return artifactInvalid()
	}
	projects, ok := portableIDs(value["projectIds"])
	if !ok || !uniqueStrings(projects) {
		return artifactInvalid()
	}
	if title, exists := value["title"]; exists {
		text, ok := title.(string)
		_, err := curationText(strings.TrimSpace(text), 500)
		if !ok || err != nil || strings.TrimSpace(text) == "" {
			return artifactInvalid()
		}
	}
	kind := stringValue(value, "kind")
	if contains([]string{"duplicate_candidate", "contradiction", "pattern", "relationship_candidate"}, kind) && len(sources) < 2 {
		return artifactInvalid()
	}
	if kind == "classification" && len(sources) != 1 {
		return artifactInvalid()
	}
	content, ok := value["content"].(map[string]any)
	if !ok {
		return artifactInvalid()
	}
	text, ok := content["text"].(string)
	_, err := curationText(strings.TrimSpace(text), 300)
	if !ok || err != nil || strings.TrimSpace(text) == "" {
		return artifactInvalid()
	}
	allowed := []string{"text", "markdown"}
	if markdown, exists := content["markdown"]; exists {
		if _, ok := markdown.(string); !ok {
			return artifactInvalid()
		}
	}
	if kind == "export" && strings.TrimSpace(stringValue(content, "markdown")) == "" {
		return artifactInvalid()
	}
	if kind == "classification" {
		allowed = append(allowed, "tags", "classification")
		if tags, exists := content["tags"]; exists {
			array, ok := tags.([]any)
			if !ok || len(array) > 20 {
				return artifactInvalid()
			}
			for _, item := range array {
				tag, err := curationText(item, 100)
				if err != nil || tag == "" {
					return artifactInvalid()
				}
			}
		}
		if class, exists := content["classification"]; exists {
			text, err := curationText(class, 200)
			if err != nil || text == "" {
				return artifactInvalid()
			}
		}
	}
	if kind == "relationship_candidate" || kind == "contradiction" {
		allowed = append(allowed, "fromRecordId", "toRecordId", "relationshipType")
		from, to := stringValue(content, "fromRecordId"), stringValue(content, "toRecordId")
		if from == to || !contains(sources, from) || !contains(sources, to) {
			return artifactInvalid()
		}
		if relationship, exists := content["relationshipType"]; exists {
			name, ok := relationship.(string)
			if !ok || !contains(relationshipTypes, name) || name == "replaces" || name == "partially_replaces" || (kind == "contradiction" && name != "contradicts") {
				return artifactInvalid()
			}
		}
	}
	for field := range content {
		if !contains(allowed, field) {
			return artifactInvalid()
		}
	}
	cited := map[string]bool{}
	for _, id := range projects {
		if !snapshot.projects[id] {
			return &protocolError{"PROJECT_NOT_FOUND", "Import references missing project.", 404}
		}
	}
	for _, id := range sources {
		record := snapshot.records[id]
		if record == nil {
			return &protocolError{"IMPORT_INVALID", "Import references missing record.", 404}
		}
		project := stringValue(record, "projectId")
		if !contains(projects, project) {
			return artifactInvalid()
		}
		cited[project] = true
	}
	if len(cited) != len(projects) {
		return artifactInvalid()
	}
	if raw, exists := value["privacyDependencies"]; exists {
		dependency, ok := raw.(map[string]any)
		if !ok || len(dependency) != 4 || !EqualJSONNumbers(dependency["version"], 1) {
			return privacyInvalid()
		}
		records, ok := portableIDs(dependency["recordIds"])
		if !ok {
			return privacyInvalid()
		}
		dependencyProjects, ok := portableIDs(dependency["projectIds"])
		if !ok {
			return privacyInvalid()
		}
		cross, ok := dependency["crossProject"].(bool)
		if !ok {
			return privacyInvalid()
		}
		projectSet := map[string]bool{}
		for _, id := range dependencyProjects {
			projectSet[id] = true
			if !snapshot.projects[id] {
				return privacyInvalid()
			}
		}
		if len(projectSet) > 1 && !cross {
			return privacyInvalid()
		}
		for _, id := range sources {
			if !contains(records, id) {
				return privacyInvalid()
			}
		}
		for _, id := range projects {
			if !projectSet[id] {
				return privacyInvalid()
			}
		}
		for _, id := range records {
			record := snapshot.records[id]
			if record == nil || !projectSet[stringValue(record, "projectId")] {
				return privacyInvalid()
			}
		}
	}
	if section == "ai_artifact" {
		if !contains([]string{"pending", "accepted", "rejected"}, stringValue(value, "status")) {
			return artifactInvalid()
		}
	} else {
		if !portableID(value["artifactId"]) {
			return importInvalid()
		}
		if err := portableEntity(ctx, conn, "ai_artifact", value["artifactId"]); err != nil {
			return err
		}
		rawSuggestion, err := get(ctx, conn, "ai_artifact", stringValue(value, "artifactId"))
		if err != nil {
			return err
		}
		var suggestion map[string]any
		if DecodeJSONNumbers(rawSuggestion, &suggestion) != nil {
			return importInvalid()
		}
		if suggestion["status"] != "accepted" {
			return importInvalid()
		}
		for _, field := range []string{"kind", "content", "sourceRecordIds", "projectIds", "generator", "privacyDependencies"} {
			if !EqualJSONNumbers(value[field], suggestion[field]) {
				return importInvalid()
			}
		}
	}
	return nil
}
