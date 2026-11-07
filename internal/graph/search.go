// Búsqueda léxica del LocalGraphDatabase: FULLTEXT de MySQL (BOOLEAN MODE) más
// fallback léxico con puntuación, espejo de search() y scan_search() del Python.
package graph

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// ftsSafeQuery replica _fts_safe_query: tokens (alfanum/CJK/underscore) con OR.
var reFTSToken = regexp.MustCompile(`[\p{L}\p{N}_]+`)

func ftsSafeQuery(query string) string {
	tokens := reFTSToken.FindAllString(query, -1)
	if len(tokens) == 0 {
		return ""
	}
	if len(tokens) > 20 {
		tokens = tokens[:20]
	}
	quoted := make([]string, len(tokens))
	for i, tk := range tokens {
		quoted[i] = fmt.Sprintf("%q", tk)
	}
	return strings.Join(quoted, " OR ")
}

// hasCJK replica _has_cjk (rango \u4e00-\u9fff).
func hasCJK(query string) bool {
	for _, ch := range query {
		if ch >= 0x4e00 && ch <= 0x9fff {
			return true
		}
	}
	return false
}

// ScanSearch replica scan_search: LIKE/substring con puntuación (sirve CJK).
func (d *Database) ScanSearch(graphID, query string, limit int) SearchResult {
	q := strings.TrimSpace(query)
	if q == "" {
		return SearchResult{Edges: []LocalEdge{}, Nodes: []LocalNode{}}
	}

	// keywords: tokens de >=2 chars; CJK añade la frase completa
	keywords := []string{}
	for _, w := range keywordSplit(q) {
		lw := strings.ToLower(strings.TrimSpace(w))
		if len([]rune(lw)) >= 2 {
			keywords = append(keywords, lw)
		}
	}
	lq := strings.ToLower(q)
	if hasCJK(q) && len([]rune(q)) >= 2 {
		found := false
		for _, kw := range keywords {
			if kw == lq {
				found = true
				break
			}
		}
		if !found {
			keywords = append(keywords, lq)
		}
	}

	result := SearchResult{Edges: []LocalEdge{}, Nodes: []LocalNode{}}
	if len(keywords) == 0 {
		return result
	}

	nodeKeywordScore := func(text string) int { return keywordScore(text, keywords, lq) }

	edges, err := d.GetAllEdges(graphID)
	if err == nil {
		hits := []scoredEdge{}
		for _, e := range edges {
			text := strings.ToLower(e.Fact + " " + e.Name)
			if s := nodeKeywordScore(text); s > 0 {
				hits = append(hits, scoredEdge{s, e})
			}
		}
		sortEdgeHits(hits)
		for i, h := range hits {
			if i >= limit {
				break
			}
			result.Edges = append(result.Edges, h.edge)
		}
	}

	nodes, err := d.GetAllNodes(graphID)
	if err == nil {
		nhits := []scoredNode{}
		for _, n := range nodes {
			text := strings.ToLower(n.Name + " " + n.Summary)
			if s := nodeKeywordScore(text); s > 0 {
				nhits = append(nhits, scoredNode{s, n})
			}
		}
		sortNodeHits(nhits)
		for i, h := range nhits {
			if i >= limit {
				break
			}
			result.Nodes = append(result.Nodes, h.node)
		}
	}
	return result
}

// keywordSplit replica re.split(r'[\s,，;；]+', q) — lista no vacía de tokens.
func keywordSplit(q string) []string {
	seps := func(r rune) bool {
		if r > 0xff {
			return r == '，' || r == '；'
		}
		return r == ',' || r == ';' || r == ' ' || r == '\t' || r == '\n' || r == '\r'
	}
	tokens := strings.FieldsFunc(q, seps)
	if tokens == nil {
		return []string{}
	}
	return tokens
}

// keywordScore replica: por keyword presente 10, +5 si kw == query completa.
func keywordScore(text string, keywords []string, fullQueryLower string) int {
	score := 0
	for _, kw := range keywords {
		if strings.Contains(text, kw) {
			score += 10
			if kw == fullQueryLower {
				score += 5
			}
		}
	}
	return score
}

