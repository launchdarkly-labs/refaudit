//nolint:forbidigo // tool is supposed to send to stdout
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/signal"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/sync/errgroup"
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
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

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
		fmt.Printf("\trefaudit %s /path/to/library/ %s /path/to/app1 /path/to/app2 | tee ~/unused1.json\n", fromArg, toArg)
		fmt.Printf("\trefaudit %s /path/to/library/ %s /path/to/app1 %s /path/to/app1/exclude | tee ~/unused2.json\n", fromArg, toArg, excludeToArg)
		os.Exit(1)
	}

	// print input  so user knows what's going on
	fmt.Fprintf(os.Stderr, "%s: %s\n", fromArg, strings.Join(from, ", "))
	fmt.Fprintf(os.Stderr, "%s: %s\n", toArg, strings.Join(to, ", "))
	fmt.Fprintf(os.Stderr, "%s: %s\n", excludeToArg, strings.Join(excludeTo, ", "))
	fmt.Fprintf(os.Stderr, "%s: %s\n", excludeFromArg, strings.Join(excludeFrom, ", "))

	globals, err := findExports(ctx, from, excludeFrom)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v", err)
		os.Exit(2)
	}

	refs, err := findImports(ctx, to, excludeTo)
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
		rpt.Exported = sortedInsert(rpt.Exported, k)
		//rpt.Exported = append(rpt.Exported, k)
		if _, ok := refs[k]; !ok {
			rpt.UnusedExports = sortedInsert(rpt.UnusedExports, k)
		}
	}
	for k := range refs {
		rpt.Imported = sortedInsert(rpt.Imported, k)
	}

	outB, err := json.MarshalIndent(rpt, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to marshal output: %v", err)
		os.Exit(2)
	}
	fmt.Println(string(outB))
}

// sortedInsert
func sortedInsert(list []string, elem string) []string {
	// find spot to insert element
	i := sort.Search(len(list), func(i int) bool { return list[i] >= elem })
	// handle not found case
	if i == len(list) {
		return append(list, elem)
	}
	// shift over and set
	list = append(list[:i+1], list[i:]...)
	list[i] = elem
	return list
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
func runOnFiles(ctx context.Context, files []string, excluding []string, fn func(file string) error) error {
	g, ctx := errgroup.WithContext(ctx)
	filesChan := make(chan string, 4) // buffered chan since walking can take a while

	// set up file consumer
	g.Go(func() error {
		for {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case f, ok := <-filesChan:
				if ok {
					if err := fn(f); err != nil {
						return err
					}
				} else {
					return nil
				}
			}
		}
	})

	// walk the dir tree, producing files
	g.Go(func() error {
		defer close(filesChan)
		vendor := fmt.Sprintf("%svendor%s", fsep, fsep)
		for _, file := range files {
			err := filepath.Walk(file,
				func(path string, info os.FileInfo, err error) error {
					if err != nil {
						return err
					}
					if ctx.Err() != nil {
						return ctx.Err()
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
					// send to consumer
					filesChan <- path

					return nil
				})
			if err != nil {
				return fmt.Errorf("could not walk %s: %w", file, err)
			}
		}
		return nil
	})

	return g.Wait()
}

func findExports(ctx context.Context, from []string, excludeFrom []string) (map[string]interface{}, error) {
	globals := make(map[string]interface{})

	fs := token.NewFileSet()
	err := runOnFiles(ctx, from, excludeFrom, func(file string) error {
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
		if d.Tok == token.VAR {
			for _, spec := range d.Specs {
				if value, ok := spec.(*ast.ValueSpec); ok {
					for _, name := range value.Names {
						v.add(name)
					}
				}
			}
		} else if d.Tok == token.TYPE {
			for _, spec := range d.Specs {
				if value, ok := spec.(*ast.TypeSpec); ok {
					v.add(value.Name)
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

func findImports(ctx context.Context, to []string, excludeTo []string) (map[string]interface{}, error) {
	refs := make(map[string]interface{})

	fs := token.NewFileSet()
	err := runOnFiles(ctx, to, excludeTo, func(file string) error {
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
