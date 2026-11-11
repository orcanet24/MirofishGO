// Persistencia de la simulación social en MySQL (o SQLite en la variante
// personal), aislada por simulation_id.
package oasis

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"mirofish/internal/dbx"
	"mirofish/internal/timex"
)

// Store es el almacén social de OASIS. Comparte el *sql.DB del grafo.
type Store struct {
	db *sql.DB

	// writeMu serializa operaciones compuestas (like + contador, follow +
	// contadores) para que dos acciones no dejen contadores a medias.
	writeMu sync.Mutex
}

// NewStore valida la conexión y crea el esquema si falta.
func NewStore(db *sql.DB) (*Store, error) {
	if db == nil {
		return nil, fmt.Errorf("OASIS requiere una conexión de base de datos")
	}
	s := &Store{db: db}
	if err := s.init(); err != nil {
		return nil, err
	}
	return s, nil
}

var socialSchemaMySQL = []string{
	`CREATE TABLE IF NOT EXISTS oasis_users (
		id BIGINT NOT NULL AUTO_INCREMENT,
		simulation_id VARCHAR(64) NOT NULL,
		agent_id INT NOT NULL,
		user_name VARCHAR(255) NOT NULL,
		name VARCHAR(255) NOT NULL,
		bio TEXT,
		persona LONGTEXT,
		created_at VARCHAR(40) NOT NULL DEFAULT '',
		num_followings INT NOT NULL DEFAULT 0,
		num_followers INT NOT NULL DEFAULT 0,
		PRIMARY KEY (id),
		UNIQUE KEY uq_oasis_user (simulation_id, agent_id),
		KEY idx_oasis_users_sim (simulation_id)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin`,
	`CREATE TABLE IF NOT EXISTS oasis_posts (
		post_id BIGINT NOT NULL AUTO_INCREMENT,
		simulation_id VARCHAR(64) NOT NULL,
		user_id BIGINT NOT NULL,
		original_post_id BIGINT NULL,
		content LONGTEXT,
		quote_content LONGTEXT,
		created_at VARCHAR(40) NOT NULL DEFAULT '',
		num_likes INT NOT NULL DEFAULT 0,
		num_dislikes INT NOT NULL DEFAULT 0,
		num_shares INT NOT NULL DEFAULT 0,
		num_reports INT NOT NULL DEFAULT 0,
		PRIMARY KEY (post_id),
		KEY idx_oasis_posts_sim (simulation_id, post_id),
		KEY idx_oasis_posts_user (user_id),
		CONSTRAINT fk_oasis_posts_user FOREIGN KEY (user_id)
			REFERENCES oasis_users(id) ON DELETE CASCADE,
		CONSTRAINT fk_oasis_posts_original FOREIGN KEY (original_post_id)
			REFERENCES oasis_posts(post_id) ON DELETE SET NULL
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin`,
	`CREATE TABLE IF NOT EXISTS oasis_comments (
		comment_id BIGINT NOT NULL AUTO_INCREMENT,
		simulation_id VARCHAR(64) NOT NULL,
		post_id BIGINT NOT NULL,
		user_id BIGINT NOT NULL,
		content LONGTEXT,
		created_at VARCHAR(40) NOT NULL DEFAULT '',
		num_likes INT NOT NULL DEFAULT 0,
		num_dislikes INT NOT NULL DEFAULT 0,
		PRIMARY KEY (comment_id),
		KEY idx_oasis_comments_sim (simulation_id, comment_id),
		KEY idx_oasis_comments_post (post_id, created_at),
		CONSTRAINT fk_oasis_comments_post FOREIGN KEY (post_id)
			REFERENCES oasis_posts(post_id) ON DELETE CASCADE,
		CONSTRAINT fk_oasis_comments_user FOREIGN KEY (user_id)
			REFERENCES oasis_users(id) ON DELETE CASCADE
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin`,
	`CREATE TABLE IF NOT EXISTS oasis_follows (
		follow_id BIGINT NOT NULL AUTO_INCREMENT,
		simulation_id VARCHAR(64) NOT NULL,
		follower_id BIGINT NOT NULL,
		followee_id BIGINT NOT NULL,
		created_at VARCHAR(40) NOT NULL DEFAULT '',
		PRIMARY KEY (follow_id),
		UNIQUE KEY uq_oasis_follow (simulation_id, follower_id, followee_id),
		KEY idx_oasis_follows_followee (followee_id),
		CONSTRAINT fk_oasis_follows_follower FOREIGN KEY (follower_id)
			REFERENCES oasis_users(id) ON DELETE CASCADE,
		CONSTRAINT fk_oasis_follows_followee FOREIGN KEY (followee_id)
			REFERENCES oasis_users(id) ON DELETE CASCADE
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin`,
	`CREATE TABLE IF NOT EXISTS oasis_likes (
		like_id BIGINT NOT NULL AUTO_INCREMENT,
		simulation_id VARCHAR(64) NOT NULL,
		user_id BIGINT NOT NULL,
		post_id BIGINT NOT NULL,
		created_at VARCHAR(40) NOT NULL DEFAULT '',
		PRIMARY KEY (like_id),
		UNIQUE KEY uq_oasis_like (simulation_id, user_id, post_id),
		KEY idx_oasis_likes_post (post_id),
		CONSTRAINT fk_oasis_likes_user FOREIGN KEY (user_id)
			REFERENCES oasis_users(id) ON DELETE CASCADE,
		CONSTRAINT fk_oasis_likes_post FOREIGN KEY (post_id)
			REFERENCES oasis_posts(post_id) ON DELETE CASCADE
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin`,
	`CREATE TABLE IF NOT EXISTS oasis_traces (
		id BIGINT NOT NULL AUTO_INCREMENT,
		simulation_id VARCHAR(64) NOT NULL,
		user_id BIGINT NOT NULL,
		created_at VARCHAR(40) NOT NULL DEFAULT '',
		action VARCHAR(64) NOT NULL,
		info LONGTEXT,
		PRIMARY KEY (id),
		KEY idx_oasis_traces_sim (simulation_id, id),
		KEY idx_oasis_traces_user (simulation_id, user_id, action)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin`,
	`CREATE TABLE IF NOT EXISTS oasis_rec (
		simulation_id VARCHAR(64) NOT NULL,
		user_id BIGINT NOT NULL,
		post_id BIGINT NOT NULL,
		PRIMARY KEY (simulation_id, user_id, post_id),
		KEY idx_oasis_rec_post (post_id),
		CONSTRAINT fk_oasis_rec_user FOREIGN KEY (user_id)
			REFERENCES oasis_users(id) ON DELETE CASCADE,
		CONSTRAINT fk_oasis_rec_post FOREIGN KEY (post_id)
			REFERENCES oasis_posts(post_id) ON DELETE CASCADE
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin`,
	`CREATE TABLE IF NOT EXISTS oasis_actions (
		id BIGINT NOT NULL AUTO_INCREMENT,
		simulation_id VARCHAR(64) NOT NULL,
		round_num INT NOT NULL DEFAULT 0,
		agent_id INT NOT NULL,
		agent_name VARCHAR(255),
		action_type VARCHAR(64) NOT NULL,
		action_args LONGTEXT,
		result LONGTEXT,
		success TINYINT NOT NULL DEFAULT 1,
		created_at VARCHAR(40) NOT NULL DEFAULT '',
		PRIMARY KEY (id),
		KEY idx_oasis_actions_sim (simulation_id, id),
		KEY idx_oasis_actions_round (simulation_id, round_num),
		KEY idx_oasis_actions_agent (simulation_id, agent_id)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin`,
	`CREATE TABLE IF NOT EXISTS oasis_runs (
		simulation_id VARCHAR(64) NOT NULL,
		state_json LONGTEXT NOT NULL,
		updated_at VARCHAR(40) NOT NULL DEFAULT '',
		PRIMARY KEY (simulation_id)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin`,
}

