// Package hostapi is the little of the COGNIX[WS] Host API the background
// process needs: workspaces, the project's trusted section, agents. The
// daemon hands the process PICODE_URL, PICODE_EXT_TOKEN and, on HTTPS,
// PICODE_CA_FILE.
package hostapi

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// Client calls the Host API.
type Client struct {
	base  string
	token string
	http  *http.Client
}

// FromEnv builds a client from the process environment; nil outside COGNIX[WS].
func FromEnv() *Client {
	base, tok := os.Getenv("PICODE_URL"), os.Getenv("PICODE_EXT_TOKEN")
	if base == "" || tok == "" {
		return nil
	}
	tr := &http.Transport{}
	if ca := os.Getenv("PICODE_CA_FILE"); ca != "" {
		if pem, err := os.ReadFile(ca); err == nil {
			pool := x509.NewCertPool()
			pool.AppendCertsFromPEM(pem)
			tr.TLSClientConfig = &tls.Config{RootCAs: pool}
		}
	}
	return &Client{base: base, token: tok, http: &http.Client{Timeout: 10 * time.Second, Transport: tr}}
}

// Workspace is one workspace the process may read.
type Workspace struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Path        string `json:"path"`
	ExtensionOn bool   `json:"extensionOn"`
}

// Agent is one agent of a workspace.
type Agent struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	CLI     string `json:"cli"`
	Running bool   `json:"running"`
	State   string `json:"state"`
	WorkDir string `json:"workDir"`
}

// Project is a workspace's .picode.json as the host sees it.
type Project struct {
	Found   bool            `json:"found"`
	Trusted bool            `json:"trusted"`
	Digest  string          `json:"digest"`
	Error   string          `json:"error"`
	Section json.RawMessage `json:"section"`
}

// ErrStatus is a non-200 answer.
type ErrStatus struct {
	Code int
	Msg  string
}

func (e ErrStatus) Error() string { return fmt.Sprintf("host %d: %s", e.Code, e.Msg) }

func (c *Client) get(path string, out any) error {
	req, _ := http.NewRequest("GET", c.base+path, nil)
	req.Header.Set("X-PiCode-Extension-Token", c.token)
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode != 200 {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(b, &e)
		return ErrStatus{resp.StatusCode, e.Error}
	}
	return json.Unmarshal(b, out)
}

// Workspaces lists the workspaces the process may read.
func (c *Client) Workspaces() ([]Workspace, error) {
	var out struct {
		Workspaces []Workspace `json:"workspaces"`
	}
	err := c.get("/api/ext/v1/workspaces", &out)
	return out.Workspaces, err
}

// Workspace finds one workspace by id.
func (c *Client) Workspace(id string) (Workspace, error) {
	var out struct {
		Workspaces []Workspace `json:"workspaces"`
	}
	if err := c.get("/api/ext/v1/workspaces", &out); err != nil {
		return Workspace{}, err
	}
	for _, w := range out.Workspaces {
		if w.ID == id {
			return w, nil
		}
	}
	return Workspace{}, errors.New("no such workspace")
}

// Project reads the workspace's project file as trusted (ref optional).
func (c *Client) Project(id, ref string) (Project, error) {
	var p Project
	q := ""
	if ref != "" {
		q = "?ref=" + url.QueryEscape(ref)
	}
	err := c.get("/api/ext/v1/workspaces/"+url.PathEscape(id)+"/project"+q, &p)
	return p, err
}

// Agents lists a workspace's agents.
func (c *Client) Agents(id string) ([]Agent, error) {
	var out struct {
		Agents []Agent `json:"agents"`
	}
	err := c.get("/api/ext/v1/agents?workspace="+url.QueryEscape(id), &out)
	return out.Agents, err
}

