// Copyright 2015 Peter Goetz
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Based on the work done in
// https://github.com/golang/mock/blob/d581abfc04272f381d7a05e4b80163ea4e2b9447/mockgen/mockgen.go

// MockGen generates mock implementations of Go interfaces.
package mockgen

// TODO: This does not support recursive embedded interfaces.
// TODO: This does not support embedding package-local interfaces in a separate file.

import (
	"bytes"
	"fmt"
	"go/format"
	"go/token"
	"io"
	"io/ioutil"
	"log"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"

	"github.com/petergtz/pegomock/pegomock/mockgen/model"
	"github.com/petergtz/pegomock/pegomock/util"
)

const importPath = "github.com/petergtz/pegomock"

func GenerateMockFileInOutputDir(
	args []string,
	outputDirPath string,
	outputFilePathOverride string,
	packageOut string,
	selfPackage string,
	debugParser bool,
	out io.Writer) {
	GenerateMockFile(
		args,
		OutputFilePath(args, outputDirPath, outputFilePathOverride),
		packageOut,
		selfPackage,
		debugParser,
		out)
}

func OutputFilePath(args []string, outputDirPath string, outputFilePathOverride string) string {
	if outputFilePathOverride != "" {
		return outputFilePathOverride
	} else if util.SourceMode(args) {
		return filepath.Join(outputDirPath, "mock_"+strings.TrimSuffix(args[0], ".go")+"_test.go")
	} else {
		return filepath.Join(outputDirPath, "mock_"+strings.ToLower(args[len(args)-1])+"_test.go")
	}
}

func GenerateMockFile(args []string, outputFilePath string, packageOut string, selfPackage string, debugParser bool, out io.Writer) {
	output := GenerateMockSourceCode(args, packageOut, selfPackage, debugParser, out)

	err := ioutil.WriteFile(outputFilePath, output, 0664)
	if err != nil {
		panic(fmt.Errorf("Failed writing to destination: %v", err))
	}
}

func GenerateMockSourceCode(args []string, packageOut string, selfPackage string, debugParser bool, out io.Writer) []byte {
	var err error

	var ast *model.Package
	var src string
	if util.SourceMode(args) {
		ast, err = ParseFile(args[0])
		src = args[0]
	} else {
		if len(args) != 2 {
			log.Fatal("Expected exactly two arguments, but got " + fmt.Sprint(args))
		}
		ast, err = Reflect(args[0], strings.Split(args[1], ","))
		src = fmt.Sprintf("%v (interfaces: %v)", args[0], args[1])
	}
	if err != nil {
		panic(fmt.Errorf("Loading input failed: %v", err))
	}

	if debugParser {
		ast.Print(out)
	}

	output, err := generateOutput(ast, src, packageOut, selfPackage)
	if err != nil {
		panic(fmt.Errorf("Failed generating mock: %v", err))
	}
	return output
}

type generator struct {
	buf    bytes.Buffer
	indent string

	packageMap map[string]string // map from import path to package name
}

func (g *generator) p(format string, args ...interface{}) *generator {
	fmt.Fprintf(&g.buf, g.indent+format+"\n", args...)
	return g
}

func (g *generator) in() *generator {
	g.indent += "\t"
	return g
}

func (g *generator) out() *generator {
	if len(g.indent) > 0 {
		g.indent = g.indent[0 : len(g.indent)-1]
	}
	return g
}

func removeDot(s string) string {
	if len(s) > 0 && s[len(s)-1] == '.' {
		return s[0 : len(s)-1]
	}
	return s
}

// sanitize cleans up a string to make a suitable package name.
// pkgName in reflect mode is the base name of the import path,
// which might have characters that are illegal to have in package names.
func sanitize(s string) string {
	t := ""
	for _, r := range s {
		if t == "" {
			if unicode.IsLetter(r) || r == '_' {
				t += string(r)
				continue
			}
		} else {
			if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' {
				t += string(r)
				continue
			}
		}
		t += "_"
	}
	if t == "_" {
		t = "x"
	}
	return t
}

