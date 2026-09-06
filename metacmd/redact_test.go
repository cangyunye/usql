package metacmd

import "testing"

func TestRedactDSN(t *testing.T) {
	cases := []struct {
		dsn  string
		want string
	}{
		{
			"sys@tenant:pass@@word1@tcp(localhost:2881)/",
			"sys@tenant:xxxxx@tcp(localhost:2881)/",
		},
		{
			"root:notarealpw@tcp(10.0.0.1:3306)/test",
			"root:xxxxx@tcp(10.0.0.1:3306)/test",
		},
		{
			"postgres://someuser:secret@db.example.com:5432/test?sslmode=disable",
			"postgres://someuser:xxxxx@db.example.com:5432/test?sslmode=disable",
		},
		{
			"host=localhost password=s3cret port=5432 dbname=test",
			"host=localhost password=xxxxx port=5432 dbname=test",
		},
		{
			"postgres://someuser@db.example.com:5432/test",
			"postgres://someuser@db.example.com:5432/test",
		},
		{
			"/path/to/db.sqlite3",
			"/path/to/db.sqlite3",
		},
	}
	for _, c := range cases {
		if got := redactDSN(c.dsn); got != c.want {
			t.Errorf("redactDSN(%q) = %q, want %q", c.dsn, got, c.want)
		}
	}
}
