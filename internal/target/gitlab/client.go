// Package gitlab bindet GitLab als Zielsystem-Plugin an (analog spec/13 für
// Zammad): REST-Client (API v4) für die Agent-Aktionen und Webhook-Verarbeitung
// (Token-verifiziert, idempotent). Einheit der Arbeit ist das Issue.
package gitlab

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client spricht die GitLab-REST-API (v4) mit einem (gebrokerten) API-Token.
// Das Token kommt pro Aufruf aus dem SecretStore — es wird nie persistiert.
type Client struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
}

func NewClient(baseURL, token string) *Client {
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		Token:   token,
		HTTP:    &http.Client{Timeout: 15 * time.Second},
	}
}

func (c *Client) do(ctx context.Context, method, path string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+"/api/v4"+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("PRIVATE-TOKEN", c.Token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("gitlab %s %s: HTTP %d: %.300s", method, path, resp.StatusCode, data)
	}
	if out != nil {
		return json.Unmarshal(data, out)
	}
	return nil
}

type Issue struct {
	ID          int      `json:"id"`
	IID         int      `json:"iid"`
	ProjectID   int      `json:"project_id"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	State       string   `json:"state"`
	Labels      []string `json:"labels"`
	WebURL      string   `json:"web_url"`
	// Assignees macht die Zuweisung für den Agenten sichtbar — Playbooks wie
	// „bearbeite nur dir zugewiesene Issues" brauchen diese Information.
	Assignees []struct {
		Username string `json:"username"`
	} `json:"assignees"`
	// References.Full ist die volle Referenz "gruppe/projekt#iid" — daraus
	// lässt sich der Projektpfad für den Intake-Filter ableiten.
	References struct {
		Full string `json:"full"`
	} `json:"references"`
}

type Project struct {
	ID                int    `json:"id"`
	PathWithNamespace string `json:"path_with_namespace"`
	Description       string `json:"description"`
	WebURL            string `json:"web_url"`
}

type Note struct {
	ID       int    `json:"id"`
	Body     string `json:"body"`
	Internal bool   `json:"internal"`
	System   bool   `json:"system"`
	Author   struct {
		Username string `json:"username"`
	} `json:"author"`
	CreatedAt string `json:"created_at"`
}

// ListProjects — GET /projects?membership=true: alle Projekte, in denen der
// Bot-Nutzer Mitglied ist. Der Einstieg für Agenten, die ihre project_ids
// noch nicht kennen.
func (c *Client) ListProjects(ctx context.Context) ([]Project, error) {
	var out []Project
	err := c.do(ctx, http.MethodGet,
		"/projects?membership=true&simple=true&archived=false&order_by=last_activity_at&per_page=100", nil, &out)
	return out, err
}

// ListIssues findet Issues — mit projectID über GET /projects/{id}/issues,
// ohne (projectID=0) über das globale GET /issues mit scope=all: alle Issues,
// die das Token sehen darf. state ist "opened" (Default), "closed" oder "all";
// labels (kommasepariert) und search schränken optional ein. assigned=true
// liefert nur Issues, die dem Bot-Nutzer des Tokens zugewiesen sind
// (scope=assigned_to_me) — für Agenten, die laut Playbook nur ihre eigene
// Zuweisung bearbeiten.
func (c *Client) ListIssues(ctx context.Context, projectID int, state, labels, search string, assigned bool) ([]Issue, error) {
	q := url.Values{}
	if state == "" {
		state = "opened"
	}
	if state != "all" {
		q.Set("state", state)
	}
	if labels != "" {
		q.Set("labels", labels)
	}
	if search != "" {
		q.Set("search", search)
	}
	if assigned {
		q.Set("scope", "assigned_to_me")
	}
	q.Set("order_by", "updated_at")
	q.Set("per_page", "100")
	path := "/issues?" + q.Encode()
	if projectID != 0 {
		path = fmt.Sprintf("/projects/%d/issues?%s", projectID, q.Encode())
	} else if !assigned {
		path = "/issues?scope=all&" + q.Encode()
	}
	var out []Issue
	err := c.do(ctx, http.MethodGet, path, nil, &out)
	return out, err
}

// GetIssue — GET /projects/{id}/issues/{iid}
func (c *Client) GetIssue(ctx context.Context, projectID, issueIID int) (Issue, error) {
	var i Issue
	err := c.do(ctx, http.MethodGet, fmt.Sprintf("/projects/%d/issues/%d", projectID, issueIID), nil, &i)
	return i, err
}

// ListNotes — GET /projects/{id}/issues/{iid}/notes (chronologisch)
func (c *Client) ListNotes(ctx context.Context, projectID, issueIID int) ([]Note, error) {
	var out []Note
	err := c.do(ctx, http.MethodGet,
		fmt.Sprintf("/projects/%d/issues/%d/notes?sort=asc&order_by=created_at", projectID, issueIID), nil, &out)
	return out, err
}

// Comment — POST /projects/{id}/issues/{iid}/notes. internal=true ist eine
// interne Notiz (nur für Projektmitglieder ab Reporter sichtbar), internal=false
// ein öffentlicher Kommentar — auch für externe Reporter sichtbar.
func (c *Client) Comment(ctx context.Context, projectID, issueIID int, body string, internal bool) (Note, error) {
	var out Note
	err := c.do(ctx, http.MethodPost, fmt.Sprintf("/projects/%d/issues/%d/notes", projectID, issueIID),
		map[string]any{"body": body, "internal": internal}, &out)
	return out, err
}

// DownloadArchive streamt das Repository-Archiv (tar.gz) —
// GET /projects/{id}/repository/archive.tar.gz, optional auf einen Ref
// (Branch, Tag, SHA) und ein Unterverzeichnis (subPath) eingeschränkt —
// Letzteres macht große Repos per Teil-Checkout handhabbar. Bewusst nicht
// über do(): der Body ist binär und kann groß sein; der Aufrufer schließt
// den Reader.
func (c *Client) DownloadArchive(ctx context.Context, projectID int, ref, subPath string) (io.ReadCloser, error) {
	path := fmt.Sprintf("/projects/%d/repository/archive.tar.gz", projectID)
	q := url.Values{}
	if ref != "" {
		q.Set("sha", ref)
	}
	if subPath != "" {
		q.Set("path", subPath)
	}
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/api/v4"+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("PRIVATE-TOKEN", c.Token)
	// Eigener HTTP-Client ohne das knappe API-Timeout — der Download eines
	// großen Repos darf länger dauern; die Grenze setzt der Aufruf-Context.
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		resp.Body.Close()
		return nil, fmt.Errorf("gitlab GET %s: HTTP %d: %.300s", path, resp.StatusCode, data)
	}
	return resp.Body, nil
}

// TreeEntry ist ein Eintrag des Repository-Baums (Datei oder Verzeichnis).
type TreeEntry struct {
	Name string `json:"name"`
	Type string `json:"type"` // "blob" (Datei) | "tree" (Verzeichnis)
	Path string `json:"path"`
}

// ListTree — GET /projects/{id}/repository/tree: den Repository-Baum
// durchblättern, ohne etwas herunterzuladen. Für Repos, die zu groß für
// einen Checkout sind: erst navigieren, dann gezielt Dateien lesen.
func (c *Client) ListTree(ctx context.Context, projectID int, path, ref string, recursive bool) ([]TreeEntry, error) {
	q := url.Values{}
	if path != "" {
		q.Set("path", path)
	}
	if ref != "" {
		q.Set("ref", ref)
	}
	if recursive {
		q.Set("recursive", "true")
	}
	q.Set("per_page", "100")
	var out []TreeEntry
	err := c.do(ctx, http.MethodGet,
		fmt.Sprintf("/projects/%d/repository/tree?%s", projectID, q.Encode()), nil, &out)
	return out, err
}

// maxReadFileBytes begrenzt eine einzelne per read_file gelesene Datei.
const maxReadFileBytes = 512 << 10 // 512 KB

// ReadFile — GET /projects/{id}/repository/files/{path}/raw: eine einzelne
// Datei lesen, ohne Checkout. Der Dateipfad wird komplett URL-kodiert
// (inkl. "/"), wie die GitLab-API es verlangt.
func (c *Client) ReadFile(ctx context.Context, projectID int, filePath, ref string) (content string, truncated bool, err error) {
	path := fmt.Sprintf("/projects/%d/repository/files/%s/raw", projectID, url.QueryEscape(filePath))
	if ref != "" {
		path += "?ref=" + url.QueryEscape(ref)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/api/v4"+path, nil)
	if err != nil {
		return "", false, err
	}
	req.Header.Set("PRIVATE-TOKEN", c.Token)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", false, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxReadFileBytes+1))
	if err != nil {
		return "", false, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", false, fmt.Errorf("gitlab GET %s: HTTP %d: %.300s", path, resp.StatusCode, data)
	}
	if len(data) > maxReadFileBytes {
		return string(data[:maxReadFileBytes]), true, nil
	}
	return string(data), false, nil
}

// SetState — PUT /projects/{id}/issues/{iid} mit state_event ("close"|"reopen").
func (c *Client) SetState(ctx context.Context, projectID, issueIID int, stateEvent string) error {
	if stateEvent != "close" && stateEvent != "reopen" {
		return fmt.Errorf("ungültiger state %q (erlaubt: close, reopen)", stateEvent)
	}
	return c.do(ctx, http.MethodPut, fmt.Sprintf("/projects/%d/issues/%d", projectID, issueIID),
		map[string]any{"state_event": stateEvent}, nil)
}

// Escalate setzt eine interne Notiz und entfernt die Zuweisung
// (assignee_ids leer), damit ein Mensch das Issue übernimmt.
func (c *Client) Escalate(ctx context.Context, projectID, issueIID int, note string) error {
	if _, err := c.Comment(ctx, projectID, issueIID, note, true); err != nil {
		return err
	}
	return c.do(ctx, http.MethodPut, fmt.Sprintf("/projects/%d/issues/%d", projectID, issueIID),
		map[string]any{"assignee_ids": []int{}}, nil)
}
