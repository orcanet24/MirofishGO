// Tests de la rebanada OASIS (clock, entorno, agente y Twitter) sobre MySQL.
package oasis

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"

	"mirofish/internal/llm"
	"mirofish/internal/testdb"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	db, err := sql.Open("mysql", testdb.New(t))
	if err != nil {
		t.Fatalf("open mysql: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	store, err := NewStore(db)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return store
}

func newTestTwitter(t *testing.T) *Twitter {
	t.Helper()
	tw := NewTwitter("sim_test", newTestStore(t), NewClock(1))
	for _, profile := range []Profile{
		{UserID: 0, UserName: "ana", Name: "Ana", Bio: "Contadora", Persona: "Analítica"},
		{UserID: 1, UserName: "beto", Name: "Beto", Bio: "Comerciante", Persona: "Impulsivo"},
	} {
		if _, err := tw.SignUp(profile.UserID, profile); err != nil {
			t.Fatalf("SignUp(%d): %v", profile.UserID, err)
		}
	}
	return tw
}

func TestClock(t *testing.T) {
	clock := NewClock(60)
	if clock.GetTimeStep() != "0" {
		t.Fatalf("time_step inicial = %q", clock.GetTimeStep())
	}
	clock.Advance()
	if clock.GetTimeStep() != "1" {
		t.Fatalf("time_step tras Advance = %q", clock.GetTimeStep())
	}
	start := time.Now()
	adjusted := clock.TimeTransfer(start.Add(time.Minute), start)
	if got := adjusted.Sub(start); got != time.Hour {
		t.Fatalf("k=60 debería multiplicar 1m por 60 (1h), got %v", got)
	}
}

func TestLoadSimulationConfigDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "simulation_config.json")
	raw := `{"simulation_id":"sim_x","time_config":{"total_simulation_hours":0},"agent_configs":[]}`
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadSimulationConfig(path)
	if err != nil {
		t.Fatalf("LoadSimulationConfig: %v", err)
	}
	if cfg.TimeConfig.TotalSimulationHours != 72 || cfg.TimeConfig.MinutesPerRound != 60 {
		t.Errorf("defaults de tiempo = %+v", cfg.TimeConfig)
	}
	if cfg.TimeConfig.AgentsPerHourMin != 5 || cfg.TimeConfig.AgentsPerHourMax < 5 {
		t.Errorf("defaults de agentes = %+v", cfg.TimeConfig)
	}
	if cfg.TotalRounds(10) != 10 {
		t.Errorf("max_rounds debe truncar, got %d", cfg.TotalRounds(10))
	}
}

func TestLoadTwitterProfiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "twitter_profiles.csv")
	raw := "user_id,name,username,user_char,description\n" +
		"0,Ana,ana,Persona de Ana,Bio de Ana\n" +
		"1,Beto,beto,Persona de Beto,Bio de Beto\n"
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	profiles, err := LoadTwitterProfiles(path)
	if err != nil {
		t.Fatalf("LoadTwitterProfiles: %v", err)
	}
	if len(profiles) != 2 || profiles[0].Name != "Ana" || profiles[1].UserName != "beto" {
		t.Fatalf("profiles = %+v", profiles)
	}
}

