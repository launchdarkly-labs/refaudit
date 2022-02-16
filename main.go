//nolint:forbidigo // tool is supposed to send to stdout
package main

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path"
	"path/filepath"
	"strings"

	"golang.org/x/tools/go/packages"
)

// https://go.dev/ref/spec#Selectors

var exists = struct{}{}

const fsep = string(filepath.Separator)

const fromArg = "--from"
const excludeFromArg = "--exclude-from"
const toArg = "--to"
const excludeToArg = "--exclude-to"

type Report struct {
	Exported      []string
	Imported      []string
	UnusedExports []string
}

func main() {
	// parse input
	from := []string{}
	excludeFrom := []string{}
	to := []string{}
	excludeTo := []string{}
	addArg := func(arg string) {}
	for _, a := range os.Args[1:] {
		switch a {
		case fromArg:
			addArg = func(arg string) { from = append(from, expandPath(arg)) }
		case excludeFromArg:
			addArg = func(arg string) { excludeFrom = append(excludeFrom, expandPath(arg)) }
		case toArg:
			addArg = func(arg string) { to = append(to, expandPath(arg)) }
		case excludeToArg:
			addArg = func(arg string) { excludeTo = append(excludeTo, expandPath(arg)) }
		default:
			addArg(a)
		}
	}
	// validate input
	if len(from) == 0 && len(to) == 0 {
		fmt.Println("Find potentially unused exports in go code. Works across repos. There will be false positives.")
		fmt.Printf("Usage:\n\trefaudit %s [files] %s [files]\n", fromArg, toArg)
		fmt.Printf("%s: Directories that contain exports.\n", fromArg)
		fmt.Printf("%s: Directories that contain imports.\n", toArg)
		fmt.Printf("%s: Directories that contain imports that you want to exclude. Optional.\n", excludeToArg)
		fmt.Printf("%s: Directories that contain exports that you want to exclude. Optional.\n", excludeFromArg)
		fmt.Println("Examples:")
		fmt.Printf("\trefaudit %s ~/code/launchdarkly/foundation/ %s ~/code/launchdarkly/gonfalon ~/code/launchdarkly/event-recorder | tee ~/unused1.json\n", fromArg, toArg)
		fmt.Printf("\trefaudit %s ~/code/launchdarkly/foundation/ %s ~/code/launchdarkly/ %s ~/code/launchdarkly/foundation/ ~/code/launchdarkly/dev/ | tee ~/unused2.json\n", fromArg, toArg, excludeToArg)
		os.Exit(1)
	}

	// print input  so user knows what's going on
	fmt.Fprintf(os.Stderr, "%s: %s\n", fromArg, strings.Join(from, ", "))
	fmt.Fprintf(os.Stderr, "%s: %s\n", toArg, strings.Join(to, ", "))
	fmt.Fprintf(os.Stderr, "%s: %s\n", excludeToArg, strings.Join(excludeTo, ", "))
	fmt.Fprintf(os.Stderr, "%s: %s\n", excludeFromArg, strings.Join(excludeFrom, ", "))

	globals, err := findExports(from, excludeFrom)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v", err)
		os.Exit(2)
	}

	refs, err := findImports(to, excludeTo)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v", err)
		os.Exit(2)
	}

	// print potentially unused globals
	rpt := Report{
		Exported:      []string{},
		Imported:      []string{},
		UnusedExports: []string{},
	}
	for k := range globals {
		rpt.Exported = append(rpt.Exported, k)
		if _, ok := refs[k]; !ok {
			rpt.UnusedExports = append(rpt.UnusedExports, k)
		}
	}
	for k := range refs {
		rpt.Imported = append(rpt.Imported, k)
	}
	outB, err := json.MarshalIndent(rpt, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to marshal output: %v", err)
		os.Exit(2)
	}
	fmt.Println(string(outB))
}

func expandPath(path string) string {
	exp, err := filepath.Abs(os.ExpandEnv(path))
	if err != nil {
		fmt.Fprintf(os.Stderr, "bad path input %s: %v", path, err)
		os.Exit(1)
	}
	return exp
}

// runOnFiles runs fn on every file/dir specified, recursively.
func runOnFiles(files []string, excluding []string, fn func(file string) error) error {
	vendor := fmt.Sprintf("%svendor%s", fsep, fsep)
	for _, file := range files {
		err := filepath.Walk(file,
			func(path string, info os.FileInfo, err error) error {
				if err != nil {
					return err
				}
				// don't run on vendor sub-directories
				if strings.Contains(path, vendor) {
					return filepath.SkipDir
				}
				// exclude any top-level paths as needed
				for _, ex := range excluding {
					if strings.TrimSuffix(path, fsep) == strings.TrimSuffix(ex, fsep) {
						return filepath.SkipDir
					}
				}
				// don't run on dirs
				if info.IsDir() {
					return nil
				}
				// don't run on non-go files
				if !strings.HasSuffix(path, ".go") {
					return nil
				}
				// run fn on the file
				if err := fn(path); err != nil {
					return err
				}

				return nil
			})
		if err != nil {
			return fmt.Errorf("could not walk %s: %w", file, err)
		}
	}
	return nil
}