func generateOutput(ast *model.Package, source string, packageOut string, selfPackage string) ([]byte, error) {
	g := new(generator)
	if err := g.Generate(source, ast, packageOut, selfPackage); err != nil {
		return nil, fmt.Errorf("Failed generating mock: %v", err)
	}
	return g.Output(), nil
}

func (g *generator) Generate(source string, pkg *model.Package, pkgName string, selfPackage string) error {
	g.p("// Automatically generated by MockGen. DO NOT EDIT!")
	g.p("// Source: %v", source)
	g.p("")

	// Get all required imports, and generate unique names for them all.
	im := pkg.Imports()
	im[importPath] = true
	g.packageMap = make(map[string]string, len(im))
	localNames := make(map[string]bool, len(im))
	for pth := range im {
		base := sanitize(path.Base(pth))

		// Local names for an imported package can usually be the basename of the import path.
		// A couple of situations don't permit that, such as duplicate local names
		// (e.g. importing "html/template" and "text/template"), or where the basename is
		// a keyword (e.g. "foo/case").
		// try base0, base1, ...
		pkgName := base
		i := 0
		for localNames[pkgName] || token.Lookup(pkgName).IsKeyword() {
			pkgName = base + strconv.Itoa(i)
			i++
		}

		g.packageMap[pth] = pkgName
		localNames[pkgName] = true
	}

	g.p("package %v", pkgName)
	g.p("")
	g.p("import (")
	g.in()
	g.p("\"reflect\"")

	for path, pkg := range g.packageMap {
		if path == selfPackage {
			continue
		}
		g.p("%v %q", pkg, path)
	}
	for _, path := range pkg.DotImports {
		g.p(". %q", path)
	}
	g.out()
	g.p(")")

	for _, iface := range pkg.Interfaces {
		g.GenerateMockInterface(iface, selfPackage)
	}

	return nil
}

func (g *generator) GenerateMockInterface(iface *model.Interface, selfPackage string) {
	mockTypeName := "Mock" + iface.Name

	g.p("")
	g.p("// Mock of %v interface", iface.Name)
	g.p("type %v struct {", mockTypeName)
	g.in().p("fail func(message string, callerSkip ...int)").out()
	g.p("}")
	g.p("")

	g.p("func New%v() *%v {", mockTypeName, mockTypeName)
	g.in().p("return &%v{fail: pegomock.GlobalFailHandler}", mockTypeName).out()
	g.p("}")
	g.p("")

	for _, method := range iface.Methods {
		g.GenerateMockMethod(mockTypeName, method, selfPackage).p("")
	}
	g.p("type Verifier%v struct {", iface.Name)
	g.in().
		p("mock *Mock%v", iface.Name).
		p("invocationCountMatcher pegomock.Matcher").
		p("inOrderContext *pegomock.InOrderContext").
		out()
	g.p("}")
	g.p("")
	g.p("func (mock *Mock%v) VerifyWasCalledOnce() *Verifier%v {", iface.Name, iface.Name)
	g.in().p("return &Verifier%v{mock, pegomock.Times(1), nil}", iface.Name).out()
	g.p("}")
	g.p("")
	g.p("func (mock *Mock%v) VerifyWasCalled(invocationCountMatcher pegomock.Matcher) *Verifier%v {", iface.Name, iface.Name)
	g.in().p("return &Verifier%v{mock, invocationCountMatcher, nil}", iface.Name).out()
	g.p("}")
	g.p("")
	g.p("func (mock *Mock%v) VerifyWasCalledInOrder(invocationCountMatcher pegomock.Matcher, inOrderContext *pegomock.InOrderContext) *Verifier%v {", iface.Name, iface.Name)
	g.in().p("return &Verifier%v{mock, invocationCountMatcher, inOrderContext}", iface.Name).out()
	g.p("}")
	g.p("")
	for _, method := range iface.Methods {
		g.GenerateVerifierMethod(iface.Name, method, selfPackage).p("")
	}
}