func TestTwitterInteractionRules(t *testing.T) {
	tw := newTestTwitter(t)

	created, err := tw.CreatePost(0, "Subimos los precios un 15%")
	if err != nil {
		t.Fatalf("CreatePost: %v", err)
	}
	postID := created["post_id"].(int64)

	if _, err := tw.LikePost(1, postID); err != nil {
		t.Fatalf("LikePost: %v", err)
	}
	if _, err := tw.LikePost(1, postID); err == nil {
		t.Fatal("el segundo like debía fallar")
	}
	post, _ := tw.Store.GetPost(tw.SimulationID, postID)
	if post.NumLikes != 1 {
		t.Errorf("num_likes = %d", post.NumLikes)
	}

	if _, err := tw.Follow(1, 0); err != nil {
		t.Fatalf("Follow: %v", err)
	}
	if _, err := tw.Follow(1, 0); err == nil {
		t.Fatal("el segundo follow debía fallar")
	}
	follower, _ := tw.Store.UserByAgent(tw.SimulationID, 1)
	followee, _ := tw.Store.UserByAgent(tw.SimulationID, 0)
	if follower.NumFollowings != 1 || followee.NumFollowers != 1 {
		t.Errorf("contadores follow = %d/%d", follower.NumFollowings, followee.NumFollowers)
	}

	reposted, err := tw.Repost(1, postID)
	if err != nil {
		t.Fatalf("Repost: %v", err)
	}
	repostID := reposted["post_id"].(int64)
	repost, _ := tw.Store.GetPost(tw.SimulationID, repostID)
	if repost.OriginalPostID == nil || *repost.OriginalPostID != postID {
		t.Errorf("repost no apunta al original: %+v", repost)
	}
	post, _ = tw.Store.GetPost(tw.SimulationID, postID)
	if post.NumShares != 1 {
		t.Errorf("num_shares tras repost = %d", post.NumShares)
	}

	if _, err := tw.QuotePost(1, repostID, "Totalmente de acuerdo"); err != nil {
		t.Fatalf("QuotePost: %v", err)
	}
	post, _ = tw.Store.GetPost(tw.SimulationID, postID)
	if post.NumShares != 2 {
		t.Errorf("num_shares tras quote = %d", post.NumShares)
	}

	// Comentar un repost debe colgar el comentario del post raíz.
	if _, err := tw.CreateComment(1, repostID, "¿Y el impacto en el café?"); err != nil {
		t.Fatalf("CreateComment: %v", err)
	}
	comments, err := tw.Store.ListComments(tw.SimulationID, &postID, 50, 0)
	if err != nil {
		t.Fatalf("ListComments: %v", err)
	}
	if len(comments) != 1 || comments[0].PostID != postID || comments[0].Content == "" {
		t.Fatalf("comments = %+v", comments)
	}
	if _, err := tw.CreateComment(1, postID, "   "); err == nil {
		t.Fatal("un comentario vacío debía fallar")
	}

	if err := tw.UpdateRecTable(); err != nil {
		t.Fatalf("UpdateRecTable: %v", err)
	}
	refreshed, err := tw.Refresh(1)
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if refreshed["success"] != true {
		t.Fatalf("refresh = %+v", refreshed)
	}
	if posts, ok := refreshed["posts"].([]map[string]any); !ok || len(posts) == 0 {
		t.Fatalf("refresh sin posts = %+v", refreshed)
	}
}

func fakeActionLLM(t *testing.T, content string) *llm.Client {
	t.Helper()
	inner, _ := json.Marshal(map[string]any{
		"action": "create_post",
		"args":   map[string]any{"content": content},
	})
	quoted, _ := json.Marshal(string(inner))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(fmt.Sprintf(
			`{"model":"fake","choices":[{"finish_reason":"stop","message":{"role":"assistant","content":%s}}]}`,
			quoted)))
	}))
	t.Cleanup(srv.Close)
	return &llm.Client{APIKey: "k", BaseURL: srv.URL, Model: "fake", HTTP: srv.Client()}
}

func testSimulationConfig() *SimulationConfig {
	return &SimulationConfig{
		SimulationID: "sim_test",
		TimeConfig: TimeConfig{
			TotalSimulationHours:      1,
			MinutesPerRound:           60,
			AgentsPerHourMin:          2,
			AgentsPerHourMax:          2,
			PeakHours:                 []int{0},
			OffPeakHours:              []int{1, 2, 3},
			PeakActivityMultiplier:    1.5,
			OffPeakActivityMultiplier: 0.3,
		},
		AgentConfigs: []AgentConfig{
			{AgentID: 0, ActivityLevel: 1, ActiveHours: []int{0}},
			{AgentID: 1, ActivityLevel: 1, ActiveHours: []int{0}},
		},
		EventConfig: EventConfig{InitialPosts: []InitialPost{
			{Content: "Bienvenidos a la simulación", PosterAgentID: 0},
		}},
	}
}

