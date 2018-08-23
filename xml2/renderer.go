package xml2

import (
	"fmt"
	"io"
	"strings"

	"github.com/gomarkdown/markdown/ast"
	"github.com/gomarkdown/markdown/html"
	"github.com/miekg/markdown/xml"
	"github.com/mmarkdown/mmark/mast"
)

// Flags control optional behavior of XML2 renderer.
type Flags int

// HTML renderer configuration options.
const (
	FlagsNone   Flags = 0
	XMLFragment Flags = 1 << iota // Don't generate a complete XML document
	SkipHTML                      // Skip preformatted HTML blocks - skips comments
	SkipImages                    // Skip embedded images, set to true for XML2

	CommonFlags Flags = SkipImages
)

// RendererOptions is a collection of supplementary parameters tweaking
// the behavior of various parts of XML2 renderer.
type RendererOptions struct {
	// Callouts are supported and detected by setting this option to the callout prefix.
	Callout string

	Flags Flags // Flags allow customizing this renderer's behavior

	// if set, called at the start of RenderNode(). Allows replacing
	// rendering of some nodes
	RenderNodeHook html.RenderNodeFunc

	// Comments is a list of comments the renderer should detect when
	// parsing code blocks and detecting callouts.
	Comments [][]byte
}

// Renderer implements Renderer interface for IETF XMLv2 output. See RFC 7941.
type Renderer struct {
	opts RendererOptions

	documentMatter ast.DocumentMatters // keep track of front/main/back matter
	section        *ast.Heading        // current open section
	title          bool                // did we output a title block

	// Track heading IDs to prevent ID collision in a single generation.
	headingIDs map[string]int
}

// NewRenderer creates and configures an Renderer object, which satisfies the Renderer interface.
func NewRenderer(opts RendererOptions) *Renderer {
	html.IDTag = "anchor"
	return &Renderer{opts: opts, headingIDs: make(map[string]int)}
}

func (r *Renderer) text(w io.Writer, text *ast.Text) {
	if _, parentIsLink := text.Parent.(*ast.Link); parentIsLink {
		r.out(w, text.Literal)
		return
	}
	if heading, parentIsHeading := text.Parent.(*ast.Heading); parentIsHeading {
		if heading.IsSpecial && xml.IsAbstract(heading.Literal) {
			return
		}

		html.EscapeHTML(w, text.Literal)
		r.outs(w, `">`)
		return
	}

	html.EscapeHTML(w, text.Literal)

	if _, parentIsCaption := text.Parent.(*ast.Caption); parentIsCaption {
		r.outs(w, `">`)
	}
}

func (r *Renderer) hardBreak(w io.Writer, node *ast.Hardbreak) {
	r.outs(w, "<vspace />")
	r.cr(w)
}

func (r *Renderer) strong(w io.Writer, node *ast.Strong, entering bool) {
	// *iff* we have a text node as a child *and* that text is 2119, we output bcp14 tags, otherwise just string.
	text := ast.GetFirstChild(node)
	if t, ok := text.(*ast.Text); ok {
		if xml.Is2119(t.Literal) {
			// out as-is.
			r.outOneOf(w, entering, "", "")
			return
		}
	}

	r.outOneOf(w, entering, `<spanx style="strong">`, "</spanx>")
}

func (r *Renderer) matter(w io.Writer, node *ast.DocumentMatter) {
	r.sectionClose(w, nil)

	switch node.Matter {
	case ast.DocumentMatterFront:
		r.cr(w)
		r.outs(w, "<front>")
		r.cr(w)
	case ast.DocumentMatterMain:
		r.cr(w)
		r.outs(w, "</front>")
		r.cr(w)
		r.cr(w)
		r.outs(w, "<middle>")
		r.cr(w)
	case ast.DocumentMatterBack:
		r.cr(w)
		r.outs(w, "</middle>")
		r.cr(w)
		r.cr(w)
		r.outs(w, "<back>")
		r.cr(w)
	}
	r.documentMatter = node.Matter
}

