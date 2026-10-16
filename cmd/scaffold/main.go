package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"text/template"
	"time"
	"unicode"
)

type Answers struct {
	Name            string    `json:"name"`
	Module          string    `json:"module"`
	Examples        string    `json:"examples"`
	TemplateVersion string    `json:"template_version"`
	ScaffoldedAt    time.Time `json:"scaffolded_at"`
}

var (
	validFeatureName = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	cmd := os.Args[1]
	args := os.Args[2:]

	var err error
	switch cmd {
	case "init":
		err = runInit(args)
	case "strip-examples":
		err = runStripExamplesCmd(args)
	case "new-feature":
		err = runNewFeature(args)
	case "template-diff":
		err = runTemplateDiff(args)
	case "help", "-h", "--help":
		printUsage()
		return
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n", cmd)
		printUsage()
		os.Exit(1)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func printOut(format string, args ...any) {
	_, _ = fmt.Fprintf(os.Stdout, format, args...)
}

func printlnOut(args ...any) {
	_, _ = fmt.Fprintln(os.Stdout, args...)
}

func printUsage() {
	printlnOut(`Usage: scaffold <command> [options]

Commands:
  init            Initialize a new project from the template
  strip-examples  Remove example code, migrations, and queries
  new-feature     Generate boilerplate for a new business capability
  template-diff   Show and classify changes from upstream template`)
}

// -----------------------------------------------------------------------------
// init
// -----------------------------------------------------------------------------

func runInit(args []string) error {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	nameFlag := fs.String("name", os.Getenv("NAME"), "Application name (e.g. my-app)")
	moduleFlag := fs.String("module", os.Getenv("MODULE"), "Go module path (e.g. github.com/owner/my-app)")
	examplesFlag := fs.String("examples", os.Getenv("EXAMPLES"), "Keep or strip examples: keep | strip")
	dryRun := fs.Bool("n", false, "Dry run; show actions without modifying disk")
	dryRunLong := fs.Bool("dry-run", false, "Dry run; show actions without modifying disk")
	force := fs.Bool("force", false, "Force re-initialization even if answers.json exists")

	if err := fs.Parse(args); err != nil {
		return err
	}
	isDryRun := *dryRun || *dryRunLong

	answersPath := filepath.Join(".template", "answers.json")
	if _, err := os.Stat(answersPath); err == nil && !*force {
		return fmt.Errorf("template has already been initialized (found %s; use --force to overwrite)", answersPath)
	}

	currentMod, err := readCurrentModule()
	if err != nil {
		return fmt.Errorf("reading go.mod: %w", err)
	}

	// Detect if post-gonew: gonew already rewrote go.mod to the user's chosen module.
	standinMod := "example" + ".com/app"
	isPostGonew := currentMod != standinMod && currentMod != "{"+"{MODULE_PATH}}"

	name := *nameFlag
	module := *moduleFlag
	examples := *examplesFlag

	if isPostGonew && module == "" {
		module = currentMod
	}

	if isTerminal(os.Stdin) {
		if module == "" {
			module = promptUserWithDefault("Go module path (e.g. github.com/org/repo)", "")
		}
		if name == "" {
			defaultName := ""
			if module != "" {
				parts := strings.Split(module, "/")
				defaultName = parts[len(parts)-1]
			}
			name = promptUserWithDefault("Application name", defaultName)
		}
		if examples == "" {
			examples = promptUserWithDefault("Include example code (orders, acme webhook)? [keep/strip]", "keep")
		}
	} else {
		if name == "" && module != "" {
			parts := strings.Split(module, "/")
			name = parts[len(parts)-1]
		}
		if examples == "" {
			examples = "keep"
		}
	}

	switch strings.ToLower(examples) {
	case "keep", "k", "yes", "y":
		examples = "keep"
	case "strip", "s", "no", "n":
		examples = "strip"
	}

	if module == "" {
		return errors.New("module path is required (pass --module or set MODULE)")
	}
	if name == "" {
		return errors.New("application name is required (pass --name or set NAME)")
	}
	if examples != "keep" && examples != "strip" {
		return fmt.Errorf("invalid examples value %q; must be 'keep' or 'strip'", examples)
	}

	version := "0.2.0"
	if data, err := os.ReadFile("TEMPLATE_VERSION"); err == nil {
		version = strings.TrimSpace(string(data))
	}

	printOut("Initializing project:\n  Name:     %s\n  Module:   %s\n  Examples: %s\n  Version:  %s\n",
		name, module, examples, version)
	if isDryRun {
		printlnOut("==> DRY RUN MODE: No files will be modified <==")
	}

	// Replace tokens in repository files
	replacements := map[string]string{
		"{" + "{APP_NAME}}":    name,
		"{" + "{MODULE_PATH}}": module,
		standinMod:             module,
	}

	err = replaceTokensInRepo(".", replacements, isDryRun)
	if err != nil {
		return fmt.Errorf("replacing tokens: %w", err)
	}

	if examples == "strip" {
		if err := executeStripExamples(".", isDryRun); err != nil {
			return fmt.Errorf("stripping examples: %w", err)
		}
	}

	if !isDryRun {
		ans := Answers{
			Name:            name,
			Module:          module,
			Examples:        examples,
			TemplateVersion: version,
			ScaffoldedAt:    time.Now().UTC(),
		}
		if err := os.MkdirAll(".template", 0o755); err != nil {
			return err
		}
		ansBytes, err := json.MarshalIndent(ans, "", "  ")
		if err != nil {
			return err
		}
		if err := os.WriteFile(answersPath, append(ansBytes, '\n'), 0o644); err != nil {
			return err
		}

		// Format files and run go mod tidy
		_ = runCmd(context.Background(), "gofmt", "-w", ".")
		_ = runCmd(context.Background(), "go", "mod", "tidy")
	}

	printlnOut("Initialization complete.")
	return nil
}

func replaceTokensInRepo(root string, replacements map[string]string, dryRun bool) error {
	skipDirs := map[string]bool{
		".git":         true,
		".template":    true,
		"bin":          true,
		"node_modules": true,
	}

	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skipDirs[d.Name()] || d.Name() == "scaffold" {
				return filepath.SkipDir
			}
			return nil
		}

		if isBinaryOrIgnored(path) {
			return nil
		}

		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}

		modified := string(content)
		changed := false
		for oldToken, newToken := range replacements {
			if strings.Contains(modified, oldToken) {
				modified = strings.ReplaceAll(modified, oldToken, newToken)
				changed = true
			}
		}

		if changed {
			if dryRun {
				printOut("Would update: %s\n", path)
			} else {
				if err := os.WriteFile(path, []byte(modified), 0o644); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

// -----------------------------------------------------------------------------
// strip-examples
// -----------------------------------------------------------------------------

func runStripExamplesCmd(args []string) error {
	fs := flag.NewFlagSet("strip-examples", flag.ContinueOnError)
	confirmFlag := fs.Bool("confirm", false, "Confirm removal of example code")
	dryRun := fs.Bool("n", false, "Dry run; show actions without modifying disk")
	dryRunLong := fs.Bool("dry-run", false, "Dry run; show actions without modifying disk")

	if err := fs.Parse(args); err != nil {
		return err
	}

	confirmed := *confirmFlag || strings.EqualFold(os.Getenv("CONFIRM"), "yes")
	if !confirmed {
		return errors.New("strip-examples removes all example code. To proceed, pass CONFIRM=yes or --confirm")
	}

	return executeStripExamples(".", *dryRun || *dryRunLong)
}

func executeStripExamples(root string, dryRun bool) error {
	// 1. Process inline markers in all text files
	err := stripInlineMarkersInRepo(root, dryRun)
	if err != nil {
		return fmt.Errorf("stripping inline markers: %w", err)
	}

	// 2. Remove whole files carrying template:example:file
	err = stripMarkedFiles(root, dryRun)
	if err != nil {
		return fmt.Errorf("removing example files: %w", err)
	}

	// 3. Remove example directories
	exampleDirs := []string{
		filepath.Join(root, "internal", "orders"),
		filepath.Join(root, "internal", "integrations", "acme"),
	}
	for _, dir := range exampleDirs {
		if _, err := os.Stat(dir); err == nil {
			if dryRun {
				printOut("Would remove directory: %s\n", dir)
			} else {
				if err := os.RemoveAll(dir); err != nil {
					return err
				}
			}
		}
	}

	// 4. Format files, regenerate sqlc and go mod tidy
	if !dryRun {
		_ = runCmd(context.Background(), "gofmt", "-w", ".")
		_ = runCmd(context.Background(), "sqlc", "generate")
		_ = runCmd(context.Background(), "go", "mod", "tidy")
	}

	return nil
}

func stripInlineMarkersInRepo(root string, dryRun bool) error {
	skipDirs := map[string]bool{
		".git":      true,
		".template": true,
		"templates": true, // Do not alter code generator templates
	}

	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skipDirs[d.Name()] || d.Name() == "scaffold" {
				return filepath.SkipDir
			}
			return nil
		}
		if isBinaryOrIgnored(path) {
			return nil
		}

		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}

		cleaned, changed, err := processInlineMarkers(string(content), path)
		if err != nil {
			return err
		}

		if changed {
			if dryRun {
				printOut("Would strip markers from: %s\n", path)
			} else {
				if err := os.WriteFile(path, []byte(cleaned), 0o644); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

var (
	beginMarkerRe = regexp.MustCompile(`^(?://|#|--)\s*template:example:begin(?:\s+([a-zA-Z0-9_\-]+))?`)
	endMarkerRe   = regexp.MustCompile(`^(?://|#|--)\s*template:example:end(?:\s+([a-zA-Z0-9_\-]+))?`)
	fileMarkerRe  = regexp.MustCompile(`(?://|#|--)\s*template:example:file`)
)

func processInlineMarkers(content string, filename string) (string, bool, error) {
	scanner := bufio.NewScanner(strings.NewReader(content))
	var out []string
	var activeTag string
	var beginLine int
	lineNum := 0
	changed := false

	for scanner.Scan() {
		lineNum++
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)

		if beginMatch := beginMarkerRe.FindStringSubmatch(trimmed); len(beginMatch) > 0 {
			if activeTag != "" {
				return "", false, fmt.Errorf("%s:%d: nested template:example:begin inside tag %q (started at line %d)",
					filename, lineNum, activeTag, beginLine)
			}
			activeTag = beginMatch[1]
			if activeTag == "" {
				activeTag = "example"
			}
			beginLine = lineNum
			changed = true
			continue
		}

		if endMatch := endMarkerRe.FindStringSubmatch(trimmed); len(endMatch) > 0 {
			if activeTag == "" {
				return "", false, fmt.Errorf("%s:%d: template:example:end without matching begin", filename, lineNum)
			}
			endTag := endMatch[1]
			if endTag == "" {
				endTag = "example"
			}
			if endTag != activeTag && activeTag != "example" && endTag != "example" {
				return "", false, fmt.Errorf("%s:%d: mismatched template:example:end %q (expected %q from line %d)",
					filename, lineNum, endTag, activeTag, beginLine)
			}
			activeTag = ""
			changed = true
			continue
		}

		if activeTag != "" {
			// Skip content inside example block
			continue
		}

		out = append(out, line)
	}

	if activeTag != "" {
		return "", false, fmt.Errorf("%s:%d: unclosed template:example:begin %q at end of file",
			filename, beginLine, activeTag)
	}

	if !changed {
		return content, false, nil
	}

	res := strings.Join(out, "\n")
	if strings.HasSuffix(content, "\n") && !strings.HasSuffix(res, "\n") {
		res += "\n"
	}
	return res, true, nil
}

func stripMarkedFiles(root string, dryRun bool) error {
	skipDirs := map[string]bool{
		".git":      true,
		".template": true,
		"templates": true,
	}

	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skipDirs[d.Name()] || d.Name() == "scaffold" {
				return filepath.SkipDir
			}
			return nil
		}
		if isBinaryOrIgnored(path) {
			return nil
		}

		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}

		if fileMarkerRe.Match(content) {
			if dryRun {
				printOut("Would delete file: %s\n", path)
			} else {
				if err := os.Remove(path); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

// -----------------------------------------------------------------------------
// new-feature
// -----------------------------------------------------------------------------

type FeatureData struct {
	Name     string
	Package  string
	Singular string
	Title    string
	Module   string
}

func runNewFeature(args []string) error {
	fs := flag.NewFlagSet("new-feature", flag.ContinueOnError)
	nameFlag := fs.String("name", os.Getenv("NAME"), "Feature name (e.g. billing)")

	if err := fs.Parse(args); err != nil {
		return err
	}

	name := strings.ToLower(strings.TrimSpace(*nameFlag))
	if name == "" {
		return errors.New("feature name is required (pass --name or set NAME)")
	}

	if !validFeatureName.MatchString(name) {
		return fmt.Errorf("invalid feature name %q: must start with a lowercase letter and contain only lowercase letters, digits, and underscores", name)
	}

	mod, err := readCurrentModule()
	if err != nil {
		return fmt.Errorf("reading module path: %w", err)
	}

	singular := strings.TrimSuffix(name, "s")
	if singular == "" {
		singular = name
	}
	title := toPascalCase(singular)

	data := FeatureData{
		Name:     name,
		Package:  name,
		Singular: singular,
		Title:    title,
		Module:   mod,
	}

	featureDir := filepath.Join("internal", name)
	if _, err := os.Stat(featureDir); err == nil {
		return fmt.Errorf("feature directory %s already exists", featureDir)
	}

	if err := os.MkdirAll(featureDir, 0o755); err != nil {
		return err
	}

	tmplMap := map[string]string{
		"feature.go.tmpl":      name + ".go",
		"service.go.tmpl":      "service.go",
		"repo.go.tmpl":         "repo.go",
		"handler.go.tmpl":      "handler.go",
		"service_test.go.tmpl": "service_test.go",
	}

	for tmplName, outName := range tmplMap {
		tmplPath := filepath.Join("templates", "feature", tmplName)
		content, err := os.ReadFile(tmplPath)
		if err != nil {
			return fmt.Errorf("reading template %s: %w", tmplPath, err)
		}

		t, err := template.New(tmplName).Delims("[[", "]]").Parse(string(content))
		if err != nil {
			return fmt.Errorf("parsing template %s: %w", tmplPath, err)
		}

		var buf bytes.Buffer
		if err := t.Execute(&buf, data); err != nil {
			return fmt.Errorf("executing template %s: %w", tmplPath, err)
		}

		outPath := filepath.Join(featureDir, outName)
		if err := os.WriteFile(outPath, buf.Bytes(), 0o644); err != nil {
			return err
		}
		printOut("Created: %s\n", outPath)
	}

	// Create migration: db/migrations/<timestamp>_<name>.sql
	timestamp := time.Now().UTC().Format("20060102150405")
	migPath := filepath.Join("db", "migrations", fmt.Sprintf("%s_%s.sql", timestamp, name))
	migContent := fmt.Sprintf(`-- +goose Up
CREATE TABLE IF NOT EXISTS %s (
    id         uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    name       text        NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE IF EXISTS %s;
`, name, name)

	if err := os.WriteFile(migPath, []byte(migContent), 0o644); err != nil {
		return err
	}
	printOut("Created: %s\n", migPath)

	// Create query: db/queries/<name>.sql
	queryPath := filepath.Join("db", "queries", fmt.Sprintf("%s.sql", name))
	queryContent := fmt.Sprintf(`-- name: Get%s :one
SELECT * FROM %s
WHERE id = sqlc.arg(id);

-- name: List%s :many
SELECT * FROM %s
ORDER BY created_at DESC
LIMIT sqlc.arg(page_size)::bigint;
`, title, name, toPascalCase(name), name)

	if err := os.WriteFile(queryPath, []byte(queryContent), 0o644); err != nil {
		return err
	}
	printOut("Created: %s\n", queryPath)

	printOut("Feature %q scaffolded successfully.\n", name)
	return nil
}

func toPascalCase(s string) string {
	parts := strings.Split(s, "_")
	var b strings.Builder
	for _, p := range parts {
		if len(p) == 0 {
			continue
		}
		runes := []rune(p)
		runes[0] = unicode.ToUpper(runes[0])
		b.WriteString(string(runes))
	}
	return b.String()
}

// -----------------------------------------------------------------------------
// template-diff
// -----------------------------------------------------------------------------

func runTemplateDiff(args []string) error {
	fs := flag.NewFlagSet("template-diff", flag.ContinueOnError)
	tagFlag := fs.String("tag", os.Getenv("TAG"), "Upstream template tag or commit to compare against")

	if err := fs.Parse(args); err != nil {
		return err
	}

	tag := *tagFlag
	if tag == "" {
		// Read ref from .template/origin if present
		if data, err := os.ReadFile(filepath.Join(".template", "origin")); err == nil {
			for _, line := range strings.Split(string(data), "\n") {
				line = strings.TrimSpace(line)
				if strings.HasPrefix(line, "ref:") {
					tag = strings.TrimSpace(strings.TrimPrefix(line, "ref:"))
					break
				}
			}
		}
	}
	if tag == "" {
		return errors.New("tag or base commit is required (pass --tag or set TAG)")
	}

	ownership, err := loadOwnership()
	if err != nil {
		return fmt.Errorf("loading ownership rules: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "git", "diff", "--name-status", tag+"...HEAD")
	out, err := cmd.Output()
	if err != nil {
		// Try two-dot diff
		cmd = exec.CommandContext(ctx, "git", "diff", "--name-status", tag)
		out, err = cmd.Output()
		if err != nil {
			return fmt.Errorf("running git diff against %s: %w", tag, err)
		}
	}

	grouped := map[string][]string{
		"template": nil,
		"shared":   nil,
		"project":  nil,
	}

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) < 2 {
			continue
		}
		status := parts[0]
		path := parts[len(parts)-1]

		tier := classifyPath(path, ownership)
		grouped[tier] = append(grouped[tier], fmt.Sprintf("  %-2s %s", status, path))
	}

	printOut("Upstream changes compared with %s:\n\n", tag)
	printlnOut("[template] (upstream-owned — recommended to adopt)")
	if len(grouped["template"]) == 0 {
		printlnOut("  (no changes)")
	} else {
		for _, item := range grouped["template"] {
			printlnOut(item)
		}
	}
	printlnOut()

	printlnOut("[shared] (extension points — review and merge)")
	if len(grouped["shared"]) == 0 {
		printlnOut("  (no changes)")
	} else {
		for _, item := range grouped["shared"] {
			printlnOut(item)
		}
	}
	printlnOut()

	printlnOut("[project] (project-owned — downstream edits)")
	if len(grouped["project"]) == 0 {
		printlnOut("  (no changes)")
	} else {
		for _, item := range grouped["project"] {
			printlnOut(item)
		}
	}

	return nil
}

type OwnershipRule struct {
	Prefix string
	Tier   string
}

func loadOwnership() ([]OwnershipRule, error) {
	data, err := os.ReadFile(filepath.Join(".template", "OWNERSHIP"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var rules []OwnershipRule
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) >= 2 {
			rules = append(rules, OwnershipRule{
				Prefix: fields[0],
				Tier:   fields[1],
			})
		}
	}
	return rules, nil
}

func classifyPath(path string, rules []OwnershipRule) string {
	for _, r := range rules {
		cleanPrefix := strings.TrimSuffix(r.Prefix, "/")
		if path == r.Prefix || path == cleanPrefix || strings.HasPrefix(path, cleanPrefix+"/") {
			return r.Tier
		}
	}
	return "project"
}

// -----------------------------------------------------------------------------
// Utilities
// -----------------------------------------------------------------------------

func readCurrentModule() (string, error) {
	data, err := os.ReadFile("go.mod")
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "module ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "module")), nil
		}
	}
	return "", errors.New("no module statement found in go.mod")
}

var stdinReader io.Reader = os.Stdin

func promptUserWithDefault(msg, defaultVal string) string {
	if defaultVal != "" {
		printOut("%s [%s]: ", msg, defaultVal)
	} else {
		printOut("%s: ", msg)
	}
	reader := bufio.NewReader(stdinReader)
	text, _ := reader.ReadString('\n')
	text = strings.TrimSpace(text)
	if text == "" {
		return defaultVal
	}
	return text
}

func isTerminal(f *os.File) bool {
	stat, err := f.Stat()
	if err != nil {
		return false
	}
	return (stat.Mode() & os.ModeCharDevice) != 0
}

func isBinaryOrIgnored(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".png", ".jpg", ".jpeg", ".gif", ".ico", ".pdf", ".zip", ".tar", ".gz", ".exe":
		return true
	}
	return false
}

func runCmd(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdout = io.Discard
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
