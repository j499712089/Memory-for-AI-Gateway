package codegraph

import (
	"path/filepath"
	"regexp"
	"strings"
)

// Symbol is a parsed code entity (function, method, class, struct, ...).
type Symbol struct {
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	Signature string `json:"signature"`
	LineStart int    `json:"line_start"`
	LineEnd   int    `json:"line_end"`
}

// Edge connects two symbols of the same file. Edge types follow the
// code_edges CHECK constraint: import / call / export / inherit / reference.
type Edge struct {
	Source string `json:"source"`
	Target string `json:"target"`
	Type   string `json:"type"`
}

// ParseResult is the union of everything extracted from one file.
type ParseResult struct {
	Symbols []Symbol
	Edges   []Edge
}

// Parser turns source code into symbols and edges. The production build uses
// tree-sitter (cgo); environments without a C toolchain use the pure-Go
// fallback parser, which keeps the worker pipeline fully functional.
type Parser interface {
	Parse(language string, content []byte) (ParseResult, error)
}

// SupportedLanguages returns the four languages indexed by the codegraph
// worker: go, javascript, typescript (incl. tsx), python.
func SupportedLanguages() []string {
	return []string{"go", "javascript", "typescript", "tsx", "python"}
}

// DetectLanguage maps a file path to one of the supported languages.
// An empty result means the file is out of scope for indexing.
func DetectLanguage(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".go":
		return "go"
	case ".js", ".mjs", ".cjs", ".jsx":
		return "javascript"
	case ".ts":
		return "typescript"
	case ".tsx":
		return "tsx"
	case ".py", ".pyi":
		return "python"
	}
	return ""
}

// NewParser returns the best parser available in this build. On cgo-enabled
// builds it prefers tree-sitter; otherwise it falls back to the heuristic
// parser so the asset sub-track remains fully testable and operational.
func NewParser() Parser {
	if parser, ok := newTreeSitterParser(); ok {
		return parser
	}
	return fallbackParser{}
}

// ---------------------------------------------------------------------------
// Pure-Go fallback parser
// ---------------------------------------------------------------------------

var (
	goFuncRe   = regexp.MustCompile(`^\s*func\s+(?:\([^)]*\)\s*)?([A-Za-z_]\w*)\s*\(`)
	goTypeRe   = regexp.MustCompile(`^\s*type\s+([A-Za-z_]\w*)\s+(struct|interface)\b`)
	goImportRe = regexp.MustCompile(`^\s*import\s+(?:[A-Za-z_]\w*\s+)?"([^"]+)"`)
	goParenRe  = regexp.MustCompile(`^\s*import\s+\(`)

	jsFuncRe   = regexp.MustCompile(`^\s*(?:export\s+)?(?:async\s+)?function\s+([A-Za-z_$]\w*)`)
	jsConstRe  = regexp.MustCompile(`^\s*(?:export\s+)?(?:const|let|var)\s+([A-Za-z_$]\w*)\s*=\s*(?:async\s*)?(?:\([^)]*\)|[A-Za-z_$]\w*)\s*=>`)
	jsClassRe  = regexp.MustCompile(`^\s*(?:export\s+)?(?:abstract\s+)?class\s+([A-Za-z_$]\w*)`)
	jsIfaceRe  = regexp.MustCompile(`^\s*(?:export\s+)?interface\s+([A-Za-z_$]\w*)`)
	jsTypeRe   = regexp.MustCompile(`^\s*(?:export\s+)?type\s+([A-Za-z_$]\w*)\s*=`)
	jsMethodRe = regexp.MustCompile(`^\s+(?:async\s+)?([A-Za-z_$]\w*)\s*\([^)]*\)\s*\{?`)
	jsImportRe = regexp.MustCompile(`^\s*import\s+(?:type\s+)?(?:[\w$*{} ,]+\s+from\s+)?["']([^"']+)["']`)

	pyFuncRe   = regexp.MustCompile(`^\s*(?:async\s+)?def\s+([A-Za-z_]\w*)\s*\(`)
	pyClassRe  = regexp.MustCompile(`^\s*class\s+([A-Za-z_]\w*)`)
	pyImportRe = regexp.MustCompile(`^\s*(?:from\s+([\w.]+)\s+)?import\s+([\w.*]+(?:\s*,\s*[\w.*]+)*)`)
)

type fallbackParser struct{}

