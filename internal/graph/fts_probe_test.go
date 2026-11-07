// Verificación explícita del índice FULLTEXT de MySQL: el fallback léxico de
// los tests de búsqueda podría encubrir un índice vacío o roto.
package graph

import "testing"

func TestMySQLFullTextSearch(t *testing.T) {
	c := testClient(t)
	alice, err := c.DB.AddNode("g1", "Alice Terminal", []string{"Entity"}, "resumen con datos", nil)
	if err != nil {
		t.Fatal(err)
	}
	cafe, err := c.DB.AddNode("g1", "Cafe Mio", []string{"Entity"}, "cafetería del centro", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.DB.AddEdge("g1", alice, cafe, "WORKS_AT", "Alice Terminal trabaja en Cafe Mio", nil); err != nil {
		t.Fatal(err)
	}

	// La consulta directa prueba que el índice FULLTEXT existe y está poblado;
	// si esto fallara, Search() estaría pasando solo por el escaneo léxico.
	var uuid string
	err = c.DB.db.QueryRow(
		"SELECT uuid FROM nodes WHERE graph_id = ? AND MATCH(name, summary) AGAINST (? IN BOOLEAN MODE) LIMIT 1",
		"g1", `"Terminal"`,
	).Scan(&uuid)
	if err != nil {
		t.Fatalf("FULLTEXT sin índice o sin sincronizar: %v", err)
	}

	// scope "both": nodos y aristas. Antes, un lado con resultados impedía el
	// fallback y las aristas podían desaparecer.
	r := c.DB.Search("g1", "Cafe", 5, "both")
	if len(r.Nodes) == 0 || len(r.Edges) == 0 {
		t.Fatalf("búsqueda both incompleta: nodes=%d edges=%d", len(r.Nodes), len(r.Edges))
	}

	// Token de 2 letras: cae por debajo de innodb_ft_min_token_size y debe
	// resolverse con el escaneo, también en ambos alcances.
	r = c.DB.Search("g1", "Al", 5, "both")
	if len(r.Nodes) == 0 || len(r.Edges) == 0 {
		t.Fatalf("fallback léxico incompleto: nodes=%d edges=%d", len(r.Nodes), len(r.Edges))
	}
}