// sortEdgeHits/sortNodeHits: score desc, created_at desc (segundo criterio).
func sortEdgeHits(hits []scoredEdge) {
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].score != hits[j].score {
			return hits[i].score > hits[j].score
		}
		return deref(hits[i].edge.CreatedAt) > deref(hits[j].edge.CreatedAt)
	})
}

func sortNodeHits(hits []scoredNode) {
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].score != hits[j].score {
			return hits[i].score > hits[j].score
		}
		return deref(hits[i].node.CreatedAt) > deref(hits[j].node.CreatedAt)
	})
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// clampLimit replica max(1, min(limit, MAX_SEARCH_LIMIT)).
func clampLimit(limit int) int {
	if limit < 1 {
		return 1
	}
	if limit > maxSearchLimit {
		return maxSearchLimit
	}
	return limit
}

type scoredEdge struct {
	score int
	edge  LocalEdge
}

type scoredNode struct {
	score int
	node  LocalNode
}

// Search replica search(): FTS5 si hay query segura y no CJK; fallback scan.
func (d *Database) Search(graphID, query string, limit int, scope string) SearchResult {
	limit = clampLimit(limit)
	_ = limit
	ftsQ := ftsSafeQuery(query)
	if ftsQ == "" || hasCJK(query) {
		return d.ScanSearch(graphID, query, clampLimit(limit))
	}

	result := SearchResult{Edges: []LocalEdge{}, Nodes: []LocalNode{}}
	queryEdges := func() ([]LocalEdge, error) {
		rows, err := d.db.Query(
			`SELECT uuid, graph_id, name, fact, source_node_uuid, target_node_uuid,
				attributes, created_at, valid_at, invalid_at, expired_at
			 FROM edges
			 WHERE graph_id = ? AND MATCH(fact, name) AGAINST (? IN BOOLEAN MODE)
			 ORDER BY MATCH(fact, name) AGAINST (? IN BOOLEAN MODE) DESC
			 LIMIT ?`,
			graphID, ftsQ, ftsQ, clampLimit(limit),
		)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		return collectEdgeRows(rows)
	}
	queryNodes := func() ([]LocalNode, error) {
		rows, err := d.db.Query(
			`SELECT uuid, graph_id, name, labels, summary, attributes, created_at
			 FROM nodes
			 WHERE graph_id = ? AND MATCH(name, summary) AGAINST (? IN BOOLEAN MODE)
			 ORDER BY MATCH(name, summary) AGAINST (? IN BOOLEAN MODE) DESC
			 LIMIT ?`,
			graphID, ftsQ, ftsQ, clampLimit(limit),
		)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		var out []LocalNode
		for rows.Next() {
			n, err := scanNodeRow(rows.Scan)
			if err != nil {
				return nil, err
			}
			out = append(out, *n)
		}
		return out, rows.Err()
	}

	// FULLTEXT ignora stopwords y tokens por debajo de innodb_ft_min_token_size;
	// si un alcance no devuelve nada se completa con el escaneo léxico. El
	// fallback es por alcance: con "both", un lado con resultados no puede
	// ocultar que el otro está vacío (el bug que dejaba sin hechos al agente).
	var scanned *SearchResult
	scanOnce := func() *SearchResult {
		if scanned == nil {
			s := d.ScanSearch(graphID, query, clampLimit(limit))
			scanned = &s
		}
		return scanned
	}

	if scope == "edges" || scope == "both" {
		edges, err := queryEdges()
		if err != nil || len(edges) == 0 {
			result.Edges = orEmptyEdges(scanOnce().Edges)
		} else {
			result.Edges = orEmptyEdges(edges)
		}
	}
	if scope == "nodes" || scope == "both" {
		nodes, err := queryNodes()
		if err != nil || len(nodes) == 0 {
			result.Nodes = orEmptyNodes(scanOnce().Nodes)
		} else {
			result.Nodes = orEmptyNodes(nodes)
		}
	}

	if len(result.Edges) == 0 && len(result.Nodes) == 0 {
		return *scanOnce()
	}
	return result
}

func orEmptyEdges(e []LocalEdge) []LocalEdge {
	if e == nil {
		return []LocalEdge{}
	}
	return e
}

func orEmptyNodes(n []LocalNode) []LocalNode {
	if n == nil {
		return []LocalNode{}
	}
	return n
}
