// Package config — port of backend/app/config.py.
//
// Carga el .env del directorio base (MIROFISH_HOME o el directorio actual)
// y expone la misma configuración que la clase Config de Python.
package config

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

// Config replicates the Python backend Config class.
type Config struct {
	SecretKey string
	Debug     bool

	// LLM (OpenAI-compatible)
	LLMAPIKey  string
	LLMBaseURL string
	LLMModel   string

	// Graph backend ("zep" | "local")
	ZEPAPIKey    string
	GraphBackend string

	// Uploads
	MaxContentLength  int64
	UploadDir         string
	AllowedExtensions []string

	// Text processing
	DefaultChunkSize    int
	DefaultChunkOverlap int

	// OASIS simulation
	OASISDefaultMaxRounds int
	SimDataDir            string
	TwitterActions        []string
	RedditActions         []string

	// Report agent
	ReportAgentMaxToolCalls      int
	ReportAgentMaxReflectionRnds int
	ReportAgentTemperature       float64

	// Persistencia del grafo y del estado: MySQL 8 / MariaDB.
	// DB_DSN tiene prioridad; si falta se compone con DB_HOST/DB_PORT/DB_USER/
	// DB_PASSWORD/DB_NAME. No hay fallback a SQLite: el binario exige la BD.
	DBDSN          string
	DBMaxOpenConns int
	DBMaxIdleConns int

	// BaseDir is the data root (like MiroFish/backend in the Python layout).
	BaseDir string

	// Host/Port del servidor HTTP (equivalente a FLASK_HOST/FLASK_PORT).
	Host string
	Port int

	// AccessKey: clave única de acceso. Si está vacía, la autenticación
	// queda desactivada (solo desarrollo). Es la única condición de acceso
	// del binario: toda /api/* la exige, y también /health si no es pública.
	AccessKey string
}

// findProjectRoot walks up from "start" looking for a .env file,
// mirroring config.py's relative path resolution.
func findProjectRoot(start string) string {
	dir, err := filepath.Abs(start)
	if err != nil {
		return start
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, ".env")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return start
		}
		dir = parent
	}
}

func getenv(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && strings.TrimSpace(v) != "" {
		return v
	}
	return def
}

func getint(key string, def int) int {
	if v, ok := os.LookupEnv(key); ok {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			return n
		}
	}
	return def
}

func getfloat(key string, def float64) float64 {
	if v, ok := os.LookupEnv(key); ok {
		if f, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
			return f
		}
	}
	return def
}

func getbool(key string) bool {
	v := strings.ToLower(getenv(key, "false"))
	// Python: os.environ.get(...,'False').lower() == 'true'
	return v == "true"
}

