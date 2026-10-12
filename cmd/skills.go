package cmd

import (
	"fmt"
	"strings"

	"github.com/mcpjungle/mcpjungle/pkg/types"
	"github.com/spf13/cobra"
)

var (
	skillsAddCmdSkills    []string
	skillsAddCmdAll       bool
	skillsAddCmdOverwrite bool
	skillsAddCmdList      bool

	skillsWriteDescription   string
	skillsWriteLicense       string
	skillsWriteCompatibility string
	skillsWriteInstructions  string
)

var skillsCmd = &cobra.Command{
	Use:   "skills",
	Short: "Manage Agent Skills",
	Long: "Manage the Agent Skills that MCPJungle exposes to MCP clients through the built-in 'skills' server.\n\n" +
		"Skills must be enabled on the server with --skills-dir or --skills-install-dir.",
	Annotations: map[string]string{
		"group": string(subCommandGroupBasic),
		"order": "10",
	},
}

var skillsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List the skills exposed by the server",
	Args:  cobra.NoArgs,
	RunE:  runSkillsList,
}

var skillsAddCmd = &cobra.Command{
	Use:   "add <source>",
	Short: "Install skills from a GitHub repository",
	Long: "Install skills from a GitHub repository or a directory within one.\n\n" +
		"Supported sources:\n" +
		"  owner/repo\n" +
		"  owner/repo/path/to/skills\n" +
		"  owner/repo@ref\n" +
		"  https://github.com/owner/repo/tree/<ref>/<path>\n\n" +
		"If the source contains a single skill, it is installed directly.\n" +
		"Otherwise, choose skills with --skill, or install all of them with --all.\n" +
		"Set GITHUB_TOKEN on the server to install from private repositories.",
	Example: "  mcpjungle skills add anthropics/skills --list\n" +
		"  mcpjungle skills add anthropics/skills --skill pdf --skill docx\n" +
		"  mcpjungle skills add https://github.com/anthropics/skills/tree/main/skills/pdf",
	Args: cobra.ExactArgs(1),
	RunE: runSkillsAdd,
}

var skillsRemoveCmd = &cobra.Command{
	Use:   "remove <name>",
	Short: "Remove an installed skill",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := apiClient.RemoveSkill(args[0]); err != nil {
			return fmt.Errorf("failed to remove skill %s: %w", args[0], err)
		}
		cmd.Printf("Removed skill %s\n", args[0])
		return nil
	},
}

var skillsUpdateCmd = &cobra.Command{
	Use:   "update <name>",
	Short: "Reinstall a skill from the source it was installed from",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		sk, err := apiClient.UpdateSkill(args[0])
		if err != nil {
			return fmt.Errorf("failed to update skill %s: %w", args[0], err)
		}
		cmd.Printf("Updated skill %s\n", sk.Name)
		return nil
	},
}

var skillsReloadCmd = &cobra.Command{
	Use:   "reload",
	Short: "Rediscover skills from the server's skills directories",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		n, err := apiClient.ReloadSkills()
		if err != nil {
			return fmt.Errorf("failed to reload skills: %w", err)
		}
		cmd.Printf("Reloaded skills, %d available\n", n)
		return nil
	},
}

var skillsCreateCmd = &cobra.Command{
	Use:   "create <name>",
	Short: "Create a skill from scratch",
	Long: "Write a new Agent Skill into the server's skills install directory.\n\n" +
		"The name must use lowercase letters, digits and single hyphens.",
	Example: "  mcpjungle skills create code-review --description \"Review pull requests\" --instructions \"Start with the diff.\"",
	Args:    cobra.ExactArgs(1),
	RunE:    runSkillsCreate,
}

var skillsEditCmd = &cobra.Command{
	Use:   "edit <name>",
	Short: "Edit an existing skill's SKILL.md",
	Long: "Rewrite the instructions and metadata of an existing skill.\n\n" +
		"Flags you omit keep their current values.",
	Example: "  mcpjungle skills edit code-review --description \"Review diffs carefully\" --instructions \"Focus on tests.\"",
	Args:    cobra.ExactArgs(1),
	RunE:    runSkillsEdit,
}

func init() {
	skillsAddCmd.Flags().StringSliceVar(&skillsAddCmdSkills, "skill", nil, "name of a skill to install; can be repeated")
	skillsAddCmd.Flags().BoolVar(&skillsAddCmdAll, "all", false, "install every skill in the source")
	skillsAddCmd.Flags().BoolVar(&skillsAddCmdOverwrite, "overwrite", false, "replace skills that are already installed")
	skillsAddCmd.Flags().BoolVar(&skillsAddCmdList, "list", false, "only list the skills in the source, don't install")

	for _, cmd := range []*cobra.Command{skillsCreateCmd, skillsEditCmd} {
		cmd.Flags().StringVar(&skillsWriteDescription, "description", "", "short description used to decide when to load the skill")
		cmd.Flags().StringVar(&skillsWriteLicense, "license", "", "optional license")
		cmd.Flags().StringVar(&skillsWriteCompatibility, "compatibility", "", "optional compatibility notes")
		cmd.Flags().StringVar(&skillsWriteInstructions, "instructions", "", "markdown body of SKILL.md, without frontmatter")
	}
	_ = skillsCreateCmd.MarkFlagRequired("description")

	skillsCmd.AddCommand(
		skillsListCmd, skillsAddCmd, skillsCreateCmd, skillsEditCmd,
		skillsRemoveCmd, skillsUpdateCmd, skillsReloadCmd,
	)
	rootCmd.AddCommand(skillsCmd)
}

