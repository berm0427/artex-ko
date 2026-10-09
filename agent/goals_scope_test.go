package agent

import "testing"

func TestScopeDeclaredInTaskText(t *testing.T) {
	task := "합성 서버 http://127.0.0.1:9090만 검사; app.example.com도 범위에 포함"
	for _, tc := range []struct {
		value string
		want  bool
	}{
		{"127.0.0.1", true},
		{"app.example.com", true},
		{"example.com", false},
		{"127.0.0.11", false},
		{"lab.invoice.example.com", false},
		{"", false},
	} {
		if got := scopeDeclaredInTaskText(tc.value, task); got != tc.want {
			t.Errorf("scopeDeclaredInTaskText(%q) = %v, want %v", tc.value, got, tc.want)
		}
	}
}
