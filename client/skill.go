package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/mcpjungle/mcpjungle/pkg/types"
)

// doSkillsRequest sends a request to a skills endpoint and decodes the JSON response into out.
func (c *Client) doSkillsRequest(method, path string, in any, expectedStatus int, out any) error {
	u, err := c.constructAPIEndpoint(path)
	if err != nil {
		return err
	}

	var body io.Reader
	if in != nil {
		payload, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(payload)
	}

	req, err := c.newRequest(method, u, body)
	if err != nil {
		return fmt.Errorf("failed to create request to %s: %w", u, err)
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send request to %s: %w", u, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != expectedStatus {
		return c.parseErrorResponse(resp)
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("failed to decode response: %w", err)
	}
	return nil
}

// ListSkills lists the Agent Skills exposed by the server.
func (c *Client) ListSkills() (*types.ListSkillsResponse, error) {
	var out types.ListSkillsResponse
	if err := c.doSkillsRequest(http.MethodGet, "/skills", nil, http.StatusOK, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetSkill returns a skill along with its instructions and bundled files.
func (c *Client) GetSkill(name string) (*types.SkillDetail, error) {
	var out types.SkillDetail
	if err := c.doSkillsRequest(http.MethodGet, "/skills/"+url.PathEscape(name), nil, http.StatusOK, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// PreviewSkills lists the skills available in a source without installing them.
func (c *Client) PreviewSkills(source string) (*types.PreviewSkillsResponse, error) {
	var out types.PreviewSkillsResponse
	in := &types.PreviewSkillsInput{Source: source}
	if err := c.doSkillsRequest(http.MethodPost, "/skills/preview", in, http.StatusOK, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// InstallSkills installs skills from a source.
func (c *Client) InstallSkills(input *types.InstallSkillsInput) (*types.InstallSkillsResponse, error) {
	var out types.InstallSkillsResponse
	if err := c.doSkillsRequest(http.MethodPost, "/skills", input, http.StatusCreated, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdateSkill reinstalls a skill from the source it was installed from.
func (c *Client) UpdateSkill(name string) (*types.Skill, error) {
	var out types.Skill
	path := "/skills/" + url.PathEscape(name) + "/update"
	if err := c.doSkillsRequest(http.MethodPost, path, nil, http.StatusOK, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// RemoveSkill removes an installed skill.
func (c *Client) RemoveSkill(name string) error {
	return c.doSkillsRequest(http.MethodDelete, "/skills/"+url.PathEscape(name), nil, http.StatusOK, nil)
}

// ReloadSkills makes the server rediscover skills from disk.
func (c *Client) ReloadSkills() (int, error) {
	var out struct {
		SkillCount int `json:"skill_count"`
	}
	if err := c.doSkillsRequest(http.MethodPost, "/skills/reload", nil, http.StatusOK, &out); err != nil {
		return 0, err
	}
	return out.SkillCount, nil
}