func runSkillsList(cmd *cobra.Command, args []string) error {
	resp, err := apiClient.ListSkills()
	if err != nil {
		return fmt.Errorf("failed to list skills: %w", err)
	}
	if !resp.Enabled {
		cmd.Println("Skills are not enabled. Start the server with --skills-dir or --skills-install-dir.")
		return nil
	}
	if len(resp.Skills) == 0 {
		cmd.Println("No skills available. Install some with 'mcpjungle skills add <source>'.")
		return nil
	}
	for i, sk := range resp.Skills {
		cmd.Printf("%d. %s\n", i+1, sk.Name)
		cmd.Printf("   %s\n", sk.Description)
		if sk.Origin != nil {
			cmd.Printf("   Installed from %s\n", originLabel(sk.Origin))
		} else if !sk.Removable {
			cmd.Println("   Provided by a skills directory")
		}
		cmd.Println()
	}
	return nil
}

func originLabel(o *types.SkillOrigin) string {
	label := o.Repo
	if o.Path != "" {
		label += "/" + o.Path
	}
	if o.Ref != "" {
		label += "@" + o.Ref
	}
	return label
}

func runSkillsAdd(cmd *cobra.Command, args []string) error {
	source := args[0]

	names := skillsAddCmdSkills
	if skillsAddCmdList || (!skillsAddCmdAll && len(names) == 0) {
		preview, err := apiClient.PreviewSkills(source)
		if err != nil {
			return fmt.Errorf("failed to read skills from %s: %w", source, err)
		}
		if skillsAddCmdList || len(preview.Skills) > 1 {
			cmd.Printf("Found %d skill(s) in %s:\n\n", len(preview.Skills), preview.Source)
			for _, c := range preview.Skills {
				status := ""
				switch {
				case c.Conflict != "":
					status = " (unavailable: " + c.Conflict + ")"
				case c.Installed:
					status = " (installed)"
				}
				cmd.Printf("  %s%s\n    %s\n", c.Name, status, c.Description)
			}
			if !skillsAddCmdList {
				cmd.Println("\nChoose skills to install with --skill <name>, or install all of them with --all.")
			}
			return nil
		}
		names = []string{preview.Skills[0].Name}
	}

	resp, err := apiClient.InstallSkills(&types.InstallSkillsInput{
		Source:    source,
		Skills:    names,
		Overwrite: skillsAddCmdOverwrite,
	})
	if err != nil {
		return fmt.Errorf("failed to install skills: %w", err)
	}
	installed := make([]string, len(resp.Installed))
	for i, sk := range resp.Installed {
		installed[i] = sk.Name
	}
	cmd.Printf("Installed %d skill(s): %s\n", len(installed), strings.Join(installed, ", "))
	cmd.Println("MCP clients can use them right away through the skills__* tools and prompts.")
	return nil
}

func runSkillsCreate(cmd *cobra.Command, args []string) error {
	sk, err := apiClient.CreateSkill(&types.CreateSkillInput{
		Name:          args[0],
		Description:   skillsWriteDescription,
		License:       skillsWriteLicense,
		Compatibility: skillsWriteCompatibility,
		Instructions:  skillsWriteInstructions,
	})
	if err != nil {
		return fmt.Errorf("failed to create skill %s: %w", args[0], err)
	}
	cmd.Printf("Created skill %s\n", sk.Name)
	cmd.Println("MCP clients can use it right away through the skills__* tools and prompts.")
	return nil
}

func runSkillsEdit(cmd *cobra.Command, args []string) error {
	existing, err := apiClient.GetSkill(args[0])
	if err != nil {
		return fmt.Errorf("failed to load skill %s: %w", args[0], err)
	}
	input := &types.EditSkillInput{
		Description:   existing.Description,
		License:       existing.License,
		Compatibility: existing.Compatibility,
		Instructions:  existing.Instructions,
	}
	if cmd.Flags().Changed("description") {
		input.Description = skillsWriteDescription
	}
	if cmd.Flags().Changed("license") {
		input.License = skillsWriteLicense
	}
	if cmd.Flags().Changed("compatibility") {
		input.Compatibility = skillsWriteCompatibility
	}
	if cmd.Flags().Changed("instructions") {
		input.Instructions = skillsWriteInstructions
	}
	sk, err := apiClient.EditSkill(args[0], input)
	if err != nil {
		return fmt.Errorf("failed to edit skill %s: %w", args[0], err)
	}
	cmd.Printf("Updated skill %s\n", sk.Name)
	return nil
}
