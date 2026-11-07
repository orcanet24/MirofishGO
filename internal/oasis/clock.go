package oasis

import (
	"strconv"
	"time"
)

// Clock replica oasis.clock.clock.Clock.
//
// En Twitter el tiempo simulado es el número de ronda (get_time_step); en
// Reddit se usa time_transfer con un factor k. La rebanada implementa ambos
// porque el runner de Twitter necesita el contador y el reloj es la base del
// entorno.
type Clock struct {
	RealStart time.Time
	K         int
	TimeStep  int
}

// NewClock crea un reloj con factor k (Python: Clock(k=1)).
func NewClock(k int) *Clock {
	if k <= 0 {
		k = 1
	}
	return &Clock{RealStart: time.Now(), K: k}
}

// TimeTransfer replica time_transfer: start_time + k*(now-real_start).
func (c *Clock) TimeTransfer(now, start time.Time) time.Time {
	diff := now.Sub(c.RealStart)
	return start.Add(time.Duration(c.K) * diff)
}

// GetTimeStep replica get_time_step: el contador como string.
func (c *Clock) GetTimeStep() string {
	return strconv.Itoa(c.TimeStep)
}

// Advance incrementa el contador (env.step en Twitter).
func (c *Clock) Advance() {
	c.TimeStep++
}
