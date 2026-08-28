package handlers

import "testing"

func TestRedactEnvJSON(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty string", "", "{}"},
		{"malformed json", "not json", "{}"},
		{"non-object json", `["a","b"]`, "{}"},
		{"single key", `{"ANTHROPIC_API_KEY":"sk-test"}`, `{"ANTHROPIC_API_KEY":"***"}`},
		{"empty value preserved", `{"FOO":""}`, `{"FOO":""}`},
		{"mixed empty and set", `{"FOO":"","BAR":"secret"}`, `{"BAR":"***","FOO":""}`},
		{"empty object", `{}`, `{}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := redactEnvJSON(c.in)
			if got != c.want {
				t.Errorf("redactEnvJSON(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestMergeEnvJSON(t *testing.T) {
	t.Run("sentinel preserves existing value", func(t *testing.T) {
		got, err := mergeEnvJSON(`{"KEY":"***"}`, `{"KEY":"real-secret"}`)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != `{"KEY":"real-secret"}` {
			t.Errorf("got %q", got)
		}
	})

	t.Run("new value replaces existing", func(t *testing.T) {
		got, err := mergeEnvJSON(`{"KEY":"new-value"}`, `{"KEY":"old-value"}`)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != `{"KEY":"new-value"}` {
			t.Errorf("got %q", got)
		}
	})

	t.Run("key omitted from incoming is deleted", func(t *testing.T) {
		got, err := mergeEnvJSON(`{"KEEP":"***"}`, `{"KEEP":"a","DROP":"b"}`)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != `{"KEEP":"a"}` {
			t.Errorf("got %q", got)
		}
	})

	t.Run("sentinel for a key not in existing is dropped", func(t *testing.T) {
		got, err := mergeEnvJSON(`{"NEW":"***"}`, `{}`)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != `{}` {
			t.Errorf("got %q", got)
		}
	})

	t.Run("empty value is a legitimate write", func(t *testing.T) {
		got, err := mergeEnvJSON(`{"KEY":""}`, `{"KEY":"was-set"}`)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != `{"KEY":""}` {
			t.Errorf("got %q", got)
		}
	})

	t.Run("malformed incoming errors", func(t *testing.T) {
		_, err := mergeEnvJSON("not json", "{}")
		if err == nil {
			t.Fatal("expected error for malformed incoming env")
		}
	})

	t.Run("malformed existing treated as empty", func(t *testing.T) {
		got, err := mergeEnvJSON(`{"KEY":"***"}`, "not json")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// KEY isn't in the (empty-treated) existing map, so sentinel drops it.
		if got != `{}` {
			t.Errorf("got %q", got)
		}
	})
}
