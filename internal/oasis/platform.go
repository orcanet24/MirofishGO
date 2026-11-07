// Plataforma Twitter de OASIS sobre MySQL.
//
// Mantiene las reglas del Python (repost/quote apuntan al post raíz, no se
// puede repetir like/follow, refresh mezcla recomendados y seguidos) pero la
// recomendación es determinista por likes/recencia en vez de embeddings: el
// modelo twhin-bert no existe en Go. Es una diferencia consciente y testeada.
package oasis

import (
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"sync"
	"time"
)

// ErrNotFound se devuelve cuando el post o usuario no existe.
var ErrNotFound = errors.New("not found")

// Twitter es la plataforma social de una simulación.
type Twitter struct {
	SimulationID string
	Store        *Store
	Clock        *Clock

	RefreshRecPostCount int
	FollowingPostCount  int
	MaxRecPostLen       int
	TrendNumDays        int
	TrendTopK           int

	mu  sync.Mutex
	rng *rand.Rand
}

// NewTwitter crea la plataforma con los mismos defaults de OasisEnv para
// Twitter: refresh 2, following 3, buffer 2.
func NewTwitter(simulationID string, store *Store, clock *Clock) *Twitter {
	if clock == nil {
		clock = NewClock(1)
	}
	return &Twitter{
		SimulationID:        simulationID,
		Store:               store,
		Clock:               clock,
		RefreshRecPostCount: 2,
		FollowingPostCount:  3,
		MaxRecPostLen:       2,
		TrendNumDays:        7,
		TrendTopK:           1,
		rng:                 rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

func (t *Twitter) now() string { return t.Clock.GetTimeStep() }

func (t *Twitter) shuffle(ids []int64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.rng.Shuffle(len(ids), func(i, j int) { ids[i], ids[j] = ids[j], ids[i] })
}

// SignUp registra el agente con el perfil del CSV.
func (t *Twitter) SignUp(agentID int, profile Profile) (*User, error) {
	userName := strings.TrimSpace(profile.UserName)
	if userName == "" {
		userName = fmt.Sprintf("agent_%d", agentID)
	}
	user, err := t.Store.SignUp(t.SimulationID, agentID, userName, profile.Name,
		profile.Bio, profile.Persona, t.now())
	if err != nil {
		return nil, err
	}
	if err := t.Store.Trace(t.SimulationID, user.ID, string(ActionSignUp), map[string]any{
		"name": profile.Name, "user_name": userName, "bio": profile.Bio,
	}, t.now()); err != nil {
		return nil, err
	}
	return user, nil
}

// userForAgent resuelve el user_id de un agente registrado.
func (t *Twitter) userForAgent(agentID int) (*User, error) {
	user, err := t.Store.UserByAgent(t.SimulationID, agentID)
	if err != nil {
		return nil, err
	}
	if user == nil {
		return nil, fmt.Errorf("agente %d sin registro en la simulación", agentID)
	}
	return user, nil
}

// CreatePost publica contenido original.
func (t *Twitter) CreatePost(agentID int, content string) (map[string]any, error) {
	if strings.TrimSpace(content) == "" {
		return nil, fmt.Errorf("el contenido del post no puede estar vacío")
	}
	user, err := t.userForAgent(agentID)
	if err != nil {
		return nil, err
	}
	postID, err := t.Store.CreatePost(t.SimulationID, user.ID, content, nil, "", t.now())
	if err != nil {
		return nil, err
	}
	if err := t.Store.Trace(t.SimulationID, user.ID, string(ActionCreatePost),
		map[string]any{"content": content, "post_id": postID}, t.now()); err != nil {
		return nil, err
	}
	return map[string]any{"success": true, "post_id": postID}, nil
}

// rootPost resuelve el post raíz de un repost; para posts comunes o quote
// devuelve el propio id.
func (t *Twitter) rootPost(postID int64) (*Post, int64, error) {
	post, err := t.Store.GetPost(t.SimulationID, postID)
	if err != nil {
		return nil, 0, err
	}
	if post == nil {
		return nil, 0, ErrNotFound
	}
	if post.OriginalPostID != nil {
		return post, *post.OriginalPostID, nil
	}
	return post, post.PostID, nil
}

// Repost replica Platform.repost: crea un post sin contenido apuntando al raíz
// y suma num_shares. Un usuario no puede repostear dos veces lo mismo.
func (t *Twitter) Repost(agentID int, postID int64) (map[string]any, error) {
	user, err := t.userForAgent(agentID)
	if err != nil {
		return nil, err
	}
	_, rootID, err := t.rootPost(postID)
	if err != nil {
		return nil, err
	}
	exists, err := t.Store.exists(
		"SELECT 1 FROM oasis_posts WHERE simulation_id = ? AND original_post_id = ? AND user_id = ?",
		t.SimulationID, rootID, user.ID,
	)
	if err != nil {
		return nil, err
	}
	if exists {
		return nil, fmt.Errorf("Repost record already exists.")
	}
	newID, err := t.Store.CreatePost(t.SimulationID, user.ID, "", &rootID, "", t.now())
	if err != nil {
		return nil, err
	}
	if err := t.Store.AddCounter(t.SimulationID, rootID, "num_shares", 1); err != nil {
		return nil, err
	}
	if err := t.Store.Trace(t.SimulationID, user.ID, string(ActionRepost),
		map[string]any{"reposted_id": postID, "new_post_id": newID}, t.now()); err != nil {
		return nil, err
	}
	return map[string]any{"success": true, "post_id": newID}, nil
}

// QuotePost replica Platform.quote_post: conserva el contenido original y
// añade quote_content; apunta al raíz y suma num_shares.
func (t *Twitter) QuotePost(agentID int, postID int64, quoteContent string) (map[string]any, error) {
	if strings.TrimSpace(quoteContent) == "" {
		return nil, fmt.Errorf("quote_content no puede estar vacío")
	}
	user, err := t.userForAgent(agentID)
	if err != nil {
		return nil, err
	}
	_, rootID, err := t.rootPost(postID)
	if err != nil {
		return nil, err
	}
	// Al citar un repost hay que tomar el contenido del post raíz, no del
	// repost (que va vacío): el Python resuelve el raíz antes de citar.
	root, err := t.Store.GetPost(t.SimulationID, rootID)
	if err != nil {
		return nil, err
	}
	if root == nil || root.Content == "" {
		return nil, fmt.Errorf("no se puede citar un post sin contenido")
	}
	newID, err := t.Store.CreatePost(t.SimulationID, user.ID, root.Content, &rootID, quoteContent, t.now())
	if err != nil {
		return nil, err
	}
	if err := t.Store.AddCounter(t.SimulationID, rootID, "num_shares", 1); err != nil {
		return nil, err
	}
	if err := t.Store.Trace(t.SimulationID, user.ID, string(ActionQuotePost),
		map[string]any{"quoted_id": postID, "new_post_id": newID}, t.now()); err != nil {
		return nil, err
	}
	return map[string]any{"success": true, "post_id": newID}, nil
}

// CreateComment replica Platform.create_comment: comenta el post raíz (si el
// id es un repost) y deja traza de la acción.
func (t *Twitter) CreateComment(agentID int, postID int64, content string) (map[string]any, error) {
	if strings.TrimSpace(content) == "" {
		return nil, fmt.Errorf("el contenido del comentario no puede estar vacío")
	}
	user, err := t.userForAgent(agentID)
	if err != nil {
		return nil, err
	}
	_, rootID, err := t.rootPost(postID)
	if err != nil {
		return nil, err
	}
	commentID, err := t.Store.CreateComment(t.SimulationID, user.ID, rootID, content, t.now())
	if err != nil {
		return nil, err
	}
	if err := t.Store.Trace(t.SimulationID, user.ID, string(ActionCreateComment),
		map[string]any{"content": content, "comment_id": commentID, "post_id": rootID}, t.now()); err != nil {
		return nil, err
	}
	return map[string]any{"success": true, "comment_id": commentID}, nil
}

// LikePost replica Platform.like_post: resuelve el raíz de un repost, impide
// el like duplicado y actualiza el contador en la misma operación.
func (t *Twitter) LikePost(agentID int, postID int64) (map[string]any, error) {
	user, err := t.userForAgent(agentID)
	if err != nil {
		return nil, err
	}
	_, rootID, err := t.rootPost(postID)
	if err != nil {
		return nil, err
	}
	likeID, created, err := t.Store.AddLike(t.SimulationID, user.ID, rootID, t.now())
	if err != nil {
		return nil, err
	}
	if !created {
		return nil, fmt.Errorf("Like record already exists.")
	}
	if err := t.Store.Trace(t.SimulationID, user.ID, string(ActionLikePost),
		map[string]any{"post_id": rootID, "like_id": likeID}, t.now()); err != nil {
		return nil, err
	}
	return map[string]any{"success": true, "like_id": likeID}, nil
}

// Follow replica Platform.follow: actualiza contadores de ambos usuarios.
func (t *Twitter) Follow(agentID, followeeAgentID int) (map[string]any, error) {
	user, err := t.userForAgent(agentID)
	if err != nil {
		return nil, err
	}
	followee, err := t.userForAgent(followeeAgentID)
	if err != nil {
		return nil, err
	}
	followID, created, err := t.Store.AddFollow(t.SimulationID, user.ID, followee.ID, t.now())
	if err != nil {
		return nil, err
	}
	if !created {
		return nil, fmt.Errorf("Follow record already exists.")
	}
	if err := t.Store.Trace(t.SimulationID, user.ID, string(ActionFollow),
		map[string]any{"follow_id": followID, "followee_id": followeeAgentID}, t.now()); err != nil {
		return nil, err
	}
	return map[string]any{"success": true, "follow_id": followID}, nil
}

// Refresh replica Platform.refresh: mezcla los post_id recomendados (muestra
// aleatoria de refresh_rec_post_count) con los posts más gustados de los
// seguidos (following_post_count), deduplica y devuelve los posts.
func (t *Twitter) Refresh(agentID int) (map[string]any, error) {
	user, err := t.userForAgent(agentID)
	if err != nil {
		return nil, err
	}
	recIDs, err := t.Store.RecPosts(t.SimulationID, user.ID)
	if err != nil {
		return nil, err
	}
	selected := append([]int64(nil), recIDs...)
	if len(selected) >= t.RefreshRecPostCount {
		t.shuffle(selected)
		selected = selected[:t.RefreshRecPostCount]
	}

	following, err := t.Store.FollowingPosts(t.SimulationID, user.ID, t.FollowingPostCount)
	if err != nil {
		return nil, err
	}
	seen := map[int64]bool{}
	ids := []int64{}
	for _, id := range selected {
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	for _, post := range following {
		if !seen[post.PostID] {
			seen[post.PostID] = true
			ids = append(ids, post.PostID)
		}
	}

	posts, err := t.Store.PostsByIDs(t.SimulationID, ids)
	if err != nil {
		return nil, err
	}
	if len(posts) == 0 {
		return map[string]any{
			"success": false,
			"message": "No posts found.",
		}, nil
	}
	// El orden estable evita que dos agentes vean listas distintas solo por
	// el orden de la consulta IN.
	sort.Slice(posts, func(i, j int) bool { return posts[i].PostID < posts[j].PostID })
	maps := make([]map[string]any, 0, len(posts))
	for _, p := range posts {
		maps = append(maps, p.ToMap())
	}
	if err := t.Store.Trace(t.SimulationID, user.ID, string(ActionRefresh),
		map[string]any{"posts": maps}, t.now()); err != nil {
		return nil, err
	}
	return map[string]any{"success": true, "posts": maps}, nil
}

// UpdateRecTable recalcula la tabla de recomendaciones. A diferencia del
// Python (embeddings/recencia por trazas), puntúa likes y shares y usa el id
// como desempate de recencia: suficiente para que el simulador funcione y
// reproducible en tests.
func (t *Twitter) UpdateRecTable() error {
	users, err := t.Store.AllUsers(t.SimulationID)
	if err != nil {
		return err
	}
	posts, err := t.Store.AllPosts(t.SimulationID)
	if err != nil {
		return err
	}
	type scored struct {
		id    int64
		score int
	}
	ranking := make([]scored, 0, len(posts))
	for _, p := range posts {
		ranking = append(ranking, scored{
			id:    p.PostID,
			score: p.NumLikes*2 + p.NumShares*3 - p.NumDislikes,
		})
	}
	sort.SliceStable(ranking, func(i, j int) bool {
		if ranking[i].score != ranking[j].score {
			return ranking[i].score > ranking[j].score
		}
		return ranking[i].id > ranking[j].id
	})
	limit := t.MaxRecPostLen
	if limit <= 0 {
		limit = 2
	}
	top := []int64{}
	for i := 0; i < len(ranking) && i < limit; i++ {
		top = append(top, ranking[i].id)
	}

	// Índice de posts propios una sola vez (evita O(usuarios × posts) en cada
	// ronda cuando la simulación crece).
	ownPosts := map[int64]map[int64]bool{}
	for _, p := range posts {
		userID := int64(p.UserID)
		if ownPosts[userID] == nil {
			ownPosts[userID] = map[int64]bool{}
		}
		ownPosts[userID][p.PostID] = true
	}

	byUser := map[int64][]int64{}
	for _, u := range users {
		// Cada usuario recibe el top global; los posts propios se excluyen
		// para que la recomendación no sea un espejo de su propio contenido.
		own := ownPosts[u.ID]
		ids := []int64{}
		for _, id := range top {
			if !own[id] {
				ids = append(ids, id)
			}
		}
		if len(ids) == 0 {
			for _, p := range posts {
				ids = append(ids, p.PostID)
			}
		}
		byUser[u.ID] = ids
	}
	return t.Store.ReplaceRec(t.SimulationID, byUser)
}

// TraceAction registra una acción del agente (todas las acciones dejan rastro,
// no solo las que tocan la BD social).
func (t *Twitter) TraceAction(agentID int, action ActionType, args map[string]any) error {
	user, err := t.userForAgent(agentID)
	if err != nil {
		return err
	}
	return t.Store.Trace(t.SimulationID, user.ID, string(action), args, t.now())
}

// Stats devuelve contadores para el entorno del agente.
func (t *Twitter) Stats(agentID int) (followers, follows int, err error) {
	user, err := t.userForAgent(agentID)
	if err != nil {
		return 0, 0, err
	}
	return user.NumFollowers, user.NumFollowings, nil
}
