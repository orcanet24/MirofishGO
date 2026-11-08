// Package webresearch — investigación de contexto actual en internet.
//
// Flujo: búsqueda (DuckDuckGo HTML, sin API key) + descarga de páginas con
// seguimiento de enlaces limitado por profundidad (mismo dominio). El resultado
// ("facts") se inyecta en la ontología y en el informe para fundamentar el
// análisis con información real y actual, no solo opiniones aisladas.
//
// Límites (control de acceso): resultados, páginas y niveles de enlace se
// derivan del nivel de profundidad pedido y se pueden capar por .env.
package webresearch

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/html"
)

// Options — límites de la investigación.
type Options struct {
	MaxResults   int
	MaxPages     int
	MaxLinkDepth int
	Timeout      time.Duration
	UserAgent    string
	Logger       *slog.Logger
}

// DepthPlan mapea el nivel de profundidad (1-5) a (resultados, páginas, niveles
// de enlace). Más profundidad = más fuentes y más recorrido de enlaces.
func DepthPlan(level int) (results, pages, linkDepth int) {
	switch level {
	case 1:
		return 3, 3, 1
	case 2:
		return 5, 5, 1
	case 3:
		return 8, 8, 2
	case 4:
		return 12, 12, 2
	case 5:
		return 15, 15, 3
	default:
		if level < 1 {
			return 3, 3, 1
		}
		return 15, 15, 3
	}
}

// SearchResult — resultado de búsqueda.
type SearchResult struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet"`
}

// Page — página descargada.
type Page struct {
	URL   string `json:"url"`
	Title string `json:"title"`
	Text  string `json:"text"`
	Depth int    `json:"depth"`
}

// Report — resultado completo de la investigación.
type Report struct {
	Query   string        `json:"query"`
	Results []SearchResult `json:"results"`
	Pages   []Page        `json:"pages"`
	Facts   string        `json:"facts"`
}

func (o Options) log() *slog.Logger {
	if o.Logger == nil {
		return slog.Default()
	}
	return o.Logger
}

func (o Options) client() *http.Client {
	timeout := o.Timeout
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	return &http.Client{Timeout: timeout}
}

func (o Options) userAgent() string {
	if o.UserAgent != "" {
		return o.UserAgent
	}
	return "Mozilla/5.0 (compatible; MiroFishResearch/1.0)"
}

// Search busca resultados en DuckDuckGo (versión HTML, sin clave).
func Search(query string, opts Options) ([]SearchResult, error) {
	if strings.TrimSpace(query) == "" {
		return nil, fmt.Errorf("consulta vacía")
	}
	maxResults := opts.MaxResults
	if maxResults <= 0 {
		maxResults, _, _ = DepthPlan(3)
	}
	endpoint := "https://html.duckduckgo.com/html/?q=" + url.QueryEscape(query)
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", opts.userAgent())
	resp, err := opts.client().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("buscador HTTP %d", resp.StatusCode)
	}
	return parseSearchResults(resp.Body, maxResults)
}

