package buildinfo

import (
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReleaseIdentityIsConsistent(t *testing.T) {
	root := repositoryRoot(t)
	version := strings.TrimSpace(readReleaseFile(t, root, "VERSION"))

	var manifest struct {
		Version  string `json:"version"`
		Artifact string `json:"artifact"`
		Checksum string `json:"checksum"`
	}
	if err := json.Unmarshal([]byte(readReleaseFile(t, root, "RELEASE-MANIFEST.json")), &manifest); err != nil {
		t.Fatal(err)
	}
	expectedArtifact := "control-center-" + version + "-linux-amd64.tar.gz"
	if manifest.Version != version || manifest.Artifact != expectedArtifact || manifest.Checksum != expectedArtifact+".sha256" {
		t.Fatalf("release manifest does not match VERSION %q: %+v", version, manifest)
	}

	required := map[string][]string{
		"README.md":                    {"# Control Center " + version, "Version `" + version + "` is the current stable release."},
		"INSTALL.md":                   {"# Install Control Center " + version, "version=" + version},
		"RELEASE_NOTES.md":             {"# Control Center " + version + " release notes"},
		"SECURITY.md":                  {"Version `" + version + "` receives security fixes."},
		"Dockerfile":                   {"ARG VERSION=" + version},
		"api/openapi-0.3.yaml":         {"  version: " + version},
		".github/workflows/verify.yml": {expectedArtifact, "control-center-" + version + "-${{ github.sha }}"},
		".github/workflows/release.yml": {
			"release/" + version,
			"tag=v" + version,
			expectedArtifact,
		},
	}
	for path, tokens := range required {
		content := readReleaseFile(t, root, path)
		for _, token := range tokens {
			if !strings.Contains(content, token) {
				t.Errorf("%s does not contain release identity %q", path, token)
			}
		}
	}
}

func TestInstallMigrationUsesSystemdEnvironmentFile(t *testing.T) {
	root := repositoryRoot(t)
	install := readReleaseFile(t, root, "INSTALL.md")
	for _, forbidden := range []string{
		". /etc/control-center/control-center.env",
		"source /etc/control-center/control-center.env",
		"sudo sh -c",
	} {
		if strings.Contains(install, forbidden) {
			t.Fatalf("installation evaluates EnvironmentFile as shell code: %q", forbidden)
		}
	}
	for _, required := range []string{
		"systemd-run --wait --pipe --collect",
		"--property=EnvironmentFile=/etc/control-center/control-center.env",
		"--setenv=MIGRATIONS_DIR=/opt/control-center/current/migrations",
		"-- /opt/control-center/current/scripts/migrate.sh",
	} {
		if !strings.Contains(install, required) {
			t.Errorf("installation is missing migration contract %q", required)
		}
	}

	values := parseEnvironmentExample(t, readReleaseFile(t, root, "config/control-center.env.example"))
	for _, key := range []string{"CC_DATABASE_URL", "PGHOST", "PGPORT", "PGDATABASE", "PGUSER", "PGPASSWORD"} {
		if values[key] == "" {
			t.Errorf("environment example is missing %s", key)
		}
	}
	databaseURL, err := url.Parse(values["CC_DATABASE_URL"])
	if err != nil {
		t.Fatal(err)
	}
	urlPassword, ok := databaseURL.User.Password()
	if !ok || urlPassword != values["PGPASSWORD"] {
		t.Fatalf("database URL and PGPASSWORD do not represent the same example secret")
	}
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	workingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Clean(filepath.Join(workingDirectory, "..", ".."))
}

func readReleaseFile(t *testing.T, root, path string) string {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}

func parseEnvironmentExample(t *testing.T, content string) map[string]string {
	t.Helper()
	values := make(map[string]string)
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			t.Fatalf("invalid environment example line %q", line)
		}
		values[key] = value
	}
	return values
}