func (r *Renderer) headingEnter(w io.Writer, heading *ast.Heading) {
	r.cr(w)

	tag := "<section"
	if heading.IsSpecial {
		tag = "<note"
		if xml.IsAbstract(heading.Literal) {
			tag = "<abstract>"
			r.outs(w, tag)
			return
		}
	}

	var attrs []string
	if heading.HeadingID != "" {
		id := r.ensureUniqueHeadingID(heading.HeadingID)
		attrID := `anchor="` + id + `"`
		attrs = append(attrs, attrID)
	}

	attr := ""
	if len(attrs) > 0 {
		attr += " " + strings.Join(attrs, " ")
	}

	// If we want to support block level attributes here, it will clash with the
	// title= attribute that is outed in text() - and thus later.
	r.outs(w, tag)
	r.outs(w, attr+` title="`)
}

func (r *Renderer) headingExit(w io.Writer, heading *ast.Heading) {
	r.cr(w)
}

func (r *Renderer) heading(w io.Writer, node *ast.Heading, entering bool) {
	if !entering {
		r.headingExit(w, node)
		return
	}

	r.sectionClose(w, node)

	r.headingEnter(w, node)
}

func (r *Renderer) citation(w io.Writer, node *ast.Citation, entering bool) {
	if !entering {
		return
	}
	for i, c := range node.Destination {
		if node.Type[i] == ast.CitationTypeSuppressed {
			continue
		}

		attr := []string{fmt.Sprintf(`target="%s"`, c)}
		r.outTag(w, "<xref", attr)
		r.outs(w, "</xref>")
	}
}

func (r *Renderer) paragraphEnter(w io.Writer, para *ast.Paragraph) {
	if p, ok := para.Parent.(*ast.ListItem); ok {
		// Skip outputting <t> in lists.
		// Fake multiple paragraphs by inserting a hard break.
		if len(p.GetChildren()) > 1 {
			first := ast.GetFirstChild(para.Parent)
			if first != para {
				r.hardBreak(w, &ast.Hardbreak{})
			}
		}
		return
	}

	tag := tagWithAttributes("<t", html.BlockAttrs(para))
	r.outs(w, tag)
}

func (r *Renderer) paragraphExit(w io.Writer, para *ast.Paragraph) {
	if _, ok := para.Parent.(*ast.ListItem); ok {
		// Skip outputting </t> in lists.
		return
	}

	r.outs(w, "</t>")
	r.cr(w)
}

func (r *Renderer) paragraph(w io.Writer, para *ast.Paragraph, entering bool) {
	if entering {
		r.paragraphEnter(w, para)
	} else {
		r.paragraphExit(w, para)
	}
}

func (r *Renderer) listEnter(w io.Writer, nodeData *ast.List) {
	var attrs []string

	if nodeData.IsFootnotesList {
		r.outs(w, "\n<div class=\"footnotes\">\n\n")
		r.cr(w)
	}
	r.cr(w)

	openTag := "<list"
	style := `style="symbols"`
	if nodeData.ListFlags&ast.ListTypeOrdered != 0 {
		style = `style="numbers"`
		if nodeData.Start > 0 {
			attrs = append(attrs, fmt.Sprintf(`start="%d"`, nodeData.Start))
		}
	}
	if nodeData.ListFlags&ast.ListTypeDefinition != 0 {
		style = `style="hanging"`
	}

	attrs = append(attrs, html.BlockAttrs(nodeData)...)
	// if there is a block level attribute with style, we shouldn't use the default.
	if !xml.AttributesContains("style", attrs) {
		attrs = append(attrs, style)
	}
	r.outTag(w, openTag, attrs)
	r.cr(w)
}

func (r *Renderer) listExit(w io.Writer, list *ast.List) {
	closeTag := "</list>"
	if list.ListFlags&ast.ListTypeOrdered != 0 {
		//closeTag = "</ol>"
	}
	if list.ListFlags&ast.ListTypeDefinition != 0 {
		//closeTag = "</dl>"
	}
	r.outs(w, closeTag)

	parent := list.Parent
	switch parent.(type) {
	case *ast.ListItem:
		if ast.GetNextNode(list) != nil {
			r.cr(w)
		}
	case *ast.Document, *ast.BlockQuote, *ast.Aside:
		r.cr(w)
	}

	if list.IsFootnotesList {
		r.outs(w, "\n</div>\n")
	}
}

