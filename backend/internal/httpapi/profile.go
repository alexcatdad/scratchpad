package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"time"

	"github.com/alexcatdad/scratchpad/backend/internal/domain"
)

func storedProfile(ctx context.Context, q entityReader) (map[string]any, error) {
	raw, err := optional(ctx, q, "profile", "owner-profile")
	if err != nil || raw == nil {
		return nil, err
	}
	var profile map[string]any
	err = json.Unmarshal(raw, &profile)
	return profile, err
}
func profileVersion(body map[string]any) (float64, error) {
	version, ok := body["expectedVersion"].(float64)
	if !ok || version < 0 || math.IsNaN(version) || math.IsInf(version, 0) || version != math.Trunc(version) {
		return 0, validationProject()
	}
	return version, nil
}
func profileConflict() error {
	return &protocolError{"CONFLICT", "Profile changed. Read it again.", 409}
}
func profileMatches(previous map[string]any, version float64) bool {
	if previous == nil {
		return version == 0
	}
	return domain.ProfileFrom(previous).MatchesVersion(version)
}
func (s *SSH) Profile(operation string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		if operation == "get" {
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
			profile, err := storedProfile(ctx, tx)
			if err != nil {
				s.fail(w, err)
				return
			}
			if profile == nil {
				display := "Owner"
				raw, err := optional(ctx, tx, "owner", "owner")
				if err != nil {
					s.fail(w, err)
					return
				}
				if raw != nil {
					var owner map[string]any
					if err = json.Unmarshal(raw, &owner); err != nil {
						s.fail(w, err)
						return
					}
					if value, exists := owner["displayName"]; exists && value != nil {
						display = fmt.Sprint(value)
					}
				}
				profile = map[string]any{"id": "owner-profile", "displayName": display, "version": 0}
			}
			profile["github"] = presentProfileGithub(domain.ProfileFrom(profile).Github)
			engine := s.Store.Engine
			if engine == "postgres" {
				engine = "postgresql"
			}
			JSON(w, 200, map[string]any{"profile": profile, "database": map[string]any{"engine": engine}})
			return
		}
		if _, err := apiActor(ctx, s.Store.DB, r, s.Origin); err != nil {
			s.fail(w, err)
			return
		}
		var body map[string]any
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 3*1024*1024))
		if decoder.Decode(&body) != nil || body == nil || decoder.Decode(new(any)) != io.EOF {
			s.fail(w, validationProject())
			return
		}
		version, err := profileVersion(body)
		if err != nil {
			s.fail(w, err)
			return
		}
		name := ""
		var github any
		if operation == "name" {
			name, err = captureText(body["displayName"], 200)
			if err != nil {
				s.fail(w, err)
				return
			}
		} else {
			before, err := storedProfile(ctx, s.Store.DB)
			if err != nil {
				s.fail(w, err)
				return
			}
			if !profileMatches(before, version) {
				s.fail(w, profileConflict())
				return
			}
			if operation == "github" {
				username, err := profileUsername(body["username"])
				if err != nil {
					s.fail(w, err)
					return
				}
				github, err = fetchPublicGithub(ctx, username)
				if err != nil {
					s.fail(w, err)
					return
				}
			}
		}
		var profile map[string]any
		err = s.Store.Write(ctx, func(conn *sql.Conn) error {
			actor, err := apiActor(ctx, conn, r, s.Origin)
			if err != nil {
				return err
			}
			previous, err := storedProfile(ctx, conn)
			if err != nil {
				return err
			}
			if !profileMatches(previous, version) {
				return profileConflict()
			}
			if previous == nil {
				profile = map[string]any{"displayName": name}
				if operation != "name" {
					display := "Owner"
					raw, err := optional(ctx, conn, "owner", "owner")
					if err != nil {
						return err
					}
					if raw != nil {
						var owner map[string]any
						if err = json.Unmarshal(raw, &owner); err != nil {
							return err
						}
						if value, exists := owner["displayName"]; exists && value != nil {
							display = fmt.Sprint(value)
						}
					}
					profile["displayName"] = display
					profile["github"] = github
				}
				err = insert(ctx, conn, "profile", "owner-profile", profile)
			} else {
				profile, err = copyEntity(previous)
				if err != nil {
					return err
				}
				if operation == "name" {
					profile["displayName"] = name
				} else {
					profile["github"] = github
				}
				err = update(ctx, conn, "profile", profile)
			}
			if err != nil {
				return err
			}
			action := "profile.updated"
			if operation == "github" {
				action = "profile.github.updated"
			}
			if operation == "unlink" {
				action = "profile.github.unlinked"
			}
			return apiAudit(ctx, conn, action, "profile", "owner-profile", actor, previous, profile)
		})
		if err != nil {
			s.fail(w, err)
			return
		}
		profile["github"] = presentProfileGithub(domain.ProfileFrom(profile).Github)
		JSON(w, 200, map[string]any{"profile": profile})
	}
}
