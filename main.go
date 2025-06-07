package main

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"log"

	// Locally injected version of https://github.com/aarol/reload v1.2.2
	"markdown-server/reload"
	// Locally injected version of https://www.github.com/alecthomas/chroma v2.17.0
	"markdown-server/chroma"
	format "markdown-server/chroma/formatters/html"
	"markdown-server/chroma/lexers"
	"markdown-server/chroma/styles"
	// Locally injected version of https://www.github.com/gomarkdown/markdown v0.0.0-20250311123330-531bef5e742b
	"markdown-server/markdown"
	"markdown-server/markdown/ast"
	"markdown-server/markdown/html"
	"markdown-server/markdown/parser"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var FullPath, _ = filepath.Abs(os.Getenv("MARKDOWN_PATH"))
var TargetFolder, _ = filepath.Abs(os.Getenv("HTML_TARGET_PATH"))

func main() {
	PopulateVariables()
	CleanUpFolders()
	WalkFileTreeTwice()
	StartServingGeneratedFiles()
}

var FolderName = ""

func PopulateVariables() {
	posForFolder := strings.LastIndex(FullPath, string(os.PathSeparator))
	FolderName = FullPath[posForFolder+1:]
}

func CleanUpFolders() {
	err := os.RemoveAll(TargetFolder)
	if err != nil {
		log.Fatalf("While deleting old files encountered error: %v", err)
	}
}

func WalkFileTreeTwice() {
	err := filepath.Walk(FullPath, WalkAndCopyCSSFilesAndFolders)
	if err != nil {
		log.Fatalf("While transfering css files/creating folders encountered error: %v", err)
	}

	err = filepath.Walk(FullPath, WalkAndCopyMarkdownFiles)
	if err != nil {
		log.Fatalf("While converting + copying markdown files encountered error: %v", err)
	}
}

func StartServingGeneratedFiles() {
	fileSystem := http.FileServer(http.Dir(TargetFolder))

	if os.Getenv("HOT_RELOAD") != "" {
		reloader := reload.New(FullPath)
		http.Handle("GET /", reloader.Handle(fileSystem))

	} else {
		http.Handle("GET /", fileSystem)
	}
	server := &http.Server{
		Addr: os.Getenv("ADDRESS"),
	}

	log.Println("Starting server")
	if err := server.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("HTTP server error: %v", err)
	}
}

/*******************************************
*** FUNCTIONS FOR TRANSFERRING THE FILES ***
********************************************/

var CSSFileList = make([]string, 0)

func WalkAndCopyCSSFilesAndFolders(path string, info fs.FileInfo, err error) error {
	if path == FullPath {
		err = os.MkdirAll(TargetFolder, 0700)
		return err
	}
	path = strings.TrimPrefix(path, FolderName)
	if info.IsDir() {
		err = os.MkdirAll(TargetFolder+path, 0700)
		if err != nil {
			return err
		}
		return err
	}
	if strings.HasSuffix(path, ".css") && strings.Count(path, string(os.PathSeparator)) == 1 {
		if err != nil {
			return err
		}
		CSSFileList = append(CSSFileList, info.Name())
		return err
	}
	return err
}

func WalkAndCopyMarkdownFiles(path string, info fs.FileInfo, err error) error {
	if path == FullPath || info.IsDir() {
		return err
	}
	path = strings.TrimPrefix(path, FolderName)
	if strings.HasSuffix(path, ".md") {
		err = CopyAndTransformMarkdownFile(FullPath+path, TargetFolder+path)
		if err != nil {
			return err
		}
	} else {
		err = CopyFile(FullPath+path, TargetFolder+path)
		if err != nil {
			return err
		}
	}
	return err
}

func CopyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	err = os.WriteFile(dst, data, 0644)
	return err
}

func CopyAndTransformMarkdownFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}

	data = GenerateHTMLFromMarkdown(data)

	err = os.WriteFile(dst, data, 0644)
	return err
}

