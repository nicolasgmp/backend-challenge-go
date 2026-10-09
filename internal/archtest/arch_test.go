package archtest

import (
	"os/exec"
	"slices"
	"strings"
	"testing"
)

const (
	module       = "jungle-gaming-challeng"
	domainRoot   = module + "/internal/domain"
	appRoot      = module + "/internal/app"
	infraRoot    = module + "/internal/infra"
	httpapiRoot  = infraRoot + "/httpapi"
	postgresRoot = infraRoot + "/postgres"
	sqsRoot      = infraRoot + "/sqs"
	uuidModule   = "github.com/google/uuid"
)

var domainGraph = map[string][]string{
	"money":   {},
	"ids":     {},
	"failure": {},
	"ledger":  {"money", "ids"},
	"wallet":  {"money", "ids", "ledger"},
	"wager":   {"money", "ids", "failure", "ledger"},
	"events":  {"money", "ids", "ledger", "failure"},
}

func inside(pkg, root string) bool {
	return pkg == root || strings.HasPrefix(pkg, root+"/")
}

func domainName(pkg string) string {
	return strings.TrimPrefix(pkg, domainRoot+"/")
}

func isThirdParty(pkg string) bool {
	first, _, _ := strings.Cut(pkg, "/")
	return strings.Contains(first, ".")
}

func forbiddenInDomain(pkg, imported string) string {
	if inside(imported, domainRoot) {
		if pkg == imported || slices.Contains(domainGraph[domainName(pkg)], domainName(imported)) {
			return ""
		}
		return "outside the domain graph"
	}
	if inside(imported, module) {
		return "domain must not import outer layers"
	}
	if inside(imported, "net/http") || (isThirdParty(imported) && imported != uuidModule) {
		return "domain must stay free of frameworks and drivers"
	}
	return ""
}

func forbidden(pkg, imported string) string {
	switch {
	case inside(pkg, domainRoot):
		return forbiddenInDomain(pkg, imported)
	case inside(pkg, appRoot) && inside(imported, infraRoot):
		return "app must not import infra"
	case inside(pkg, httpapiRoot) && (inside(imported, postgresRoot) || inside(imported, sqsRoot)):
		return "httpapi must not import postgres or sqs"
	default:
		return ""
	}
}

func violations(imports map[string][]string) []string {
	var found []string
	for pkg, list := range imports {
		for _, imported := range list {
			if reason := forbidden(pkg, imported); reason != "" {
				found = append(found, pkg+" imports "+imported+": "+reason)
			}
		}
	}
	slices.Sort(found)
	return found
}

func listImports(t *testing.T) map[string][]string {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), "go", "list", "-f", `{{.ImportPath}}{{range .Imports}} {{.}}{{end}}{{range .TestImports}} {{.}}{{end}}{{range .XTestImports}} {{.}}{{end}}`, "./...")
	cmd.Dir = "../.."
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list: %v", err)
	}

	imports := map[string][]string{}
	for line := range strings.Lines(string(output)) {
		fields := strings.Fields(line)
		if len(fields) > 0 {
			imports[fields[0]] = fields[1:]
		}
	}
	return imports
}

func TestArchitecture(t *testing.T) {
	imports := listImports(t)
	if len(imports[domainRoot+"/wager"]) == 0 {
		t.Fatal("go list did not return the domain packages")
	}
	for _, violation := range violations(imports) {
		t.Error(violation)
	}
}
