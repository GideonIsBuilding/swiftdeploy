package internal

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Manifest is the full structure of manifest.yaml
type Manifest struct {
	Services ServicesConfig `yaml:"services"`
	Nginx    NginxConfig    `yaml:"nginx"`
	Network  NetworkConfig  `yaml:"network"`
}

type ServicesConfig struct {
	Image   string `yaml:"image"`
	Port    int    `yaml:"port"`
	Mode    string `yaml:"mode"`
	Version string `yaml:"version"`
}

type NginxConfig struct {
	Image        string `yaml:"image"`
	Port         int    `yaml:"port"`
	ProxyTimeout int    `yaml:"proxy_timeout"`
}

type NetworkConfig struct {
	Name       string `yaml:"name"`
	DriverType string `yaml:"driver_type"`
}

// LoadManifest reads and parses manifest.yaml
func LoadManifest(path string) (*Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading manifest: %w", err)
	}
	var m Manifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parsing manifest: %w", err)
	}
	return &m, nil
}

// ValidateFields checks all required fields are present
func (m *Manifest) ValidateFields() error {
	if m.Services.Image == "" {
		return fmt.Errorf("services.image is required")
	}
	if m.Services.Port == 0 {
		return fmt.Errorf("services.port is required")
	}
	if m.Nginx.Image == "" {
		return fmt.Errorf("nginx.image is required")
	}
	if m.Nginx.Port == 0 {
		return fmt.Errorf("nginx.port is required")
	}
	if m.Network.Name == "" {
		return fmt.Errorf("network.name is required")
	}
	if m.Network.DriverType == "" {
		return fmt.Errorf("network.driver_type is required")
	}
	return nil
}

// UpdateMode updates services.mode in manifest.yaml in-place,
// preserving all other fields and structure.
func UpdateMode(path, mode string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading manifest: %w", err)
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("parsing manifest: %w", err)
	}

	// doc.Content[0] is the top-level mapping node
	root := doc.Content[0]
	for i := 0; i < len(root.Content)-1; i += 2 {
		if root.Content[i].Value == "services" {
			services := root.Content[i+1]
			for j := 0; j < len(services.Content)-1; j += 2 {
				if services.Content[j].Value == "mode" {
					services.Content[j+1].Value = mode
					out, err := yaml.Marshal(&doc)
					if err != nil {
						return fmt.Errorf("marshaling manifest: %w", err)
					}
					return os.WriteFile(path, out, 0644)
				}
			}
			// mode key doesn't exist yet — add it
			services.Content = append(services.Content,
				&yaml.Node{Kind: yaml.ScalarNode, Value: "mode"},
				&yaml.Node{Kind: yaml.ScalarNode, Value: mode},
			)
			out, err := yaml.Marshal(&doc)
			if err != nil {
				return fmt.Errorf("marshaling manifest: %w", err)
			}
			return os.WriteFile(path, out, 0644)
		}
	}
	return fmt.Errorf("services section not found in manifest")
}