func (fallbackParser) Parse(language string, content []byte) (ParseResult, error) {
	switch language {
	case "go":
		return parseGoSource(content)
	case "javascript", "typescript", "tsx":
		return parseJSTS(content)
	case "python":
		return parsePython(content)
	}
	return ParseResult{}, nil
}

func lines(content []byte) []string {
	raw := strings.Split(string(content), "\n")
	return raw
}

// detectCallTargets returns edges for every `name(` call found in the body,
// where name is a locally defined symbol. Only definition symbols within the
// same file are linked, keeping edges meaningful without type resolution.
func detectCallTargets(body string, defined map[string]bool) []Edge {
	if body == "" {
		return nil
	}
	callRe := regexp.MustCompile(`\b([A-Za-z_$]\w*)\s*\(`)
	var edges []Edge
	seen := make(map[string]bool)
	for _, match := range callRe.FindAllStringSubmatch(body, -1) {
		name := match[1]
		if !defined[name] || seen[name] {
			continue
		}
		seen[name] = true
		edges = append(edges, Edge{Source: "", Target: name, Type: "call"})
	}
	return edges
}

func buildEdgesFromCalls(symbols []Symbol, rawLines []string) []Edge {
	byName := make(map[string]bool, len(symbols))
	for _, symbol := range symbols {
		byName[symbol.Name] = true
	}
	// Determine each symbol's body boundary: up to the next symbol start.
	bounds := make([][2]int, len(symbols))
	for index, symbol := range symbols {
		end := len(rawLines)
		if index+1 < len(symbols) {
			end = symbols[index+1].LineStart - 1
			if end < symbol.LineEnd {
				end = symbol.LineEnd
			}
		}
		bounds[index] = [2]int{symbol.LineStart, end}
	}
	var edges []Edge
	for index, symbol := range symbols {
		if symbol.LineStart <= 0 {
			continue
		}
		start := symbol.LineStart - 1
		end := bounds[index][1]
		if end > len(rawLines) {
			end = len(rawLines)
		}
		if start >= end {
			continue
		}
		body := strings.Join(rawLines[start:end], "\n")
		for _, edge := range detectCallTargets(body, byName) {
			edge.Source = symbol.Name
			edges = append(edges, edge)
		}
	}
	return edges
}

func parseGoSource(content []byte) (ParseResult, error) {
	raw := lines(content)
	var result ParseResult
	inImportBlock := false
	for index, line := range raw {
		lineNo := index + 1
		if goParenRe.MatchString(line) {
			inImportBlock = true
			continue
		}
		if inImportBlock {
			if strings.Contains(line, ")") {
				inImportBlock = false
			}
			if match := goImportRe.FindStringSubmatch(line); len(match) > 1 {
				result.Symbols = append(result.Symbols, Symbol{Name: match[1], Kind: "import", LineStart: lineNo, LineEnd: lineNo})
			}
			continue
		}
		if match := goImportRe.FindStringSubmatch(line); len(match) > 1 {
			result.Symbols = append(result.Symbols, Symbol{Name: match[1], Kind: "import", LineStart: lineNo, LineEnd: lineNo})
			continue
		}
		if match := goFuncRe.FindStringSubmatch(line); len(match) > 1 {
			result.Symbols = append(result.Symbols, Symbol{Name: match[1], Kind: "function", Signature: strings.TrimSpace(line), LineStart: lineNo, LineEnd: lineNo})
			continue
		}
		if match := goTypeRe.FindStringSubmatch(line); len(match) > 2 {
			kind := "struct"
			if match[2] == "interface" {
				kind = "interface"
			}
			result.Symbols = append(result.Symbols, Symbol{Name: match[1], Kind: kind, Signature: strings.TrimSpace(line), LineStart: lineNo, LineEnd: lineNo})
		}
	}
	// Close open function bodies for edge extraction.
	for index := range result.Symbols {
		end := len(raw)
		if index+1 < len(result.Symbols) {
			end = result.Symbols[index+1].LineStart - 1
		}
		if result.Symbols[index].LineEnd < end {
			result.Symbols[index].LineEnd = end
		}
	}
	result.Edges = buildEdgesFromCalls(result.Symbols, raw)
	return result, nil
}

