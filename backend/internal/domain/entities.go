// Package domain holds the typed state used by central application decisions.
// Persisted maps remain the source for writes and wire output so unknown imported
// fields and flexible immutable payloads are not lost when a known field changes.
package domain

import "errors"

type Entity struct {
	ID, CreatedAt, UpdatedAt string
	Version                  float64
	HasVersion               bool
}

func EntityFrom(fields map[string]any) Entity {
	entity := Entity{ID: text(fields, "id"), CreatedAt: text(fields, "createdAt"), UpdatedAt: text(fields, "updatedAt")}
	entity.Version, entity.HasVersion = fields["version"].(float64)
	if value, ok := fields["version"].(int); ok {
		entity.Version, entity.HasVersion = float64(value), true
	}
	return entity
}

type RepositoryMirroring struct {
	Enabled     bool
	RecordTypes []string
}

type ProjectSettings struct {
	EnabledRecordTypes                 []string
	RepoMirroring                      RepositoryMirroring
	CrossProjectAnalysis, AIProcessing bool
}

var supportedRecordTypes = []string{"decision", "adr", "business_decision", "finding", "qa", "failure", "constraint", "project_state"}
var errInvalidProjectSettings = errors.New("invalid project settings")

func RecordTypes() []string { return append([]string{}, supportedRecordTypes...) }

func DefaultProjectSettings(kind string) ProjectSettings {
	return ProjectSettings{
		EnabledRecordTypes:   RecordTypes(),
		RepoMirroring:        RepositoryMirroring{RecordTypes: []string{"decision", "adr", "business_decision"}},
		CrossProjectAnalysis: kind != "external",
	}
}

func ParseProjectSettings(raw any) (ProjectSettings, error) {
	invalid := errInvalidProjectSettings
	fields, ok := raw.(map[string]any)
	if !ok {
		return ProjectSettings{}, invalid
	}
	mirror, ok := fields["repoMirroring"].(map[string]any)
	if !ok {
		return ProjectSettings{}, invalid
	}
	enabled, ok := mirror["enabled"].(bool)
	if !ok {
		return ProjectSettings{}, invalid
	}
	cross, ok := fields["crossProjectAnalysis"].(bool)
	if !ok {
		return ProjectSettings{}, invalid
	}
	ai, ok := fields["aiProcessing"].(bool)
	if !ok {
		return ProjectSettings{}, invalid
	}
	parseTypes := func(raw any) ([]string, error) {
		values := []string{}
		switch array := raw.(type) {
		case []string:
			values = append(values, array...)
		case []any:
			for _, entry := range array {
				value, ok := entry.(string)
				if !ok {
					return nil, invalid
				}
				values = append(values, value)
			}
		default:
			return nil, invalid
		}
		for _, value := range values {
			found := false
			for _, known := range supportedRecordTypes {
				if value == known {
					found = true
				}
			}
			if !found {
				return nil, invalid
			}
		}
		return values, nil
	}
	mirrorTypes, err := parseTypes(mirror["recordTypes"])
	if err != nil {
		return ProjectSettings{}, err
	}
	enabledRaw, exists := fields["enabledRecordTypes"]
	if !exists {
		enabledRaw = RecordTypes()
	}
	enabledTypes, err := parseTypes(enabledRaw)
	if err != nil {
		return ProjectSettings{}, err
	}
	return ProjectSettings{EnabledRecordTypes: enabledTypes, RepoMirroring: RepositoryMirroring{Enabled: enabled, RecordTypes: mirrorTypes}, CrossProjectAnalysis: cross, AIProcessing: ai}, nil
}

func (settings ProjectSettings) Fields() map[string]any {
	return map[string]any{"enabledRecordTypes": settings.EnabledRecordTypes, "repoMirroring": map[string]any{"enabled": settings.RepoMirroring.Enabled, "recordTypes": settings.RepoMirroring.RecordTypes}, "crossProjectAnalysis": settings.CrossProjectAnalysis, "aiProcessing": settings.AIProcessing}
}

type Project struct {
	Entity
	Name, Slug, Kind string
	Settings         ProjectSettings
}

func ProjectFrom(fields map[string]any) (Project, error) {
	settings, err := ParseProjectSettings(fields["settings"])
	return Project{Entity: EntityFrom(fields), Name: text(fields, "name"), Slug: text(fields, "slug"), Kind: text(fields, "kind"), Settings: settings}, err
}

// MakeExternal retains local AI consent while removing sharing defaults.
func (project *Project) MakeExternal() {
	if project.Kind != "external" {
		project.Settings.RepoMirroring.Enabled = false
		project.Settings.CrossProjectAnalysis = false
	}
	project.Kind = "external"
}

type Profile struct {
	Entity
	DisplayName string
	Github      any
}

func ProfileFrom(fields map[string]any) Profile {
	return Profile{Entity: EntityFrom(fields), DisplayName: text(fields, "displayName"), Github: fields["github"]}
}

func (profile Profile) MatchesVersion(expected float64) bool {
	return profile.HasVersion && profile.Version == expected
}

type GlobalSettings struct {
	Entity
	AIEnabled              bool
	DefaultProjectSettings *ProjectSettings
}

func GlobalSettingsFrom(fields map[string]any) GlobalSettings {
	settings := GlobalSettings{Entity: EntityFrom(fields), AIEnabled: fields["aiEnabled"] == true}
	if defaults, err := ParseProjectSettings(fields["defaultProjectSettings"]); err == nil {
		settings.DefaultProjectSettings = &defaults
	}
	return settings
}

type Metadata struct {
	Entity
	RecordID        string
	Tags            []any
	Archived        bool
	LegacyStatus    any
	HasLegacyStatus bool
}

func MetadataFrom(fields map[string]any) Metadata {
	status, exists := fields["legacyStatus"]
	tags, _ := fields["tags"].([]any)
	if values, ok := fields["tags"].([]string); ok {
		tags = make([]any, len(values))
		for i, value := range values {
			tags[i] = value
		}
	}
	return Metadata{Entity: EntityFrom(fields), RecordID: text(fields, "recordId"), Tags: tags, Archived: truthy(fields["archived"]), LegacyStatus: status, HasLegacyStatus: exists}
}

type Relationship struct {
	Entity
	FromRecordID, ToRecordID, Type, Status string
}

func RelationshipFrom(fields map[string]any) Relationship {
	return Relationship{Entity: EntityFrom(fields), FromRecordID: text(fields, "fromRecordId"), ToRecordID: text(fields, "toRecordId"), Type: text(fields, "type"), Status: text(fields, "status")}
}

func truthy(value any) bool {
	switch value := value.(type) {
	case nil:
		return false
	case bool:
		return value
	case string:
		return value != ""
	case float64:
		return value != 0
	default:
		return true
	}
}

func text(fields map[string]any, key string) string {
	value, _ := fields[key].(string)
	return value
}
