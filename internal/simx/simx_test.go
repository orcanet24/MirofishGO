// Tests del núcleo de simulación: lector de entidades, perfiles por reglas,
// configuración con LLM falsa y ciclo de preparación completo del manager.
package simx

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"mirofish/internal/graph"
	"mirofish/internal/llm"
	"mirofish/internal/testdb"
)

func newTestGraph(t *testing.T) *graph.Client {
	t.Helper()
	client, err := graph.NewClient(testdb.New(t))
	if err != nil {
		t.Fatalf("graph client: %v", err)
	}
	t.Cleanup(func() { _ = client.DB.Close() })
	return client
}

func seedGraph(t *testing.T, client *graph.Client) {
	t.Helper()
	if _, err := client.CreateGraph("g1", "Test", ""); err != nil {
		t.Fatal(err)
	}
	ana, err := client.DB.AddNode("g1", "Ana", []string{"Entity", "Person"}, "Contadora pública", map[string]any{"specialization": "ISLR"})
	if err != nil {
		t.Fatal(err)
	}
	cafe, err := client.DB.AddNode("g1", "Cafe Mio", []string{"Entity", "Organization"}, "Cafetería", nil)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := client.DB.AddNode("g1", "Lugar sin tipo", []string{"Entity"}, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = plain
	if _, err := client.DB.AddEdge("g1", ana, cafe, "WORKS_FOR", "Ana trabaja en Cafe Mio", nil); err != nil {
		t.Fatal(err)
	}
}

func TestEntityReaderFilter(t *testing.T) {
	client := newTestGraph(t)
	seedGraph(t, client)

	reader := NewEntityReader(client)
	filtered, err := reader.FilterDefinedEntities("g1", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if filtered.TotalCount != 3 {
		t.Errorf("total = %d, want 3", filtered.TotalCount)
	}
	if filtered.FilteredCount != 2 {
		t.Fatalf("filtered = %d, want 2", filtered.FilteredCount)
	}
	types := map[string]bool{}
	for _, et := range filtered.EntityTypes {
		types[et] = true
	}
	if !types["Person"] || !types["Organization"] {
		t.Errorf("types = %v", filtered.EntityTypes)
	}
	for _, e := range filtered.Entities {
		if len(e.RelatedEdges) == 0 {
			t.Errorf("entidad %s sin edges relacionadas", e.Name)
		}
	}
}

func TestEntityReaderByType(t *testing.T) {
	client := newTestGraph(t)
	seedGraph(t, client)

	reader := NewEntityReader(client)
	people, err := reader.GetEntitiesByType("g1", "Person", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(people) != 1 || people[0].Name != "Ana" {
		t.Fatalf("people = %+v", people)
	}
}

// fakeConfigProvider devuelve una configuración válida para todos los pasos.
func fakeConfigProvider(t *testing.T) *httptest.Server {
	t.Helper()
	inner := `{
  "total_simulation_hours": 48, "minutes_per_round": 60,
  "agents_per_hour_min": 2, "agents_per_hour_max": 6,
  "peak_hours": [19,20,21,22], "off_peak_hours": [0,1,2,3,4,5],
  "morning_hours": [6,7,8], "work_hours": [9,10,11,12,13,14,15,16,17,18],
  "reasoning": "ok",
  "hot_topics": ["precios"], "narrative_direction": "descubrimiento",
  "initial_posts": [{"content": "Que opinan del nuevo ERP?", "poster_type": "Person"}],
  "agent_configs": []
}`
	inner = string(mustJSONString(inner))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(fmt.Sprintf(`{"model":"fake","choices":[{"finish_reason":"stop","message":{"role":"assistant","content":%s}}]}`, inner)))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func mustJSONString(s string) []byte {
	b, _ := json.Marshal(s)
	return b
}

func TestPrepareSimulationEndToEnd(t *testing.T) {
	client := newTestGraph(t)
	seedGraph(t, client)
	srv := fakeConfigProvider(t)

	manager := NewManager(filepath.Join(t.TempDir(), "sims"))
	manager.ConfigGeneratorFactory = func() *ConfigGenerator {
		g := NewConfigGenerator()
		g.LLMFactory = func() (*llm.Client, error) {
			return &llm.Client{APIKey: "k", BaseURL: srv.URL, Model: "fake", HTTP: srv.Client()}, nil
		}
		return g
	}

	state := manager.CreateSimulation("proj_1", "g1", true, true, nil)
	if state.Status != SimCreated {
		t.Fatalf("status = %s", state.Status)
	}

	prepared, err := manager.PrepareSimulation(
		client,
		state.SimulationID,
		"Simular la discusion sobre el ERP.",
		"Texto del documento de prueba.",
		nil,
		false, // perfiles por reglas (sin LLM)
		func(stage string, progress int, message string, extra map[string]any) {},
		2,
		"es",
		"general",
	)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if prepared.Status != SimReady {
		t.Fatalf("status = %s (error=%v)", prepared.Status, prepared.Error)
	}
	if prepared.EntitiesCount != 2 {
		t.Errorf("entities = %d", prepared.EntitiesCount)
	}
	if prepared.ProfilesCount != 2 {
		t.Errorf("profiles = %d", prepared.ProfilesCount)
	}
	if !prepared.ConfigGenerated {
		t.Error("config no generada")
	}
	if len(prepared.EntityTypes) == 0 {
		t.Error("sin entity_types")
	}

	// artefactos en disco
	dir := filepath.Join(manager.DataDir, state.SimulationID)
	for _, name := range []string{"state.json", "reddit_profiles.json", "twitter_profiles.csv", "simulation_config.json"} {
		if _, err := os.ReadFile(filepath.Join(dir, name)); err != nil {
			t.Errorf("falta %s: %v", name, err)
		}
	}

	// perfiles reddit con campos obligatorios de OASIS
	profiles, err := manager.GetProfiles(state.SimulationID, "reddit")
	if err != nil {
		t.Fatal(err)
	}
	if len(profiles) != 2 {
		t.Fatalf("profiles = %d", len(profiles))
	}
	for _, p := range profiles {
		for _, key := range []string{"user_id", "username", "name", "bio", "persona", "mbti", "gender", "age", "country"} {
			if _, ok := p[key]; !ok {
				t.Errorf("profile sin %q", key)
			}
		}
	}

	// config cargada
	cfg := manager.GetSimulationConfig(state.SimulationID)
	if cfg == nil {
		t.Fatal("config nil")
	}
	if _, ok := cfg["time_config"]; !ok {
		t.Error("config sin time_config")
	}

	// lista y recarga desde disco
	list := manager.ListSimulations("")
	if len(list) != 1 {
		t.Fatalf("list = %d", len(list))
	}
	reloaded := manager.LoadSimulationState(state.SimulationID)
	if reloaded == nil || reloaded.Status != SimReady {
		t.Fatalf("reloaded = %+v", reloaded)
	}
}

func TestIPCClientServer(t *testing.T) {
	dir := t.TempDir()
	client := NewIPCClient(dir)
	server := NewIPCServer(dir)
	server.Start()
	defer server.Stop()

	go func() {
		for i := 0; i < 100; i++ {
			if cmd := server.PollCommands(); cmd != nil {
				server.SendSuccess(cmd.CommandID, map[string]any{"echo": cmd.Args["prompt"]})
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	}()

	resp, err := client.SendInterview(1, "hola", "", 2.0)
	if err != nil {
		t.Fatalf("send interview: %v", err)
	}
	if resp.Status != StatusCompleted {
		t.Fatalf("status = %s", resp.Status)
	}
	if resp.Result["echo"] != "hola" {
		t.Errorf("result = %v", resp.Result)
	}
}

func TestIPCEnvAlive(t *testing.T) {
	dir := t.TempDir()
	client := NewIPCClient(dir)
	server := NewIPCServer(dir)
	if client.CheckEnvAlive() {
		t.Error("env no debería estar vivo")
	}
	server.Start()
	if !client.CheckEnvAlive() {
		t.Error("env debería estar vivo")
	}
	server.Stop()
	if client.CheckEnvAlive() {
		t.Error("env debería estar detenido")
	}
}
