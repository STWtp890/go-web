package architecture_test

// The package doc comment lives in dependencies_test.go. This file adds the
// structural half of "qq-search never calls py-agent".

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"packages/gen/servicearch"
)

// ingressHelper is the only package allowed to import the qqsource contract. It
// is a wire-mapping helper with no client: it converts payloads, it does not
// dial.
const ingressHelper = "internal/ingress/pyagent/"

// TestNoSourceClientInProductionCode proves structurally that qq-search cannot
// call py-agent. Both the query path and the rebuild path are covered, because
// neither can reach a source client that does not exist:
//
//   - no production file dials anything (grpc.Dial / grpc.DialContext /
//     grpc.NewClient), and no production file references the generated
//     QQSourceServiceClient;
//   - the qqsource contract — where that client type lives — is imported only by
//     the documented ingress helper.
//
// The integration suite adds a runtime control on top of this: the rebuild runs
// while the only address a source client could have used is closed.
func TestNoSourceClientInProductionCode(t *testing.T) {
	root, err := servicearch.RepositoryRoot()
	if err != nil {
		t.Fatal(err)
	}
	serviceRoot := filepath.Join(root, "apps", "qq-search")

	banned := []string{"grpc.Dial", "grpc.DialContext", "grpc.NewClient", "QQSourceServiceClient"}
	var offenders []string
	err = filepath.WalkDir(serviceRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(serviceRoot, path)
		if err != nil {
			return err
		}
		slashed := filepath.ToSlash(relative)
		text := string(content)
		for _, needle := range banned {
			if strings.Contains(text, needle) {
				offenders = append(offenders, slashed+" references "+needle)
			}
		}
		if strings.Contains(text, "packages/gen/qqsource/v1") && !strings.HasPrefix(slashed, ingressHelper) {
			offenders = append(offenders, slashed+" imports the qqsource contract outside "+ingressHelper)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, offender := range offenders {
		t.Error(offender)
	}
}
