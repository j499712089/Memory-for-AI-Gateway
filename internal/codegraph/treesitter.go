//go:build cgo

package codegraph

import (
	"fmt"
	"strings"

	tree_sitter "github.com/tree-sitter/go-tree-sitter"
	tree_sitter_go "github.com/tree-sitter/tree-sitter-go/bindings/go"
	tree_sitter_javascript "github.com/tree-sitter/tree-sitter-javascript/bindings/go"
	tree_sitter_python "github.com/tree-sitter/tree-sitter-python/bindings/go"
	tree_sitter_typescript "github.com/tree-sitter/tree-sitter-typescript/bindings/go"
)

// treeSitterParser implements Parser on top of tree-sitter. It is only
// compiled when cgo is available (the grammar bindings embed C sources);
// otherwise NewParser falls back to the pure-Go heuristic parser.
type treeSitterParser struct{}

func newTreeSitterParser() (Parser, bool) {
	return treeSitterParser{}, true
}

var tsLanguages = map[string]func() *tree_sitter.Language{
	"go":         func() *tree_sitter.Language { return tree_sitter.NewLanguage(tree_sitter_go.Language()) },
	"javascript": func() *tree_sitter.Language { return tree_sitter.NewLanguage(tree_sitter_javascript.Language()) },
	"typescript": func() *tree_sitter.Language {
		return tree_sitter.NewLanguage(tree_sitter_typescript.LanguageTypescript())
	},
	"tsx":    func() *tree_sitter.Language { return tree_sitter.NewLanguage(tree_sitter_typescript.LanguageTSX()) },
	"python": func() *tree_sitter.Language { return tree_sitter.NewLanguage(tree_sitter_python.Language()) },
}

func (treeSitterParser) Parse(language string, content []byte) (ParseResult, error) {
	languageFunc, ok := tsLanguages[language]
	if !ok {
		return ParseResult{}, nil
	}
	parser := tree_sitter.NewParser()
	defer parser.Close()
	if err := parser.SetLanguage(languageFunc()); err != nil {
		return ParseResult{}, fmt.Errorf("set tree-sitter language %s: %w", language, err)
	}
	tree := parser.Parse(content, nil)
	if tree == nil {
		return ParseResult{}, nil
	}
	defer tree.Close()

	result := ParseResult{}
	walkNode(tree.RootNode(), content, &result)
	result.Edges = buildEdgesFromCalls(result.Symbols, lines(content))
	return result, nil
}

// symbolKind maps tree-sitter node kinds to our symbol taxonomy. Node kinds
// are grammar-specific and stable across minor grammar releases.
func symbolKind(nodeKind string) (kind string, ok bool) {
	switch {
	case strings.HasSuffix(nodeKind, "function_declaration"),
		strings.HasSuffix(nodeKind, "function_definition"):
		return "function", true
	case strings.HasSuffix(nodeKind, "method_declaration"),
		strings.HasSuffix(nodeKind, "method_definition"):
		return "method", true
	case nodeKind == "class_declaration" || nodeKind == "class_definition":
		return "class", true
	case nodeKind == "struct_spec" || nodeKind == "struct_declaration":
		return "struct", true
	case nodeKind == "interface_declaration" || nodeKind == "interface_spec":
		return "interface", true
	case nodeKind == "type_alias_declaration":
		return "type", true
	case nodeKind == "import_statement" || nodeKind == "import_declaration" ||
		nodeKind == "import_spec" || nodeKind == "import_from_statement":
		return "import", true
	}
	return "", false
}

func walkNode(node *tree_sitter.Node, content []byte, result *ParseResult) {
	if node == nil {
		return
	}
	if kind, ok := symbolKind(node.Kind()); ok {
		name := ""
		if nameNode := node.ChildByFieldName("name"); nameNode != nil {
			name = nodeText(nameNode, content)
		}
		if name == "" {
			// Imports and anonymous constructs carry no name field; fall back
			// to the node text for imports, skip the rest.
			if kind == "import" {
				name = strings.Trim(strings.TrimSpace(nodeText(node, content)), "\"'")
			} else {
				return
			}
		}
		start, end := node.StartPosition(), node.EndPosition()
		result.Symbols = append(result.Symbols, Symbol{
			Name:      name,
			Kind:      kind,
			Signature: strings.TrimSpace(nodeText(node, content)),
			LineStart: int(start.Row) + 1,
			LineEnd:   int(end.Row) + 1,
		})
	}
	for index := uint(0); index < node.NamedChildCount(); index++ {
		walkNode(node.NamedChild(index), content, result)
	}
}

func nodeText(node *tree_sitter.Node, content []byte) string {
	if node == nil {
		return ""
	}
	start, end := node.StartByte(), node.EndByte()
	if start > end || int(end) > len(content) {
		return ""
	}
	return string(content[start:end])
}
