package baseline

import (
	"strings"
	"testing"
)

// Each kind keeps what says what the file is and offers, and nothing of a
// body or a value.
func TestSkeleton(t *testing.T) {
	for _, c := range []struct {
		path, body  string
		want, never []string
	}{
		{"src/main/java/shop/billing/InvoiceService.java", `package shop.billing;

import java.util.List;
import shop.core.Money;

/**
 * Writes the monthly invoices. Runs once a month.
 */
@Service
public class InvoiceService {
    private final int retries = 3;

    @Scheduled(cron = "0 0 1 * *")
    public List<Invoice> writeMonthly(Money floor) {
        return List.of();
    }
}`, []string{"package shop.billing;", "import shop.core.Money;", "/** Writes the monthly invoices.", "@Service", "public class InvoiceService", "@Scheduled(cron = \"0 0 1 * *\")", "public List<Invoice> writeMonthly(Money floor)"},
			[]string{"return List.of()", "retries = 3", "Runs once a month"}},
		{"web/src/app/claims.service.ts", `import { Injectable } from '@angular/core';
import { Api } from './api';

@Injectable({ providedIn: 'root' })
export class ClaimsService {
  constructor(private api: Api) {}
  load() { return this.api.get('/claims'); }
}
export const routes = [{ path: 'claims', component: ClaimsPage }];`, []string{"import { Api } from './api';", "@Injectable({ providedIn: 'root' })", "export class ClaimsService", "path: 'claims'"},
			[]string{"this.api.get"}},
		{"internal/billing/run.go", `package billing

import (
	"context"
	"shop/internal/store"
)

// Run writes the month's invoices.
func Run(ctx context.Context) error {
	return nil
}

func helper() {}

type Invoice struct{ ID int }`, []string{"package billing", "import (", "\t\"shop/internal/store\"", "// Run writes the month's invoices.\nfunc Run(ctx context.Context) error", "type Invoice struct"},
			[]string{"func helper", "return nil"}},
		{"tools/sync.py", "import os\nfrom shop import api\n\n@retry\ndef sync(n):\n    return n\n\nclass Job:\n    pass\n",
			[]string{"import os", "from shop import api", "@retry", "def sync(n):", "class Job:"}, []string{"return n", "pass"}},
		{"deploy/values.yaml", "replicas: 2\nimage:\n  tag: \"1.2\"\n  pullSecret: hunter2\n    deeper: x\n# comment: no\n",
			[]string{"replicas:", "image:", "  tag:", "  pullSecret:"}, []string{"hunter2", "\"1.2\"", "deeper", "comment"}},
		{"src/main/resources/application.properties", "spring.datasource.url=jdbc:x\n# note=1\napp.mail.password=hunter2\n",
			[]string{"spring.datasource.url", "app.mail.password"}, []string{"jdbc:x", "hunter2", "note"}},
		{"db/migration/V1__init.sql", "CREATE TABLE claim (\n  id BIGINT PRIMARY KEY,\n  amount NUMERIC(10,2) NOT NULL,\n  CONSTRAINT x CHECK (amount > 0)\n);\nINSERT INTO claim VALUES (1, 2);\nCREATE INDEX claim_amount ON claim(amount);\n",
			[]string{"CREATE TABLE claim (", "  id", "  amount", "CREATE INDEX claim_amount ON claim(amount);"}, []string{"INSERT", "CONSTRAINT"}},
		{"pom.xml", "<project>\n  <groupId>shop</groupId>\n  <artifactId>billing</artifactId>\n  <dependencies><dependency><scope>test</scope></dependency></dependencies>\n</project>\n",
			[]string{"<groupId>shop</groupId>", "<artifactId>billing</artifactId>"}, []string{"<scope>"}},
		{"process/claim.bpmn", `<bpmn:definitions xmlns:bpmn="x"><bpmn:process id="claim" name="Schaden prüfen" isExecutable="true">
  <bpmn:userTask id="check" name="Prüfen" camunda:assignee="x"/>
</bpmn:process></bpmn:definitions>`, []string{`bpmn:process id="claim" name="Schaden prüfen"`, `bpmn:userTask id="check" name="Prüfen"`}, []string{"assignee"}},
		{"schema/claim.avsc", `{"type": "record", "name": "Claim", "namespace": "shop.events",
 "fields": [{"name": "id", "type": "string"}]}`, []string{`"type": "record", "name": "Claim", "namespace": "shop.events"`, `"name": "id", "type": "string"`}, nil},
		{"Containerfile", "FROM golang:1.27 AS build\nRUN go build ./...\nEXPOSE 8080\nENTRYPOINT [\"/app\"]\n",
			[]string{"FROM golang:1.27 AS build", "EXPOSE 8080", "ENTRYPOINT [\"/app\"]"}, []string{"RUN go build"}},
		{"Jenkinsfile", "pipeline {\n  agent any\n  stages {\n    stage('Build') { steps { sh 'make' } }\n  }\n}\n",
			[]string{"pipeline", "agent any", "stages", "stage('Build')"}, nil},
		{"README.md", "# Billing\n\nWrites invoices.\n\n## Run locally\n\n#### deep\n", []string{"# Billing", "## Run locally"}, []string{"Writes invoices", "deep"}},
		{"Makefile.unknown", "\n\nfirst\nsecond\nthird\nfourth\n", []string{"first", "second", "third"}, []string{"fourth"}},
	} {
		got := Skeleton(c.path, []byte(c.body))
		for _, w := range c.want {
			if !strings.Contains(got, w) {
				t.Errorf("%s: skeleton lacks %q:\n%s", c.path, w, got)
			}
		}
		for _, n := range c.never {
			if strings.Contains(got, n) {
				t.Errorf("%s: skeleton keeps %q:\n%s", c.path, n, got)
			}
		}
	}
}