func (r *Renderer) list(w io.Writer, list *ast.List, entering bool) {
	// need to be wrapped in a paragraph, except when we're already in a list.
	_, parentIsList := list.Parent.(*ast.ListItem)
	if entering {
		if !parentIsList {
			r.paragraphEnter(w, &ast.Paragraph{})
		}
		r.listEnter(w, list)
	} else {
		r.listExit(w, list)
		if !parentIsList {
			r.paragraphExit(w, &ast.Paragraph{})
		}
	}
}

func (r *Renderer) listItemEnter(w io.Writer, listItem *ast.ListItem) {
	if listItem.RefLink != nil {
		return
	}

	openTag := "<t>"
	if listItem.ListFlags&ast.ListTypeDefinition != 0 {
		openTag = "<vspace />"
	}
	if listItem.ListFlags&ast.ListTypeTerm != 0 {
		openTag = "<t hangText=\""
	}
	r.outs(w, openTag)
}

func (r *Renderer) listItemExit(w io.Writer, listItem *ast.ListItem) {
	closeTag := "</t>"
	if listItem.ListFlags&ast.ListTypeTerm != 0 {
		closeTag = `">`
	}
	r.outs(w, closeTag)
	r.cr(w)
}

func (r *Renderer) listItem(w io.Writer, listItem *ast.ListItem, entering bool) {
	if entering {
		r.listItemEnter(w, listItem)
	} else {
		r.listItemExit(w, listItem)
	}
}

func (r *Renderer) codeBlock(w io.Writer, codeBlock *ast.CodeBlock) {
	var attrs []string
	attrs = appendLanguageAttr(attrs, codeBlock.Info)
	attrs = append(attrs, html.BlockAttrs(codeBlock)...)

	r.cr(w)
	_, inFigure := codeBlock.Parent.(*ast.CaptionFigure)
	if inFigure {
		r.outTag(w, "<artwork", attrs)
	} else {
		r.outTag(w, "<figure><artwork", attrs)
	}

	if r.opts.Comments != nil {
		r.EscapeHTMLCallouts(w, codeBlock.Literal)
	} else {
		html.EscapeHTML(w, codeBlock.Literal)
	}
	if inFigure {
		r.outs(w, "</artwork>")
	} else {
		r.outs(w, "</artwork></figure>")
	}
	r.cr(w)
}

func (r *Renderer) tableCell(w io.Writer, tableCell *ast.TableCell, entering bool) {
	if !entering {
		r.outOneOf(w, tableCell.IsHeader, "</ttcol>", "</c>")
		r.cr(w)
		return
	}

	// entering
	var attrs []string
	openTag := "<c"
	if tableCell.IsHeader {
		openTag = "<ttcol"
	}
	align := tableCell.Align.String()
	if align != "" {
		attrs = append(attrs, fmt.Sprintf(`align="%s"`, align))
	}
	if ast.GetPrevNode(tableCell) == nil {
		r.cr(w)
	}
	r.outTag(w, openTag, attrs)
}

func (r *Renderer) tableBody(w io.Writer, node *ast.TableBody, entering bool) {
	r.outOneOfCr(w, entering, "", "")
}

func (r *Renderer) htmlSpan(w io.Writer, span *ast.HTMLSpan) {
	if r.opts.Flags&SkipHTML == 0 {
		r.out(w, span.Literal)
	}
}

func (r *Renderer) callout(w io.Writer, callout *ast.Callout) {
	r.outs(w, `<spanx style="emph">`)
	r.out(w, callout.ID)
	r.outs(w, "</spanx>")
}

func (r *Renderer) crossReference(w io.Writer, cr *ast.CrossReference, entering bool) {
	if entering {
		r.outTag(w, "<xref", []string{"target=\"" + string(cr.Destination) + "\""})
		return
	}
	r.outs(w, "</xref>")
}

func (r *Renderer) index(w io.Writer, index *ast.Index) {
	r.outs(w, "<iref")
	r.outs(w, " item=\"")
	html.EscapeHTML(w, index.Item)
	r.outs(w, "\"")
	if index.Primary {
		r.outs(w, ` primary="true"`)
	}
	if len(index.Subitem) != 0 {
		r.outs(w, " subitem=\"")
		html.EscapeHTML(w, index.Subitem)
		r.outs(w, "\"")
	}
	r.outs(w, "></iref>")
}

func (r *Renderer) link(w io.Writer, link *ast.Link, entering bool) {
	r.outs(w, "<eref")
	r.outs(w, " target=\"")
	html.EscapeHTML(w, link.Destination)
	r.outs(w, `"></iref>`) // link.Content/Literal can be used here.
}

