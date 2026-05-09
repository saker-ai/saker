package dialect

import (
	"errors"
	"testing"
)

func TestParseDSN(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in       string
		wantSch  string
		wantBody string
		wantErr  bool
	}{
		{"/var/saker/app.db", "sqlite", "/var/saker/app.db", false},
		{"./relative.db", "sqlite", "./relative.db", false},
		{"sqlite:///abs/path.db", "sqlite", "/abs/path.db", false},
		{"sqlite::memory:", "sqlite", ":memory:", false},
		{"file:/abs/foo.db", "sqlite", "/abs/foo.db", false},
		{"postgres://u:p@h/db", "postgres", "postgres://u:p@h/db", false},
		{"postgresql://u:p@h/db?sslmode=disable", "postgres", "postgresql://u:p@h/db?sslmode=disable", false},
		{"", "", "", true},
		{"mysql://u:p@h/db", "", "", true},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			scheme, body, err := ParseDSN(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if scheme != tc.wantSch || body != tc.wantBody {
				t.Fatalf("got (%q, %q) want (%q, %q)", scheme, body, tc.wantSch, tc.wantBody)
			}
		})
	}
}

func TestResolve(t *testing.T) {
	t.Parallel()
	d, err := Resolve("sqlite")
	if err != nil {
		t.Fatalf("sqlite resolve: %v", err)
	}
	if d.Name() != "sqlite" {
		t.Fatalf("want sqlite, got %s", d.Name())
	}
	if _, err := Resolve("nope"); !errors.Is(err, ErrUnknownDialect) {
		t.Fatalf("expected ErrUnknownDialect, got %v", err)
	}
}