// The review's cases: an interface's methods, a column or annotation
// repeated further on, a table closed on its last column, a doc line too
// long for its declaration, a config of another format, a continuation.
func TestSkeletonEdges(t *testing.T) {
	for _, c := range []struct {
		path, body  string
		want, never []string
	}{
		{"src/ClaimRepository.java", "public interface ClaimRepository extends JpaRepository<Claim, Long> {\n    List<Claim> findByOwner(String owner);\n    Optional<Claim> findFirstByNumber(String n) throws IOException;\n    void touch();\n}\nclass Helper {\n    int count(List<String> xs) {\n        return size(xs);\n        if (x) {\n    }\n}\n",
			[]string{"List<Claim> findByOwner(String owner);", "Optional<Claim> findFirstByNumber(String n) throws IOException;", "void touch();", "int count(List<String> xs)"},
			[]string{"return size", "if (x)"}},
		{"src/Two.java", "class Two {\n    @Transactional\n    public void a() {}\n    @Transactional\n    public void b() {}\n}\n",
			[]string{"@Transactional\n    public void a()", "@Transactional\n    public void b()"}, nil},
		{"db/V2__two.sql", "CREATE TABLE claim (\n  id BIGINT,\n  amount NUMERIC NOT NULL);\nINSERT INTO claim VALUES (1);\nUPDATE claim SET amount = 2;\nCREATE TABLE invoice (\n  id BIGINT,\n  amount NUMERIC\n);\nALTER TABLE invoice\n  ADD COLUMN due DATE;\n",
			[]string{"CREATE TABLE claim (\n  id\n  amount\nCREATE TABLE invoice (\n  id\n  amount\nALTER TABLE invoice"}, []string{"INSERT", "UPDATE", "  ADD"}},
		{"internal/long.go", "package long\n\n// " + strings.Repeat("Viel Text ", 30) + "\nfunc Run() error { return nil }\n",
			[]string{"func Run() error", "// Viel Text"}, nil},
		{"config/app.toml", "[database]\nurl = \"postgres://x\"\npassword = \"Winter2026!\"\n", []string{"[database]", "url", "password"}, []string{"Winter2026!", "postgres"}},
		{".env.production", "DB_PASSWORD=Winter2026!\n", []string{"DB_PASSWORD"}, []string{"Winter2026!"}},
		{"conf/app.ini", "; comment\n[mail]\nhost=smtp.example.invalid\n", []string{"[mail]", "host"}, []string{"smtp", "comment"}},
		{"a.properties", "key.one=abc\\\n  abcDEF123=\nkey.two=2\n", []string{"key.one", "key.two"}, []string{"abcDEF123"}},
		// The medium review's: values out of a Containerfile, an export, a
		// default; JSON at any indentation or none; a switch arm no
		// signature, a default method one; Go's grouped names; more SQL.
		{"Containerfile", "FROM x\nENV DB_PASSWORD=Winter2026! MODE=prod\nENV OLD_KEY old value\nARG NPM_TOKEN=abc123\n",
			[]string{"ENV DB_PASSWORD MODE", "ENV OLD_KEY", "ARG NPM_TOKEN"}, []string{"Winter2026!", "abc123", "old value", "prod"}},
		{"web/src/env.ts", "export const API_KEY = 'k-123';\nexport function load() {}\n", []string{"export const API_KEY", "export function load()"}, []string{"k-123"}},
		{"src/Mail.java", "class Mail {\n    @Value(\"${mail.password:Winter2026!}\")\n    String password;\n}\n", []string{"@Value(\"${mail.password}\")"}, []string{"Winter2026!"}},
		{"appsettings.json", "{\n    \"Logging\": {\n        \"LogLevel\": {}\n    },\n    \"ConnectionStrings\": {}\n}\n", []string{"\"Logging\":", "\"ConnectionStrings\":"}, []string{"LogLevel"}},
		{"min.json", `{"a":1,"b":{"c":2}}`, []string{"\"a\":", "\"b\":"}, []string{"\"c\""}},
		{"list.json", "[1, 2, 3]\n", []string{"[1, 2, 3]"}, nil},
		{"src/Sw.java", "interface Sw {\n    default int size() { return 0; }\n    void run(int x) {\n        switch (x) {\n            default: handle(x);\n        }\n    }\n}\n",
			[]string{"default int size()", "void run(int x)"}, []string{"handle(x)"}},
		{"errs.go", "package e\n\nvar (\n\tErrGone = errors.New(\"gone\")\n\tlocal = 1\n)\n\nconst Limit = 5\n\ntype (\n\tA struct{}\n)\n",
			[]string{"var (", "\tErrGone", "const Limit", "type (", "\tA struct{}"}, []string{"\"gone\"", "local", "= 5"}},
		{"db/V3.sql", "CREATE OR REPLACE VIEW claim_open AS\nSELECT id\nFROM claim;\nCREATE TABLE stats AS\nSELECT a\nFROM b;\nCREATE TEMPORARY TABLE tmp (\n  x INT\n);\n",
			[]string{"CREATE OR REPLACE VIEW claim_open AS", "CREATE TABLE stats AS", "CREATE TEMPORARY TABLE tmp (\n  x"}, []string{"  SELECT", "  FROM"}},
	} {
		got := Skeleton(c.path, []byte(c.body))
		for _, w := range c.want {
			if !strings.Contains(got, w) {
				t.Errorf("%s: skeleton lacks %q:\n%s", c.path, w, got)
			}
		}
		for _, n := range c.never {
			if strings.Contains(got, n) {
				t.Errorf("%s: skeleton keeps %q:\n%s", c.path, n, got)
			}
		}
	}
}

