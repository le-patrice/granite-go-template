package main

import (
	"strings"
	"testing"
)

func TestProcessInlineMarkers_Success(t *testing.T) {
	input := `package main

import (
	"fmt"
	// template:example:begin orders
	"example.com/app/internal/orders"
	// template:example:end orders
)

func main() {
	// template:example:begin orders
	orders.DoSomething()
	// template:example:end orders
	fmt.Println("core")
}
`
	want := `package main

import (
	"fmt"
)

func main() {
	fmt.Println("core")
}
`

	got, changed, err := processInlineMarkers(input, "test.go")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !changed {
		t.Fatalf("expected changed=true")
	}
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestProcessInlineMarkers_DifferentCommentStyles(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name: "yaml style",
			input: `key: value
# template:example:begin acme
acme_secret: 12345
# template:example:end acme
other: true
`,
			want: `key: value
other: true
`,
		},
		{
			name: "sql style",
			input: `SELECT 1;
-- template:example:begin orders
SELECT * FROM orders;
-- template:example:end orders
SELECT 2;
`,
			want: `SELECT 1;
SELECT 2;
`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, changed, err := processInlineMarkers(tc.input, "test")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !changed {
				t.Fatalf("expected changed=true")
			}
			if got != tc.want {
				t.Errorf("got:\n%s\nwant:\n%s", got, tc.want)
			}
		})
	}
}

func TestProcessInlineMarkers_Errors(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		errSubstr string
	}{
		{
			name: "nested begin",
			input: `// template:example:begin outer
line 1
// template:example:begin inner
line 2
// template:example:end inner
// template:example:end outer
`,
			errSubstr: "nested template:example:begin",
		},
		{
			name: "unclosed begin",
			input: `// template:example:begin unclosed
line 1
`,
			errSubstr: "unclosed template:example:begin",
		},
		{
			name: "end without begin",
			input: `line 1
// template:example:end stray
`,
			errSubstr: "template:example:end without matching begin",
		},
		{
			name: "mismatched tags",
			input: `// template:example:begin foo
line 1
// template:example:end bar
`,
			errSubstr: "mismatched template:example:end",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := processInlineMarkers(tc.input, "test.go")
			if err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tc.errSubstr) {
				t.Errorf("expected error containing %q, got: %v", tc.errSubstr, err)
			}
		})
	}
}

func TestClassifyPath(t *testing.T) {
	rules := []OwnershipRule{
		{Prefix: "deployments/", Tier: "template"},
		{Prefix: "Makefile", Tier: "template"},
		{Prefix: "internal/app/", Tier: "shared"},
		{Prefix: "internal/orders/", Tier: "project"},
	}

	cases := []struct {
		path string
		want string
	}{
		{"deployments/Dockerfile", "template"},
		{"Makefile", "template"},
		{"internal/app/api.go", "shared"},
		{"internal/orders/handler.go", "project"},
		{"unknown/file.txt", "project"},
	}

	for _, c := range cases {
		got := classifyPath(c.path, rules)
		if got != c.want {
			t.Errorf("classifyPath(%q) = %q; want %q", c.path, got, c.want)
		}
	}
}

func TestToPascalCase(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"billing", "Billing"},
		{"order_item", "OrderItem"},
		{"customer_accounts", "CustomerAccounts"},
		{"api", "Api"},
	}

	for _, c := range cases {
		got := toPascalCase(c.input)
		if got != c.want {
			t.Errorf("toPascalCase(%q) = %q; want %q", c.input, got, c.want)
		}
	}
}

func TestValidFeatureName(t *testing.T) {
	valid := []string{"billing", "order", "order_item", "v2_accounts"}
	invalid := []string{"", "123billing", "Billing", "order-item", "billing$"}

	for _, v := range valid {
		if !validFeatureName.MatchString(v) {
			t.Errorf("expected %q to be valid", v)
		}
	}
	for _, inv := range invalid {
		if validFeatureName.MatchString(inv) {
			t.Errorf("expected %q to be invalid", inv)
		}
	}
}
