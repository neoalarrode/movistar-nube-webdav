package config

import (
	"testing"
	"time"
)

func TestEnvHelpers(t *testing.T) {
	t.Setenv("X_SEC", "0.5")
	if got := envSeconds("X_SEC", time.Minute); got != 500*time.Millisecond {
		t.Errorf("envSeconds(0.5) = %v", got)
	}
	for _, bad := range []string{"", "abc", "-3", "0", "NaN", "Inf"} {
		t.Setenv("X_SEC", bad)
		if got := envSeconds("X_SEC", time.Minute); got != time.Minute {
			t.Errorf("envSeconds(%q) = %v, esperado el valor por defecto", bad, got)
		}
	}
	t.Setenv("X_INT", "12")
	if envInt("X_INT", 3) != 12 {
		t.Error("envInt(12)")
	}
	t.Setenv("X_INT", "-1")
	if envInt("X_INT", 3) != 3 {
		t.Error("envInt(-1) debía dar el valor por defecto")
	}
	t.Setenv("X_BOOL", "off")
	if envBool("X_BOOL", true) {
		t.Error("envBool(off)")
	}
	t.Setenv("X_BOOL", "quizá")
	if !envBool("X_BOOL", true) {
		t.Error("envBool(no reconocido) debía dar el valor por defecto")
	}
}