func parseJSTS(content []byte) (ParseResult, error) {
	raw := lines(content)
	var result ParseResult
	inClass := false
	var classSymbols []Symbol
	for index, line := range raw {
		lineNo := index + 1
		if match := jsImportRe.FindStringSubmatch(line); len(match) > 1 {
			result.Symbols = append(result.Symbols, Symbol{Name: match[1], Kind: "import", LineStart: lineNo, LineEnd: lineNo})
			continue
		}
		if match := jsClassRe.FindStringSubmatch(line); len(match) > 1 {
			result.Symbols = append(result.Symbols, Symbol{Name: match[1], Kind: "class", Signature: strings.TrimSpace(line), LineStart: lineNo, LineEnd: lineNo})
			inClass = true
			classSymbols = append(classSymbols, result.Symbols[len(result.Symbols)-1])
			continue
		}
		if match := jsIfaceRe.FindStringSubmatch(line); len(match) > 1 {
			result.Symbols = append(result.Symbols, Symbol{Name: match[1], Kind: "interface", Signature: strings.TrimSpace(line), LineStart: lineNo, LineEnd: lineNo})
			continue
		}
		if match := jsTypeRe.FindStringSubmatch(line); len(match) > 1 {
			result.Symbols = append(result.Symbols, Symbol{Name: match[1], Kind: "type", Signature: strings.TrimSpace(line), LineStart: lineNo, LineEnd: lineNo})
			continue
		}
		if match := jsFuncRe.FindStringSubmatch(line); len(match) > 1 {
			result.Symbols = append(result.Symbols, Symbol{Name: match[1], Kind: "function", Signature: strings.TrimSpace(line), LineStart: lineNo, LineEnd: lineNo})
			continue
		}
		if match := jsConstRe.FindStringSubmatch(line); len(match) > 1 {
			result.Symbols = append(result.Symbols, Symbol{Name: match[1], Kind: "function", Signature: strings.TrimSpace(line), LineStart: lineNo, LineEnd: lineNo})
			continue
		}
		if inClass {
			if match := jsMethodRe.FindStringSubmatch(line); len(match) > 1 {
				result.Symbols = append(result.Symbols, Symbol{Name: match[1], Kind: "method", Signature: strings.TrimSpace(line), LineStart: lineNo, LineEnd: lineNo})
			}
		}
		if strings.HasPrefix(strings.TrimSpace(line), "}") && len(classSymbols) > 0 {
			class := classSymbols[len(classSymbols)-1]
			for index := range result.Symbols {
				if result.Symbols[index].Name == class.Name && result.Symbols[index].Kind == "class" {
					result.Symbols[index].LineEnd = lineNo
					break
				}
			}
			classSymbols = classSymbols[:len(classSymbols)-1]
			if len(classSymbols) == 0 {
				inClass = false
			}
		}
	}
	result.Edges = buildEdgesFromCalls(result.Symbols, raw)
	return result, nil
}

func parsePython(content []byte) (ParseResult, error) {
	raw := lines(content)
	var result ParseResult
	inClass := false
	for index, line := range raw {
		lineNo := index + 1
		if match := pyImportRe.FindStringSubmatch(line); len(match) > 0 {
			target := match[1]
			if target == "" {
				target = match[2]
			} else {
				target = target + "." + strings.Split(match[2], ",")[0]
			}
			if target != "" {
				result.Symbols = append(result.Symbols, Symbol{Name: strings.TrimSpace(target), Kind: "import", LineStart: lineNo, LineEnd: lineNo})
			}
			continue
		}
		if match := pyClassRe.FindStringSubmatch(line); len(match) > 1 {
			result.Symbols = append(result.Symbols, Symbol{Name: match[1], Kind: "class", Signature: strings.TrimSpace(line), LineStart: lineNo, LineEnd: lineNo})
			inClass = true
			continue
		}
		if match := pyFuncRe.FindStringSubmatch(line); len(match) > 1 {
			kind := "function"
			if inClass {
				kind = "method"
			}
			result.Symbols = append(result.Symbols, Symbol{Name: match[1], Kind: kind, Signature: strings.TrimSpace(line), LineStart: lineNo, LineEnd: lineNo})
			continue
		}
		if strings.TrimSpace(line) == "" && len(result.Symbols) > 0 {
			// Python blocks are indentation based; a blank line at column 0
			// closes the previous class/function region.
			last := &result.Symbols[len(result.Symbols)-1]
			if last.LineEnd < lineNo-1 {
				last.LineEnd = lineNo - 1
			}
			if last.Kind == "class" {
				inClass = false
			}
		}
	}
	result.Edges = buildEdgesFromCalls(result.Symbols, raw)
	return result, nil
}