// socialSchemaSQLite — mismo esquema (columnas idénticas) en dialecto
// SQLite: AUTOINCREMENT, UNIQUE/Índices explícitos y FK sintaxis SQLite.
var socialSchemaSQLite = []string{
	`CREATE TABLE IF NOT EXISTS oasis_users (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		simulation_id TEXT NOT NULL,
		agent_id INTEGER NOT NULL,
		user_name TEXT NOT NULL,
		name TEXT NOT NULL,
		bio TEXT,
		persona TEXT,
		created_at TEXT NOT NULL DEFAULT '',
		num_followings INTEGER NOT NULL DEFAULT 0,
		num_followers INTEGER NOT NULL DEFAULT 0,
		UNIQUE (simulation_id, agent_id)
	)`,
	`CREATE INDEX IF NOT EXISTS idx_oasis_users_sim ON oasis_users(simulation_id)`,
	`CREATE TABLE IF NOT EXISTS oasis_posts (
		post_id INTEGER PRIMARY KEY AUTOINCREMENT,
		simulation_id TEXT NOT NULL,
		user_id INTEGER NOT NULL,
		original_post_id INTEGER,
		content TEXT,
		quote_content TEXT,
		created_at TEXT NOT NULL DEFAULT '',
		num_likes INTEGER NOT NULL DEFAULT 0,
		num_dislikes INTEGER NOT NULL DEFAULT 0,
		num_shares INTEGER NOT NULL DEFAULT 0,
		num_reports INTEGER NOT NULL DEFAULT 0,
		FOREIGN KEY (user_id) REFERENCES oasis_users(id) ON DELETE CASCADE,
		FOREIGN KEY (original_post_id) REFERENCES oasis_posts(post_id) ON DELETE SET NULL
	)`,
	`CREATE INDEX IF NOT EXISTS idx_oasis_posts_sim ON oasis_posts(simulation_id, post_id)`,
	`CREATE INDEX IF NOT EXISTS idx_oasis_posts_user ON oasis_posts(user_id)`,
	`CREATE TABLE IF NOT EXISTS oasis_comments (
		comment_id INTEGER PRIMARY KEY AUTOINCREMENT,
		simulation_id TEXT NOT NULL,
		post_id INTEGER NOT NULL,
		user_id INTEGER NOT NULL,
		content TEXT,
		created_at TEXT NOT NULL DEFAULT '',
		num_likes INTEGER NOT NULL DEFAULT 0,
		num_dislikes INTEGER NOT NULL DEFAULT 0,
		FOREIGN KEY (post_id) REFERENCES oasis_posts(post_id) ON DELETE CASCADE,
		FOREIGN KEY (user_id) REFERENCES oasis_users(id) ON DELETE CASCADE
	)`,
	`CREATE INDEX IF NOT EXISTS idx_oasis_comments_sim ON oasis_comments(simulation_id, comment_id)`,
	`CREATE INDEX IF NOT EXISTS idx_oasis_comments_post ON oasis_comments(post_id, created_at)`,
	`CREATE TABLE IF NOT EXISTS oasis_follows (
		follow_id INTEGER PRIMARY KEY AUTOINCREMENT,
		simulation_id TEXT NOT NULL,
		follower_id INTEGER NOT NULL,
		followee_id INTEGER NOT NULL,
		created_at TEXT NOT NULL DEFAULT '',
		UNIQUE (simulation_id, follower_id, followee_id),
		FOREIGN KEY (follower_id) REFERENCES oasis_users(id) ON DELETE CASCADE,
		FOREIGN KEY (followee_id) REFERENCES oasis_users(id) ON DELETE CASCADE
	)`,
	`CREATE INDEX IF NOT EXISTS idx_oasis_follows_followee ON oasis_follows(followee_id)`,
	`CREATE TABLE IF NOT EXISTS oasis_likes (
		like_id INTEGER PRIMARY KEY AUTOINCREMENT,
		simulation_id TEXT NOT NULL,
		user_id INTEGER NOT NULL,
		post_id INTEGER NOT NULL,
		created_at TEXT NOT NULL DEFAULT '',
		UNIQUE (simulation_id, user_id, post_id),
		FOREIGN KEY (user_id) REFERENCES oasis_users(id) ON DELETE CASCADE,
		FOREIGN KEY (post_id) REFERENCES oasis_posts(post_id) ON DELETE CASCADE
	)`,
	`CREATE INDEX IF NOT EXISTS idx_oasis_likes_post ON oasis_likes(post_id)`,
	`CREATE TABLE IF NOT EXISTS oasis_traces (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		simulation_id TEXT NOT NULL,
		user_id INTEGER NOT NULL,
		created_at TEXT NOT NULL DEFAULT '',
		action TEXT NOT NULL,
		info TEXT,
		FOREIGN KEY (user_id) REFERENCES oasis_users(id) ON DELETE CASCADE
	)`,
	`CREATE INDEX IF NOT EXISTS idx_oasis_traces_sim ON oasis_traces(simulation_id, id)`,
	`CREATE INDEX IF NOT EXISTS idx_oasis_traces_user ON oasis_traces(simulation_id, user_id, action)`,
	`CREATE TABLE IF NOT EXISTS oasis_rec (
		simulation_id TEXT NOT NULL,
		user_id INTEGER NOT NULL,
		post_id INTEGER NOT NULL,
		PRIMARY KEY (simulation_id, user_id, post_id),
		FOREIGN KEY (user_id) REFERENCES oasis_users(id) ON DELETE CASCADE,
		FOREIGN KEY (post_id) REFERENCES oasis_posts(post_id) ON DELETE CASCADE
	)`,
	`CREATE INDEX IF NOT EXISTS idx_oasis_rec_post ON oasis_rec(post_id)`,
	`CREATE TABLE IF NOT EXISTS oasis_actions (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		simulation_id TEXT NOT NULL,
		round_num INTEGER NOT NULL DEFAULT 0,
		agent_id INTEGER NOT NULL,
		agent_name TEXT,
		action_type TEXT NOT NULL,
		action_args TEXT,
		result TEXT,
		success INTEGER NOT NULL DEFAULT 1,
		created_at TEXT NOT NULL DEFAULT ''
	)`,
	`CREATE INDEX IF NOT EXISTS idx_oasis_actions_sim ON oasis_actions(simulation_id, id)`,
	`CREATE INDEX IF NOT EXISTS idx_oasis_actions_round ON oasis_actions(simulation_id, round_num)`,
	`CREATE INDEX IF NOT EXISTS idx_oasis_actions_agent ON oasis_actions(simulation_id, agent_id)`,
	`CREATE TABLE IF NOT EXISTS oasis_runs (
		simulation_id TEXT NOT NULL PRIMARY KEY,
		state_json TEXT NOT NULL,
		updated_at TEXT NOT NULL DEFAULT ''
	)`,
}

