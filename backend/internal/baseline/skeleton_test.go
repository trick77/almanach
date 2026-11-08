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
