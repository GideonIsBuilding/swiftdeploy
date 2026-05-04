package cmd

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/gideonisbuilding/swiftdeploy/internal"
	"github.com/spf13/cobra"
)

var validateCmd = &cobra.Command{
	Use:   "validate",
	Short: "Run 5 pre-flight checks before deploying",
	RunE:  runValidate,
}

func runValidate(cmd *cobra.Command, args []string) error {
	fmt.Printf("🔍 Running pre-flight checks...\n")

	allPassed := true

	// Check 1: manifest.yaml exists and is valid YAML
	allPassed = check(
		"[1/5] manifest.yaml exists and is valid YAML",
		func() error {
			_, err := internal.LoadManifest("manifest.yaml")
			return err
		},
	) && allPassed

	// Check 2: required fields are present and non-empty
	allPassed = check(
		"[2/5] All required fields are present and non-empty",
		func() error {
			m, err := internal.LoadManifest("manifest.yaml")
			if err != nil {
				return err
			}
			return m.ValidateFields()
		},
	) && allPassed

	// Check 3: Docker image exists locally
	allPassed = check(
		"[3/5] Docker image exists locally",
		func() error {
			m, err := internal.LoadManifest("manifest.yaml")
			if err != nil {
				return err
			}
			out, err := exec.Command("docker", "image", "inspect", m.Services.Image).CombinedOutput()
			if err != nil {
				return fmt.Errorf("image %q not found locally — run: docker build -t %s .\n       %s",
					m.Services.Image, m.Services.Image, strings.TrimSpace(string(out)))
			}
			return nil
		},
	) && allPassed

	// Check 4: Nginx port is not already bound — unless it's our own nginx container
	allPassed = check(
		"[4/5] Nginx port is not already bound on the host",
		func() error {
			m, err := internal.LoadManifest("manifest.yaml")
			if err != nil {
				return err
			}
			addr := fmt.Sprintf(":%d", m.Nginx.Port)
			ln, err := net.Listen("tcp", addr)
			if err != nil {
				// Port is in use — check if it's our own nginx container
				out, dockerErr := exec.Command(
					"docker", "ps", "--filter", "name=nginx",
					"--filter", fmt.Sprintf("publish=%d", m.Nginx.Port),
					"--format", "{{.Names}}",
				).Output()
				if dockerErr == nil && strings.TrimSpace(string(out)) != "" {
					// Our nginx is already running — this is fine for validate
					return nil
				}
				return fmt.Errorf("port %d is already in use by another process", m.Nginx.Port)
			}
			ln.Close()
			return nil
		},
	) && allPassed

	// Check 5: nginx.conf is syntactically valid
	// We replace the "app" upstream hostname with "127.0.0.1" before testing
	// because nginx -t resolves upstream hostnames, and "app" only exists
	// inside the Docker network (not on the host during validation).
	allPassed = check(
		"[5/5] Generated nginx.conf is syntactically valid",
		func() error {
			if _, err := os.Stat("nginx.conf"); os.IsNotExist(err) {
				return fmt.Errorf("nginx.conf not found — run: swiftdeploy init first")
			}

			// Read the real config
			confData, err := os.ReadFile("nginx.conf")
			if err != nil {
				return fmt.Errorf("reading nginx.conf: %w", err)
			}

			// Substitute the Docker-internal upstream with 127.0.0.1 for validation only.
			// We match just "http://app:" so whitespace differences don't matter.
			testConf := strings.ReplaceAll(string(confData), "http://app:", "http://127.0.0.1:")

			// Write to a temp file
			tmpFile, err := os.CreateTemp("", "nginx-validate-*.conf")
			if err != nil {
				return fmt.Errorf("creating temp config: %w", err)
			}
			tmpPath := tmpFile.Name()
			defer os.Remove(tmpPath)

			if _, err := tmpFile.WriteString(testConf); err != nil {
				tmpFile.Close()
				return fmt.Errorf("writing temp config: %w", err)
			}
			tmpFile.Close()

			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			_ = cwd

			// Run nginx -t inside Docker using the substituted config
			out, err := exec.Command(
				"docker", "run", "--rm",
				"-v", fmt.Sprintf("%s:/etc/nginx/nginx.conf:ro", tmpPath),
				"nginx:latest",
				"nginx", "-t", "-c", "/etc/nginx/nginx.conf",
			).CombinedOutput()
			if err != nil {
				// Filter out the noisy docker-entrypoint output — only show nginx errors
				lines := strings.Split(string(out), "\n")
				var nginxErrors []string
				for _, l := range lines {
					if strings.Contains(l, "[emerg]") || strings.Contains(l, "[error]") || strings.Contains(l, "test failed") {
						nginxErrors = append(nginxErrors, l)
					}
				}
				if len(nginxErrors) > 0 {
					return fmt.Errorf("nginx config invalid:\n       %s", strings.Join(nginxErrors, "\n       "))
				}
				return fmt.Errorf("nginx config invalid:\n%s", strings.TrimSpace(string(out)))
			}
			return nil
		},
	) && allPassed

	fmt.Println()
	if allPassed {
		fmt.Println("✅ All checks passed. Ready to deploy.")
		return nil
	}

	fmt.Println("❌ One or more checks failed. Fix the issues above before deploying.")
	os.Exit(1)
	return nil
}

// check runs a single validation and prints PASS/FAIL
func check(name string, fn func() error) bool {
	err := fn()
	if err != nil {
		fmt.Printf("  ❌ FAIL  %s\n     → %s\n", name, err)
		return false
	}
	fmt.Printf("  ✅ PASS  %s\n", name)
	// Small pause so output is readable
	time.Sleep(100 * time.Millisecond)
	return true
}