// socialSchema elige el conjunto según el motor activo.
func socialSchema() []string {
	if dbx.Active() == dbx.SQLite {
		return socialSchemaSQLite
	}
	return socialSchemaMySQL
}

func (s *Store) init() error {
	for _, stmt := range socialSchema() {
		if _, err := s.db.Exec(stmt); err != nil {
			return fmt.Errorf("esquema OASIS (%s): %w", dbx.Active(), err)
		}
	}
	return nil
}

// Reset deja la simulación sin usuarios, posts ni acciones. Se llama al
// iniciar una corrida nueva, nunca al reanudar.
func (s *Store) Reset(simulationID string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	// El orden respeta las FK aunque estén en CASCADE.
	for _, table := range []string{"oasis_actions", "oasis_rec", "oasis_traces", "oasis_likes", "oasis_comments", "oasis_follows", "oasis_posts", "oasis_users"} {
		if _, err := tx.Exec("DELETE FROM "+table+" WHERE simulation_id = ?", simulationID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// User es una fila de oasis_users.
type User struct {
	ID            int64
	AgentID       int
	UserName      string
	Name          string
	Bio           string
	Persona       string
	CreatedAt     string
	NumFollowings int
	NumFollowers  int
}

// SignUp crea el usuario del agente. Un segundo signup del mismo agent_id
// devuelve el usuario existente (el reset ya limpió la simulación).
func (s *Store) SignUp(simulationID string, agentID int, userName, name, bio, persona, createdAt string) (*User, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	res, err := s.db.Exec(
		`INSERT INTO oasis_users
		 (simulation_id, agent_id, user_name, name, bio, persona, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		simulationID, agentID, userName, name, bio, persona, createdAt,
	)
	if err != nil {
		// Segundo signup del mismo agent_id → devuelve el usuario existente.
		if dbx.IsDuplicate(err) {
			return s.userByAgentLocked(simulationID, agentID)
		}
		return nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	return &User{
		ID: id, AgentID: agentID, UserName: userName, Name: name,
		Bio: bio, Persona: persona, CreatedAt: createdAt,
	}, nil
}

func (s *Store) userByAgentLocked(simulationID string, agentID int) (*User, error) {
	row := s.db.QueryRow(
		`SELECT id, agent_id, user_name, name, bio, persona, created_at,
		        num_followings, num_followers
		 FROM oasis_users WHERE simulation_id = ? AND agent_id = ?`,
		simulationID, agentID,
	)
	return scanUser(row.Scan)
}

// UserByAgent devuelve nil si el agente no se ha registrado.
func (s *Store) UserByAgent(simulationID string, agentID int) (*User, error) {
	row := s.db.QueryRow(
		`SELECT id, agent_id, user_name, name, bio, persona, created_at,
		        num_followings, num_followers
		 FROM oasis_users WHERE simulation_id = ? AND agent_id = ?`,
		simulationID, agentID,
	)
	return scanUser(row.Scan)
}

// UserByID devuelve nil si el usuario no existe.
func (s *Store) UserByID(simulationID string, userID int64) (*User, error) {
	row := s.db.QueryRow(
		`SELECT id, agent_id, user_name, name, bio, persona, created_at,
		        num_followings, num_followers
		 FROM oasis_users WHERE simulation_id = ? AND id = ?`,
		simulationID, userID,
	)
	return scanUser(row.Scan)
}

func scanUser(scan func(...any) error) (*User, error) {
	var u User
	var bio, persona sql.NullString
	err := scan(&u.ID, &u.AgentID, &u.UserName, &u.Name, &bio, &persona,
		&u.CreatedAt, &u.NumFollowings, &u.NumFollowers)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	u.Bio = bio.String
	u.Persona = persona.String
	return &u, nil
}

// AllUsers devuelve los usuarios en orden de agent_id (equivale a la tabla
// user del Python, indexada por user_id).
func (s *Store) AllUsers(simulationID string) ([]User, error) {
	rows, err := s.db.Query(
		`SELECT id, agent_id, user_name, name, bio, persona, created_at,
		        num_followings, num_followers
		 FROM oasis_users WHERE simulation_id = ? ORDER BY agent_id`,
		simulationID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []User{}
	for rows.Next() {
		u, err := scanUser(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, *u)
	}
	return out, rows.Err()
}

// CreatePost inserta un post y devuelve su id.
func (s *Store) CreatePost(simulationID string, userID int64, content string, originalPostID *int64, quoteContent, createdAt string) (int64, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	res, err := s.db.Exec(
		`INSERT INTO oasis_posts
		 (simulation_id, user_id, original_post_id, content, quote_content, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		simulationID, userID, originalPostID, content, quoteContent, createdAt,
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// GetPost devuelve nil si el post no pertenece a la simulación.
func (s *Store) GetPost(simulationID string, postID int64) (*Post, error) {
	row := s.db.QueryRow(
		`SELECT post_id, user_id, original_post_id, content, quote_content,
		        created_at, num_likes, num_dislikes, num_shares, num_reports
		 FROM oasis_posts WHERE simulation_id = ? AND post_id = ?`,
		simulationID, postID,
	)
	var p Post
	var original sql.NullInt64
	var content, quote, createdAt sql.NullString
	err := row.Scan(&p.PostID, &p.UserID, &original, &content, &quote,
		&createdAt, &p.NumLikes, &p.NumDislikes, &p.NumShares, &p.NumReports)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if original.Valid {
		id := original.Int64
		p.OriginalPostID = &id
	}
	p.Content = content.String
	p.QuoteContent = quote.String
	p.CreatedAt = createdAt.String
	return &p, nil
}

// PostsByIDs devuelve los posts existentes, sin fallar si algún id ya no está.
func (s *Store) PostsByIDs(simulationID string, ids []int64) ([]Post, error) {
	if len(ids) == 0 {
		return []Post{}, nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, 0, len(ids)+1)
	args = append(args, simulationID)
	for _, id := range ids {
		args = append(args, id)
	}
	rows, err := s.db.Query(
		`SELECT post_id, user_id, original_post_id, content, quote_content,
		        created_at, num_likes, num_dislikes, num_shares, num_reports
		 FROM oasis_posts WHERE simulation_id = ? AND post_id IN (`+placeholders+`)`,
		args...,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Post{}
	for rows.Next() {
		var p Post
		var original sql.NullInt64
		var content, quote, createdAt sql.NullString
		if err := rows.Scan(&p.PostID, &p.UserID, &original, &content, &quote,
			&createdAt, &p.NumLikes, &p.NumDislikes, &p.NumShares, &p.NumReports); err != nil {
			return nil, err
		}
		if original.Valid {
			id := original.Int64
			p.OriginalPostID = &id
		}
		p.Content = content.String
		p.QuoteContent = quote.String
		p.CreatedAt = createdAt.String
		out = append(out, p)
	}
	return out, rows.Err()
}

// AllPosts devuelve todos los posts de la simulación en orden de creación.
func (s *Store) AllPosts(simulationID string) ([]Post, error) {
	rows, err := s.db.Query(
		`SELECT post_id, user_id, original_post_id, content, quote_content,
		        created_at, num_likes, num_dislikes, num_shares, num_reports
		 FROM oasis_posts WHERE simulation_id = ? ORDER BY post_id`,
		simulationID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Post{}
	for rows.Next() {
		var p Post
		var original sql.NullInt64
		var content, quote, createdAt sql.NullString
		if err := rows.Scan(&p.PostID, &p.UserID, &original, &content, &quote,
			&createdAt, &p.NumLikes, &p.NumDislikes, &p.NumShares, &p.NumReports); err != nil {
			return nil, err
		}
		if original.Valid {
			id := original.Int64
			p.OriginalPostID = &id
		}
		p.Content = content.String
		p.QuoteContent = quote.String
		p.CreatedAt = createdAt.String
		out = append(out, p)
	}
	return out, rows.Err()
}

// ListPosts devuelve los posts de la simulación, del más nuevo al más viejo,
// con la paginación de /api/simulation/<id>/posts.
func (s *Store) ListPosts(simulationID string, limit, offset int) ([]Post, error) {
	if limit <= 0 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := s.db.Query(
		`SELECT post_id, user_id, original_post_id, content, quote_content,
		        created_at, num_likes, num_dislikes, num_shares, num_reports
		 FROM oasis_posts WHERE simulation_id = ?
		 ORDER BY created_at DESC, post_id DESC LIMIT ? OFFSET ?`,
		simulationID, limit, offset,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanPosts(rows)
}

// CountPosts cuenta los posts de la simulación.
func (s *Store) CountPosts(simulationID string) (int, error) {
	var count int
	err := s.db.QueryRow(
		"SELECT COUNT(*) FROM oasis_posts WHERE simulation_id = ?", simulationID,
	).Scan(&count)
	return count, err
}

// CreateComment inserta un comentario de un agente sobre un post.
func (s *Store) CreateComment(simulationID string, userID, postID int64, content, createdAt string) (int64, error) {
	res, err := s.db.Exec(
		`INSERT INTO oasis_comments
		 (simulation_id, post_id, user_id, content, created_at)
		 VALUES (?, ?, ?, ?, ?)`,
		simulationID, postID, userID, content, createdAt,
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// ListComments devuelve comentarios, del más nuevo al más viejo. postID nil
// incluye todos los posts de la simulación.
func (s *Store) ListComments(simulationID string, postID *int64, limit, offset int) ([]Comment, error) {
	if limit <= 0 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	query := `SELECT comment_id, post_id, user_id, content, created_at,
	                 num_likes, num_dislikes
	          FROM oasis_comments WHERE simulation_id = ?`
	args := []any{simulationID}
	if postID != nil {
		query += " AND post_id = ?"
		args = append(args, *postID)
	}
	query += " ORDER BY created_at DESC, comment_id DESC LIMIT ? OFFSET ?"
	args = append(args, limit, offset)

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Comment{}
	for rows.Next() {
		var c Comment
		var content sql.NullString
		if err := rows.Scan(&c.CommentID, &c.PostID, &c.UserID, &content,
			&c.CreatedAt, &c.NumLikes, &c.NumDislikes); err != nil {
			return nil, err
		}
		c.Content = content.String
		out = append(out, c)
	}
	return out, rows.Err()
}

// AddCounter incrementa un contador del post. column se valida contra una
// lista blanca para no construir SQL con entrada externa.
func (s *Store) AddCounter(simulationID string, postID int64, column string, delta int) error {
	allowed := map[string]bool{"num_likes": true, "num_dislikes": true, "num_shares": true, "num_reports": true}
	if !allowed[column] {
		return fmt.Errorf("contador no permitido: %s", column)
	}
	// El máximo con 0 evita contadores negativos si una acción se repite por
	// un reintento tras un fallo de red (GREATEST en MySQL, max() en SQLite).
	_, err := s.db.Exec(
		"UPDATE oasis_posts SET "+column+" = "+dbx.Greatest(column+" + ?", "0")+" WHERE simulation_id = ? AND post_id = ?",
		delta, simulationID, postID,
	)
	return err
}

// HasLike indica si el usuario ya dio like al post.
func (s *Store) HasLike(simulationID string, userID, postID int64) (bool, error) {
	return s.exists("SELECT 1 FROM oasis_likes WHERE simulation_id = ? AND user_id = ? AND post_id = ?", simulationID, userID, postID)
}

// AddLike inserta el like y suma el contador de forma atómica. Devuelve el id
// del like y false si ya existía.
func (s *Store) AddLike(simulationID string, userID, postID int64, createdAt string) (int64, bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	res, err := s.db.Exec(
		"INSERT INTO oasis_likes (simulation_id, user_id, post_id, created_at) VALUES (?, ?, ?, ?)",
		simulationID, userID, postID, createdAt,
	)
	if err != nil {
		// Like repetido: no es error, reporta false.
		if dbx.IsDuplicate(err) {
			return 0, false, nil
		}
		return 0, false, err
	}
	if _, err := s.db.Exec(
		"UPDATE oasis_posts SET num_likes = num_likes + 1 WHERE simulation_id = ? AND post_id = ?",
		simulationID, postID,
	); err != nil {
		return 0, false, err
	}
	id, _ := res.LastInsertId()
	return id, true, nil
}

// RemoveLike elimina el like y resta el contador. Devuelve false si no existía.
func (s *Store) RemoveLike(simulationID string, userID, postID int64) (int64, bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	var likeID int64
	err := s.db.QueryRow(
		"SELECT like_id FROM oasis_likes WHERE simulation_id = ? AND user_id = ? AND post_id = ?",
		simulationID, userID, postID,
	).Scan(&likeID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	if _, err := s.db.Exec("DELETE FROM oasis_likes WHERE like_id = ?", likeID); err != nil {
		return 0, false, err
	}
	if _, err := s.db.Exec(
		"UPDATE oasis_posts SET num_likes = "+dbx.Greatest("num_likes - 1", "0")+" WHERE simulation_id = ? AND post_id = ?",
		simulationID, postID,
	); err != nil {
		return 0, false, err
	}
	return likeID, true, nil
}

// HasFollow indica si follower ya sigue a followee.
func (s *Store) HasFollow(simulationID string, followerID, followeeID int64) (bool, error) {
	return s.exists(
		"SELECT 1 FROM oasis_follows WHERE simulation_id = ? AND follower_id = ? AND followee_id = ?",
		simulationID, followerID, followeeID,
	)
}

// AddFollow inserta el follow y actualiza ambos contadores.
func (s *Store) AddFollow(simulationID string, followerID, followeeID int64, createdAt string) (int64, bool, error) {
	if followerID == followeeID {
		return 0, false, fmt.Errorf("no puedes seguirte a ti mismo")
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	res, err := s.db.Exec(
		"INSERT INTO oasis_follows (simulation_id, follower_id, followee_id, created_at) VALUES (?, ?, ?, ?)",
		simulationID, followerID, followeeID, createdAt,
	)
	if err != nil {
		// Follow repetido: no es error, reporta false.
		if dbx.IsDuplicate(err) {
			return 0, false, nil
		}
		return 0, false, err
	}
	if _, err := s.db.Exec(
		"UPDATE oasis_users SET num_followings = num_followings + 1 WHERE id = ?", followerID,
	); err != nil {
		return 0, false, err
	}
	if _, err := s.db.Exec(
		"UPDATE oasis_users SET num_followers = num_followers + 1 WHERE id = ?", followeeID,
	); err != nil {
		return 0, false, err
	}
	id, _ := res.LastInsertId()
	return id, true, nil
}

// RemoveFollow elimina el follow y ajusta contadores.
func (s *Store) RemoveFollow(simulationID string, followerID, followeeID int64) (int64, bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	var followID int64
	err := s.db.QueryRow(
		"SELECT follow_id FROM oasis_follows WHERE simulation_id = ? AND follower_id = ? AND followee_id = ?",
		simulationID, followerID, followeeID,
	).Scan(&followID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	if _, err := s.db.Exec("DELETE FROM oasis_follows WHERE follow_id = ?", followID); err != nil {
		return 0, false, err
	}
	if _, err := s.db.Exec(
		"UPDATE oasis_users SET num_followings = "+dbx.Greatest("num_followings - 1", "0")+" WHERE id = ?", followerID,
	); err != nil {
		return 0, false, err
	}
	if _, err := s.db.Exec(
		"UPDATE oasis_users SET num_followers = "+dbx.Greatest("num_followers - 1", "0")+" WHERE id = ?", followeeID,
	); err != nil {
		return 0, false, err
	}
	return followID, true, nil
}

// FollowingPosts devuelve los posts de los usuarios seguidos, con más likes
// primero (consulta de refresh en Twitter).
func (s *Store) FollowingPosts(simulationID string, followerID int64, limit int) ([]Post, error) {
	rows, err := s.db.Query(
		`SELECT p.post_id, p.user_id, p.original_post_id, p.content, p.quote_content,
		        p.created_at, p.num_likes, p.num_dislikes, p.num_shares, p.num_reports
		 FROM oasis_posts p
		 JOIN oasis_follows f ON p.user_id = f.followee_id
		 WHERE f.simulation_id = ? AND f.follower_id = ?
		 ORDER BY p.num_likes DESC, p.post_id DESC LIMIT ?`,
		simulationID, followerID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanPosts(rows)
}

func scanPosts(rows *sql.Rows) ([]Post, error) {
	out := []Post{}
	for rows.Next() {
		var p Post
		var original sql.NullInt64
		var content, quote, createdAt sql.NullString
		if err := rows.Scan(&p.PostID, &p.UserID, &original, &content, &quote,
			&createdAt, &p.NumLikes, &p.NumDislikes, &p.NumShares, &p.NumReports); err != nil {
			return nil, err
		}
		if original.Valid {
			id := original.Int64
			p.OriginalPostID = &id
		}
		p.Content = content.String
		p.QuoteContent = quote.String
		p.CreatedAt = createdAt.String
		out = append(out, p)
	}
	return out, rows.Err()
}

// Trace registra la acción en oasis_traces (memoria auditable del agente).
func (s *Store) Trace(simulationID string, userID int64, action string, info map[string]any, createdAt string) error {
	b, err := json.Marshal(info)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(
		"INSERT INTO oasis_traces (simulation_id, user_id, created_at, action, info) VALUES (?, ?, ?, ?, ?)",
		simulationID, userID, createdAt, action, string(b),
	)
	return err
}

// RecPosts devuelve los post_id recomendados al usuario.
func (s *Store) RecPosts(simulationID string, userID int64) ([]int64, error) {
	rows, err := s.db.Query(
		"SELECT post_id FROM oasis_rec WHERE simulation_id = ? AND user_id = ? ORDER BY post_id",
		simulationID, userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []int64{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// ReplaceRec reemplaza la tabla de recomendaciones en una transacción.
func (s *Store) ReplaceRec(simulationID string, byUser map[int64][]int64) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec("DELETE FROM oasis_rec WHERE simulation_id = ?", simulationID); err != nil {
		return err
	}
	stmt, err := tx.Prepare(dbx.InsertIgnore() + " oasis_rec (simulation_id, user_id, post_id) VALUES (?, ?, ?)")
	if err != nil {
		return err
	}
	defer stmt.Close()
	for userID, posts := range byUser {
		for _, postID := range posts {
			if _, err := stmt.Exec(simulationID, userID, postID); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

// AppendAction guarda una acción en oasis_actions.
func (s *Store) AppendAction(simulationID string, record ActionRecord) error {
	argsJSON, _ := json.Marshal(record.ActionArgs)
	resultJSON, _ := json.Marshal(record.Result)
	success := 0
	if record.Success {
		success = 1
	}
	_, err := s.db.Exec(
		`INSERT INTO oasis_actions
		 (simulation_id, round_num, agent_id, agent_name, action_type,
		  action_args, result, success, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		simulationID, record.Round, record.AgentID, record.AgentName,
		record.ActionType, string(argsJSON), string(resultJSON), success,
		record.Timestamp,
	)
	return err
}

// PurgeSimulation borra TODOS los datos sociales de la simulación de la BD
// (posts, comentarios, likes, follows, traces, recs, acciones, runs, users).
// Se usa en el borrado total ("derecho al olvido").
func (s *Store) PurgeSimulation(simulationID string) error {
	if simulationID == "" || strings.ContainsAny(simulationID, `/\`) {
		return fmt.Errorf("id inválido")
	}
	for _, table := range []string{
		"oasis_posts", "oasis_comments", "oasis_likes", "oasis_follows",
		"oasis_traces", "oasis_rec", "oasis_actions", "oasis_runs", "oasis_users",
	} {
		if _, err := s.db.Exec("DELETE FROM "+table+" WHERE simulation_id = ?", simulationID); err != nil {
			return fmt.Errorf("purge %s: %w", table, err)
		}
	}
	return nil
}

// ListActions devuelve acciones filtradas, de la más antigua a la más nueva.
func (s *Store) ListActions(simulationID string, limit, offset int, platform string, agentID *int, roundNum *int) ([]ActionRecord, error) {
	// Esta rebanada solo escribe acciones de Twitter; platform=reddit no
	// devuelve filas en vez de inventar datos.
	if platform != "" && platform != "twitter" {
		return []ActionRecord{}, nil
	}
	if limit <= 0 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}
	query := `SELECT round_num, created_at, agent_id, agent_name, action_type,
	                 action_args, result, success
	          FROM oasis_actions WHERE simulation_id = ?`
	args := []any{simulationID}
	if agentID != nil {
		query += " AND agent_id = ?"
		args = append(args, *agentID)
	}
	if roundNum != nil {
		query += " AND round_num = ?"
		args = append(args, *roundNum)
	}
	query += " ORDER BY id LIMIT ? OFFSET ?"
	args = append(args, limit, offset)

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ActionRecord{}
	for rows.Next() {
		var rec ActionRecord
		var argsRaw, resultRaw sql.NullString
		var success int
		if err := rows.Scan(&rec.Round, &rec.Timestamp, &rec.AgentID, &rec.AgentName,
			&rec.ActionType, &argsRaw, &resultRaw, &success); err != nil {
			return nil, err
		}
		rec.ActionArgs = map[string]any{}
		if argsRaw.String != "" {
			_ = json.Unmarshal([]byte(argsRaw.String), &rec.ActionArgs)
		}
		if resultRaw.String != "" {
			_ = json.Unmarshal([]byte(resultRaw.String), &rec.Result)
		}
		rec.Success = success != 0
		out = append(out, rec)
	}
	return out, rows.Err()
}

// CountActions cuenta acciones por plataforma lógica.
func (s *Store) CountActions(simulationID, platform string) (int, error) {
	if platform != "" && platform != "twitter" {
		return 0, nil
	}
	var count int
	err := s.db.QueryRow(
		"SELECT COUNT(*) FROM oasis_actions WHERE simulation_id = ?", simulationID,
	).Scan(&count)
	return count, err
}

// SaveRunState publica el estado de ejecución (una fila por simulación).
func (s *Store) SaveRunState(simulationID string, state RunState) error {
	b, err := json.Marshal(state.ToMap())
	if err != nil {
		return err
	}
	q := `INSERT INTO oasis_runs (simulation_id, state_json, updated_at)
		 VALUES (?, ?, ?)`
	if dbx.Active() == dbx.SQLite {
		q += `
		 ON CONFLICT(simulation_id) DO UPDATE SET
		   state_json = excluded.state_json, updated_at = excluded.updated_at`
	} else {
		q += `
		 ON DUPLICATE KEY UPDATE state_json = VALUES(state_json), updated_at = VALUES(updated_at)`
	}
	_, err = s.db.Exec(q, simulationID, string(b), timex.Now())
	return err
}

// LoadRunState devuelve nil si la simulación nunca se ejecutó.
func (s *Store) LoadRunState(simulationID string) (*RunState, error) {
	var raw string
	err := s.db.QueryRow(
		"SELECT state_json FROM oasis_runs WHERE simulation_id = ?", simulationID,
	).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var state RunState
	if err := json.Unmarshal([]byte(raw), &state); err != nil {
		return nil, err
	}
	return &state, nil
}

func (s *Store) exists(query string, args ...any) (bool, error) {
	var one int
	err := s.db.QueryRow(query, args...).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}
