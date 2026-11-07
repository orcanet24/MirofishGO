// Loggers del Report Agent: agent_log.jsonl (estructurado) y console_log.txt
// (texto plano), con el mismo formato que el backend Python.
package report

import (
	"fmt"
	"math"
	"time"

	"mirofish/internal/timex"
)

type agentLogger struct {
	manager  *Manager
	reportID string
	start    time.Time
}

func newAgentLogger(m *Manager, reportID string) *agentLogger {
	return &agentLogger{manager: m, reportID: reportID, start: time.Now()}
}

// log escribe una entrada JSONL con timestamp/elapsed/details.
func (l *agentLogger) log(action, stage string, sectionTitle *string, sectionIndex *int, details map[string]any) {
	if l == nil || l.manager == nil {
		return
	}
	if details == nil {
		details = map[string]any{}
	}
	entry := map[string]any{
		"timestamp":       timex.Now(),
		"elapsed_seconds": math.Round(time.Since(l.start).Seconds()*100) / 100,
		"report_id":       l.reportID,
		"action":          action,
		"stage":           stage,
		"section_title":   sectionTitle,
		"section_index":   sectionIndex,
		"details":         details,
	}
	_ = l.manager.AppendAgentLog(l.reportID, entry)
}

type consoleLogger struct {
	manager  *Manager
	reportID string
}

func newConsoleLogger(m *Manager, reportID string) *consoleLogger {
	return &consoleLogger{manager: m, reportID: reportID}
}

func (c *consoleLogger) write(level, msg string) {
	if c == nil || c.manager == nil {
		return
	}
	line := fmt.Sprintf("[%s] %s: %s", time.Now().Format("15:04:05"), level, msg)
	_ = c.manager.AppendConsoleLog(c.reportID, line)
}

func (c *consoleLogger) Info(format string, args ...any) {
	c.write("INFO", fmt.Sprintf(format, args...))
}

func (c *consoleLogger) Warning(format string, args ...any) {
	c.write("WARNING", fmt.Sprintf(format, args...))
}

func (c *consoleLogger) Error(format string, args ...any) {
	c.write("ERROR", fmt.Sprintf(format, args...))
}

// Close replica close(): no hay handler que desregistrar.
func (c *consoleLogger) Close() {}