// Load builds the Config from environment + .env, exactly as config.py does.
func Load() *Config {
	// Base directory: MIROFISH_HOME env > .env search upwards from cwd > cwd.
	base := os.Getenv("MIROFISH_HOME")
	if base == "" {
		base = findProjectRoot(".")
	}
	// Load .env with override semantics like load_dotenv(override=True).
	// Se cargan los dos ubicaciones posibles (raíz y backend/), de modo que
	// un .env raíz mínimo no impide leer backend/.env.
	for _, envPath := range []string{
		filepath.Join(base, ".env"),
		filepath.Join(base, "backend", ".env"),
	} {
		if _, err := os.Stat(envPath); err != nil {
			continue
		}
		if err := godotenv.Overload(envPath); err != nil {
			log.Printf("warning: no se pudo cargar %s: %v", envPath, err)
		}
	}

	c := &Config{
		BaseDir:          base,
		SecretKey:        getenv("SECRET_KEY", "mirofish-secret-key"),
		Debug:            getbool("FLASK_DEBUG"),
		LLMAPIKey:        getenv("LLM_API_KEY", ""),
		LLMBaseURL:       getenv("LLM_BASE_URL", "https://api.openai.com/v1"),
		LLMModel:         getenv("LLM_MODEL_NAME", "gpt-4o-mini"),
		ZEPAPIKey:        os.Getenv("ZEP_API_KEY"),
		MaxContentLength: int64(getint("MAX_CONTENT_LENGTH", 50*1024*1024)),
		// Python: UPLOAD_FOLDER = backend/app/../uploads → <raíz>/backend/uploads
		UploadDir: filepath.Join(base, "backend", "uploads"),
		// Python subset: {'pdf','md','txt','markdown'}
		AllowedExtensions:   []string{".pdf", ".md", ".txt", ".markdown"},
		DefaultChunkSize:    getint("DEFAULT_CHUNK_SIZE", 500),
		DefaultChunkOverlap: getint("DEFAULT_CHUNK_OVERLAP", 50),

		OASISDefaultMaxRounds: getint("OASIS_DEFAULT_MAX_ROUNDS", 10),
		SimDataDir:            filepath.Join(base, "backend", "uploads", "simulations"),
		TwitterActions:        []string{"CREATE_POST", "LIKE_POST", "REPOST", "FOLLOW", "DO_NOTHING", "QUOTE_POST"},
		RedditActions:         []string{"LIKE_POST", "DISLIKE_POST", "CREATE_POST", "CREATE_COMMENT", "LIKE_COMMENT", "DISLIKE_COMMENT", "SEARCH_POSTS", "SEARCH_USER", "TREND", "REFRESH", "DO_NOTHING", "FOLLOW", "MUTE"},

		ReportAgentMaxToolCalls:      getint("REPORT_AGENT_MAX_TOOL_CALLS", 5),
		ReportAgentMaxReflectionRnds: getint("REPORT_AGENT_MAX_REFLECTION_ROUNDS", 2),
		ReportAgentTemperature:       getfloat("REPORT_AGENT_TEMPERATURE", 0.5),

		DBDSN:          databaseDSN(),
		DBMaxOpenConns: getint("DB_MAX_OPEN_CONNS", 20),
		DBMaxIdleConns: getint("DB_MAX_IDLE_CONNS", 10),

		Host: func() string {
			if v := getenv("MIROFISH_HOST", ""); v != "" {
				return v
			}
			return getenv("FLASK_HOST", "0.0.0.0")
		}(),
		Port: func() int {
			if v := getint("MIROFISH_PORT", 0); v > 0 {
				return v
			}
			return getint("FLASK_PORT", 5001)
		}(),
		AccessKey: getenv("ACCESS_KEY", ""),
	}

	// GRAPH_BACKEND: el binario Go solo implementa el backend local
	// (internal/graph, persistido en MySQL). Python elegía "zep" al ver
	// ZEP_API_KEY; replicar eso aquí haría que la API dijera "zep" mientras
	// escribe en local: un fallo silencioso. Por eso ZEP_API_KEY no cambia el
	// backend y pedir GRAPH_BACKEND=zep falla en Validate.
	if env := strings.TrimSpace(os.Getenv("GRAPH_BACKEND")); env != "" {
		c.GraphBackend = strings.ToLower(env)
	} else {
		if c.ZEPAPIKey != "" {
			log.Printf("warning: ZEP_API_KEY está configurada pero el binario Go solo soporta el grafo local; se usará GRAPH_BACKEND=local")
		}
		c.GraphBackend = "local"
	}
	return c
}

// Validate returns configuration errors, mirroring Config.validate().
func (c *Config) Validate() []string {
	var errors []string
	if c.LLMAPIKey == "" {
		errors = append(errors, "LLM_API_KEY 未配置")
	}
	if c.GraphBackend != "local" && c.GraphBackend != "" {
		errors = append(errors, "GRAPH_BACKEND="+c.GraphBackend+" no está implementado en el binario Go (solo 'local'); usa GRAPH_BACKEND=local")
	}
	if c.DBDSN == "" {
		errors = append(errors, "DB_DSN/DB_NAME 未配置：el grafo requiere MySQL 8 o MariaDB (no hay fallback a SQLite)")
	}
	if os.Getenv("ZEP_API_URL") != "" {
		errors = append(errors, "ZEP_API_URL 不受支持；usa GRAPH_BACKEND=local o Zep Cloud con ZEP_API_KEY")
	}
	if c.Debug {
		fmt.Println("warning: FLASK_DEBUG está activado; no usar en producción")
	}
	return errors
}

// UseLocalGraph reports whether the local graph backend is active.
func (c *Config) UseLocalGraph() bool {
	return c.GraphBackend == "local"
}

// databaseDSN compone la cadena de conexión MySQL desde .env. DB_DSN manda
// (necesario si la contraseña lleva caracteres raros); si no, se arma con las
// variables sueltas. Las fechas se guardan como texto ISO, no como DATETIME.
func databaseDSN() string {
	if dsn := strings.TrimSpace(os.Getenv("DB_DSN")); dsn != "" {
		return dsn
	}
	name := strings.TrimSpace(os.Getenv("DB_NAME"))
	if name == "" {
		return ""
	}
	host := getenv("DB_HOST", "127.0.0.1")
	port := getenv("DB_PORT", "3306")
	user := getenv("DB_USER", "root")
	pass := os.Getenv("DB_PASSWORD")
	return fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?charset=utf8mb4&collation=utf8mb4_bin&parseTime=false",
		user, pass, host, port, name)
}
