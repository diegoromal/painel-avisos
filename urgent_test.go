package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func testApp(t *testing.T) *App {
	t.Helper()
	db, err := openDB(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return &App{db: db, hub: newHub(), wake: make(chan struct{}, 1)}
}

// Instalação antiga (sem as chaves da v1.2) passa para o novo padrão sem perder as outras configurações.
func TestUrgentSettingsMigration(t *testing.T) {
	dir := t.TempDir()
	db, err := openDB(dir)
	if err != nil {
		t.Fatal(err)
	}
	db.Exec(`DELETE FROM settings WHERE key IN ('urgent_mode','urgent_seconds','urgent_beep_repeat')`)
	db.Exec(`UPDATE settings SET value='20' WHERE key='rotate_seconds'`)
	db.Exec(`UPDATE settings SET value='0' WHERE key='sound_enabled'`)
	db.Close()
	db, err = openDB(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	st, _ := getSettings(db)
	want := map[string]string{"urgent_mode": "interleave", "urgent_seconds": "60", "urgent_beep_repeat": "1", "rotate_seconds": "20", "sound_enabled": "0"}
	for k, v := range want {
		if st[k] != v {
			t.Errorf("%s = %q, quer %q", k, st[k], v)
		}
	}
}

func TestStateCarriesUrgentFields(t *testing.T) {
	a := testApp(t)
	s, err := a.stateFor("tv")
	if err != nil {
		t.Fatal(err)
	}
	if s.UrgentMode != "interleave" || s.UrgentSeconds != 60 || !s.UrgentBeepRepeat || s.Version != version {
		t.Fatalf("padrões errados: %+v", s)
	}
	a.db.Exec(`UPDATE settings SET value='fixed' WHERE key='urgent_mode'`)
	a.db.Exec(`UPDATE settings SET value='30' WHERE key='urgent_seconds'`)
	a.db.Exec(`UPDATE settings SET value='0' WHERE key='urgent_beep_repeat'`)
	s, _ = a.stateFor("tv")
	if s.UrgentMode != "fixed" || s.UrgentSeconds != 30 || s.UrgentBeepRepeat {
		t.Fatalf("valores salvos não chegaram à TV: %+v", s)
	}
	// valores inválidos gravados direto no banco não quebram a TV
	a.db.Exec(`UPDATE settings SET value='xyz' WHERE key='urgent_mode'`)
	a.db.Exec(`UPDATE settings SET value='3' WHERE key='urgent_seconds'`)
	s, _ = a.stateFor("tv")
	if s.UrgentMode != "interleave" || s.UrgentSeconds != 60 {
		t.Fatalf("fallback errado: %+v", s)
	}
}

func TestSaveUrgentSettingsValidation(t *testing.T) {
	a := testApp(t)
	save := func(body string) int {
		r := httptest.NewRequest(http.MethodPut, "/api/admin/settings", strings.NewReader(body))
		r = r.WithContext(context.WithValue(r.Context(), userKey, &User{Username: "admin", Role: "admin"}))
		w := httptest.NewRecorder()
		a.handleSaveSettings(w, r)
		return w.Code
	}
	cases := []struct {
		body string
		code int
	}{
		{`{"urgent_mode":"fixed","urgent_seconds":"45","urgent_beep_repeat":"0"}`, 200},
		{`{"urgent_mode":"interleave","urgent_seconds":"10"}`, 200},
		{`{"urgent_seconds":"600"}`, 200},
		{`{"urgent_seconds":"9"}`, 400},
		{`{"urgent_seconds":"601"}`, 400},
		{`{"urgent_seconds":"abc"}`, 400},
		{`{"urgent_mode":"sempre"}`, 400},
		{`{"urgent_beep_repeat":"talvez"}`, 400},
		{`{"sound_enabled":"2"}`, 400},
	}
	for _, c := range cases {
		if got := save(c.body); got != c.code {
			t.Errorf("%s → HTTP %d, quer %d", c.body, got, c.code)
		}
	}
	st, _ := getSettings(a.db)
	if st["urgent_mode"] != "interleave" || st["urgent_seconds"] != "600" || st["urgent_beep_repeat"] != "0" {
		t.Errorf("estado final inesperado: %v", st)
	}
}
