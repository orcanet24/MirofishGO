// Port de backend/app/services/simulation_ipc.py — comunicación por archivos
// entre el backend y el proceso de simulación.
package simx

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// CommandType replica el enum de comandos.
type CommandType string

const (
	CommandInterview     CommandType = "interview"
	CommandBatchInterview CommandType = "batch_interview"
	CommandCloseEnv      CommandType = "close_env"
)

// CommandStatus replica el enum de estados.
type CommandStatus string

const (
	StatusPending    CommandStatus = "pending"
	StatusProcessing CommandStatus = "processing"
	StatusCompleted  CommandStatus = "completed"
	StatusFailed     CommandStatus = "failed"
)

// IPCCommand replica el dataclass IPCCommand.
type IPCCommand struct {
	CommandID   string         `json:"command_id"`
	CommandType CommandType    `json:"command_type"`
	Args        map[string]any `json:"args"`
	Timestamp   string         `json:"timestamp"`
}

// IPCResponse replica el dataclass IPCResponse.
type IPCResponse struct {
	CommandID string         `json:"command_id"`
	Status    CommandStatus  `json:"status"`
	Result    map[string]any `json:"result"`
	Error     *string        `json:"error"`
	Timestamp string         `json:"timestamp"`
}

// IPCClient replica SimulationIPCClient (lado backend).
type IPCClient struct {
	SimulationDir string
	CommandsDir   string
	ResponsesDir  string
}

// NewIPCClient crea el cliente para el directorio de simulación.
func NewIPCClient(simulationDir string) *IPCClient {
	c := &IPCClient{
		SimulationDir: simulationDir,
		CommandsDir:   filepath.Join(simulationDir, "ipc_commands"),
		ResponsesDir:  filepath.Join(simulationDir, "ipc_responses"),
	}
	_ = os.MkdirAll(c.CommandsDir, 0o755)
	_ = os.MkdirAll(c.ResponsesDir, 0o755)
	return c
}

// SendCommand replica send_command con espera por archivos.
func (c *IPCClient) SendCommand(commandType CommandType, args map[string]any, timeout, pollInterval float64) (*IPCResponse, error) {
	if pollInterval <= 0 {
		pollInterval = 0.5
	}
	commandID := newUUID4()
	command := IPCCommand{
		CommandID:   commandID,
		CommandType: commandType,
		Args:        args,
		Timestamp:   time.Now().Format("2006-01-02T15:04:05.999999"),
	}
	commandFile := filepath.Join(c.CommandsDir, commandID+".json")
	b, _ := json.MarshalIndent(command, "", "  ")
	if err := os.WriteFile(commandFile, b, 0o644); err != nil {
		return nil, err
	}
	logger.Info(fmt.Sprintf("Enviando comando IPC: %s, command_id=%s", commandType, commandID))

	responseFile := filepath.Join(c.ResponsesDir, commandID+".json")
	start := time.Now()
	for time.Since(start).Seconds() < timeout {
		if _, err := os.Stat(responseFile); err == nil {
			raw, err := os.ReadFile(responseFile)
			if err == nil {
				var data map[string]any
				if json.Unmarshal(raw, &data) == nil {
					response := ipcResponseFromMap(data)
					_ = os.Remove(commandFile)
					_ = os.Remove(responseFile)
					logger.Info(fmt.Sprintf("Respuesta IPC recibida: command_id=%s, status=%s", commandID, response.Status))
					return response, nil
				}
			}
			logger.Warn("Error al analizar la respuesta")
		}
		time.Sleep(time.Duration(pollInterval * float64(time.Second)))
	}
	logger.Error(fmt.Sprintf("Tiempo de espera de respuesta IPC agotado: command_id=%s", commandID))
	_ = os.Remove(commandFile)
	return nil, fmt.Errorf("Tiempo de espera de respuesta del comando agotado (%v segundos)", timeout)
}

// SendInterview replica send_interview.
func (c *IPCClient) SendInterview(agentID int, prompt, platform string, timeout float64) (*IPCResponse, error) {
	args := map[string]any{"agent_id": agentID, "prompt": prompt}
	if platform != "" {
		args["platform"] = platform
	}
	return c.SendCommand(CommandInterview, args, timeout, 0.5)
}

// SendBatchInterview replica send_batch_interview.
func (c *IPCClient) SendBatchInterview(interviews []map[string]any, platform string, timeout float64) (*IPCResponse, error) {
	args := map[string]any{"interviews": interviews}
	if platform != "" {
		args["platform"] = platform
	}
	return c.SendCommand(CommandBatchInterview, args, timeout, 0.5)
}

// SendCloseEnv replica send_close_env.
func (c *IPCClient) SendCloseEnv(timeout float64) (*IPCResponse, error) {
	return c.SendCommand(CommandCloseEnv, map[string]any{}, timeout, 0.5)
}