/**********************************************
*** FUNCTIONS TRANSFORMING MARKDOWN TO HTML ***
***********************************************/

var Extensions = parser.NoIntraEmphasis | parser.Tables | parser.FencedCode |
	parser.Autolink | parser.Strikethrough | parser.SpaceHeadings | parser.OrderedListStart |
	parser.BackslashLineBreak | parser.DefinitionLists | parser.EmptyLinesBreakList | parser.Footnotes |
	parser.SuperSubscript
var TitleExpression = regexp.MustCompile(`---\s*\ntitle: (.*?)\n---\s*\n`)

func GenerateHTMLFromMarkdown(markdownText []byte) []byte {
	titleText := ""
	result := TitleExpression.FindSubmatch(markdownText)
	if result != nil {
		markdownText = markdownText[len(result[0]):]
		titleText = string(result[1])
	}

	markdownText = markdown.NormalizeNewlines(markdownText)
	markdownText = markdown.ToHTML(markdownText, parser.NewWithExtensions(Extensions), GetRenderer())

	markdownText = append([]byte("<!DOCTYPE html>"+
		"<html lang=\"de\">"+
		"<head>"+
		"<meta charset=\"UTF-8\">"+
		"<title>"+titleText+"</title>"+
		GetCSSLinkTags()+
		"</head>"+
		"<body>"+
		"<div class=\"content\">"),
		markdownText...)
	markdownText = append(markdownText, []byte("</div></body></html>")...)

	return markdownText
}

func GetRenderer() *html.Renderer {
	opts := html.RendererOptions{
		Flags:          html.CommonFlags,
		RenderNodeHook: SpecialCodeBlockRenderHook,
	}
	return html.NewRenderer(opts)
}

func GetCSSLinkTags() string {
	result := ""
	for _, entry := range CSSFileList {
		result += "<link rel=\"stylesheet\" href=\"/" + entry + "\">\n"
	}
	return result
}

func SpecialCodeBlockRenderHook(w io.Writer, node ast.Node, _ bool) (ast.WalkStatus, bool) {
	switch node.(type) {
	case *ast.CodeBlock:
		CodeBlock(w, node.(*ast.CodeBlock))
	default:
		return ast.GoToNext, false
	}
	return ast.GoToNext, true
}

func CodeBlock(w io.Writer, node *ast.CodeBlock) {
	_, _ = w.Write([]byte("\n"))

	if len(node.Info) != 0 {
		Format(w, node.Literal, node.Info)
	} else {
		_, _ = w.Write([]byte("<pre><code>"))
		EscapeHTML(w, bytes.TrimSpace(node.Literal))
		_, _ = w.Write([]byte("</code></pre>"))
	}
	_, _ = w.Write([]byte("\n"))
}

func Format(writer io.Writer, source []byte, language []byte) {
	l := lexers.Get(string(language))
	if l == nil {
		l = lexers.Fallback
	}

	l = chroma.Coalesce(l)

	formatter := format.New(format.WithClasses(true), format.Standalone(false), format.WithLineNumbers(true))

	s := styles.Get("github")
	if s == nil {
		s = styles.Fallback
	}

	s.Types()
	it, err := l.Tokenise(nil, string(SpecialTrim(source)))
	if err != nil {
		return
	}

	_ = formatter.Format(writer, s, it)
}

func EscapeHTML(w io.Writer, d []byte) {
	var start, end int
	n := len(d)
	for end < n {
		escSeq := Escaper[d[end]]
		if escSeq != nil {
			_, _ = w.Write(d[start:end])
			_, _ = w.Write(escSeq)
			start = end + 1
		}
		end++
	}
	if start < n && end <= n {
		_, _ = w.Write(d[start:end])
	}
}

var Escaper = [256][]byte{
	'&': []byte("&amp;"),
	'<': []byte("&lt;"),
	'>': []byte("&gt;"),
	'"': []byte("&quot;"),
}

func SpecialTrim(input []byte) []byte {
	end := bytes.LastIndexByte(input, '\n')
	return input[:end]
}