// GenerateMockMethod generates a mock method implementation.
// If non-empty, pkgOverride is the package in which unqualified types reside.
func (g *generator) GenerateMockMethod(mockType string, method *model.Method, pkgOverride string) *generator {
	_, _, argString, returnTypes, retString, callArgs := getStuff(method, g, pkgOverride)
	g.p("func (mock *%v) %v(%v)%v {", mockType, method.Name, argString, retString)
	g.in()
	r := ""
	if len(method.Out) > 0 {
		r = "result :="
	}
	reflectReturnTypes := make([]string, len(returnTypes))
	for i, returnType := range returnTypes {
		reflectReturnTypes[i] = fmt.Sprintf("reflect.TypeOf((*%v)(nil)).Elem()", returnType)
	}
	// TODO: this is repeated in verifier generation
	// if method.Variadic != nil {
	// 	g.p("params := []pegomock.Param{%v}", strings.Join(argNames[0:len(argNames)-1], ", "))
	// 	g.p("for _, param := range %v {", argNames[len(argNames)-1]).in()
	// 	g.p("params = append(params, param)")
	// 	g.out().p("}")
	// } else {
	g.p("params := []pegomock.Param{%v}", callArgs)

	// }
	g.p("%v pegomock.GetGenericMockFrom(mock).Invoke(\"%v\", params, %v, []reflect.Type{%v})",
		r, method.Name, method.Variadic != nil, strings.Join(reflectReturnTypes, ", "))
	if len(method.Out) > 0 {
		// TODO: translate LastInvocation into a Matcher so it can be used as key for Stubbings
		for i, returnType := range returnTypes {
			g.p("var ret%v %v", i, returnType)
		}
		g.p("if len(result) != 0 {")
		g.in()
		returnValues := make([]string, len(returnTypes))
		for i, returnType := range returnTypes {
			g.p("if result[%v] != nil {", i)
			g.in().p("ret%v  = result[%v].(%v)", i, i, returnType)
			g.p("}")
			returnValues[i] = fmt.Sprintf("ret%v", i)
		}
		g.out()
		g.p("}")
		g.p("return %v", strings.Join(returnValues, ", "))
	}
	g.out()
	g.p("}")
	return g
}

func resultCast(returnTypes []string) string {
	castedResults := make([]string, len(returnTypes))
	for i, returnType := range returnTypes {
		castedResults[i] = fmt.Sprintf("result[%v].(%v)", i, returnType)
	}
	return strings.Join(castedResults, ", ")
}

func (g *generator) GenerateVerifierMethod(interfaceName string, method *model.Method, pkgOverride string) *generator {
	args, argNames, argString, _, _, argNamesString := getStuff(method, g, pkgOverride)
	// TODO: argTypesFrom should not be necessary. This stuff should be done in getStuff
	argTypes := argTypesFrom(args)

	if method.Variadic != nil {
		argTypes[len(argTypes)-1] = strings.Replace(argTypes[len(argTypes)-1], "...", "[]", 1)
	}

	returnTypeString := fmt.Sprintf("%v_%v_OngoingVerification", interfaceName, method.Name)

	argsAsArray := make([]string, len(args))
	for i, arg := range args {
		_, t := splitArg(arg)
		t = strings.Replace(t, "...", "[]", 1)
		argsAsArray[i] = fmt.Sprintf("_param%v []%v", i, t)
	}

	g.p("type %v struct {", returnTypeString)
	g.p("mock *Mock%v", interfaceName)
	g.p("}")

	g.p("func (c *%v) getCapturedArguments() (%v) {", returnTypeString, strings.Join(argTypes, ", "))

	if len(args) > 0 {
		g.p("%v := c.getAllCapturedArguments()", strings.Join(argNames, ", "))

		prefixedArgNames := make([]string, len(argNames))
		for i, argName := range argNames {
			prefixedArgNames[i] = argName + "[len(" + argName + ")-1]"
		}
		g.p("return %v", strings.Join(prefixedArgNames, ", "))
	}
	g.p("}")

	g.p("func (c *%v) getAllCapturedArguments() (%v) {", returnTypeString, strings.Join(argsAsArray, ", "))
	if len(args) > 0 {
		calcParams(g, argString, args, method.Name)
		g.p("return")
	}
	g.p("}")

	g.p("func (verifier *Verifier%v) %v(%v) *%v {", interfaceName, method.Name, argString, returnTypeString)
	// if method.Variadic != nil {
	// 	g.p("params := []pegomock.Param{%v}", strings.Join(argNames[0:len(argNames)-1], ", "))
	// 	g.p("for _, param := range %v {", argNames[len(argNames)-1]).in()
	// 	g.p("params = append(params, param)")
	// 	g.out().p("}")
	// } else {
	g.p("params := []pegomock.Param{%v}", argNamesString)

	// }

	g.p("pegomock.GetGenericMockFrom(verifier.mock).Verify(verifier.inOrderContext, verifier.invocationCountMatcher, \"%v\", params, %v)",
		method.Name, method.Variadic != nil)
	g.p("return &%v{verifier.mock}", returnTypeString)

	g.p("}")

	return g
}

