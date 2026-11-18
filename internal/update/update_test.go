package update

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNewer(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"v1.2.1", "1.2.0", true},
		{"v1.2.1", "v1.2.1", false},
		{"v1.2.1", "1.2.1-dev", false},
		{"v1.10.0", "v1.9.9", true},
		{"v2.0.0", "v1.99.99", true},
		{"v1.2.0", "v1.2.1", false},
		{"junk", "v1.0.0", false},
		{"v1.3.0", "junk", false},
	} {
		if got := Newer(c.a, c.b); got != c.want {
			t.Errorf("Newer(%q, %q) = %v", c.a, c.b, got)
		}
	}
}

func TestCheck(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") == "" {
			t.Error("no User-Agent: GitHub's API requires one")
		}
		w.Write([]byte(`{"tag_name":"v9.0.0","body":"notes","html_url":"https://example/r"}`))
	}))
	defer srv.Close()
	old := apiURL
	apiURL = srv.URL
	defer func() { apiURL = old }()

	u := &Updater{Current: "v1.2.1"}
	if err := u.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	st := u.Status()
	if !st.Newer || st.Latest != "v9.0.0" || st.Notes != "notes" {
		t.Fatalf("%+v", st)
	}
	if err := (&Updater{Current: "v9.0.0"}).Apply(); err == nil {
		t.Fatal("applied with nothing newer")
	}
}