// CheckEnvAlive replica check_env_alive.
func (c *IPCClient) CheckEnvAlive() bool {
	raw, err := os.ReadFile(filepath.Join(c.SimulationDir, "env_status.json"))
	if err != nil {
		return false
	}
	var status map[string]any
	if json.Unmarshal(raw, &status) != nil {
		return false
	}
	s, _ := status["status"].(string)
	return s == "alive"
}

// IPCServer replica SimulationIPCServer (lado del proceso de simulación).
type IPCServer struct {
	SimulationDir string
	CommandsDir   string
	ResponsesDir  string
	running       bool
}

// NewIPCServer crea el servidor para el directorio de simulación.
func NewIPCServer(simulationDir string) *IPCServer {
	s := &IPCServer{
		SimulationDir: simulationDir,
		CommandsDir:   filepath.Join(simulationDir, "ipc_commands"),
		ResponsesDir:  filepath.Join(simulationDir, "ipc_responses"),
	}
	_ = os.MkdirAll(s.CommandsDir, 0o755)
	_ = os.MkdirAll(s.ResponsesDir, 0o755)
	return s
}

func (s *IPCServer) Start() {
	s.running = true
	s.updateEnvStatus("alive")
}

func (s *IPCServer) Stop() {
	s.running = false
	s.updateEnvStatus("stopped")
}

func (s *IPCServer) updateEnvStatus(status string) {
	b, _ := json.MarshalIndent(map[string]any{
		"status":    status,
		"timestamp": time.Now().Format("2006-01-02T15:04:05.999999"),
	}, "", "  ")
	_ = os.WriteFile(filepath.Join(s.SimulationDir, "env_status.json"), b, 0o644)
}

// PollCommands replica poll_commands: el comando más antiguo primero.
func (s *IPCServer) PollCommands() *IPCCommand {
	entries, err := os.ReadDir(s.CommandsDir)
	if err != nil {
		return nil
	}
	type fileTime struct {
		path    string
		modTime time.Time
	}
	files := []fileTime{}
	for _, e := range entries {
		if e.IsDir() || !hasSuffix(e.Name(), ".json") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		files = append(files, fileTime{filepath.Join(s.CommandsDir, e.Name()), info.ModTime()})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].modTime.Before(files[j].modTime) })
	for _, f := range files {
		raw, err := os.ReadFile(f.path)
		if err != nil {
			continue
		}
		var data map[string]any
		if json.Unmarshal(raw, &data) != nil {
			continue
		}
		return ipcCommandFromMap(data)
	}
	return nil
}

func (s *IPCServer) sendResponse(response *IPCResponse) {
	b, _ := json.MarshalIndent(response, "", "  ")
	_ = os.WriteFile(filepath.Join(s.ResponsesDir, response.CommandID+".json"), b, 0o644)
	_ = os.Remove(filepath.Join(s.CommandsDir, response.CommandID+".json"))
}

// SendSuccess replica send_success.
func (s *IPCServer) SendSuccess(commandID string, result map[string]any) {
	s.sendResponse(&IPCResponse{
		CommandID: commandID,
		Status:    StatusCompleted,
		Result:    result,
		Timestamp: time.Now().Format("2006-01-02T15:04:05.999999"),
	})
}

// SendError replica send_error.
func (s *IPCServer) SendError(commandID string, errorMsg string) {
	s.sendResponse(&IPCResponse{
		CommandID: commandID,
		Status:    StatusFailed,
		Error:     &errorMsg,
		Timestamp: time.Now().Format("2006-01-02T15:04:05.999999"),
	})
}

// ===== helpers =====

func ipcCommandFromMap(data map[string]any) *IPCCommand {
	cmd := &IPCCommand{
		CommandID:   strOr(data["command_id"]),
		CommandType: CommandType(strOr(data["command_type"])),
		Args:        anyMap(data["args"]),
		Timestamp:   strOr(data["timestamp"]),
	}
	if cmd.Timestamp == "" {
		cmd.Timestamp = time.Now().Format("2006-01-02T15:04:05.999999")
	}
	return cmd
}

func ipcResponseFromMap(data map[string]any) *IPCResponse {
	resp := &IPCResponse{
		CommandID: strOr(data["command_id"]),
		Status:    CommandStatus(strOr(data["status"])),
		Result:    anyMap(data["result"]),
		Timestamp: strOr(data["timestamp"]),
	}
	if s, ok := data["error"].(string); ok {
		resp.Error = &s
	}
	if resp.Timestamp == "" {
		resp.Timestamp = time.Now().Format("2006-01-02T15:04:05.999999")
	}
	return resp
}

func hasSuffix(s, suffix string) bool {
	return len(s) >= len(suffix) && s[len(s)-len(suffix):] == suffix
}

// newUUID4 genera un id v4 con guiones (como str(uuid.uuid4())).
func newUUID4() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	s := hex.EncodeToString(b[:])
	return s[:8] + "-" + s[8:12] + "-" + s[12:16] + "-" + s[16:20] + "-" + s[20:]
}
