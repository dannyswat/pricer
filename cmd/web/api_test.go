package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func post(t *testing.T, path, body string) (int, map[string]any) {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	newHandler().ServeHTTP(rec, req)
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("%s: bad JSON %q: %v", path, rec.Body.String(), err)
	}
	return rec.Code, out
}

func premium(t *testing.T, body string) float64 {
	t.Helper()
	code, out := post(t, "/api/price", body)
	if code != http.StatusOK {
		t.Fatalf("status %d: %v", code, out)
	}
	return out["headline"].([]any)[0].(map[string]any)["value"].(float64)
}

func TestPutMatchesPricerDefaults(t *testing.T) {
	// The CLI prints 6.003998 for the default 1-year at-the-money put.
	got := premium(t, `{"product":"put"}`)
	if got < 6.0039 || got > 6.0041 {
		t.Fatalf("put premium = %v, want 6.003998", got)
	}
}

func TestDownInPutMovesWithBarrierAndStrike(t *testing.T) {
	vanilla := premium(t, `{"product":"put","strike":100}`)
	far := premium(t, `{"product":"down-in-put","strike":100,"barrier":50}`)
	near := premium(t, `{"product":"down-in-put","strike":100,"barrier":95}`)
	if !(far < near && near < vanilla) {
		t.Fatalf("want far barrier %v < near barrier %v < vanilla %v", far, near, vanilla)
	}
	if hi := premium(t, `{"product":"down-in-put","strike":110,"barrier":90}`); hi <= premium(t, `{"product":"down-in-put","strike":100,"barrier":90}`) {
		t.Fatalf("higher strike should cost more, got %v", hi)
	}
}

func TestKnockedInStatus(t *testing.T) {
	_, out := post(t, "/api/price", `{"product":"down-in-put","spot":85,"barrier":90}`)
	if out["status"] != "Knocked in" {
		t.Fatalf("status = %v, want Knocked in", out["status"])
	}
}

func TestCurveIncludesPayoffAndMarkers(t *testing.T) {
	code, out := post(t, "/api/curve", `{"product":"down-in-put"}`)
	if code != http.StatusOK {
		t.Fatalf("status %d: %v", code, out)
	}
	if len(out["points"].([]any)) < 10 || len(out["markers"].([]any)) != 2 {
		t.Fatalf("unexpected curve: %v", out)
	}
}

func TestBasketAcceptsUnderliers(t *testing.T) {
	body := `{"product":"worst-of-down-in-put","paths":2000,"underliers":[{"spot":100,"initial":100,"vol":0.2}]}`
	if code, out := post(t, "/api/price", body); code != http.StatusOK {
		t.Fatalf("status %d: %v", code, out)
	}
}

func TestErrors(t *testing.T) {
	for name, body := range map[string]string{
		"unknown product": `{"product":"nope"}`,
		"bad date":        `{"product":"put","expiry":"tomorrow"}`,
		"negative spot":   `{"product":"put","spot":-1}`,
		"too many paths":  `{"product":"worst-of-put","paths":99999999}`,
		"empty basket":    `{"product":"worst-of-put","underliers":[]}`,
	} {
		if code, out := post(t, "/api/price", body); code < 400 || out["error"] == nil {
			t.Errorf("%s: status %d, body %v", name, code, out)
		}
	}
}