func TestRunnerCompletesAndPersists(t *testing.T) {
	store := newTestStore(t)
	cfg := testSimulationConfig()
	runner := NewRunner(cfg, store, fakeActionLLM(t, "Opino que el precio es justo"), nil)
	profiles := []Profile{
		{UserID: 0, UserName: "ana", Name: "Ana", Bio: "Contadora", Persona: "Analítica"},
		{UserID: 1, UserName: "beto", Name: "Beto", Bio: "Comerciante", Persona: "Impulsivo"},
	}

	go runner.Run(profiles, 1)
	select {
	case <-runner.Done():
	case <-time.After(15 * time.Second):
		t.Fatal("el runner no terminó")
	}

	state := runner.State()
	if state.RunnerStatus != "completed" || state.CurrentRound != 1 || state.TotalRounds != 1 {
		t.Fatalf("state = %+v", state)
	}
	if state.TwitterActionsCount < 2 {
		t.Errorf("acciones = %d (esperaba post inicial + al menos una acción)", state.TwitterActionsCount)
	}
	posts, err := store.AllPosts(cfg.SimulationID)
	if err != nil {
		t.Fatal(err)
	}
	if len(posts) < 2 {
		t.Errorf("posts = %d", len(posts))
	}
	actions, err := store.ListActions(cfg.SimulationID, 100, 0, "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(actions) < 2 {
		t.Errorf("actions persistidas = %d", len(actions))
	}

	persisted, err := store.LoadRunState(cfg.SimulationID)
	if err != nil || persisted == nil || persisted.RunnerStatus != "completed" {
		t.Fatalf("run_state persistido = %+v, err=%v", persisted, err)
	}
}

func TestRunnerStopsBeforeFirstRound(t *testing.T) {
	store := newTestStore(t)
	cfg := testSimulationConfig()
	runner := NewRunner(cfg, store, fakeActionLLM(t, "hola"), nil)
	runner.Stop()

	go runner.Run([]Profile{{UserID: 0, UserName: "ana", Name: "Ana"}}, 5)
	select {
	case <-runner.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("el runner no terminó tras Stop")
	}
	state := runner.State()
	if state.RunnerStatus != "stopped" {
		t.Fatalf("runner_status = %q (error=%v)", state.RunnerStatus, state.Error)
	}
	if state.CurrentRound != 0 {
		t.Errorf("current_round = %d", state.CurrentRound)
	}
}

func TestEngineRequiresForceToRepeat(t *testing.T) {
	store := newTestStore(t)
	cfg := testSimulationConfig()
	client := fakeActionLLM(t, "Segunda corrida")
	engine := NewEngine(store, WithEngineLLMFactory(func() (*llm.Client, error) { return client, nil }))
	profiles := []Profile{{UserID: 0, UserName: "ana", Name: "Ana"}}

	if _, err := engine.Start(cfg, profiles, 1, false); err != nil {
		t.Fatalf("primer Start: %v", err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for engine.IsRunning(cfg.SimulationID) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if engine.IsRunning(cfg.SimulationID) {
		t.Fatal("la primera corrida no terminó")
	}

	if _, err := engine.Start(cfg, profiles, 1, false); err == nil {
		t.Fatal("repetir sin force debía fallar")
	}
	if _, err := engine.Start(cfg, profiles, 1, true); err != nil {
		t.Fatalf("Start con force: %v", err)
	}
	for engine.IsRunning(cfg.SimulationID) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	run, err := engine.State(cfg.SimulationID)
	if err != nil || run == nil || run.RunnerStatus != "completed" {
		t.Fatalf("run final = %+v, err=%v", run, err)
	}
}

func TestParseActionJSON(t *testing.T) {
	action, err := ParseActionJSON("```json\n{\"action\":\"like_post\",\"args\":{\"post_id\":3}}\n```")
	if err != nil {
		t.Fatalf("ParseActionJSON: %v", err)
	}
	if action.Type != ActionLikePost || strings.TrimSpace(fmt.Sprint(action.Args["post_id"])) != "3" {
		t.Fatalf("action = %+v", action)
	}
	comment, err := ParseActionJSON(`{"action":"create_comment","args":{"post_id":3,"content":"hola"}}`)
	if err != nil || comment.Type != ActionCreateComment {
		t.Fatalf("create_comment = %+v, err=%v", comment, err)
	}
	if _, err := ParseActionJSON(`{"action":"like_comment","args":{}}`); err == nil {
		t.Fatal("una acción fuera de la rebanada debía rechazarse")
	}
}