func (r *Renderer) image(w io.Writer, node *ast.Image, entering bool) {
	if entering {
		r.imageEnter(w, node)
	} else {
		r.imageExit(w, node)
	}
}

func (r *Renderer) imageEnter(w io.Writer, image *ast.Image) {
	dest := image.Destination
	r.outs(w, `<img src="`)
	html.EscapeHTML(w, dest)
	r.outs(w, `" alt="`)
}

func (r *Renderer) imageExit(w io.Writer, image *ast.Image) {
	if image.Title != nil {
		r.outs(w, `" name="`)
		html.EscapeHTML(w, image.Title)
	}
	r.outs(w, `" />`)
}

func (r *Renderer) code(w io.Writer, node *ast.Code) {
	r.outs(w, `<spanx style="verb">`)
	html.EscapeHTML(w, node.Literal)
	r.outs(w, "</spanx>")
}

func (r *Renderer) mathBlock(w io.Writer, mathBlock *ast.MathBlock) {
	r.outs(w, `<artwork type="math">`+"\n")
	if r.opts.Comments != nil {
		r.EscapeHTMLCallouts(w, mathBlock.Literal)
	} else {
		html.EscapeHTML(w, mathBlock.Literal)
	}
	r.outs(w, `</artwork>`)
	r.cr(w)
}

func (r *Renderer) captionFigure(w io.Writer, captionFigure *ast.CaptionFigure, entering bool) {
	// If the captionFigure has a table as child element *don't* output the figure tags,
	// because 7991 is weird.
	for _, child := range captionFigure.GetChildren() {
		if _, ok := child.(*ast.Table); ok {
			return
		}
	}

	if !entering {
		r.outs(w, "</figure>")
		return
	}

	// anchor attribute needs to be put in the figure, not the artwork.

	r.outs(w, "<figure")
	r.outs(w, ` title="`)
	// Now render the caption and then *remove* it from the tree.
	for _, child := range captionFigure.GetChildren() {
		if caption, ok := child.(*ast.Caption); ok {
			ast.WalkFunc(caption, func(node ast.Node, entering bool) ast.WalkStatus {
				return r.RenderNode(w, node, entering)
			})

			ast.RemoveFromTree(caption)
		}
	}
}

func (r *Renderer) table(w io.Writer, tab *ast.Table, entering bool) {
	if !entering {
		r.outs(w, "</texttable>")
		return
	}

	attrs := html.BlockAttrs(tab)
	// TODO(miek): this definitely needs some helper function(s).
	s := ""
	if len(attrs) > 0 {
		s += " " + strings.Join(attrs, " ")
	}

	r.outs(w, "<texttable")
	r.outs(w, s)
	// Now render the caption if our parent is a ast.CaptionFigure
	// and then *remove* it from the tree.
	captionFigure, ok := tab.Parent.(*ast.CaptionFigure)
	if !ok {
		r.outs(w, `>`)
		return
	}

	r.outs(w, ` title="`)

	for _, child := range captionFigure.GetChildren() {
		if caption, ok := child.(*ast.Caption); ok {
			ast.WalkFunc(caption, func(node ast.Node, entering bool) ast.WalkStatus {
				return r.RenderNode(w, node, entering)
			})

			ast.RemoveFromTree(caption)
			break
		}
	}
}

