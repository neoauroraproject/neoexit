package routing

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInstall(t *testing.T) {
	dir := t.TempDir()
	if st := Check(dir); st.Installed {
		t.Fatal("empty folder reported installed")
	}
	if changed, err := Install(dir); err != nil || !changed {
		t.Fatalf("first install: changed=%v err=%v", changed, err)
	}
	if st := Check(dir); !st.Installed || !st.Current {
		t.Fatalf("after install: %+v", st)
	}
	if changed, _ := Install(dir); changed {
		t.Fatal("second install rewrote identical files")
	}
	os.WriteFile(filepath.Join(dir, "hmray.dat"), []byte("old"), 0o644)
	if st := Check(dir); !st.Installed || st.Current {
		t.Fatalf("old file: %+v", st)
	}
	if changed, _ := Install(dir); !changed {
		t.Fatal("old file not replaced")
	}
}

func TestFiles(t *testing.T) {
	for _, f := range Files {
		if f.Size < 1000 || len(f.Sum) != 12 {
			t.Fatalf("%s: %+v", f.Name, f)
		}
		if _, ok := Data(f.Name); !ok {
			t.Fatal(f.Name)
		}
	}
}
