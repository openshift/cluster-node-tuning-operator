// This generator is based on the api doc generator of the Prometheus Operator
// see https://github.com/coreos/prometheus-operator/tree/master/cmd/po-docgen
// Changes:
// - also print string types
package main

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/doc"
	"go/parser"
	"go/token"
	"os"
	"reflect"
	"strings"
)

func main() {
	printAPIDocs(os.Args[2:])
}

const (
	firstParagraph = `
# Performance Profile

This document documents the PerformanceProfile API introduced by the Performance controller.

> This document is generated from code comments on the ` + "`PerformanceProfile`" + ` struct.
> When contributing a change to this document please do so by changing those code comments.`
)

var (
	links = map[string]string{
		"metav1.ObjectMeta":    "https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.17/#objectmeta-v1-meta",
		"metav1.ListMeta":      "https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.17/#listmeta-v1-meta",
		"metav1.LabelSelector": "https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.17/#labelselector-v1-meta",
	}

	selfLinks = map[string]string{}
)

func toSectionLink(name string) string {
	name = strings.ToLower(name)
	name = strings.Replace(name, " ", "-", -1)
	return name
}

func printTOC(types []KubeTypes) {
	fmt.Printf("\n## Table of Contents\n")
	for _, t := range types {
		strukt := t[0]
		fmt.Printf("* [%s](#%s)\n", strukt.Name, toSectionLink(strukt.Name))
	}
}

func printAPIDocs(paths []string) {
	fmt.Println(firstParagraph)

	types := ParseDocumentationFrom(paths)
	for _, t := range types {
		strukt := t[0]
		selfLinks[strukt.Name] = "#" + strings.ToLower(strukt.Name)
	}

	// we need to parse once more to now add the self links
	types = ParseDocumentationFrom(paths)

	printTOC(types)

	for _, t := range types {
		strukt := t[0]
		fmt.Printf("\n## %s\n\n%s\n\n", strukt.Name, strukt.Doc)

		if len(t) > 1 {
			fmt.Println("| Field | Description | Scheme | Required |")
			fmt.Println("| ----- | ----------- | ------ | -------- |")
			fields := t[1:]
			for _, f := range fields {
				fmt.Println("|", f.Name, "|", f.Doc, "|", f.Type, "|", f.Mandatory, "|")
			}
		} else {
			fmt.Printf("%s is of type `string`.\n", strukt.Name)
		}

		fmt.Println("")
		fmt.Println("[Back to TOC](#table-of-contents)")
	}
}

// Pair of strings. We keed the name of fields and the doc
type Pair struct {
	Name, Doc, Type string
	Mandatory       bool
}

// KubeTypes is an array to represent all available types in a parsed file. [0] is for the type itself
type KubeTypes []Pair

// ParseDocumentationFrom gets all types' documentation and returns them as an
// array. Each type is again represented as an array (we have to use arrays as we
// need to be sure for the order of the fields). This function returns fields and
// struct definitions that have no documentation as {name, ""}.
func ParseDocumentationFrom(srcs []string) []KubeTypes {
	var docForTypes []KubeTypes

	for _, src := range srcs {
		pkg := astFrom(src)

		for _, kubType := range pkg.Types {
			if structType, ok := kubType.Decl.Specs[0].(*ast.TypeSpec).Type.(*ast.StructType); ok {
				var ks KubeTypes
				ks = append(ks, Pair{kubType.Name, fmtRawDoc(kubType.Doc), "", false})

				for _, field := range structType.Fields.List {
					typeString := fieldType(field.Type)
					fieldMandatory := fieldRequired(field)
					if n := fieldName(field); n != "-" {
						fieldDoc := fmtRawDoc(field.Doc.Text())
						ks = append(ks, Pair{n, fieldDoc, typeString, fieldMandatory})
					}
				}
				docForTypes = append(docForTypes, ks)
			} else {
				var ks KubeTypes
				ks = append(ks, Pair{kubType.Name, fmtRawDoc(kubType.Doc), "string", false})
				docForTypes = append(docForTypes, ks)
			}
		}
	}

	return docForTypes
}