// Publish sends the ephemeral feed event ext.<id>.<name> (events:publish).
func (c *Client) Publish(name string, data any) error {
	b, _ := json.Marshal(map[string]any{"name": name, "data": data})
	req, _ := http.NewRequest("POST", c.base+"/api/ext/v1/events", bytes.NewReader(b))
	req.Header.Set("X-PiCode-Extension-Token", c.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != 200 && resp.StatusCode != 201 && resp.StatusCode != 204 {
		return ErrStatus{resp.StatusCode, "event refused"}
	}
	return nil
}

// MergeEntry is the part of a core merge entry the flow reads.
type MergeEntry struct {
	ID          string `json:"id"`
	State       string `json:"state"`
	Note        string `json:"note"`
	Target      string `json:"target"`
	RequestedBy string `json:"requestedBy"`
	// Rules and Conflict say why a change waits (ADR-0411 D5, D7): the
	// approval rules that hold it for the person (needs-you), and a
	// conflict with the target (needs-update).
	Rules    []RuleVerdict  `json:"rules,omitempty"`
	Conflict *QueueConflict `json:"conflict,omitempty"`
}

// RuleVerdict is one approval rule that holds a change, with its reason.
type RuleVerdict struct {
	Rule   string `json:"rule"`
	Reason string `json:"reason"`
}

// QueueConflict is a change's conflict with its target: the files, and who
// resolves it now — "agent" (AskedAgent was asked) or "person".
type QueueConflict struct {
	Files      []string `json:"files"`
	WaitsFor   string   `json:"waitsFor,omitempty"`
	AskedAgent string   `json:"askedAgent,omitempty"`
}

// RequestMerge asks the core to merge an agent's change (merge:request).
func (c *Client) RequestMerge(agentID string) (MergeEntry, error) {
	b, _ := json.Marshal(map[string]string{"agentId": agentID})
	req, _ := http.NewRequest("POST", c.base+"/api/ext/v1/merge", bytes.NewReader(b))
	req.Header.Set("X-PiCode-Extension-Token", c.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return MergeEntry{}, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var out struct {
		Entry MergeEntry `json:"entry"`
		Error string     `json:"error"`
	}
	_ = json.Unmarshal(raw, &out)
	if resp.StatusCode != 200 {
		return MergeEntry{}, ErrStatus{resp.StatusCode, out.Error}
	}
	return out.Entry, nil
}

// MergeState reads one merge this extension asked for.
func (c *Client) MergeState(agentID, entryID string) (MergeEntry, error) {
	var out struct {
		Entry MergeEntry `json:"entry"`
	}
	err := c.get("/api/ext/v1/merge?agentId="+url.QueryEscape(agentID)+"&entry="+url.QueryEscape(entryID), &out)
	return out.Entry, err
}

// QueueEntry is one entry of a workspace's Merge queue, as the core's
// Merge screen reads it (ADR-0411): its place in line, the rules that hold
// it, a conflict, and the agent on its branch.
type QueueEntry struct {
	ID          string         `json:"id"`
	Branch      string         `json:"branch"`
	Target      string         `json:"target"`
	State       string         `json:"state"`
	Note        string         `json:"note"`
	RequestedBy string         `json:"requestedBy"`
	CreatedAt   string         `json:"createdAt"`
	StartedAt   string         `json:"startedAt"`
	UpdatedAt   string         `json:"updatedAt"`
	Position    int            `json:"position,omitempty"`
	Rules       []RuleVerdict  `json:"rules,omitempty"`
	Conflict    *QueueConflict `json:"conflict,omitempty"`
	Agent       *struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"agent,omitempty"`
}

// Reasons joins the rules that hold an entry, in their own words.
func Reasons(rules []RuleVerdict) string {
	var rs []string
	for _, r := range rules {
		rs = append(rs, strings.TrimSuffix(r.Reason, "."))
	}
	return strings.Join(rs, "; ")
}

// MergeQueue reads the Merge queue of a workspace's repository
// (merge:request): the waiting and running entries in order, and the
// newest that ended.
func (c *Client) MergeQueue(workspace string) (active, ended []QueueEntry, err error) {
	var out struct {
		Entries []QueueEntry `json:"entries"`
		Ended   []QueueEntry `json:"ended"`
	}
	err = c.get("/api/ext/v1/merge/queue?workspace="+url.QueryEscape(workspace), &out)
	return out.Entries, out.Ended, err
}
