package perception

import "testing"

func TestInferProjectFromPath(t *testing.T) {
	tests := []struct {
		name string
		path string
		want string
	}{
		{"Projects dir", "/home/user/Projects/felixlm/train.py", "felixlm"},
		{"Projects nested", "/home/user/Projects/mnemonic/internal/agent/foo.go", "mnemonic"},
		{"src dir", "/home/user/src/webapp/index.js", "webapp"},
		{"repos dir", "/home/user/repos/mylib/lib.go", "mylib"},
		{"workspace dir", "/home/user/workspace/tool/main.go", "tool"},
		{"no project parent", "/etc/nginx/nginx.conf", ""},
		{"empty path", "", ""},
		{"just Projects dir", "/home/user/Projects", ""},
		{"lowercase projects", "/home/user/projects/app/main.go", "app"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := inferProjectFromPath(tt.path)
			if got != tt.want {
				t.Errorf("inferProjectFromPath(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}
