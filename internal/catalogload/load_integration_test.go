//go:build integration

package catalogload_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/kibanyadev/dojo/internal/storage"
)

const (
	v1 = "0123456789ab"
	v2 = "ba9876543210"
)

func envExample(t *testing.T, key string) string {
	t.Helper()
	b, err := os.ReadFile("../../.env.example")
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range strings.Split(string(b), "\n") {
		if v, ok := strings.CutPrefix(l, key+"="); ok {
			return v
		}
	}
	t.Fatalf("%s not in .env.example", key)
	return ""
}

func psql(t *testing.T, sql string) string {
	t.Helper()
	ids, err := exec.Command("docker", "ps", "-q",
		"--filter", "label=com.docker.compose.project="+os.Getenv("DOJO_COMPOSE_PROJECT"),
		"--filter", "label=com.docker.compose.service=db").Output()
	id := strings.TrimSpace(string(ids))
	if err != nil || id == "" {
		t.Fatalf("find db container: %v %q", err, ids)
	}
	out, err := exec.Command("docker", "exec", "-e", "PGPASSWORD="+envExample(t, "POSTGRES_PASSWORD"), id,
		"psql", "-h", id, "-U", "dojo", "-d", "dojo", "-v", "ON_ERROR_STOP=1", "-tA", "-c", sql).CombinedOutput()
	if err != nil {
		t.Fatalf("%s: %v %s", sql, err, out)
	}
	return strings.TrimSpace(string(out))
}

