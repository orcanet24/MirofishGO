package timex

import (
	"testing"
	"time"
)

// Python datetime.isoformat() escribe siempre 6 dígitos de microsegundos
// cuando la fracción no es cero, y los omite cuando es exactamente cero.
// El layout .999999 de Go recortaba los ceros finales y producía ".123".
func TestISOFormatMatchesPython(t *testing.T) {
	cases := []struct {
		name string
		t    time.Time
		want string
	}{
		{
			name: "microsegundos con ceros finales",
			t:    time.Date(2026, 11, 6, 12, 0, 0, 123000000, time.UTC),
			want: "2026-11-06T12:00:00.123000",
		},
		{
			name: "un microsegundo",
			t:    time.Date(2026, 11, 6, 12, 0, 0, 1000, time.UTC),
			want: "2026-11-06T12:00:00.000001",
		},
		{
			name: "sin fracción",
			t:    time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC),
			want: "2026-11-06T12:00:00",
		},
	}
	for _, tc := range cases {
		if got := ISO(tc.t); got != tc.want {
			t.Errorf("%s: ISO = %q, want %q", tc.name, got, tc.want)
		}
	}
}
