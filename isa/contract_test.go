package isa

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestFinalIllegalEncodingClassGate(t *testing.T) {
	classes := map[string][]uint32{
		"reserved-opcode":        {0x0000007f, 0x0000005b, 0x0000007b},
		"disabled-C":             {0x00000001},
		"disabled-RV64":          {0x0000001b, 0x0000003b, bitsF3(3, opLoad), bitsF3(3, opStore)},
		"disabled-D":             {bitsF7(1, 0, opFloat), 1<<25 | opFMAdd, bitsF3(3, opFLoad), bitsF3(3, opFStore)},
		"disabled-A":             {0x0000202f},
		"disabled-vector":        {0x00000057},
		"disabled-accelerators":  {bitsF7(2, 0, opCustom), bitsF7(3, 0, opCustom), bitsF3(5, opGather), bitsF3(2, opGather), bitsF3(4, opGather), bitsF3(6, opGather), bitsF3(7, opGather)},
		"reserved-funct":         {bitsF3(2, opBranch), bitsF7(2, 0, opReg), bitsF7(7, 4, opReg)},
		"reserved-format":        {3<<25 | opFMAdd, bitsF7(1, 0, opFloat)},
		"reserved-rounding-mode": {bitsF7(0, 5, opFloat), bitsF7(0, 6, opFloat)},
		"reserved-register-field": {bitsF7(0, 0, opCustom) | 1<<7, bitsF7(0, 0, opCustom) | 1<<20,
			bitsF7(0, 3, opCustom) | 1<<7, bitsF7(1, 0, opCustom) | 1<<20},
		"rtl-default-legality-hole": {bitsRS2(1, 0x2c, 0, opFloat), bitsRS2(2, 0x60, 0, opFloat),
			bitsF7(0x10, 3, opFloat), bitsF7(4, 3, opCustom), 0x10500073},
	}
	required := []string{
		"reserved-opcode", "disabled-C", "disabled-RV64", "disabled-D", "disabled-A",
		"disabled-vector", "disabled-accelerators", "reserved-funct", "reserved-format", "reserved-rounding-mode",
		"reserved-register-field", "rtl-default-legality-hole",
	}
	for _, class := range required {
		if len(classes[class]) == 0 {
			t.Errorf("illegal encoding class %q has no vector", class)
		}
	}
	for class, words := range classes {
		t.Run(class, func(t *testing.T) {
			for _, word := range words {
				decoded, err := Decode(word)
				if err == nil {
					t.Errorf("%#08x unexpectedly decoded as %s", word, decoded.Name)
					continue
				}
				var illegal *IllegalInstructionError
				if !errors.As(err, &illegal) || illegal.Word != word {
					t.Errorf("%#08x returned %T %v", word, err, err)
				}
			}
		})
	}
}

func TestISAPublicSurfaceIsStatelessAndSingleInstruction(t *testing.T) {
	allowedExportedFunctions := map[string]bool{
		"Catalog": true, "CSRCatalog": true, "Decode": true,
		"EvaluateInteger": true, "CompleteMemory": true,
		"EvaluateFloat": true, "CompleteFloatMemory": true,
		"EvaluateSystem": true,
		"EvaluateCustom": true, "CompletePackedLoad": true,
	}
	forbiddenTopLevelTypes := map[string]bool{
		"State": true, "Machine": true, "Device": true, "Core": true, "Warp": true,
		"Scheduler": true, "Executor": true, "Kernel": true, "Cache": true, "MMU": true,
	}
	forbiddenFunctionFragments := []string{"Fetch", "Step", "Run", "Schedule", "Dispatch", "Launch", "Tick"}
	allowedImports := map[string]bool{
		"fmt": true,
		"vortex.local/simulator/support/softfloat": true,
	}

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	seenExportedFunctions := make(map[string]bool)
	files := 0
	for _, dirEntry := range entries {
		name := dirEntry.Name()
		if dirEntry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		files++
		file, err := parser.ParseFile(token.NewFileSet(), filepath.Clean(name), nil, 0)
		if err != nil {
			t.Errorf("parse %s: %v", name, err)
			continue
		}
		for _, imported := range file.Imports {
			path, err := strconv.Unquote(imported.Path.Value)
			if err != nil || !allowedImports[path] {
				t.Errorf("production ISA import %q in %s is outside stateless allowlist", imported.Path.Value, name)
			}
		}
		for _, declaration := range file.Decls {
			switch node := declaration.(type) {
			case *ast.FuncDecl:
				if node.Name.Name == "init" {
					t.Errorf("production ISA must not have init side effects (%s)", name)
				}
				if node.Recv == nil && ast.IsExported(node.Name.Name) {
					seenExportedFunctions[node.Name.Name] = true
					if !allowedExportedFunctions[node.Name.Name] {
						t.Errorf("unexpected exported ISA operation %s in %s", node.Name.Name, name)
					}
					for _, fragment := range forbiddenFunctionFragments {
						if strings.Contains(node.Name.Name, fragment) {
							t.Errorf("ISA exposes forbidden orchestration operation %s", node.Name.Name)
						}
					}
				}

			case *ast.GenDecl:
				if node.Tok == token.VAR && name != "catalog.go" && name != "csr_catalog.go" {
					t.Errorf("package variable outside immutable manifest files in %s", name)
				}
				if node.Tok == token.VAR {
					for _, spec := range node.Specs {
						for _, variable := range spec.(*ast.ValueSpec).Names {
							if ast.IsExported(variable.Name) {
								t.Errorf("exported mutable package variable %s in %s", variable.Name, name)
							}
						}
					}
				}
				if node.Tok == token.TYPE {
					for _, spec := range node.Specs {
						typeName := spec.(*ast.TypeSpec).Name.Name
						if forbiddenTopLevelTypes[typeName] {
							t.Errorf("ISA defines forbidden long-lived owner type %s in %s", typeName, name)
						}
					}
				}
			}
		}
	}
	if files == 0 {
		t.Fatal("no production ISA files audited")
	}
	for function := range allowedExportedFunctions {
		if !seenExportedFunctions[function] {
			t.Errorf("documented single-instruction API %s is missing", function)
		}
	}
}
