package main

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"io"
	"os"
	"sort"
	"strings"

	args "github.com/anacrolix/bargle"
	"github.com/anacrolix/log"
	"golang.org/x/tools/go/packages"
)

func main() {
	err := mainErr()
	if err != nil {
		log.Fatalf("error in main: %v", err)
	}
}

var logger = log.Default.WithNames("gorond").WithDefaultLevel(log.Debug)

func mainErr() error {
	argParser := args.NewParser()
	var pkgPatterns []string

parse:
	for {
		switch {
		case argParser.Parse(args.Positional(args.AppendSlice(&pkgPatterns, args.String))):
		default:
			break parse
		}
	}
	argParser.FailIfArgsRemain()
	if argParser.Err() != nil {
		return argParser.Err()
	}
	stdPackages, err := packages.Load(nil, "std")
	if err != nil {
		return err
	}
	stdPkgPaths := make(map[string]bool)
	for _, pkg := range stdPackages {
		stdPkgPaths[pkg.PkgPath] = true
	}
	pkgs, err := packages.Load(
		&packages.Config{
			Mode:  packages.NeedModule | packages.NeedFiles | packages.NeedName,
			Tests: true,
		},
		pkgPatterns...,
	)
	if err != nil {
		return err
	}
	for _, pkg := range pkgs {
		logger.Levelf(log.Debug, pkg.Name, pkg.PkgPath)
		if strings.HasSuffix(pkg.PkgPath, ".test") {
			continue
		}
		err := groupPackageImports(pkg, stdPkgPaths)
		if err != nil {
			return err
		}
	}
	return nil
}

func groupPackageImports(pkg *packages.Package, stdPkgPaths map[string]bool) error {
	module := pkg.Module
	fileSet := token.NewFileSet()
	logger.Printf("ignored files: %q", pkg.IgnoredFiles)
	for _, fileSlice := range [][]string{pkg.GoFiles, pkg.IgnoredFiles} {
		for _, filePath := range fileSlice {
			logger.Print(filePath)
			file, err := parser.ParseFile(fileSet, filePath, nil, parser.ParseComments)
			if err != nil {
				return err
			}
			err = fixFile(file, stdPkgPaths, module, filePath, fileSet)
			if err != nil {
				return err
			}
			//break
		}

	}
	return nil

}

func fixFile(
	file *ast.File,
	stdPkgPaths map[string]bool,
	module *packages.Module,
	filePath string,
	fileSet *token.FileSet,
) (err error) {
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok {
			continue
		}
		var imps []*ast.ImportSpec
		for _, spec := range gen.Specs {
			imp, ok := spec.(*ast.ImportSpec)
			if !ok {
				continue
			}
			imps = append(imps, imp)
		}
		if len(imps) == 0 {
			continue
		}
		groups := groupImports(imps, stdPkgPaths, module.Path)
		gen.Specs = nil
		for _, imp := range joinImportGroups(groups.std, groups.nonLocal, groups.local) {
			imp.Path.ValuePos = 0
			if imp.Name != nil {
				imp.Name.NamePos = 0
			}
			gen.Specs = append(gen.Specs, imp)
		}
	}
	var proposed bytes.Buffer
	err = format.Node(&proposed, fileSet, file)
	if err != nil {
		return
	}
	existing, err := os.ReadFile(filePath)
	if err != nil {
		return
	}
	if bytes.Compare(existing, proposed.Bytes()) == 0 {
		return
	}
	fmt.Println(filePath)
	tmpFile, err := os.CreateTemp("", "gorond")
	if err != nil {
		return
	}
	defer tmpFile.Close()
	_, err = io.Copy(tmpFile, &proposed)
	if err != nil {
		os.Remove(tmpFile.Name())
		return
	}
	err = os.Rename(tmpFile.Name(), filePath)
	return
}

func pathFromSpec(spec *ast.ImportSpec) string {
	return strings.Trim(spec.Path.Value, `"`)
}

func joinImportGroups(groups ...[]*ast.ImportSpec) (joined []*ast.ImportSpec) {
	for _, group := range groups {
		if len(group) > 0 && len(joined) > 0 {
			joined = append(joined, emptyImportSpec())
		}
		joined = append(joined, group...)
	}
	return
}

func emptyImportSpec() *ast.ImportSpec {
	return &ast.ImportSpec{Path: &ast.BasicLit{Value: "", Kind: token.STRING}}
}

func groupImports(all []*ast.ImportSpec, stdPkgs map[string]bool, localModule string) (groups importGroups) {
	for _, imp := range all {
		//log.Printf("%q", imp.Path.Value)
		path := pathFromSpec(imp)
		if stdPkgs[path] {
			groups.std = append(groups.std, imp)
		} else if path == localModule || strings.HasPrefix(path, localModule+"/") {
			groups.local = append(groups.local, imp)
		} else {
			groups.nonLocal = append(groups.nonLocal, imp)
		}
	}
	return
}

type importGroups struct {
	std      []*ast.ImportSpec
	nonLocal []*ast.ImportSpec
	local    []*ast.ImportSpec
}

func (me importGroups) sort() {
	me.sortSlice(me.std)
	me.sortSlice(me.nonLocal)
	me.sortSlice(me.local)
}

func (me importGroups) sortSlice(slice []*ast.ImportSpec) {
	sort.Slice(slice, func(i, j int) bool {
		return pathFromSpec(slice[i]) < pathFromSpec(slice[j])
	})
}