// Fan-in's review cases: a nested Go module, two of them; a Java wildcard
// and a type of the own package used by name; a Kotlin file of other
// types; an ESM import naming the built file.
func TestFanInEdges(t *testing.T) {
	bodies := map[string][]byte{
		"backend/go.mod":              []byte("module example.invalid/app\n"),
		"backend/internal/store/s.go": []byte("package store\n"),
		"backend/cmd/main.go":         []byte("package main\nimport \"example.invalid/app/internal/store\"\n"),
		"tool/go.mod":                 []byte("module example.invalid/tool\n"),
		"tool/lib/l.go":               []byte("package lib\n"),
		"tool/main.go":                []byte("package main\nimport \"example.invalid/tool/lib\"\n"),
		"src/shop/core/Money.java":    []byte("package shop.core;\nclass Money {}\n"),
		"src/shop/core/Ledger.java":   []byte("package shop.core;\nclass Ledger { Money total; MoneyBag bag; }\n"),
		"src/shop/core/Unused.java":   []byte("package shop.core;\nclass Unused { int Moneys; }\n"),
		"src/shop/app/App.java":       []byte("package shop.app;\nimport shop.core.*;\n"),
		"src/shop/core/Types.kt":      []byte("package shop.core\ndata class Claim(val id: String)\nobject Ids\n"),
		"src/shop/app/Use.kt":         []byte("package shop.app\nimport shop.core.Claim\n"),
		"web/src/api.ts":              []byte("export const api = 1;\n"),
		"web/src/main.ts":             []byte("import { api } from './api.js';\n"),
		// A static wildcard counts its class; a string that looks like an
		// import is none; a test imports nothing; a comment declares nothing.
		"src/shop/core/Consts.java":       []byte("package shop.core;\nclass Consts { static int MAX = 1; }\n"),
		"src/shop/app/UseConsts.java":     []byte("package shop.app;\nimport static shop.core.Consts.*;\n"),
		"backend/internal/conf/c.go":      []byte("package conf\n"),
		"backend/cmd/str.go":              []byte("package main\nvar p = []string{\n\t\"example.invalid/app/internal/conf\",\n}\n"),
		"backend/internal/conf/c_test.go": []byte("package conf\nimport \"example.invalid/app/internal/store\"\n"),
		"src/shop/core/Doc.kt":            []byte("package shop.core\n/** see class Phantom */\nclass Real\n"),
		"src/shop/app/Ph.kt":              []byte("package shop.app\nimport shop.core.Phantom\nimport shop.core.Real\n"),
	}
	in := FanIn(bodies)
	for p, want := range map[string]int{
		"backend/internal/store/s.go": 1, "tool/lib/l.go": 1,
		"src/shop/core/Money.java": 2, "src/shop/core/Ledger.java": 1, "src/shop/core/Unused.java": 1,
		"src/shop/core/Types.kt": 2, "web/src/api.ts": 1,
		"src/shop/core/Consts.java": 2, "backend/internal/conf/c.go": 0, "src/shop/core/Doc.kt": 2,
	} {
		if in[p] != want {
			t.Errorf("fan-in of %s = %d, want %d (all: %v)", p, in[p], want, in)
		}
	}
}

