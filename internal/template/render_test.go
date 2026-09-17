package template

import (
	"reflect"
	"testing"
)

func TestRenderBasic(t *testing.T) {
	out, err := Render("hello {{name}}", map[string]string{"name": "world"})
	if err != nil {
		t.Fatal(err)
	}
	if out != "hello world" {
		t.Fatalf("got %q", out)
	}
}

func TestRenderDefault(t *testing.T) {
	out, err := Render("env={{env:dev}}", nil)
	if err != nil {
		t.Fatal(err)
	}
	if out != "env=dev" {
		t.Fatalf("got %q", out)
	}

	out, err = Render("env={{env:dev}}", map[string]string{"env": "prod"})
	if err != nil {
		t.Fatal(err)
	}
	if out != "env=prod" {
		t.Fatalf("got %q", out)
	}
}

func TestRenderMissing(t *testing.T) {
	_, err := Render("{{a}} {{b}} {{c:has-default}} {{a}}", map[string]string{"a": "1"})
	if err == nil {
		t.Fatal("expected missing error")
	}
	m, ok := IsMissing(err)
	if !ok {
		t.Fatalf("not a MissingError: %v", err)
	}
	if !reflect.DeepEqual(m.Keys, []string{"b"}) {
		t.Fatalf("missing keys = %v", m.Keys)
	}
}

func TestRenderUnterminated(t *testing.T) {
	_, err := Render("hello {{name", nil)
	if err == nil {
		t.Fatal("expected unterminated error")
	}
}

func TestKeysExtractsUnique(t *testing.T) {
	got := Keys("{{a}}-{{b:x}}-{{a}}")
	want := []string{"a", "b"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}
