package provider

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// The full examples are tested with the real Terraform CLI: plugin-testing's state checks do not support
// for_each, and this also proves the provider address registry.terraform.io/awacino/awacino.

type exampleEnv struct{ endpoint, keyID, secret string }

var (
	buildOnce sync.Once
	buildDir  string
	buildErr  error
)

// providerBuild builds this provider once per test run for dev_overrides.
func providerBuild(t *testing.T) string {
	t.Helper()
	buildOnce.Do(func() {
		dir, err := os.MkdirTemp("", "tfprov-awacino-")
		if err != nil {
			buildErr = err
			return
		}
		cmd := exec.Command("go", "build", "-o", filepath.Join(dir, "terraform-provider-awacino"), ".")
		cmd.Dir = "../.."
		if out, err := cmd.CombinedOutput(); err != nil {
			buildErr = fmt.Errorf("go build: %v\n%s", err, out)
			return
		}
		buildDir = dir
	})
	if buildErr != nil {
		t.Fatal(buildErr)
	}
	return buildDir
}

func terraformBin(t *testing.T) string {
	t.Helper()
	if p := os.Getenv("TF_ACC_TERRAFORM_PATH"); p != "" {
		return p
	}
	p, err := exec.LookPath("terraform")
	if err != nil {
		t.Skip("terraform not found (set TF_ACC_TERRAFORM_PATH)")
	}
	return p
}

// runExample copies examples/<name>/main.tf to a temp dir and runs apply, plan (must be empty) and destroy.
func runExample(t *testing.T, name string, env exampleEnv, vars map[string]any) {
	t.Helper()
	tf := terraformBin(t)
	bin := providerBuild(t)
	dir := t.TempDir()
	src, err := os.ReadFile(filepath.Join("../../examples", name, "main.tf"))
	if err != nil {
		t.Fatal(err)
	}
	must(t, os.WriteFile(filepath.Join(dir, "main.tf"), src, 0o600))
	vb, _ := json.Marshal(vars)
	must(t, os.WriteFile(filepath.Join(dir, "terraform.tfvars.json"), vb, 0o600))
	rc := filepath.Join(dir, "dev.tfrc")
	must(t, os.WriteFile(rc, []byte(fmt.Sprintf("provider_installation {\n  dev_overrides {\n    \"awacino/awacino\" = %q\n  }\n  direct {}\n}\n", bin)), 0o600))

	run := func(args ...string) (string, error) {
		cmd := exec.Command(tf, args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "TF_CLI_CONFIG_FILE="+rc, "TF_IN_AUTOMATION=1", "CHECKPOINT_DISABLE=1",
			"AWACINO_ENDPOINT="+env.endpoint, "AWACINO_KEY_ID="+env.keyID, "AWACINO_KEY_SECRET="+env.secret)
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	destroyed := false
	t.Cleanup(func() {
		if !destroyed {
			if out, err := run("destroy", "-auto-approve", "-no-color", "-input=false"); err != nil {
				t.Errorf("cleanup destroy failed: %v\n%s", err, out)
			}
		}
	})
	if out, err := run("apply", "-auto-approve", "-no-color", "-input=false"); err != nil {
		t.Fatalf("apply: %v\n%s", err, out)
	}
	out, err := run("plan", "-detailed-exitcode", "-no-color", "-input=false")
	var ee *exec.ExitError
	if errors.As(err, &ee) && ee.ExitCode() == 2 {
		t.Fatalf("a second plan is not empty:\n%s", out)
	} else if err != nil {
		t.Fatalf("plan: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Provider development overrides") {
		t.Logf("note: dev_overrides warning not seen")
	}
	if out, err := run("destroy", "-auto-approve", "-no-color", "-input=false"); err != nil {
		t.Fatalf("destroy: %v\n%s", err, out)
	}
	destroyed = true
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