// load runs the real job, as `make catalog-load` does, against dir.
func load(t *testing.T, dir, version string) (string, error) {
	t.Helper()
	cmd := exec.Command("docker", "compose", "-p", os.Getenv("DOJO_COMPOSE_PROJECT"),
		"-f", "compose.yaml", "-f", "compose.it.yaml", "--env-file", ".env.example",
		"--profile", "jobs", "run", "--rm", "-T", "catalog-load", "-version", version)
	cmd.Dir = "../.."
	cmd.Env = append(os.Environ(), "EDGE_PORT=", "FAKE_GITHUB_PORT=", "ADMIN_PORT=", "DOJO_S3_PORT=", "CATALOG_DIR="+dir)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func wipe(t *testing.T) {
	t.Helper()
	sql := "DELETE FROM hint_opens; DELETE FROM item_completions; DELETE FROM ticket_progress; DELETE FROM learners WHERE github_id = 9101; " +
		"DELETE FROM catalog_items; DELETE FROM belt_awards; DELETE FROM belt_marks; DELETE FROM path_roles; DELETE FROM paths; DELETE FROM roles; DELETE FROM catalog_state"
	psql(t, sql)
	t.Cleanup(func() { psql(t, sql) })
}

// fixture copies the shared test catalog so a test can change it.
func fixture(t *testing.T) string {
	t.Helper()
	dst := t.TempDir()
	if out, err := exec.Command("cp", "-R", "../catalog/testdata/catalog/.", dst).CombinedOutput(); err != nil {
		t.Fatalf("copy fixture: %v %s", err, out)
	}
	// Bundles are content addressed and the store outlives a test, so give each test its own bytes.
	nonce := []byte("\n# " + t.Name() + "\n")
	checks, err := filepath.Glob(filepath.Join(dst, "*", "*", "check.sh"))
	if err != nil || len(checks) != 4 {
		t.Fatalf("fixture checks: %v %v", checks, err)
	}
	for _, c := range checks {
		b, err := os.ReadFile(c)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(c, append(b, nonce...), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dst
}

func mustLoad(t *testing.T, dir, version, want string) {
	t.Helper()
	out, err := load(t, dir, version)
	if err != nil || !strings.Contains(out, want) {
		t.Fatalf("load: %v\nwant %q in:\n%s", err, want, out)
	}
}

func TestFreshLoad(t *testing.T) {
	wipe(t)
	mustLoad(t, fixture(t), v1, "catalog "+v1+" loaded: 4 tickets, 0 practice, 4 bundles uploaded, 0 reused")

	if got := psql(t, "SELECT count(*) || ',' || count(DISTINCT bundle_key) || ',' || bool_and(catalog_version = '"+v1+"' AND kind = 'ticket') FROM catalog_items"); got != "4,4,true" {
		t.Fatalf("items: %s", got)
	}
	if got := psql(t, "SELECT (SELECT count(*) FROM paths) || ',' || (SELECT count(*) FROM roles) || ',' || (SELECT count(*) FROM path_roles) || ',' || (SELECT catalog_version FROM catalog_state WHERE id = 1)"); got != "1,2,2,"+v1 {
		t.Fatalf("structure: %s", got)
	}
	if got := psql(t, "SELECT summary FROM roles WHERE slug = 'support-l1'"); got != "Tickets, users, permissions, a calm reply" {
		t.Fatalf("summary: %q", got)
	}
	row := psql(t, "SELECT position || '|' || est_minutes || '|' || tags::text || '|' || cardinality(hints) || '|' || left(bundle_key, 28) FROM catalog_items WHERE id = 'support-l1/perms-01'")
	if row != "1|20|{permissions}|3|bundles/support-l1/perms-01/" {
		t.Fatalf("perms-01: %s", row)
	}

	key := psql(t, "SELECT bundle_key FROM catalog_items WHERE id = 'support-l1/perms-01'")
	size := psql(t, "SELECT bundle_bytes FROM catalog_items WHERE id = 'support-l1/perms-01'")
	obj, err := newStore(t).Stat(t.Context(), key)
	if err != nil || size != itoa(obj.Size) {
		t.Fatalf("object %s: %+v %v (db says %s bytes)", key, obj, err, size)
	}
}

func TestReloadIsIdempotentAndKeepsUnchangedObjects(t *testing.T) {
	wipe(t)
	dir := fixture(t)
	mustLoad(t, dir, v1, "4 bundles uploaded, 0 reused")
	before := psql(t, "SELECT string_agg(id || bundle_key, ',' ORDER BY id) FROM catalog_items")

	mustLoad(t, dir, v2, "catalog "+v2+" loaded: 4 tickets, 0 practice, 0 bundles uploaded, 4 reused")
	if after := psql(t, "SELECT string_agg(id || bundle_key, ',' ORDER BY id) FROM catalog_items"); after != before {
		t.Fatalf("keys changed:\n%s\n%s", before, after)
	}
	if got := psql(t, "SELECT count(*) || ',' || bool_and(catalog_version = '"+v2+"') || ',' || (SELECT catalog_version FROM catalog_state) FROM catalog_items"); got != "4,true,"+v2 {
		t.Fatalf("after reload: %s", got)
	}

	// Changing one ticket uploads one object; the other three keep theirs.
	check := filepath.Join(dir, "foundations", "free-disk", "check.sh")
	b, err := os.ReadFile(check)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(check, append(b, "\n# changed\n"...), 0o644); err != nil {
		t.Fatal(err)
	}
	mustLoad(t, dir, v2, "4 tickets, 0 practice, 1 bundles uploaded, 3 reused")
	if got := psql(t, "SELECT count(*) FROM catalog_items WHERE bundle_key <> ''"); got != "4" {
		t.Fatalf("items: %s", got)
	}
}

func TestRemovalRefusedWhenProgressExists(t *testing.T) {
	wipe(t)
	dir := fixture(t)
	mustLoad(t, dir, v1, "4 tickets")
	psql(t, "INSERT INTO learners (org_id, github_id, github_login, username, age_confirmed_at, terms_accepted_at, terms_version) "+
		"SELECT id, 9101, 'cl', 'cl-9101', now(), now(), 't1' FROM orgs WHERE slug = 'public'; "+
		"INSERT INTO ticket_progress (org_id, learner_id, item_id, state, first_started_at) "+
		"SELECT org_id, id, 'support-l1/service-02', 'started', now() FROM learners WHERE github_id = 9101")

	if err := os.RemoveAll(filepath.Join(dir, "support-l1", "service-02")); err != nil {
		t.Fatal(err)
	}
	out, err := load(t, dir, v2)
	if err == nil || !strings.Contains(out, "items with learner progress would disappear: support-l1/service-02") {
		t.Fatalf("want a refusal naming the item, got %v\n%s", err, out)
	}
	// Rolled back whole: the item, and the version, are untouched.
	if got := psql(t, "SELECT count(*) || ',' || (SELECT catalog_version FROM catalog_state) FROM catalog_items WHERE catalog_version = '"+v1+"'"); got != "4,"+v1 {
		t.Fatalf("after refusal: %s", got)
	}

	// Without progress the same removal goes through.
	psql(t, "DELETE FROM ticket_progress")
	mustLoad(t, dir, v2, "3 tickets")
	if got := psql(t, "SELECT count(*) FROM catalog_items"); got != "3" {
		t.Fatalf("after removal: %s", got)
	}
}

func newStore(t *testing.T) *storage.S3 {
	t.Helper()
	cfg, err := storage.LoadConfig(func(k string) string {
		switch k {
		case "DOJO_S3_ENDPOINT":
			return "http://127.0.0.1:" + os.Getenv("DOJO_S3_PORT")
		case "DOJO_S3_ACCESS_KEY", "DOJO_S3_SECRET_KEY":
			return envExample(t, k)
		}
		return ""
	}, true)
	if err != nil {
		t.Fatal(err)
	}
	s, err := storage.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func TestLoadUpdatesAndRequiresSummary(t *testing.T) {
	wipe(t)
	dir := fixture(t)
	mustLoad(t, dir, v1, "4 tickets")

	file := filepath.Join(dir, "catalog.yaml")
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(strings.Replace(string(b), "Tickets, users, permissions, a calm reply", "A new summary", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	mustLoad(t, dir, v2, "4 tickets")
	if got := psql(t, "SELECT summary FROM roles WHERE slug = 'support-l1'"); got != "A new summary" {
		t.Fatalf("summary after reload: %q", got)
	}

	bad := strings.Replace(string(b), "    summary: Tickets, users, permissions, a calm reply\n", "", 1)
	if err := os.WriteFile(file, []byte(bad), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := load(t, dir, v1)
	if err == nil || !strings.Contains(out, "roles[1].summary") || !strings.Contains(out, "support-l1") {
		t.Fatalf("missing summary: %v\n%s", err, out)
	}
	if got := psql(t, "SELECT catalog_version FROM catalog_state WHERE id = 1"); got != v2 {
		t.Fatalf("failed load changed state: %q", got)
	}
}