// RenderNode renders a markdown node to XML.
func (r *Renderer) RenderNode(w io.Writer, node ast.Node, entering bool) ast.WalkStatus {
	if r.opts.RenderNodeHook != nil {
		status, didHandle := r.opts.RenderNodeHook(w, node, entering)
		if didHandle {
			return status
		}
	}
	switch node := node.(type) {
	case *ast.Document:
		// do nothing
	case *mast.Title:
		r.titleBlock(w, node)
		r.title = true
	case *mast.Bibliography:
		r.bibliography(w, node, entering)
	case *mast.BibliographyItem:
		r.bibliographyItem(w, node)
	case *mast.DocumentIndex, *mast.IndexLetter, *mast.IndexItem, *mast.IndexSubItem, *mast.IndexLink:
		// generated by xml2rfc, do nothing.
	case *ast.Text:
		r.text(w, node)
	case *ast.Softbreak:
		r.cr(w)
	case *ast.Hardbreak:
		r.hardBreak(w, node)
	case *ast.Callout:
		r.callout(w, node)
	case *ast.Emph:
		r.outOneOf(w, entering, `<spanx style="emph">`, "</spanx>")
	case *ast.Strong:
		r.strong(w, node, entering)
	case *ast.Del:
		r.outOneOf(w, entering, "<del>", "</del>")
	case *ast.Citation:
		r.citation(w, node, entering)
	case *ast.DocumentMatter:
		if entering {
			r.matter(w, node)
		}
	case *ast.Heading:
		r.heading(w, node, entering)
	case *ast.Paragraph:
		r.paragraph(w, node, entering)
	case *ast.HTMLSpan:
		r.htmlSpan(w, node) // only html comments are allowed.
	case *ast.HTMLBlock:
		// discard; we use these only for <references>.
	case *ast.List:
		r.list(w, node, entering)
	case *ast.ListItem:
		r.listItem(w, node, entering)
	case *ast.CodeBlock:
		r.codeBlock(w, node)
	case *ast.Caption:
		// no tags because we are used in attributes, i.e. title=
		r.outOneOf(w, entering, "", "")
	case *ast.CaptionFigure:
		r.captionFigure(w, node, entering)
	case *ast.Table:
		r.table(w, node, entering)
	case *ast.TableCell:
		r.tableCell(w, node, entering)
	case *ast.TableHeader:
		r.outOneOf(w, entering, "", "")
	case *ast.TableBody:
		r.tableBody(w, node, entering)
	case *ast.TableRow:
		r.outOneOf(w, entering, "", "")
	case *ast.TableFooter:
		r.outOneOf(w, entering, "", "")
	case *ast.BlockQuote:
		tag := tagWithAttributes("<blockquote", html.BlockAttrs(node))
		r.outOneOfCr(w, entering, tag, "</blockquote>")
	case *ast.Aside:
		tag := tagWithAttributes("<aside", html.BlockAttrs(node))
		r.outOneOfCr(w, entering, tag, "</aside>")
	case *ast.CrossReference:
		r.crossReference(w, node, entering)
	case *ast.Index:
		if entering {
			r.index(w, node)
		}
	case *ast.Link:
		r.link(w, node, entering)
	case *ast.Math:
		r.outOneOf(w, entering, `<spanx style="verb">`, "</spanx>")
	case *ast.Image:
		if r.opts.Flags&SkipImages != 0 {
			return ast.SkipChildren
		}
		r.image(w, node, entering)
	case *ast.Code:
		r.code(w, node)
	case *ast.MathBlock:
		r.mathBlock(w, node)
	default:
		panic(fmt.Sprintf("Unknown node %T", node))
	}
	return ast.GoToNext
}

// RenderHeader writes HTML document preamble and TOC if requested.
func (r *Renderer) RenderHeader(w io.Writer, ast ast.Node) {
	if r.opts.Flags&XMLFragment != 0 {
		return
	}

	r.writeDocumentHeader(w)
}

// RenderFooter writes HTML document footer.
func (r *Renderer) RenderFooter(w io.Writer, _ ast.Node) {
	r.sectionClose(w, nil)

	switch r.documentMatter {
	case ast.DocumentMatterFront:
		r.outs(w, "\n</front>\n")
	case ast.DocumentMatterMain:
		r.outs(w, "\n</middle>\n")
	case ast.DocumentMatterBack:
		r.outs(w, "\n</back>\n")
	}

	if r.title {
		io.WriteString(w, "\n</rfc>")
	}
}

func (r *Renderer) writeDocumentHeader(w io.Writer) {
	if r.opts.Flags&XMLFragment != 0 {
		return
	}
	r.outs(w, `<?xml version="1.0" encoding="utf-8"?>`)
	r.cr(w)
	r.outs(w, `<!-- name="GENERATOR" content="github.com/mmarkdown/mmark markdown processor for Go" -->`)
	r.cr(w)
	r.outs(w, `<!DOCTYPE rfc SYSTEM 'rfc2629.dtd' []>`)
	r.cr(w)
}

func tagWithAttributes(name string, attrs []string) string {
	s := name
	if len(attrs) > 0 {
		s += " " + strings.Join(attrs, " ")
	}
	return s + ">"
}
