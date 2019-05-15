package mockery

import (
	"fmt"
	"go/ast"
	"go/build"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type Walker struct {
	BaseDir   string
	Recursive bool
	Filter    *regexp.Regexp
	LimitOne  bool
	BuildTags []string
}

type WalkerVisitor interface {
	VisitWalk(*Interface) error
	GenerateMockRegister(*parserEntry) error
}

func (this *Walker) Walk(visitor WalkerVisitor) (generated bool) {
	parser := NewParser(this.BuildTags)
	this.doWalk(parser, this.BaseDir, visitor)

	err := parser.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error walking: %v\n", err)
		os.Exit(1)
	}

	if parser.registerEntry != nil {
		visitor.GenerateMockRegister(parser.registerEntry)
	}

	for _, iface := range parser.Interfaces() {
		if !this.Filter.MatchString(iface.Name) {
			continue
		}
		err := visitor.VisitWalk(iface)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error walking %s: %s\n", iface.Name, err)
			os.Exit(1)
		}
		generated = true
		if this.LimitOne {
			return
		}
	}

	return
}

func (this *Walker) doWalk(p *Parser, dir string, visitor WalkerVisitor) (generated bool) {
	files, err := ioutil.ReadDir(dir)
	if err != nil {
		return
	}

	for _, file := range files {
		if strings.HasPrefix(file.Name(), ".") || strings.HasPrefix(file.Name(), "_") {
			continue
		}

		path := filepath.Join(dir, file.Name())

		if file.IsDir() {
			if this.Recursive {
				generated = this.doWalk(p, path, visitor) || generated
				if generated && this.LimitOne {
					return
				}
			}
			continue
		}

		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			continue
		}

		err = p.Parse(path)
		if err != nil {
			fmt.Fprintln(os.Stderr, "Error parsing file: ", err)
			continue
		}
	}

	return
}

type GeneratorVisitor struct {
	InPackage bool
	Note      string
	Osp       OutputStreamProvider
	// The name of the output package, if InPackage is false (defaults to "mocks")
	PackageName string
}

func (this *GeneratorVisitor) GenerateMockRegister(entry *parserEntry) error {

	template := `
package mocks

import (
	"%s"
	
	"github.com/17media/api/setup/dimanager"
)
	
func RegisterMock(m *dimanager.Manager) *%s {
	mockObj := &%s{}
	m.ProvideMock(func() %s.%s { return mockObj }, "%s")
	return mockObj
}
`
	//fmt.Printf("%#v", entry)
	//srcPath := filepath.Join(build.Default.GOPATH, "src")
	srcPath := strings.Join([]string{build.Default.GOPATH, "src", ""}, "/")
	//fmt.Println(srcPath)
	importpkg := strings.Replace(filepath.Dir(entry.fileName), srcPath, "", 1)
	//fmt.Println(importpkg)
	pkg := filepath.Base(importpkg)

	interfaceName := ""
	depName := ""

	//fset := token.NewFileSet()
	ast.Inspect(entry.syntax, func(node ast.Node) bool {
		switch nt := node.(type) {
		case *ast.FuncDecl:
			if strings.HasPrefix(nt.Name.Name, "Get") {
				//ast.Print(fset, nt)
				starReturn := nt.Type.Results.List[0].Type.(*ast.Ident)
				interfaceName = starReturn.Name
			}
		case *ast.FieldList:
			//ast.Print(fset, nt)
			if len(nt.List) == 2 {
				first, firstOK := nt.List[0].Type.(*ast.SelectorExpr)
				if !firstOK {
					return true
				}
				if first.Sel.Name != "In" {
					return true
				}
				depName = nt.List[1].Tag.Value
				depName = strings.Split(depName, "\"")[1]
			}
		}
		return true
	})
	//fmt.Print(this.PackageName)
	formatCode := fmt.Sprintf(template, importpkg, interfaceName, interfaceName, pkg, interfaceName, depName)
	fmt.Println(formatCode)
	err := ioutil.WriteFile("./mocks/register.go", []byte(formatCode), 0644)
	if err != nil {
		return err
	}

	return nil
}

func (this *GeneratorVisitor) VisitWalk(iface *Interface) error {
	defer func() {
		if r := recover(); r != nil {
			fmt.Printf("Unable to generated mock for '%s': %s\n", iface.Name, r)
			return
		}
	}()

	var out io.Writer
	var pkg string

	if this.InPackage {
		pkg = filepath.Dir(iface.FileName)
	} else {
		pkg = this.PackageName
	}

	out, err, closer := this.Osp.GetWriter(iface)
	if err != nil {
		fmt.Printf("Unable to get writer for %s: %s", iface.Name, err)
		os.Exit(1)
	}
	defer closer()

	gen := NewGenerator(iface, pkg, this.InPackage)
	gen.GeneratePrologueNote(this.Note)
	gen.GeneratePrologue(pkg)

	err = gen.Generate()
	if err != nil {
		return err
	}

	err = gen.Write(out)
	if err != nil {
		return err
	}
	return nil
}
