package httpx

import "testing"

func TestPublicPath(t *testing.T) {
	public := []string{"/", "/index.html", "/assets/app.js", "/proyecto/1", "/health", "/ready", "/icon.png"}
	for _, p := range public {
		if !publicPath(p) {
			t.Errorf("%q debería ser público", p)
		}
	}
	protected := []string{"/api", "/api/", "/api/graph/project/list", "/api/simulation/list", "/api/report/list"}
	for _, p := range protected {
		if publicPath(p) {
			t.Errorf("%q debería exigir clave", p)
		}
	}
}
