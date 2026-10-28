package reviewbrief

import "testing"

const sampleDiff = `diff --git a/a.go b/a.go
index 111..222 100644
--- a/a.go
+++ b/a.go
@@ -1,2 +1,3 @@
 keep
-old
-- a sql comment
+new
+new2
diff --git a/new.txt b/new.txt
new file mode 100644
--- /dev/null
+++ b/new.txt
@@ -0,0 +1 @@
+hello
diff --git a/gone_test.go b/gone_test.go
deleted file mode 100644
--- a/gone_test.go
+++ /dev/null
@@ -1 +0,0 @@
-func TestX(t *testing.T) {}
diff --git a/o.go b/n.go
similarity index 100%
rename from o.go
rename to n.go
diff --git a/img.png b/img.png
Binary files a/img.png and b/img.png differ
`

func TestParseUnifiedDiff(t *testing.T) {
	fs := ParseUnifiedDiff(sampleDiff)
	if len(fs) != 5 {
		t.Fatalf("files = %d", len(fs))
	}
	want := []struct {
		path, status string
		add, rem     int
	}{
		{"a.go", "modified", 2, 2}, {"new.txt", "added", 1, 0}, {"gone_test.go", "deleted", 0, 1},
		{"n.go", "renamed", 0, 0}, {"img.png", "modified", 0, 0},
	}
	for i, w := range want {
		g := fs[i]
		if g.Path != w.path || g.Status != w.status || g.Added != w.add || g.Removed != w.rem {
			t.Errorf("file %d = %+v, want %+v", i, g, w)
		}
	}
	if fs[3].OldPath != "o.go" {
		t.Errorf("old path = %q", fs[3].OldPath)
	}
	if fs[0].RemovedLines[1] != "- a sql comment" {
		t.Errorf("removed lines = %q", fs[0].RemovedLines)
	}
}