// parseSearchResults extrae título/url/snippet del HTML de DuckDuckGo.
func parseSearchResults(r io.Reader, max int) ([]SearchResult, error) {
	doc, err := html.Parse(r)
	if err != nil {
		return nil, err
	}
	var out []SearchResult
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if len(out) >= max {
			return
		}
		if n.Type == html.ElementNode && n.Data == "a" {
			cls := attr(n, "class")
			if strings.Contains(cls, "result__a") {
				res := SearchResult{URL: attr(n, "href"), Title: text(n)}
				// DuckDuckGo envuelve la URL real en ?uddg=...&rut=...: la decodificamos.
				if i := strings.Index(res.URL, "uddg="); i >= 0 {
					raw := res.URL[i+len("uddg="):]
					if j := strings.Index(raw, "&"); j >= 0 {
						raw = raw[:j]
					}
					if dec, err := url.QueryUnescape(raw); err == nil && dec != "" {
						res.URL = dec
					}
				}
				// snippet: hermano con clase result__snippet
				if sib := n.Parent; sib != nil {
					for c := sib.FirstChild; c != nil; c = c.NextSibling {
						if c.Type == html.ElementNode && strings.Contains(attr(c, "class"), "result__snippet") {
							res.Snippet = text(c)
						}
					}
				}
				out = append(out, res)
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	if len(out) == 0 {
		return nil, fmt.Errorf("sin resultados")
	}
	return out, nil
}

// Fetch descarga páginas en BFS desde las semillas: mismo dominio, hasta
// MaxPages y MaxLinkDepth niveles de enlace.
func Fetch(seeds []string, opts Options) ([]Page, error) {
	maxPages := opts.MaxPages
	if maxPages <= 0 {
		_, maxPages, _ = DepthPlan(3)
	}
	maxDepth := opts.MaxLinkDepth
	if maxDepth <= 0 {
		_, _, maxDepth = DepthPlan(3)
	}
	out := []Page{}
	visited := map[string]bool{}
	queue := []struct {
		url   string
		depth int
	}{}
	for _, s := range seeds {
		if u := normalizeURL(s); u != "" && !visited[u] {
			visited[u] = true
			queue = append(queue, struct {
				url   string
				depth int
			}{u, 0})
		}
	}
	for len(queue) > 0 && len(out) < maxPages {
		item := queue[0]
		queue = queue[1:]
		page, links, err := fetchPage(item.url, opts)
		if err != nil {
			opts.log().Warn("webresearch: no se pudo descargar", "url", item.url, "error", err)
			continue
		}
		page.Depth = item.depth
		out = append(out, page)
		if item.depth < maxDepth {
			for _, l := range links {
				u := normalizeURL(l)
				if u == "" || visited[u] {
					continue
				}
				if !sameDomain(u, item.url) {
					continue
				}
				visited[u] = true
				queue = append(queue, struct {
					url   string
					depth int
				}{u, item.depth + 1})
			}
		}
	}
	return out, nil
}

// fetchPage descarga y extrae texto + enlaces de una URL.
func fetchPage(rawURL string, opts Options) (Page, []string, error) {
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return Page{}, nil, err
	}
	req.Header.Set("User-Agent", opts.userAgent())
	resp, err := opts.client().Do(req)
	if err != nil {
		return Page{}, nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Page{}, nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	limited := io.LimitReader(resp.Body, 4<<20) // 4 MB por página
	doc, err := html.Parse(limited)
	if err != nil {
		return Page{}, nil, err
	}
	title, body := extractText(doc)
	links := extractLinks(doc)
	return Page{URL: rawURL, Title: title, Text: body}, links, nil
}

// Research = Search + Fetch + Facts.
func Research(query string, opts Options) (*Report, error) {
	results, err := Search(query, opts)
	if err != nil {
		return nil, err
	}
	pages, _ := Fetch(urlsOf(results), opts)
	r := &Report{Query: query, Results: results, Pages: pages}
	var b strings.Builder
	for i, p := range pages {
		if len([]rune(b.String())) > 24000 {
			break
		}
		fmt.Fprintf(&b, "\n\n=== Fuente %d: %s (%s) ===\n", i+1, p.Title, p.URL)
		txt := p.Text
		if len([]rune(txt)) > 8000 {
			txt = string([]rune(txt)[:8000]) + "…"
		}
		b.WriteString(txt)
	}
	r.Facts = b.String()
	return r, nil
}

func urlsOf(results []SearchResult) []string {
	out := make([]string, 0, len(results))
	for _, r := range results {
		if r.URL != "" {
			out = append(out, r.URL)
		}
	}
	return out
}

// ===== helpers HTML =====

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

// text extrae el texto visible de un nodo.
func text(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(x *html.Node) {
		if x.Type == html.TextNode {
			b.WriteString(x.Data)
		}
		for c := x.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return strings.Join(strings.Fields(b.String()), " ")
}

// extractText devuelve (título, texto) de una página, saltando script/style/nav.
func extractText(doc *html.Node) (string, string) {
	title := ""
	var body strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			switch strings.ToLower(n.Data) {
			case "title":
				title = text(n)
			case "script", "style", "noscript", "svg", "nav", "footer", "header", "form":
				return
			case "p", "div", "section", "article", "li", "h1", "h2", "h3", "h4", "br", "tr":
				body.WriteString("\n")
			}
		}
		if n.Type == html.TextNode {
			body.WriteString(n.Data)
			body.WriteString(" ")
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	cleaned := strings.Join(strings.Fields(body.String()), " ")
	return title, cleaned
}

// extractLinks obtiene los href absolutos de la página.
func extractLinks(doc *html.Node) []string {
	out := []string{}
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && strings.ToLower(n.Data) == "a" {
			if href := attr(n, "href"); href != "" {
				out = append(out, href)
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return out
}

// normalizeURL resuelve URLs relativas y descarta esquemas no http(s).
func normalizeURL(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return ""
	}
	return u.String()
}

func sameDomain(a, b string) bool {
	ua, err1 := url.Parse(a)
	ub, err2 := url.Parse(b)
	if err1 != nil || err2 != nil {
		return false
	}
	return ua.Host == ub.Host
}
