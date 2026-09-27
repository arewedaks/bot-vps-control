package main

import "testing"

// buildStatsMessage dan buildSysInfoMessage tetap di package main: keduanya
// memanggil lapisan pembacaan sumber daya sistem yang belum dipisah. Test-nya
// ikut di sini, dekat dengan yang diuji.

// TestBuildersTidakPanic memastikan pembangun pesan tidak crash.
func TestBuildersTidakPanic(t *testing.T) {
	for nama, fn := range map[string]func() string{
		"buildStatsMessage":   buildStatsMessage,
		"buildSysInfoMessage": buildSysInfoMessage,
	} {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("%s panic: %v", nama, r)
				}
			}()
			out := fn()
			if out == "" {
				t.Errorf("%s menghasilkan string kosong", nama)
			}
		}()
	}
}