func astFrom(filePath string) *doc.Package {
	fset := token.NewFileSet()

	f, err := parser.ParseFile(fset, filePath, nil, parser.ParseComments)
	if err != nil {
		fmt.Println(err)
		return nil
	}

	p, err := doc.NewFromFiles(fset, []*ast.File{f}, "")
	if err != nil {
		fmt.Println(err)
		return nil
	}

	return p
}

func fmtRawDoc(rawDoc string) string {
	var buffer bytes.Buffer
	delPrevChar := func() {
		if buffer.Len() > 0 {
			buffer.Truncate(buffer.Len() - 1) // Delete the last " " or "\n"
		}
	}

	// Ignore all lines after ---
	rawDoc = strings.Split(rawDoc, "---")[0]

	for _, line := range strings.Split(rawDoc, "\n") {
		line = strings.TrimRight(line, " ")
		leading := strings.TrimLeft(line, " ")
		switch {
		case len(line) == 0: // Keep paragraphs
			delPrevChar()
			buffer.WriteString("\n\n")
		case strings.HasPrefix(leading, "TODO"): // Ignore one line TODOs
		case strings.HasPrefix(leading, "+"): // Ignore instructions to go2idl
		default:
			if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
				delPrevChar()
				line = "\n" + line + "\n" // Replace it with newline. This is useful when we have a line with: "Example:\n\tJSON-someting..."
			} else {
				line += " "
			}
			buffer.WriteString(line)
		}
	}

	postDoc := strings.TrimRight(buffer.String(), "\n")
	postDoc = strings.Replace(postDoc, "\\\"", "\"", -1) // replace user's \" to "
	postDoc = strings.Replace(postDoc, "\"", "\\\"", -1) // Escape "
	postDoc = strings.Replace(postDoc, "\n", "\\n", -1)
	postDoc = strings.Replace(postDoc, "\t", "\\t", -1)
	postDoc = strings.Replace(postDoc, "|", "\\|", -1)
	postDoc = strings.Replace(postDoc, "<", "&lt;", -1)
	postDoc = strings.Replace(postDoc, ">", "&gt;", -1)

	return postDoc
}

func toLink(typeName string) string {
	selfLink, hasSelfLink := selfLinks[typeName]
	if hasSelfLink {
		return wrapInLink(typeName, selfLink)
	}

	link, hasLink := links[typeName]
	if hasLink {
		return wrapInLink(typeName, link)
	}

	return typeName
}

func wrapInLink(text, link string) string {
	return fmt.Sprintf("[%s](%s)", text, link)
}

// fieldName returns the name of the field as it should appear in JSON format
// "-" indicates that this field is not part of the JSON representation
func fieldName(field *ast.Field) string {
	jsonTag := ""
	if field.Tag != nil {
		jsonTag = reflect.StructTag(field.Tag.Value[1 : len(field.Tag.Value)-1]).Get("json") // Delete first and last quotation
		if strings.Contains(jsonTag, "inline") {
			return "-"
		}
	}

	jsonTag = strings.Split(jsonTag, ",")[0] // This can return "-"
	if jsonTag == "" {
		if field.Names != nil {
			return field.Names[0].Name
		}
		return field.Type.(*ast.Ident).Name
	}
	return jsonTag
}

// fieldRequired returns whether a field is a required field.
func fieldRequired(field *ast.Field) bool {
	if field.Tag != nil {
		jsonTag := reflect.StructTag(field.Tag.Value[1 : len(field.Tag.Value)-1]).Get("json") // Delete first and last quotation
		return !strings.Contains(jsonTag, "omitempty")
	}

	return false
}

func fieldType(typ ast.Expr) string {
	switch field := typ.(type) {
	case *ast.Ident:
		return toLink(field.Name)
	case *ast.StarExpr:
		return "*" + toLink(fieldType(field.X))
	case *ast.SelectorExpr:
		e := field
		pkg := e.X.(*ast.Ident)
		t := e.Sel
		return toLink(pkg.Name + "." + t.Name)
	case *ast.ArrayType:
		return "[]" + toLink(fieldType(field.Elt))
	case *ast.MapType:
		return "map[" + toLink(fieldType(field.Key)) + "]" + toLink(fieldType(field.Value))
	default:
		return ""
	}
}
