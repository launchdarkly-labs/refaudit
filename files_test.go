package main

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFileList(t *testing.T) {
	found := []string{}
	searchDir := expandPath(".")
	require.NoError(t, runOnFiles(context.TODO(), []string{searchDir}, []string{"go.mod", "go.sum"}, func(file string) error {
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
	exports, err := findExports(context.TODO(), []string{searchDir}, []string{})
	require.NoError(t, err)
	if _, ok := exports["github.com/launchdarkly-labs/refaudit/internal/dummy.ExportedFunction"]; !ok {
		assert.FailNow(t, "missing exported function")
	}
	if _, ok := exports["github.com/launchdarkly-labs/refaudit/internal/dummy.ExportedVariable"]; !ok {
		assert.FailNow(t, "missing exported variable")
	}
	if _, ok := exports["github.com/launchdarkly-labs/refaudit/internal/dummy.ExportedStruct"]; !ok {
		assert.FailNow(t, "missing exported struct")
	}
	if _, ok := exports["github.com/launchdarkly-labs/refaudit/internal/dummy.ExportedInterface"]; !ok {
		assert.FailNow(t, "missing exported interface")
	}
}

func TestImports(t *testing.T) {
	searchDir := expandPath("./internal/dummy/")
	imports, err := findImports(context.TODO(), []string{searchDir}, []string{})
	require.NoError(t, err)
	if _, ok := imports["fmt.Print"]; !ok {
		assert.FailNow(t, "missing imported function call")
	}
	if _, ok := imports["fmt.Stringer"]; !ok {
		assert.FailNow(t, "missing imported interface ref")
	}
}
