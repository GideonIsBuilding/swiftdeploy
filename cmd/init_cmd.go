package cmd

import (
	"fmt"
	"os"
	"text/template"

	"github.com/gideonisbuilding/swiftdeploy/internal"
	"github.com/spf13/cobra"
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Generate nginx.conf and docker-compose.yml from manifest.yaml",
	RunE:  runInit,
}

func runInit(cmd *cobra.Command, args []string) error {
	return generateConfigs()
}

// generateConfigs is shared by init and deploy
func generateConfigs() error {
	m, err := internal.LoadManifest("manifest.yaml")
	if err != nil {
		return fmt.Errorf("❌ Failed to load manifest: %w", err)
	}

	if err := renderTemplate("templates/nginx.conf.tmpl", "nginx.conf", m); err != nil {
		return fmt.Errorf("❌ Failed to generate nginx.conf: %w", err)
	}
	fmt.Println("✅ Generated nginx.conf")

	if err := renderTemplate("templates/docker-compose.yml.tmpl", "docker-compose.yml", m); err != nil {
		return fmt.Errorf("❌ Failed to generate docker-compose.yml: %w", err)
	}
	fmt.Println("✅ Generated docker-compose.yml")

	return nil
}

func renderTemplate(tmplPath, outPath string, data any) error {
	tmplContent, err := os.ReadFile(tmplPath)
	if err != nil {
		return fmt.Errorf("reading template %s: %w", tmplPath, err)
	}

	tmpl, err := template.New(tmplPath).Parse(string(tmplContent))
	if err != nil {
		return fmt.Errorf("parsing template %s: %w", tmplPath, err)
	}

	f, err := os.Create(outPath)
	if err != nil {
		return fmt.Errorf("creating %s: %w", outPath, err)
	}
	defer f.Close()

	if err := tmpl.Execute(f, data); err != nil {
		return fmt.Errorf("rendering template %s: %w", tmplPath, err)
	}
	return nil
}