func findExports(from []string, excludeFrom []string) (map[string]interface{}, error) {
	globals := make(map[string]interface{})

	fs := token.NewFileSet()
	err := runOnFiles(from, excludeFrom, func(file string) error {
		f, err := parser.ParseFile(fs, file, nil, parser.AllErrors)
		if err != nil {
			return fmt.Errorf("could not parse %s: %w", file, err)
		}

		// find the public-facing full package path for the file
		cfg := &packages.Config{Mode: packages.NeedName, Tests: false, Dir: path.Dir(file)}
		pkgs, err := packages.Load(cfg, fmt.Sprintf("file=%s", file))
		if err != nil {
			return fmt.Errorf("could not parse package in %s: %w", file, err)
		}
		pkgPath := ""
		for _, pkg := range pkgs {
			if pkg.Name != "" {
				pkgPath = pkg.PkgPath
			}
		}
		if pkgPath == "" {
			// probably a test
			return nil
		}
		pkgPath = strings.Trim(pkgPath, "\"")

		// scan the file for exports
		v := newExportVisitor(f, globals, pkgPath)
		ast.Walk(v, f)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to find exports: %w", err)
	}
	return globals, nil
}

// exportVisitor tracks public exports.
type exportVisitor struct {
	f       *ast.File
	pkgPath string
	exports map[string]interface{}
}

func newExportVisitor(f *ast.File, exports map[string]interface{}, pkgPath string) exportVisitor {
	return exportVisitor{f, pkgPath, exports}
}

func (v exportVisitor) Visit(n ast.Node) ast.Visitor {
	if n == nil {
		return nil
	}

	switch d := n.(type) {
	case *ast.AssignStmt:
		if d.Tok != token.DEFINE {
			return v
		}
		for _, name := range d.Lhs {
			v.add(name)
		}

	case *ast.FuncDecl:
		v.add(d.Name)
	case *ast.GenDecl:
		if d.Tok != token.VAR {
			return v
		}
		for _, spec := range d.Specs {
			if value, ok := spec.(*ast.ValueSpec); ok {
				for _, name := range value.Names {
					v.add(name)
				}
			}
		}
	}

	return v
}

func (v exportVisitor) add(n ast.Node) {
	ident, ok := n.(*ast.Ident)
	if !ok {
		return
	}
	if ident.Name == "_" || ident.Name == "" {
		return
	}
	if ident.Obj != nil && ident.Obj.Pos() == ident.Pos() {
		if ident.IsExported() {
			v.exports[v.pkgPath+"."+ident.Name] = exists
		}
	}
}

func findImports(to []string, excludeTo []string) (map[string]interface{}, error) {
	refs := make(map[string]interface{})

	fs := token.NewFileSet()
	err := runOnFiles(to, excludeTo, func(file string) error {
		f, err := parser.ParseFile(fs, file, nil, parser.AllErrors)

		if err != nil {
			return fmt.Errorf("could not parse %s: %w", file, err)
		} else {
			v := newRefVisitor(f, refs)
			ast.Walk(v, f)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to find exports: %v", err)
	}
	return refs, nil
}

// refVisitor tracks import references.
type refVisitor struct {
	f    *ast.File
	refs map[string]interface{}
	// alias -> real pkg
	importedPkgs map[string]string
}

func newRefVisitor(f *ast.File, refs map[string]interface{}) refVisitor {
	ip := make(map[string]string)
	for _, decl := range f.Decls {
		genDecl, ok := decl.(*ast.GenDecl)
		if !ok || genDecl.Tok != token.IMPORT {
			continue
		}

		for _, spec := range genDecl.Specs {
			importSpec, ok := spec.(*ast.ImportSpec)
			if ok {
				impName := strings.Trim(importSpec.Path.Value, "\"")
				splits := strings.Split(impName, "/")
				alias := splits[len(splits)-1]
				if importSpec.Name != nil {
					alias = importSpec.Name.Name
				}
				ip[alias] = impName
			}

		}
	}

	return refVisitor{f, refs, ip}
}

func (v refVisitor) Visit(n ast.Node) ast.Visitor {
	if n == nil {
		return nil
	}

	if d, ok := n.(*ast.SelectorExpr); ok {
		xIdent, ok := d.X.(*ast.Ident)
		if !ok {
			return v
		}
		if imp, ok := v.importedPkgs[xIdent.Name]; ok {
			v.refs[imp+"."+d.Sel.Name] = exists
		}
	}
	return v
}
