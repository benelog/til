package main

import (
	"bytes"
	"html/template"

	"github.com/microcosm-cc/bluemonday"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/renderer/html"
)

var (
	md = goldmark.New(
		goldmark.WithExtensions(extension.GFM),
		goldmark.WithRendererOptions(html.WithHardWraps()),
	)
	sanitizer = newSanitizer()
)

func newSanitizer() *bluemonday.Policy {
	p := bluemonday.UGCPolicy()
	// GFM 체크박스와 코드 하이라이팅용 클래스를 살려둔다.
	p.AllowAttrs("type", "checked", "disabled").OnElements("input")
	p.AllowAttrs("class").Matching(bluemonday.SpaceSeparatedTokens).OnElements("code", "pre", "span", "ul", "li")
	p.AllowElements("table", "thead", "tbody", "tr", "th", "td")
	return p
}

// renderMarkdown 은 마크다운을 HTML 로 바꾸고 정화한다.
// 쓰기는 로그인한 사람만 가능하지만, 붙여넣기한 남의 마크다운에 스크립트가
// 섞여 들어올 수 있으므로 렌더 단계에서 한 번 걸러준다.
func renderMarkdown(src string) template.HTML {
	var buf bytes.Buffer
	if err := md.Convert([]byte(src), &buf); err != nil {
		return template.HTML(template.HTMLEscapeString(src))
	}
	return template.HTML(sanitizer.SanitizeBytes(buf.Bytes()))
}
