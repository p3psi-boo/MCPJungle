package api

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/mcpjungle/mcpjungle/internal/service/mcp"
	"github.com/mcpjungle/mcpjungle/internal/service/skills"
	"github.com/mcpjungle/mcpjungle/pkg/apierrors"
	"github.com/mcpjungle/mcpjungle/pkg/types"
)

var errSkillsDisabled = fmt.Errorf(
	"skills are not enabled; start the server with --skills-dir or --skills-install-dir: %w",
	apierrors.ErrInvalidInput,
)

func toSkillType(sk *skills.Skill) types.Skill {
	out := types.Skill{
		Name:          sk.Name,
		Description:   sk.Description,
		License:       sk.License,
		Compatibility: sk.Compatibility,
		PromptName:    mcp.SkillsServerName + "__" + sk.Name,
		Removable:     sk.Removable,
	}
	if sk.Origin != nil {
		out.Origin = &types.SkillOrigin{
			Source:      sk.Origin.Source,
			Repo:        sk.Origin.Repo,
			Ref:         sk.Origin.Ref,
			Path:        sk.Origin.Path,
			InstalledAt: sk.Origin.InstalledAt,
		}
	}
	return out
}

// skillStoreOrAbort returns the skill store, or writes an error response and returns nil
// if skills are disabled.
func (s *Server) skillStoreOrAbort(c *gin.Context) *skills.Store {
	store := s.mcpService.SkillStore()
	if store == nil {
		handleServiceError(c, errSkillsDisabled)
	}
	return store
}

func (s *Server) listSkillsHandler(includeInstallDir bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		resp := types.ListSkillsResponse{Skills: []types.Skill{}}
		store := s.mcpService.SkillStore()
		if store != nil {
			resp.Enabled = true
			resp.CanInstall = store.InstallDir() != ""
			if includeInstallDir {
				resp.InstallDir = store.InstallDir()
			}
			for _, sk := range store.List() {
				resp.Skills = append(resp.Skills, toSkillType(&sk))
			}
		}
		c.JSON(http.StatusOK, resp)
	}
}

func (s *Server) getSkillHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		store := s.skillStoreOrAbort(c)
		if store == nil {
			return
		}
		sk, err := store.Get(c.Param("name"))
		if err != nil {
			handleServiceError(c, err)
			return
		}
		instructions, err := sk.Instructions()
		if err != nil {
			handleServiceError(c, err)
			return
		}
		files, truncated, err := sk.Files()
		if err != nil {
			handleServiceError(c, err)
			return
		}
		if files == nil {
			files = []string{}
		}
		c.JSON(http.StatusOK, types.SkillDetail{
			Skill:          toSkillType(sk),
			Instructions:   instructions,
			Files:          files,
			FilesTruncated: truncated,
		})
	}
}

func (s *Server) previewSkillsHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		store := s.skillStoreOrAbort(c)
		if store == nil {
			return
		}
		var input types.PreviewSkillsInput
		if err := c.ShouldBindJSON(&input); err != nil {
			c.JSON(http.StatusBadRequest, types.APIErrorResponse{Error: err.Error()})
			return
		}
		preview, err := store.Preview(c.Request.Context(), input.Source)
		if err != nil {
			handleServiceError(c, err)
			return
		}
		resp := types.PreviewSkillsResponse{Source: preview.Source, Skills: []types.SkillCandidate{}}
		for _, cand := range preview.Skills {
			resp.Skills = append(resp.Skills, types.SkillCandidate{
				Name:        cand.Name,
				Description: cand.Description,
				Path:        cand.Path,
				Installed:   cand.Installed,
				Conflict:    cand.Conflict,
			})
		}
		c.JSON(http.StatusOK, resp)
	}
}

func (s *Server) installSkillsHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		store := s.skillStoreOrAbort(c)
		if store == nil {
			return
		}
		var input types.InstallSkillsInput
		if err := c.ShouldBindJSON(&input); err != nil {
			c.JSON(http.StatusBadRequest, types.APIErrorResponse{Error: err.Error()})
			return
		}
		installed, err := store.Install(c.Request.Context(), input.Source, input.Skills, input.Overwrite)
		if err != nil {
			handleServiceError(c, err)
			return
		}
		resp := types.InstallSkillsResponse{Installed: make([]types.Skill, len(installed))}
		for i := range installed {
			resp.Installed[i] = toSkillType(&installed[i])
		}
		c.JSON(http.StatusCreated, resp)
	}
}

func (s *Server) updateSkillHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		store := s.skillStoreOrAbort(c)
		if store == nil {
			return
		}
		sk, err := store.Update(c.Request.Context(), c.Param("name"))
		if err != nil {
			handleServiceError(c, err)
			return
		}
		c.JSON(http.StatusOK, toSkillType(sk))
	}
}

func (s *Server) removeSkillHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		store := s.skillStoreOrAbort(c)
		if store == nil {
			return
		}
		if err := store.Remove(c.Param("name")); err != nil {
			handleServiceError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"deleted": true})
	}
}

func (s *Server) reloadSkillsHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		store := s.skillStoreOrAbort(c)
		if store == nil {
			return
		}
		if err := store.Reload(); err != nil {
			handleServiceError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"skill_count": len(store.List())})
	}
}
