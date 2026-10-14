package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func profileUsername(value any) (string, error) {
	name, err := captureText(value, 39)
	if err != nil || !githubLoginPattern.MatchString(name) || strings.Contains(name, "--") {
		return "", validationProject()
	}
	return name, nil
}
func nullableProfileText(body map[string]any, key string, max int) (any, error) {
	value, exists := body[key]
	if !exists {
		return nil, validationProject()
	}
	if value == nil {
		return nil, nil
	}
	return curationText(value, max)
}
func profileAvatar(value any) (any, error) {
	if value == nil || value == "" {
		return nil, nil
	}
	raw, ok := value.(string)
	if !ok {
		return nil, validationProject()
	}
	target, err := url.Parse(raw)
	if err != nil || !target.IsAbs() {
		return nil, validationProject()
	}
	target.Scheme = strings.ToLower(target.Scheme)
	if target.User != nil && target.User.Username() == "" {
		password, _ := target.User.Password()
		if password == "" {
			target.User = nil
		}
	}
	if target.Scheme != "https" || strings.ToLower(target.Hostname()) != "avatars.githubusercontent.com" || target.User != nil || (target.Port() != "" && target.Port() != "443") {
		return nil, nil
	}
	target.Scheme = "https"
	target.Host = "avatars.githubusercontent.com"
	if target.Path == "" {
		target.Path = "/"
	}
	return target.String(), nil
}
func presentProfileGithub(value any) any {
	body, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	name, err := profileUsername(body["username"])
	if err != nil {
		return nil
	}
	display, err := nullableProfileText(body, "displayName", 200)
	if err != nil {
		return nil
	}
	avatar, err := nullableProfileText(body, "avatarUrl", 2048)
	if err != nil {
		return nil
	}
	at, ok := body["fetchedAt"].(string)
	if !ok || !isoTimestampPattern.MatchString(at) {
		return nil
	}
	if _, _, err = timestampParts(at); err != nil {
		return nil
	}
	avatar, err = profileAvatar(avatar)
	if err != nil {
		avatar = nil
	}
	return map[string]any{"username": name, "displayName": display, "avatarUrl": avatar, "profileUrl": "https://github.com/" + name, "fetchedAt": at}
}
func fetchPublicGithub(ctx context.Context, username string) (map[string]any, error) {
	unavailable := &protocolError{"GITHUB_UNAVAILABLE", "Could not retrieve the public GitHub profile. Try again later.", 503}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, "GET", "https://api.github.com/users/"+url.PathEscape(username), nil)
	if err != nil {
		return nil, unavailable
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("X-GitHub-Api-Version", "2026-03-10")
	request.Header.Set("User-Agent", "Scratchpad-public-profile")
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		return nil, unavailable
	}
	defer response.Body.Close()
	if response.StatusCode == 404 {
		return nil, &protocolError{"GITHUB_PROFILE_NOT_FOUND", "Public GitHub profile was not found.", 404}
	}
	if response.StatusCode == 403 || response.StatusCode == 429 {
		return nil, &protocolError{"GITHUB_RATE_LIMITED", "GitHub is limiting public profile requests. Try again later.", 503}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, unavailable
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 65537))
	if err != nil || len(data) > 65536 {
		return nil, unavailable
	}
	var body map[string]any
	if json.Unmarshal(data, &body) != nil {
		return nil, unavailable
	}
	name, err := profileUsername(body["login"])
	if err != nil || body["type"] != "User" || !strings.EqualFold(name, username) {
		return nil, unavailable
	}
	display, err := nullableProfileText(body, "name", 200)
	if err != nil {
		return nil, unavailable
	}
	avatar, err := nullableProfileText(body, "avatar_url", 2048)
	if err != nil {
		return nil, unavailable
	}
	avatar, err = profileAvatar(avatar)
	if err != nil {
		return nil, unavailable
	}
	return map[string]any{"username": name, "displayName": display, "avatarUrl": avatar, "profileUrl": "https://github.com/" + name, "fetchedAt": timestamp(time.Now())}, nil
}
