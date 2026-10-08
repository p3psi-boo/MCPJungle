package types

import "time"

// SkillOrigin records where an installed skill was downloaded from.
type SkillOrigin struct {
	Source      string    `json:"source"`
	Repo        string    `json:"repo"`
	Ref         string    `json:"ref,omitempty"`
	Path        string    `json:"path,omitempty"`
	InstalledAt time.Time `json:"installed_at"`
}

// Skill describes an Agent Skill exposed by the gateway.
type Skill struct {
	Name          string `json:"name"`
	Description   string `json:"description"`
	License       string `json:"license,omitempty"`
	Compatibility string `json:"compatibility,omitempty"`
	// PromptName is the canonical name of the MCP prompt that activates the skill.
	PromptName string `json:"prompt_name"`
	// Removable is true for skills installed at runtime, which can be updated or removed.
	Removable bool         `json:"removable"`
	Origin    *SkillOrigin `json:"origin,omitempty"`
}

// SkillDetail is a skill along with its instructions and bundled files.
type SkillDetail struct {
	Skill
	Instructions   string   `json:"instructions"`
	Files          []string `json:"files"`
	FilesTruncated bool     `json:"files_truncated,omitempty"`
}

// ListSkillsResponse is the response of the list skills endpoint.
type ListSkillsResponse struct {
	// Enabled is false when the server was started without any skills directory.
	Enabled bool `json:"enabled"`
	// CanInstall is true when skills can be installed at runtime.
	CanInstall bool `json:"can_install"`
	// InstallDir is only reported by the dashboard API.
	InstallDir string  `json:"install_dir,omitempty"`
	Skills     []Skill `json:"skills"`
}

// PreviewSkillsInput is the request body of the preview skills endpoint.
type PreviewSkillsInput struct {
	// Source is a GitHub repository or directory, eg- "anthropics/skills" or
	// "https://github.com/anthropics/skills/tree/main/skills/pdf".
	Source string `json:"source"`
}

// SkillCandidate is a skill found in a source that can be installed.
type SkillCandidate struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Path        string `json:"path"`
	Installed   bool   `json:"installed"`
	Conflict    string `json:"conflict,omitempty"`
}

// PreviewSkillsResponse lists the skills available in a source.
type PreviewSkillsResponse struct {
	Source string           `json:"source"`
	Skills []SkillCandidate `json:"skills"`
}

// InstallSkillsInput is the request body of the install skills endpoint.
type InstallSkillsInput struct {
	Source string `json:"source"`
	// Skills are the names of the skills to install. Empty means all skills in the source.
	Skills []string `json:"skills,omitempty"`
	// Overwrite replaces skills that are already installed.
	Overwrite bool `json:"overwrite,omitempty"`
}

// InstallSkillsResponse is the response of the install skills endpoint.
type InstallSkillsResponse struct {
	Installed []Skill `json:"installed"`
}