// A skeleton is capped, a long line cut on a rune, an empty body none.
func TestSkeletonCaps(t *testing.T) {
	var b strings.Builder
	b.WriteString("package big\n")
	for i := 0; i < 2000; i++ {
		b.WriteString("func Exported")
		b.WriteString(strings.Repeat("x", i%7))
		b.WriteString(string(rune('A' + i%26)))
		b.WriteString("() {}\n")
	}
	if got := Skeleton("big.go", []byte(b.String())); len(got) > SkeletonMaxBytes || got == "" {
		t.Errorf("skeleton of %d bytes, want 1 to %d", len(got), SkeletonMaxBytes)
	}
	long := "func A" + strings.Repeat("ä", 200) + "() {}\n"
	got := Skeleton("long.go", []byte("package l\n"+long))
	if !strings.HasSuffix(strings.TrimSpace(got), "…") || !strings.Contains(got, "func Aä") || strings.ContainsRune(got, '\uFFFD') {
		t.Errorf("long line = %q, want it cut on a rune", got)
	}
	if Skeleton("empty.go", []byte(" \n\t")) != "" {
		t.Error("an empty body has a skeleton")
	}
}

// Fan-in counts each importer once: Java by package and class (a static
// import by its class), TS by a relative import, Go by package.
func TestFanIn(t *testing.T) {
	bodies := map[string][]byte{
		"src/shop/core/Money.java": []byte("package shop.core;\npublic class Money {}\n"),
		"src/shop/core/Rules.java": []byte("package shop.core;\npublic class Rules { public static int max() { return 1; } }\n"),
		"src/shop/billing/A.java":  []byte("package shop.billing;\nimport shop.core.Money;\nimport shop.core.Money;\nimport static shop.core.Rules.max;\n"),
		"src/shop/billing/B.kt":    []byte("package shop.billing\nimport shop.core.Money\n"),
		"web/src/api.ts":           []byte("export const api = 1;\n"),
		"web/src/app/claims.ts":    []byte("import { api } from '../api';\nconst x = require('../api');\n"),
		"web/src/index.ts":         []byte("import './app/claims';\n"),
		"go.mod":                   []byte("module shop\n\ngo 1.27\n"),
		"internal/store/store.go":  []byte("package store\n"),
		"internal/store/rows.go":   []byte("package store\n"),
		"internal/store/x_test.go": []byte("package store\n"),
		"cmd/app/main.go":          []byte("package main\n\nimport (\n\t\"fmt\"\n\tdb \"shop/internal/store\"\n)\n"),
		"internal/billing/run.go":  []byte("package billing\nimport \"shop/internal/store\"\n"),
	}
	in := FanIn(bodies)
	for p, want := range map[string]int{
		"src/shop/core/Money.java": 2, "src/shop/core/Rules.java": 1, "web/src/api.ts": 1, "web/src/app/claims.ts": 1,
		"internal/store/store.go": 2, "internal/store/rows.go": 2, "internal/store/x_test.go": 0, "cmd/app/main.go": 0,
	} {
		if in[p] != want {
			t.Errorf("fan-in of %s = %d, want %d (all: %v)", p, in[p], want, in)
		}
	}
}

// Stage 0 holds every file's skeleton, its size and the tree's.
func TestBuildStageZero(t *testing.T) {
	bodies := map[string][]byte{"a.go": []byte("package a\n\nfunc A() {\n\treturn\n}\n"), "b.txt": []byte("one\n")}
	z := BuildStageZero(bodies, map[string]int{"a.go": 3})
	if len(z.Skeletons) != 2 || z.Bytes != int64(len(bodies["a.go"])+len(bodies["b.txt"])) || z.SkeletonBytes == 0 || z.SkeletonBytes >= z.Bytes || z.Churn["a.go"] != 3 {
		t.Errorf("stage 0 = %+v", z)
	}
}