func splitArg(arg string) (argName string, argType string) {
	array := strings.Split(arg, " ")
	return array[0], array[1]
}

func argTypesFrom(args []string) []string {
	result := make([]string, len(args))
	for i, arg := range args {
		_, t := splitArg(arg)
		result[i] = t
	}
	return result
}

func calcParams(g *generator, argString string, args []string, methodName string) {
	if argString != "" {
		g.p("params := pegomock.GetGenericMockFrom(c.mock).GetInvocationParams(\"%v\")", methodName)
		g.p("if len(params) > 0 {")
		for i, arg := range args {
			paramType := strings.Replace(strings.Split(arg, " ")[1], "...", "[]", -1)
			g.p("_param%v = make([]%v, len(params[%v]))", i, paramType, i)
			g.p("for u, param := range params[%v] {", i)
			g.p("_param%v[u]=param.(%v)", i, paramType)
			g.p("}")
		}
		g.p("}")
	}
}

func getStuff(method *model.Method, g *generator, pkgOverride string) (
	args []string,
	argNames []string,
	argString string,
	rets []string,
	retString string,
	argNamesString string,
) {
	args = make([]string, len(method.In))
	argNames = make([]string, len(method.In))
	for i, p := range method.In {
		name := p.Name
		if name == "" {
			name = fmt.Sprintf("_param%d", i)
		}
		ts := p.Type.String(g.packageMap, pkgOverride)
		args[i] = name + " " + ts
		argNames[i] = name
	}
	if method.Variadic != nil {
		name := method.Variadic.Name
		if name == "" {
			name = fmt.Sprintf("_param%d", len(method.In))
		}
		ts := method.Variadic.Type.String(g.packageMap, pkgOverride)
		args = append(args, name+" ..."+ts)
		argNames = append(argNames, name)
	}
	argString = strings.Join(args, ", ")

	rets = make([]string, len(method.Out))
	for i, p := range method.Out {
		rets[i] = p.Type.String(g.packageMap, pkgOverride)
	}
	retString = strings.Join(rets, ", ")
	if len(rets) > 1 {
		retString = "(" + retString + ")"
	}
	if retString != "" {
		retString = " " + retString
	}

	argNamesString = strings.Join(argNames, ", ")
	// TODO: variadic arguments
	// if method.Variadic != nil {
	// 	// Non-trivial. The generated code must build a []interface{},
	// 	// but the variadic argument may be any type.
	// 	g.p("_s := []interface{}{%s}", strings.Join(argNames[:len(argNames)-1], ", "))
	// 	g.p("for _, _x := range %s {", argNames[len(argNames)-1])
	// 	g.in()
	// 	g.p("_s = append(_s, _x)")
	// 	g.out()
	// 	g.p("}")
	// 	callArgs = ", _s..."
	// }
	return
}

// Output returns the generator's output, formatted in the standard Go style.
func (g *generator) Output() []byte {
	src, err := format.Source(g.buf.Bytes())
	if err != nil {
		panic(fmt.Errorf("Failed to format generated source code: %s\n%s", err, g.buf.String()))
	}
	return src
}

func panicOnError(err error) {
	if err != nil {
		panic(err)
	}
}
