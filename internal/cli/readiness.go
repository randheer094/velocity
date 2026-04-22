package cli

import (
	"embed"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

//go:embed all:templates
var readinessTemplates embed.FS

type projectType string

const (
	projectGo      projectType = "go"
	projectAndroid projectType = "android"
	projectUnknown projectType = ""
)

const (
	claudeDirPath   = ".claude"
	claudeMdPath    = ".claude/CLAUDE.md"
	prepareSkillRel = ".claude/skills/prepare-for-pr/SKILL.md"
)

type readinessReport struct {
	root        string
	projectType projectType
	claudeMd    checkResult
	claudeDir   checkResult
	prepareSkill checkResult
}

type checkResult struct {
	name string
	ok   bool
	detail string
}

func (r readinessReport) ready() bool {
	return r.claudeMd.ok && r.claudeDir.ok && r.prepareSkill.ok
}

func (r readinessReport) write(w io.Writer) {
	fmt.Fprintf(w, "Velocity readiness report for %s\n", r.root)
	if r.projectType == projectUnknown {
		fmt.Fprintln(w, "Project type: unknown (neither Go nor Android markers found)")
	} else {
		fmt.Fprintf(w, "Project type: %s\n", r.projectType)
	}
	fmt.Fprintln(w)
	for _, c := range []checkResult{r.claudeDir, r.claudeMd, r.prepareSkill} {
		mark := "[FAIL]"
		if c.ok {
			mark = "[ OK ]"
		}
		fmt.Fprintf(w, "  %s %s\n", mark, c.name)
		if c.detail != "" {
			fmt.Fprintf(w, "         %s\n", c.detail)
		}
	}
	fmt.Fprintln(w)
	if r.ready() {
		fmt.Fprintln(w, "Result: READY")
		return
	}
	fmt.Fprintln(w, "Result: NOT READY")
	if r.projectType == projectUnknown {
		fmt.Fprintln(w, "Hint: add a go.mod (Go) or build.gradle[.kts] / settings.gradle[.kts] (Android), then run `velocity prepare <path>`.")
	} else {
		fmt.Fprintf(w, "Hint: run `velocity prepare %s` to install the missing pieces.\n", r.root)
	}
}

func detectProjectType(root string) projectType {
	if fileExists(filepath.Join(root, "go.mod")) {
		return projectGo
	}
	androidMarkers := []string{
		"build.gradle",
		"build.gradle.kts",
		"settings.gradle",
		"settings.gradle.kts",
	}
	for _, m := range androidMarkers {
		if fileExists(filepath.Join(root, m)) {
			return projectAndroid
		}
	}
	return projectUnknown
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !info.IsDir()
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.IsDir()
}

func inspectReadiness(root string) readinessReport {
	r := readinessReport{
		root:        root,
		projectType: detectProjectType(root),
	}

	r.claudeDir = checkResult{name: ".claude/ directory at project root"}
	if dirExists(filepath.Join(root, claudeDirPath)) {
		r.claudeDir.ok = true
	} else {
		r.claudeDir.detail = "missing — velocity stores CLAUDE.md, skills, and rules under .claude/"
	}

	r.claudeMd = checkResult{name: "CLAUDE.md under .claude/ (.claude/CLAUDE.md)"}
	if fileExists(filepath.Join(root, claudeMdPath)) {
		r.claudeMd.ok = true
	} else {
		r.claudeMd.detail = "missing — create .claude/CLAUDE.md or run `velocity prepare <path>`"
	}

	r.prepareSkill = checkResult{name: "prepare-for-pr skill installed (.claude/skills/prepare-for-pr/SKILL.md)"}
	if fileExists(filepath.Join(root, prepareSkillRel)) {
		r.prepareSkill.ok = true
	} else {
		r.prepareSkill.detail = "missing — install with `velocity prepare <path>`"
	}

	return r
}

func newCheckCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "check PROJECTPATH",
		Short: "Report whether a project is ready for velocity",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := resolveProjectPath(args[0])
			if err != nil {
				return err
			}
			report := inspectReadiness(root)
			report.write(cmd.OutOrStdout())
			if !report.ready() {
				return errors.New("project is not velocity-ready")
			}
			return nil
		},
	}
}

func newPrepareCmd() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "prepare PROJECTPATH",
		Short: "Install CLAUDE.md and the prepare-for-pr skill (Go / Android)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := resolveProjectPath(args[0])
			if err != nil {
				return err
			}
			pt := detectProjectType(root)
			if pt == projectUnknown {
				return fmt.Errorf("unsupported project: %s has neither go.mod nor build.gradle[.kts]; prepare currently supports Go and Android", root)
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Detected %s project at %s\n", pt, root)
			written, skipped, err := installTemplates(root, pt, force)
			if err != nil {
				return err
			}
			for _, p := range written {
				fmt.Fprintf(out, "  wrote    %s\n", p)
			}
			for _, p := range skipped {
				fmt.Fprintf(out, "  skipped  %s (exists; pass --force to overwrite)\n", p)
			}
			fmt.Fprintln(out)
			fmt.Fprintln(out, "Done. Run `velocity check "+root+"` to verify.")
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "Overwrite existing files")
	return cmd
}

func resolveProjectPath(raw string) (string, error) {
	if raw == "" {
		return "", errors.New("PROJECTPATH is required")
	}
	abs, err := filepath.Abs(raw)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", raw, err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("stat %s: %w", abs, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s is not a directory", abs)
	}
	return abs, nil
}

func installTemplates(root string, pt projectType, force bool) (written, skipped []string, err error) {
	templateRoot := filepath.ToSlash(filepath.Join("templates", string(pt)))
	err = fs.WalkDir(readinessTemplates, templateRoot, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		rel := strings.TrimPrefix(path, templateRoot+"/")
		dst := filepath.Join(root, filepath.FromSlash(rel))
		if _, statErr := os.Stat(dst); statErr == nil && !force {
			skipped = append(skipped, rel)
			return nil
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		data, err := readinessTemplates.ReadFile(path)
		if err != nil {
			return err
		}
		if err := os.WriteFile(dst, data, 0o644); err != nil {
			return err
		}
		written = append(written, rel)
		return nil
	})
	if err != nil {
		return written, skipped, err
	}
	return written, skipped, nil
}
