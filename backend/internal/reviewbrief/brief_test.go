package reviewbrief

import "testing"

func TestOneLine(t *testing.T) {
	cases := []struct {
		in   string
		n    int
		want string
	}{
		{"", 10, ""},
		{"\n\n  ## Add thing \nmore", 50, "Add thing"},
		{"- * item", 50, "item"},
		{"abcdefghij", 4, "abcd…"},
	}
	for _, c := range cases {
		if got := OneLine(c.in, c.n); got != c.want {
			t.Errorf("OneLine(%q,%d)=%q want %q", c.in, c.n, got, c.want)
		}
	}
}
