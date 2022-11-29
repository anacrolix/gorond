package main

import (
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"log"
	"os"
	"sort"
	"strings"

	"github.com/anacrolix/bargle"
	"golang.org/x/tools/go/packages"
)

func main() {
	err := mainErr()
	if err != nil {
		log.Fatalf("error in main: %v", err)
	}
}

func mainErr() error {
	argParser := args.NewParser()
	var pkgPattern string
	if !argParser.Parse(args.Positional(args.String(&pkgPattern))) {
		return argParser.Fail()
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
	pkgs, err := packages.Load(&packages.Config{Mode: packages.NeedModule | packages.NeedFiles}, pkgPattern)
	if err != nil {
		return err
	}
	for _, pkg := range pkgs {
		err := groupPackageImports(pkg, stdPkgPaths)
		if err != nil {
			return err
		}
		//break
	}
	return nil
}

func groupPackageImports(pkg *packages.Package, stdPkgPaths map[string]bool) error {
	module := pkg.Module
	fileSet := token.NewFileSet()
	for _, filePath := range pkg.GoFiles {
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
	return nil

}

func fixFile(file *ast.File, stdPkgPaths map[string]bool, module *packages.Module, filePath string, fileSet *token.FileSet) error {
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
	outFile, err := os.OpenFile(filePath, os.O_WRONLY|os.O_TRUNC, 0)
	if err != nil {
		return err
	}
	defer outFile.Close()
	log.Printf("fixing %q", filePath)
	return format.Node(outFile, fileSet, file)
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
		log.Printf("%q", imp.Path.Value)
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
