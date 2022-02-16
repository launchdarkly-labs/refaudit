package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFileList(t *testing.T) {
	found := []string{}
	searchDir := expandPath(".")
	require.NoError(t, runOnFiles([]string{searchDir}, []string{"go.mod", "go.sum"}, func(file string) error {
		found = append(found, file)
		return nil
	}))
	require.Contains(t, found, expandPath("./files_test.go"))
	require.Contains(t, found, expandPath("./main.go"))
	require.NotContains(t, found, expandPath("./go.mod"))
	require.NotContains(t, found, expandPath("./go.sum"))
}

func TestExports(t *testing.T) {
	searchDir := expandPath("./internal/dummy/")
	exports, err := findExports([]string{searchDir}, []string{})
	require.NoError(t, err)
	if _, ok := exports["github.com/launchdarkly-labs/refaudit/internal/dummy.ExportedFunction"]; !ok {
		require.FailNow(t, "missing export")
	}
	if _, ok := exports["github.com/launchdarkly-labs/refaudit/internal/dummy.ExportedVariable"]; !ok {
		require.FailNow(t, "missing export")
	}
}

func TestImports(t *testing.T) {
	searchDir := expandPath("./internal/dummy/")
	imports, err := findImports([]string{searchDir}, []string{})
	require.NoError(t, err)
	if _, ok := imports["fmt.Print"]; !ok {
		require.FailNow(t, "missing import")
	}
}
