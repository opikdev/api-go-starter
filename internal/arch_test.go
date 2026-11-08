package internal_test

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

type pkg struct {
	ImportPath string
	Imports    []string
}

func packages(t *testing.T) []pkg {
	t.Helper()
	out, err := exec.Command("go", "list", "-json=ImportPath,Imports", "github.com/opikdev/api-go-starter/...").Output()
	if err != nil {
		t.Fatal(err)
	}
	var list []pkg
	decoder := json.NewDecoder(strings.NewReader(string(out)))
	for decoder.More() {
		var p pkg
		if err := decoder.Decode(&p); err != nil {
			t.Fatal(err)
		}
		list = append(list, p)
	}
	return list
}

func TestDependenciesPointOneWay(t *testing.T) {
	const module = "github.com/opikdev/api-go-starter/internal/"
	forbidden := map[string][]string{
		"store":     {"net/http", module + "app", module + "api"},
		"auth":      {"net/http", module + "app", module + "store"},
		"publicid":  {module},
		"problem":   {module},
		"config":    {module},
		"database":  {"net/http", module + "app", module + "api"},
		"api":       {module + "app", module + "store"},
		"apiclient": {module},
	}
	found := 0
	for _, p := range packages(t) {
		name := strings.TrimPrefix(p.ImportPath, module)
		rules, ok := forbidden[name]
		if !ok {
			continue
		}
		found++
		for _, imported := range p.Imports {
			for _, rule := range rules {
				if strings.HasPrefix(imported, rule) {
					t.Errorf("%s must not import %s", name, imported)
				}
			}
		}
	}
	if found != len(forbidden) {
		t.Fatalf("checked %d of %d packages", found, len(forbidden))
	}
}
